package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// recordUsage applies token usage to the session's running totals and
// publishes the cost-/context-related bus events.  Pricing failures are
// absorbed here and never propagate as agent errors — see the package doc.
//
// T2.1 — cost roll-up: PropagateTotals walks the parent chain atomically so
// the primary session's running total stays correct even when the caller is a
// sub-agent.  A CostUpdated event is published for EVERY ancestor so the TUI
// footer, which subscribes to the root id, sees the rolled-up number.  The
// delta published at each level is the SAME per-turn delta (not a cumulative
// sum): each row in the chain accumulates the delta independently, so
// multi-level nesting doesn't multiply counts — it just updates each row once.
//
// T2.3 — threshold suggestion: after each turn, if usage crosses
// CompactionThresholdPct for the first time this crossing, a
// [bus.CompactionRequested] with Source="threshold" is published once.
// The flag is reset when usage falls below (threshold - 5) percentage points
// or when compaction completes (see Compact).
func (a *Agent) recordUsage(ctx context.Context, sessionID, modelName string, u provider.Usage) {
	if u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 {
		// No usage reported (provider may emit empty Usage on some
		// stream variants); skip the increment and the bus event.
		return
	}

	money := a.computeCost(ctx, modelName, u)

	delta := session.Totals{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		CostUSD:          money.USD,
	}

	// PropagateTotals walks the parent chain (leaf → root) and applies the
	// delta to each row atomically.  On failure, we log and continue —
	// per-message usage data is already persisted by AppendMessage so the
	// data is not lost.
	ancestors, err := a.opts.Store.PropagateTotals(ctx, sessionID, delta)
	if err != nil {
		slog.Warn("agent: propagate session totals failed",
			"session_id", sessionID, "err", err)
		// Fall back to single-session update so at minimum the source
		// session is still updated.
		if updateErr := a.opts.Store.UpdateSessionTotals(ctx, sessionID, delta); updateErr != nil {
			slog.Warn("agent: fallback update session totals also failed",
				"session_id", sessionID, "err", updateErr)
		}
		// Publish a single CostUpdated for the leaf session only.
		if totalsForEvent, getErr := a.opts.Store.GetSession(ctx, sessionID); getErr == nil {
			bus.Publish(a.opts.Bus, bus.CostUpdated{
				SessionID:        sessionID,
				InputTokens:      totalsForEvent.Totals.InputTokens,
				OutputTokens:     totalsForEvent.Totals.OutputTokens,
				CacheReadTokens:  totalsForEvent.Totals.CacheReadTokens,
				CacheWriteTokens: totalsForEvent.Totals.CacheWriteTokens,
				ReasoningTokens:  u.ReasoningTokens,
				DollarsTotal:     totalsForEvent.Totals.CostUSD,
				At:               a.opts.Now(),
			})
		}
	} else {
		// Publish CostUpdated for each updated session (leaf first, root last).
		// The TUI footer subscribes to the root id; sub-agent block headers
		// subscribe to the leaf id.  Subscribing to intermediate ancestors is
		// not done today — those rows silently accumulate in the DB and are
		// visible in the sessions modal's per-row breakdown.
		for _, ancestorID := range ancestors {
			totalsForEvent, getErr := a.opts.Store.GetSession(ctx, ancestorID)
			if getErr != nil {
				slog.Warn("agent: load session for cost event failed",
					"session_id", ancestorID, "err", getErr)
				continue
			}
			bus.Publish(a.opts.Bus, bus.CostUpdated{
				SessionID:        ancestorID,
				InputTokens:      totalsForEvent.Totals.InputTokens,
				OutputTokens:     totalsForEvent.Totals.OutputTokens,
				CacheReadTokens:  totalsForEvent.Totals.CacheReadTokens,
				CacheWriteTokens: totalsForEvent.Totals.CacheWriteTokens,
				ReasoningTokens:  u.ReasoningTokens,
				DollarsTotal:     totalsForEvent.Totals.CostUSD,
				At:               a.opts.Now(),
			})
		}
	}

	used := u.InputTokens + u.OutputTokens
	maxTok := a.opts.ContextWindow
	var pct float64
	if maxTok > 0 {
		pct = float64(used) / float64(maxTok)
	}
	bus.Publish(a.opts.Bus, bus.ContextUsageUpdated{
		SessionID:       sessionID,
		UsedTokens:      used,
		MaxTokens:       maxTok,
		PctUsed:         pct,
		ReasoningTokens: u.ReasoningTokens,
		At:              a.opts.Now(),
	})

	// T2.3 threshold suggestion: fire once per crossing.
	// pct is in [0.0, 1.0]; threshold is in [0, 99] as a percentage.
	threshold := a.opts.CompactionThresholdPct
	if threshold > 0 && maxTok > 0 {
		pctAs100 := pct * 100
		hysteresis := 5.0

		a.mu.Lock()
		fired := a.thresholdFired[sessionID]
		if !fired && pctAs100 >= threshold {
			// First crossing: fire the advisory suggestion.
			a.thresholdFired[sessionID] = true
			a.mu.Unlock()
			bus.Publish(a.opts.Bus, bus.CompactionRequested{
				SessionID: sessionID,
				Source:    "threshold",
				UsagePct:  pctAs100,
				At:        a.opts.Now(),
			})
		} else if fired && pctAs100 < (threshold-hysteresis) {
			// Usage has dropped enough below threshold — reset so the
			// banner will re-appear if it climbs back above threshold.
			a.thresholdFired[sessionID] = false
			a.mu.Unlock()
		} else {
			a.mu.Unlock()
		}
	}
}

// Compact summarises the session's pre-marker history and writes a new
// compaction marker.  The agent itself generates the summary by calling
// the provider with a dedicated "summarise this conversation" prompt.
//
// Returns [ErrNothingToCompact] if there are fewer than 4 messages since
// the latest marker — summarising 1–3 messages is not worth a provider
// round-trip.  ErrNothingToCompact does NOT publish any events.
//
// Bus events published in order:
//
//   - [bus.CompactionStarted] — before the provider call (always, unless
//     ErrNothingToCompact is returned).
//   - [bus.CompactionCompleted] — after the marker is persisted (success).
//   - [bus.CompactionFailed] — if any error occurs after Started was published.
//
// Compact is permission-free (it uses no tools) and serialises against Send
// on the same session through the per-session lock.
func (a *Agent) Compact(ctx context.Context, sessionID string) (*session.Marker, error) {
	if a.isClosed() {
		return nil, ErrClosed
	}
	if sessionID == "" {
		return nil, fmt.Errorf("agent: Compact: sessionID required")
	}

	lock := a.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	if a.isClosed() {
		return nil, ErrClosed
	}

	msgs, _, err := a.opts.Store.MessagesSinceLatestMarker(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent: Compact: load messages: %w", err)
	}
	if len(msgs) < 4 {
		return nil, ErrNothingToCompact
	}

	sess, err := a.opts.Store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent: Compact: load session: %w", err)
	}

	// Publish CompactionStarted before the provider call so the TUI can
	// show the "compacting…" notice as early as possible.
	startedAt := a.opts.Now()
	bus.Publish(a.opts.Bus, bus.CompactionStarted{
		SessionID:         sessionID,
		MessagesToCompact: len(msgs),
		InputTokensBefore: sess.Totals.InputTokens,
		At:                startedAt,
	})

	summary, usage, err := a.generateCompactionSummary(ctx, sess.Model.Name, msgs)
	if err != nil {
		bus.Publish(a.opts.Bus, bus.CompactionFailed{
			SessionID: sessionID,
			Reason:    err.Error(),
			At:        a.opts.Now(),
		})
		return nil, fmt.Errorf("agent: Compact: generate summary: %w", err)
	}

	beforeID := msgs[len(msgs)-1].ID
	marker, err := a.opts.Store.AddCompactionMarker(ctx, sessionID, beforeID, summary, usage.InputTokens)
	if err != nil {
		bus.Publish(a.opts.Bus, bus.CompactionFailed{
			SessionID: sessionID,
			Reason:    err.Error(),
			At:        a.opts.Now(),
		})
		return nil, fmt.Errorf("agent: Compact: add marker: %w", err)
	}

	durationMs := time.Since(startedAt).Milliseconds()
	bus.Publish(a.opts.Bus, bus.CompactionCompleted{
		SessionID:        sessionID,
		MarkerID:         marker.ID,
		SummaryTokens:    usage.InputTokens,
		InputTokensAfter: usage.InputTokens, // estimate: summary becomes the new context
		DurationMs:       durationMs,
		At:               a.opts.Now(),
	})

	// Reset the threshold-fired flag so the suggestion re-appears if usage
	// climbs back above threshold after compaction.
	a.mu.Lock()
	delete(a.thresholdFired, sessionID)
	a.mu.Unlock()

	return marker, nil
}

// compactionSystemPrompt is the instruction sent to the provider when
// generating a summary for [Agent.Compact].  Kept verbatim so changes
// produce visible diffs in code review.
const compactionSystemPrompt = "Summarize the following conversation in 2-3 paragraphs, " +
	"preserving all decisions, file paths, and outstanding tasks. " +
	"Be concrete; do not editorialize."

// generateCompactionSummary calls the provider with the compaction system
// prompt over msgs, drains the stream collecting text deltas, and returns
// the resulting summary together with the final Usage.
func (a *Agent) generateCompactionSummary(
	ctx context.Context, modelName string, msgs []*session.Message,
) (string, provider.Usage, error) {
	values := make([]session.Message, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		values = append(values, *m)
	}

	req := provider.Request{
		ModelName: modelName,
		Messages:  values,
		System:    compactionSystemPrompt,
		MaxTokens: a.opts.CompactionMaxTokens,
		// Tools intentionally empty: the model must not call a tool
		// while summarising.
	}

	ch, err := a.opts.Provider.Stream(ctx, req)
	if err != nil {
		return "", provider.Usage{}, err
	}

	var (
		text      []byte
		lastUsage provider.Usage
	)
	for {
		select {
		case <-ctx.Done():
			go discardStream(ch)
			return "", provider.Usage{}, ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				return string(text), lastUsage, nil
			}
			switch ev.Type {
			case provider.EventTextDelta:
				text = append(text, ev.Text...)
			case provider.EventUsage, provider.EventMessageStart:
				if ev.Usage.InputTokens != 0 || ev.Usage.OutputTokens != 0 {
					lastUsage = ev.Usage
				}
			case provider.EventError:
				return "", provider.Usage{}, ev.Err
			case provider.EventDone:
				// Continue draining until the channel closes so
				// the adapter's goroutine can exit cleanly.
			}
		}
	}
}

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"charm.land/fantasy"

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
	updates, err := a.opts.Store.PropagateTotals(ctx, sessionID, delta)
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
		// Publish CostUpdated for each updated session (leaf first, root last)
		// straight from the totals PropagateTotals read back — no per-ancestor
		// session loads.  The TUI footer subscribes to the root id; sub-agent
		// block headers subscribe to the leaf id.  Subscribing to intermediate
		// ancestors is not done today — those rows silently accumulate in the
		// DB and are visible in the sessions modal's per-row breakdown.
		for _, update := range updates {
			bus.Publish(a.opts.Bus, bus.CostUpdated{
				SessionID:        update.SessionID,
				InputTokens:      update.Totals.InputTokens,
				OutputTokens:     update.Totals.OutputTokens,
				CacheReadTokens:  update.Totals.CacheReadTokens,
				CacheWriteTokens: update.Totals.CacheWriteTokens,
				ReasoningTokens:  u.ReasoningTokens,
				DollarsTotal:     update.Totals.CostUSD,
				At:               a.opts.Now(),
			})
		}
	}

	// Context-window pressure includes prompt tokens served from provider
	// cache: cached tokens may be cheaper, but they still occupy the model's
	// input context. Cache-write tokens are already represented by InputTokens
	// for the turn and are not added again. Reasoning tokens are internal
	// thinking tokens the model emits before its visible output; on reasoning
	// models they consume real context budget but are reported separately from
	// OutputTokens, so they must be added explicitly or the gauge under-counts.
	used := u.InputTokens + u.CacheReadTokens + u.OutputTokens + u.ReasoningTokens
	maxTok := a.opts.ContextWindow
	// The provider reserves MaxOutput tokens of the window for the response,
	// so the effective ceiling for prompt/input tokens is ContextWindow minus
	// MaxOutput.  Dividing by that ceiling keeps the gauge from reading
	// optimistically as usage approaches the real limit.  Guard against a
	// misconfigured MaxOutput >= ContextWindow by falling back to the full
	// window.
	denom := maxTok
	if a.opts.MaxOutput > 0 && a.opts.MaxOutput < maxTok {
		denom = maxTok - a.opts.MaxOutput
	}
	var pct float64
	if denom > 0 {
		pct = float64(used) / float64(denom)
	}
	bus.Publish(a.opts.Bus, bus.ContextUsageUpdated{
		SessionID:       sessionID,
		UsedTokens:      used,
		MaxTokens:       maxTok,
		PctUsed:         pct,
		ReasoningTokens: u.ReasoningTokens,
		At:              a.opts.Now(),
	})

	// Store the latest known usage for this session so the next turn's
	// user envelope can include latest-known usage without the model having
	// to calculate it.
	a.mu.Lock()
	a.latestUsage[sessionID] = sessionUsage{usedTokens: used, pctUsed: pct}
	a.mu.Unlock()

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
	// climbs back above threshold after compaction.  Also clear the latest
	// known context usage: the stale pre-compaction numbers would be
	// misleading in the first post-compaction turn envelope.
	a.mu.Lock()
	delete(a.thresholdFired, sessionID)
	delete(a.latestUsage, sessionID)
	a.mu.Unlock()

	return marker, nil
}

// compactionSystemPrompt is the instruction sent to the provider when
// generating a summary for [Agent.Compact].  Kept verbatim so changes
// produce visible diffs in code review.
const compactionSystemPrompt = "Summarize the following conversation in 2-3 paragraphs, " +
	"preserving all decisions, file paths, and outstanding tasks. " +
	"Be concrete; do not editorialize."

// generateCompactionSummary calls the no-tool Fantasy internal agent. Summary
// usage is stored on the compaction marker for saved-token UX; it is not added
// to per-session cost totals, matching the existing accounting behavior for
// background summarization.
func (a *Agent) generateCompactionSummary(
	ctx context.Context, _ string, msgs []*session.Message,
) (string, provider.Usage, error) {
	if a.runtime != nil && a.runtime.hasFantasyModel() {
		fmsgs := append([]fantasy.Message{fantasy.NewSystemMessage(compactionSystemPrompt)}, toFantasyMessages(msgs, nil, "", nil, nil, nil, 0, 0, 0)...)
		fmsgs = append(fmsgs, fantasy.NewUserMessage("Summarize the conversation above."))
		return a.runtime.Summarize(ctx, fmsgs, a.opts.CompactionMaxTokens)
	}
	return "", provider.Usage{}, fmt.Errorf("agent: fantasy model is not configured")
}

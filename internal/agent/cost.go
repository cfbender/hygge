package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// recordUsage applies token usage to the session's running totals and
// publishes the cost-/context-related bus events.  Pricing failures are
// absorbed here and never propagate as agent errors — see the package doc.
func (a *Agent) recordUsage(ctx context.Context, sessionID string, u provider.Usage) {
	if u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 {
		// No usage reported (provider may emit empty Usage on some
		// stream variants); skip the increment and the bus event.
		return
	}

	money := a.computeCost(ctx, u)

	delta := session.Totals{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		CostUSD:          money.USD,
	}
	if err := a.opts.Store.UpdateSessionTotals(ctx, sessionID, delta); err != nil {
		// A failure to bump totals is logged but not fatal: the
		// per-message usage was already persisted by AppendMessage,
		// so the data is not lost — it just won't be reflected in the
		// session's running totals.  Still emit the bus events from
		// the totals view we just attempted to write.
		slog.Warn("agent: update session totals failed",
			"session_id", sessionID, "err", err)
	}

	totalsForEvent, err := a.opts.Store.GetSession(ctx, sessionID)
	if err != nil {
		slog.Warn("agent: load session for cost event failed",
			"session_id", sessionID, "err", err)
		return
	}

	bus.Publish(a.opts.Bus, bus.CostUpdated{
		SessionID:        sessionID,
		InputTokens:      totalsForEvent.Totals.InputTokens,
		OutputTokens:     totalsForEvent.Totals.OutputTokens,
		CacheReadTokens:  totalsForEvent.Totals.CacheReadTokens,
		CacheWriteTokens: totalsForEvent.Totals.CacheWriteTokens,
		DollarsTotal:     totalsForEvent.Totals.CostUSD,
		At:               a.opts.Now(),
	})

	used := u.InputTokens + u.OutputTokens
	maxTok := a.opts.ContextWindow
	var pct float64
	if maxTok > 0 {
		pct = float64(used) / float64(maxTok)
	}
	bus.Publish(a.opts.Bus, bus.ContextUsageUpdated{
		SessionID:  sessionID,
		UsedTokens: used,
		MaxTokens:  maxTok,
		PctUsed:    pct,
		At:         a.opts.Now(),
	})
}

// Compact summarises the session's pre-marker history and writes a new
// compaction marker.  The agent itself generates the summary by calling
// the provider with a dedicated "summarise this conversation" prompt.
//
// Returns [ErrNothingToCompact] if there are fewer than 4 messages since
// the latest marker — summarising 1–3 messages is not worth a provider
// round-trip.
//
// Compact is permission-free (it uses no tools) and emits no bus events.
// It does, however, serialise against Send on the same session through
// the per-session lock.
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

	summary, usage, err := a.generateCompactionSummary(ctx, msgs)
	if err != nil {
		return nil, fmt.Errorf("agent: Compact: generate summary: %w", err)
	}

	beforeID := msgs[len(msgs)-1].ID
	marker, err := a.opts.Store.AddCompactionMarker(ctx, sessionID, beforeID, summary, usage.InputTokens)
	if err != nil {
		return nil, fmt.Errorf("agent: Compact: add marker: %w", err)
	}
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
	ctx context.Context, msgs []*session.Message,
) (string, provider.Usage, error) {
	values := make([]session.Message, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		values = append(values, *m)
	}

	req := provider.Request{
		ModelName: a.providerModelName(),
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

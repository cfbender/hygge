package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// latencyProvider emits a scripted single-text turn after a fixed delay,
// emulating provider round-trip time. Safe for concurrent Streams.
type latencyProvider struct {
	name  string
	delay time.Duration
}

func (p *latencyProvider) Name() string { return p.name }

func (p *latencyProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}

func (p *latencyProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	return nil, nil
}

func (p *latencyProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	script := scriptText("done", provider.Usage{InputTokens: 900, OutputTokens: 100})
	ch := make(chan provider.Event, 8)
	go func() {
		defer close(ch)
		if p.delay > 0 {
			select {
			case <-time.After(p.delay):
			case <-ctx.Done():
				return
			}
		}
		for _, ev := range script.events {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}

// BenchmarkSubagentSends measures whether subagent turns sharing one parent
// session serialize each other. Each iteration runs four single-turn sends on
// four fresh subagent sessions of the same parent — sequentially or
// concurrently — including the totals propagation up the shared parent chain.
// If the concurrent variants approach the sequential wall time, something on
// the send path (agent state, store access) is serializing subagents.
//
// The 10ms variants emulate provider latency, which concurrency should hide;
// the 0ms variants expose pure agent+store overhead.
func BenchmarkSubagentSends(b *testing.B) {
	const fanout = 4
	for _, bc := range []struct {
		name       string
		delay      time.Duration
		concurrent bool
	}{
		{"sequential-0ms", 0, false},
		{"concurrent-0ms", 0, true},
		{"sequential-10ms", 10 * time.Millisecond, false},
		{"concurrent-10ms", 10 * time.Millisecond, true},
	} {
		b.Run(bc.name, func(b *testing.B) {
			env := newTestEnv(b)
			a := env.newAgent(&latencyProvider{name: "fake", delay: bc.delay})
			ctx := context.Background()

			for b.Loop() {
				b.StopTimer()
				children := make([]string, fanout)
				for i := range children {
					sess, err := env.Store.CreateSession(ctx, session.NewSession{
						ProjectDir: env.pwd,
						Model:      session.ModelRef{Provider: "fake", Name: "fake-model"},
						ParentID:   env.sessionID,
						Kind:       session.KindSubagent,
					})
					if err != nil {
						b.Fatalf("CreateSession: %v", err)
					}
					children[i] = sess.ID
				}
				b.StartTimer()

				if bc.concurrent {
					var wg sync.WaitGroup
					for _, id := range children {
						wg.Go(func() {
							if _, err := a.Send(ctx, id, userText("go")); err != nil {
								b.Errorf("Send(%s): %v", id, err)
							}
						})
					}
					wg.Wait()
				} else {
					for _, id := range children {
						if _, err := a.Send(ctx, id, userText("go")); err != nil {
							b.Fatalf("Send(%s): %v", id, err)
						}
					}
				}
			}
		})
	}
}

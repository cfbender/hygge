package agent

import (
	"context"
	"sync"
	"testing"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/provider"
)

// The handle must never expose a torn bundle: every loaded handle's ref,
// provider seam, provider id, and Fantasy model belong to the same SetModel
// call, no matter how reads interleave with swaps.
func TestSetModelHandleStaysConsistentUnderConcurrentSwaps(t *testing.T) {
	env := newTestEnv(t)

	provA := newFakeProvider("alpha")
	provB := newFakeProvider("beta")
	fmA := &fakeFantasyModel{provider: "alpha", model: "model-a", text: "a"}
	fmB := &fakeFantasyModel{provider: "beta", model: "model-b", text: "b"}

	a := env.newAgent(provA, func(o *Options) { o.FantasyModel = fmA })
	if err := a.SetModel("alpha", "model-a", provA, fmA); err != nil {
		t.Fatalf("SetModel: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Go(func() {
		defer close(stop)
		for i := range 1000 {
			if i%2 == 0 {
				_ = a.SetModel("beta", "model-b", provB, fmB)
			} else {
				_ = a.SetModel("alpha", "model-a", provA, fmA)
			}
		}
	})

	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				h := a.handle.Load()
				switch h.ref.Provider {
				case "alpha":
					if h.providerID != "alpha" || h.provider != provider.Provider(provA) || h.fantasyModel != fantasy.LanguageModel(fmA) {
						t.Errorf("torn handle for alpha: providerID=%q provider=%v model=%v", h.providerID, h.provider, h.fantasyModel)
						return
					}
				case "beta":
					if h.providerID != "beta" || h.provider != provider.Provider(provB) || h.fantasyModel != fantasy.LanguageModel(fmB) {
						t.Errorf("torn handle for beta: providerID=%q provider=%v model=%v", h.providerID, h.provider, h.fantasyModel)
						return
					}
				default:
					t.Errorf("unexpected ref provider %q", h.ref.Provider)
					return
				}
			}
		})
	}
	wg.Wait()
}

// Cost accounting reads the active provider while the UI may hot-swap models;
// under the race detector this guards the lock-free read path.
func TestComputeCostDoesNotRaceSetModel(t *testing.T) {
	env := newTestEnv(t)

	provA := newFakeProvider("alpha")
	provB := newFakeProvider("beta")
	fmA := &fakeFantasyModel{provider: "alpha", model: "model-a", text: "a"}
	fmB := &fakeFantasyModel{provider: "beta", model: "model-b", text: "b"}

	a := env.newAgent(provA, func(o *Options) { o.FantasyModel = fmA })

	ctx := context.Background()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Go(func() {
		defer close(stop)
		for i := range 500 {
			if i%2 == 0 {
				_ = a.SetModel("beta", "model-b", provB, fmB)
			} else {
				_ = a.SetModel("alpha", "model-a", provA, fmA)
			}
		}
	})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = a.computeCost(ctx, "model-a", provider.Usage{InputTokens: 10, OutputTokens: 5})
			_ = a.providerName()
			_ = a.activeModel()
		}
	})
	wg.Wait()
}

package agent

import (
	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// modelHandle bundles every piece of active-model identity that must change
// together: the configured provider/model ref, the provider seam, the
// lower-case provider id used for token accounting, and the Fantasy model.
//
// Handles are immutable — swapping models stores a new handle into the
// shared atomic pointer, so concurrent readers (turn loops, cost
// accounting, the runtime's internal calls) always observe a consistent
// bundle and never need a lock. The Agent and its Runtime share one
// pointer; Agent.SetModel is the only production writer.
type modelHandle struct {
	ref          session.ModelRef
	provider     provider.Provider
	providerID   string
	fantasyModel fantasy.LanguageModel
}

func newModelHandle(ref session.ModelRef, prv provider.Provider, fm fantasy.LanguageModel) *modelHandle {
	return &modelHandle{
		ref:          ref,
		provider:     prv,
		providerID:   providerNameFor(prv),
		fantasyModel: fm,
	}
}

// Package llm will become the catwalk client wrapper + model-resolution
// layer in Phase 1.  For now this file only confirms the upstream packages
// compile and are properly wired into go.mod.
package llm_test

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
)

// TestPhase0_ImportsCompile asserts that both upstream packages are
// correctly declared in go.mod and that the key types we plan to use
// in Phase 1 resolve without error.  No APIs are called.
func TestPhase0_ImportsCompile(_ *testing.T) {
	// catwalk: verify Provider struct is accessible.
	_ = catwalk.Provider{}

	// fantasy: verify NewAgentTool generic constructor resolves.
	_ = fantasy.NewAgentTool[struct{}]("probe", "compile-only probe", nil)
}

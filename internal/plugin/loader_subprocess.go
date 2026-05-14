package plugin

import (
	"context"
	"fmt"
)

// subprocessLoader is a placeholder for a future subprocess JSON-RPC runtime.
// It always returns false from CanLoad so it never wins the loader selection.
// The type exists to keep the Loader abstraction honest and to provide a clear
// error message when someone tries to use an entrypoint that isn't .lua.
//
// When subprocess plugins are implemented:
//   - SubprocessLoader.CanLoad should return true for non-.lua entrypoints.
//   - SubprocessLoader.Load should spawn the process and exchange the
//     initialize handshake to discover the plugin's capabilities.
//   - The Plugin implementation should multiplex JSON-RPC calls to the
//     subprocess and map responses back to PluginTool/HookRegistration etc.
type subprocessLoader struct{}

func (subprocessLoader) CanLoad(_ string, _ Manifest) bool { return false }

func (subprocessLoader) Load(name, _, _ string, _ Manifest) (Plugin, error) {
	return nil, fmt.Errorf(
		"plugin: subprocess loader is not yet implemented; "+
			"plugin %q requires a non-Lua runtime. "+
			"Subprocess JSON-RPC plugins are architecturally reserved for v0.4+",
		name,
	)
}

// subprocessPlugin is the compile-time shape-check.  It satisfies the Plugin
// interface but panics on every method call.  A real implementation would
// manage a live subprocess.
type subprocessPlugin struct {
	name, source string
	manifest     Manifest
}

func (p *subprocessPlugin) Name() string       { return p.name }
func (p *subprocessPlugin) Source() string     { return p.source }
func (p *subprocessPlugin) Manifest() Manifest { return p.manifest }
func (*subprocessPlugin) Load(_ context.Context, _ Host) error {
	panic("subprocessPlugin: not implemented")
}
func (*subprocessPlugin) Close(_ context.Context) error {
	panic("subprocessPlugin: not implemented")
}

// Ensure interface compliance at compile time.
var _ Plugin = (*subprocessPlugin)(nil)
var _ Loader = subprocessLoader{}

package provider

import (
	"fmt"
	"sync"
)

// Factory builds a Provider from a per-model options map.  The options map
// is the merged view of provider-level config and model.options from the
// loaded hygge config.
type Factory func(opts map[string]any) (Provider, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds a Factory under name.  It is intended to be called from a
// provider subpackage's init() function.  Registering the same name twice
// panics; this is a programmer error caught at process start.
func Register(name string, f Factory) {
	if name == "" {
		panic("provider: Register called with empty name")
	}
	if f == nil {
		panic("provider: Register called with nil factory")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("provider: duplicate Register for %q", name))
	}
	registry[name] = f
}

// Get returns the Factory registered under name, or ErrUnknownProvider when
// none is registered.
func Get(name string) (Factory, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, name)
	}
	return f, nil
}

// Names returns the sorted set of registered provider names.  Useful for
// help text and configuration validation.
func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// resetForTest clears the registry.  Test-only helper; exposed via
// internal/provider package tests through a same-package _test.go file.
func resetForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Factory{}
}

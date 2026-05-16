package plugin

import _ "embed"

//go:embed luals/hygge.lua
var luaLSTypeStub []byte

// LuaLSTypeStub returns Hygge's LuaLS/LuaCATS definition file for plugin
// authors. Callers receive a copy so the embedded bytes cannot be mutated.
func LuaLSTypeStub() []byte {
	return append([]byte(nil), luaLSTypeStub...)
}

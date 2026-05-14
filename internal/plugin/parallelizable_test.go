package plugin_test

import (
	"context"
	"path/filepath"
	"testing"
)

// TestLuaLoader_parallelizable verifies that `parallelizable = true` in the
// Lua registration table results in Parallelizable() returning true, and that
// omitting the key defaults to false.
func TestLuaLoader_parallelizable(t *testing.T) {
	reg, toolReg, _, _, _ := buildTestRegistry(t)

	dir := filepath.Join(testdataDir(t), "parallel")
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Tool registered with parallelizable = true.
	parallelTool, ok := toolReg.Get("parallel_tool")
	if !ok {
		t.Fatal("tool 'parallel_tool' not registered")
	}
	if !parallelTool.Parallelizable() {
		t.Error("parallel_tool.Parallelizable() = false, want true")
	}

	// Tool registered without the parallelizable key (defaults to false).
	serialTool, ok := toolReg.Get("serial_tool")
	if !ok {
		t.Fatal("tool 'serial_tool' not registered")
	}
	if serialTool.Parallelizable() {
		t.Error("serial_tool.Parallelizable() = true, want false (default)")
	}
}

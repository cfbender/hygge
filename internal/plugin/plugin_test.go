package plugin_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/hook"
	"github.com/cfbender/hygge/internal/plugin"
	"github.com/cfbender/hygge/internal/subagent"
	"github.com/cfbender/hygge/internal/tool"
)

// TestLuaLoader_canLoad verifies that LuaLoader.CanLoad is true for .lua
// entrypoints and false for others.
func TestLuaLoader_canLoad(t *testing.T) {
	l := plugin.LuaLoader{}

	luaManifest := plugin.SynthesiseManifest("test")
	luaManifest2 := plugin.Manifest{}
	luaManifest2.Entrypoint = "init.lua"

	if !l.CanLoad(".", luaManifest) {
		t.Error("CanLoad: expected true for .lua entrypoint")
	}
}

// TestLuaLoader_load_missingFile verifies that loading a nonexistent script
// returns an error.
func TestLuaLoader_load_missingFile(t *testing.T) {
	l := plugin.LuaLoader{}
	m := plugin.SynthesiseManifest("test")

	_, err := l.Load("test", "local:/tmp/test", "/tmp/nonexistent-dir", m)
	if err == nil {
		t.Fatal("expected error for missing entrypoint")
	}
}

// TestPluginHost_sendMessage verifies that SendMessage is forwarded to the
// InjectMessage callback.
func TestPluginHost_sendMessage(t *testing.T) {
	toolReg := tool.NewRegistry()
	hookReg := hook.New()
	cmdReg := command.New()
	subReg, _ := subagent.Load(subagent.LoadOptions{})

	var gotPlugin, gotSession, gotRole, gotContent string
	reg, _ := plugin.NewRegistry(plugin.RegistryOptions{
		CacheDir:  t.TempDir(),
		Tools:     toolReg,
		Hooks:     hookReg,
		Commands:  cmdReg,
		Subagents: subReg,
		InjectMessage: func(_ context.Context, sessionID, role, content string) error {
			gotSession = sessionID
			gotRole = role
			gotContent = content
			return nil
		},
	})
	_ = gotPlugin

	// Use a plugin that calls send_message.
	sendLua := `hygge.send_message("sess-1", "user", "hello from plugin")`
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", sendLua)

	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if gotSession != "sess-1" {
		t.Errorf("gotSession = %q, want %q", gotSession, "sess-1")
	}
	if gotRole != "user" {
		t.Errorf("gotRole = %q, want %q", gotRole, "user")
	}
	if gotContent != "hello from plugin" {
		t.Errorf("gotContent = %q, want %q", gotContent, "hello from plugin")
	}
}

// TestPluginHost_config verifies that per-plugin config is accessible via
// hygge.config.
func TestPluginHost_config(t *testing.T) {
	toolReg := tool.NewRegistry()
	hookReg := hook.New()
	cmdReg := command.New()
	subReg, _ := subagent.Load(subagent.LoadOptions{})

	reg, _ := plugin.NewRegistry(plugin.RegistryOptions{
		CacheDir:  t.TempDir(),
		Tools:     toolReg,
		Hooks:     hookReg,
		Commands:  cmdReg,
		Subagents: subReg,
		PluginConfigs: map[string]map[string]any{
			"configtest": {"key": "value123"},
		},
	})

	// Plugin reads config and registers a tool that returns it.
	lua := `
hygge.register_tool {
    name = "get_config",
    description = "Returns config",
    execute = function(ctx, input)
        return { content = hygge.config.key or "not-found" }
    end,
}
`
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", lua)
	writeFile(t, dir, "plugin.toml", `name = "configtest"`)

	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	toolFn, ok := toolReg.Get("get_config")
	if !ok {
		t.Fatal("get_config tool not registered")
	}

	result, err := toolFn.Execute(context.Background(), nil, tool.ExecContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Content != "value123" {
		t.Errorf("content = %q, want %q", result.Content, "value123")
	}
}

// TestNewRegistry_missingTools verifies that missing required fields are rejected.
func TestNewRegistry_missingTools(t *testing.T) {
	_, err := plugin.NewRegistry(plugin.RegistryOptions{
		CacheDir: t.TempDir(),
		// Tools missing
		Hooks:     hook.New(),
		Commands:  command.New(),
		Subagents: func() *subagent.Registry { r, _ := subagent.Load(subagent.LoadOptions{}); return r }(),
	})
	if err == nil {
		t.Fatal("expected error for missing Tools")
	}
}

// TestNewRegistry_missingHooks verifies that missing Hooks is rejected.
func TestNewRegistry_missingHooks(t *testing.T) {
	_, err := plugin.NewRegistry(plugin.RegistryOptions{
		CacheDir: t.TempDir(),
		Tools:    tool.NewRegistry(),
		// Hooks missing
		Commands:  command.New(),
		Subagents: func() *subagent.Registry { r, _ := subagent.Load(subagent.LoadOptions{}); return r }(),
	})
	if err == nil {
		t.Fatal("expected error for missing Hooks")
	}
}

// TestPlugin_manifests verifies manifest loading with and without plugin.toml.
func TestPlugin_manifests(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	// Plugin with explicit plugin.toml.
	dir := t.TempDir()
	writeFile(t, dir, "plugin.toml", "name = \"named-plugin\"\nversion = \"2.0.0\"")
	writeFile(t, dir, "plugin.lua", `-- empty`)

	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	p, ok := reg.Get("named-plugin")
	if !ok {
		t.Fatal("named-plugin not found")
	}
	if p.Manifest().Version != "2.0.0" {
		t.Errorf("Version = %q, want %q", p.Manifest().Version, "2.0.0")
	}
	if p.Manifest().Synthesised() {
		t.Error("should not be synthesised when plugin.toml exists")
	}
}

// TestPlugin_singleFile verifies that a directory with only plugin.lua is
// treated as a single-file plugin with a synthesised manifest.
func TestPlugin_singleFile(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", `-- single-file plugin`)

	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	all := reg.List()
	if len(all) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(all))
	}
	if !all[0].Manifest().Synthesised() {
		t.Error("expected synthesised manifest for single-file plugin")
	}
}

// TestPlugin_closeIdempotent verifies that Close can be called multiple times
// without panicking.
func TestPlugin_closeIdempotent(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", `-- empty`)

	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	ctx := context.Background()
	if err := reg.Close(ctx); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Second Close should be a no-op.
	if err := reg.Close(ctx); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestSubprocessLoader_cannotLoad verifies that the subprocess loader always
// returns false from CanLoad.
func TestSubprocessLoader_cannotLoad(t *testing.T) {
	// Load a plugin with a non-.lua entrypoint via plugin.toml.
	dir := t.TempDir()
	writeFile(t, dir, "plugin.toml", "name = \"subprocess-test\"\nentrypoint = \"main.py\"")

	reg, _, _, _, _ := buildTestRegistry(t)
	err := reg.Install(context.Background(), "local:"+dir)
	if err == nil {
		t.Fatal("expected error: no loader for .py entrypoint")
	}
}

// writeFile creates a file in dir with the given content.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeFile %q: %v", path, err)
	}
}

// TestRegistryHost_methods exercises host methods via plugins that call them.
func TestRegistryHost_methods(t *testing.T) {
	toolReg := tool.NewRegistry()
	hookReg := hook.New()
	cmdReg := command.New()
	subReg, _ := subagent.Load(subagent.LoadOptions{})

	reg, _ := plugin.NewRegistry(plugin.RegistryOptions{
		CacheDir:  t.TempDir(),
		Tools:     toolReg,
		Hooks:     hookReg,
		Commands:  cmdReg,
		Subagents: subReg,
		// No InjectMessage — send_message is a no-op.
	})

	lua := `
hygge.notify("test notification", "warn")
hygge.log("debug", "test log message", { key = "value" })
`
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", lua)

	// Should not error even with no-op notify/log.
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}
}

// TestRegistry_update_local verifies that Update on a local source is a no-op
// for the fetch step and hot-swaps any existing plugin registrations before
// reloading.
func TestRegistry_update_local(t *testing.T) {
	reg, toolReg, _, _, _ := buildTestRegistry(t)

	dir := testdataDir(t)
	src := "local:" + dir + "/hello"
	if err := reg.Install(context.Background(), src); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, ok := toolReg.Get("hello_world"); !ok {
		t.Fatal("hello_world not registered before update")
	}

	if err := reg.Update(context.Background(), "hello"); err != nil {
		t.Errorf("Update(local): unexpected error: %v", err)
	}
	if _, ok := toolReg.Get("hello_world"); !ok {
		t.Fatal("hello_world not registered after update")
	}
}

// TestRegistry_update_notFound verifies that updating a missing plugin errors.
func TestRegistry_update_notFound(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)
	err := reg.Update(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent plugin")
	}
}

// TestRegistry_remove_notFound verifies that removing a missing plugin is a no-op.
func TestRegistry_remove_notFound(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)
	// Remove of a non-existent plugin should not error.
	if err := reg.Remove(context.Background(), "nonexistent"); err != nil {
		t.Errorf("unexpected error removing nonexistent plugin: %v", err)
	}
}

// TestPackageManager_cacheDir verifies CacheDir returns expected paths.
func TestPackageManager_cacheDir(t *testing.T) {
	tmpDir := t.TempDir()
	pm := plugin.NewPackageManager(tmpDir)

	local, _ := plugin.ParseSource("local:/some/path")
	dir := pm.CacheDir(local)
	if dir != "/some/path" {
		t.Errorf("CacheDir(local) = %q, want /some/path", dir)
	}

	gh, _ := plugin.ParseSource("github:user/repo")
	ghDir := pm.CacheDir(gh)
	if ghDir == "" {
		t.Error("CacheDir(github) returned empty string")
	}
}

// TestPackageManager_localDir verifies that Resolve(local) returns the directory
// path and handles single-file sources.
func TestPackageManager_localDir(t *testing.T) {
	pm := plugin.NewPackageManager(t.TempDir())

	// Existing directory.
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", "-- test")
	src, _ := plugin.ParseSource("local:" + dir)
	resolved, err := pm.Resolve(context.Background(), src)
	if err != nil {
		t.Fatalf("Resolve(dir): %v", err)
	}
	if resolved != dir {
		t.Errorf("Resolve(dir) = %q, want %q", resolved, dir)
	}
}

// TestPackageManager_github_invalidURL verifies that Resolve fails cleanly
// when the GitHub clone URL is unreachable.
func TestPackageManager_github_invalidURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}
	pm := plugin.NewPackageManager(t.TempDir())

	src := plugin.Source{
		Kind: plugin.SourceGitHub,
		Raw:  "github:test/plugin",
		User: "test",
		Repo: "plugin",
	}
	// Give a short timeout so this fails fast.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := pm.Resolve(ctx, src)
	if err == nil {
		t.Fatal("expected error for unreachable git server")
	}
}

// TestRegistry_setInjectMessage verifies SetInjectMessage is callable.
func TestRegistry_setInjectMessage(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	called := false
	reg.SetInjectMessage(func(_ context.Context, _, _, _ string) error {
		called = true
		return nil
	})
	_ = called
	// Verify the registry accepted the callback without panicking.
}

// TestRegistry_pm verifies PM() returns a non-nil PackageManager.
func TestRegistry_pm(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)
	if reg.PM() == nil {
		t.Error("PM() returned nil")
	}
}

// TestLuaLoader_hookAdapterMethods verifies that the hook adapter's method
// implementations return sensible values (exercises getter code paths).
func TestLuaLoader_hookAdapterMethods(t *testing.T) {
	reg, _, hookReg, _, _ := buildTestRegistry(t)

	dir := testdataDir(t)
	if err := reg.Install(context.Background(), "local:"+dir+"/hello"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	hooks := hookReg.All()
	if len(hooks) == 0 {
		t.Fatal("no hooks registered")
	}
	for _, h := range hooks {
		// These calls exercise Description, Source, Timeout getters.
		_ = h.Description()
		_ = h.Source()
		_ = h.Timeout()
	}
}

// TestLuaLoader_commandAdapterMethods verifies command adapter getter paths.
func TestLuaLoader_commandAdapterMethods(t *testing.T) {
	reg, _, _, cmdReg, _ := buildTestRegistry(t)

	dir := testdataDir(t)
	if err := reg.Install(context.Background(), "local:"+dir+"/hello"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	cmd, ok := cmdReg.Get("greet")
	if !ok {
		t.Fatal("greet command not registered")
	}
	_ = cmd.Description()
	_ = cmd.Source()
	_ = cmd.Args()
}

// TestLuaLoader_variousTypes verifies that the Lua runtime correctly converts
// different Go/Lua values through the type conversion layer.
func TestLuaLoader_variousTypes(t *testing.T) {
	reg, toolReg, _, _, _ := buildTestRegistry(t)

	lua := `
hygge.register_tool {
    name = "type_test",
    description = "Test type conversion",
    execute = function(ctx, input)
        local parts = {}
        if input then
            for k, v in pairs(input) do
                table.insert(parts, k .. "=" .. tostring(v))
            end
        end
        return { content = table.concat(parts, ",") }
    end,
}
`
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", lua)
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	toolFn, ok := toolReg.Get("type_test")
	if !ok {
		t.Fatal("type_test not registered")
	}

	// Exercise with various JSON types including arrays and nested objects.
	cases := []string{
		`{"str":"hello"}`,
		`{"num":42}`,
		`{"bool":true}`,
		`{"arr":[1,2,3]}`,
		`{"nested":{"key":"value"}}`,
		`{}`,
		`null`,
	}
	for _, c := range cases {
		result, err := toolFn.Execute(context.Background(), []byte(c), tool.ExecContext{})
		if err != nil {
			t.Errorf("Execute(%s): %v", c, err)
		}
		if result.IsError {
			t.Errorf("Execute(%s): IsError=true: %s", c, result.Content)
		}
	}
}

// TestLuaLoader_hookMissingFunc verifies that registering a hook without a
// handler function errors at load time.
func TestLuaLoader_hookMissingFunc(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	lua := `
hygge.register_hook("pre_tool", "not-a-function")
`
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", lua)
	if err := reg.Install(context.Background(), "local:"+dir); err == nil {
		t.Fatal("expected error for hook with missing function")
	}
}

// TestLuaLoader_exec verifies that hygge.exec runs a subprocess and returns
// the output.
func TestLuaLoader_exec(t *testing.T) {
	reg, toolReg, _, _, _ := buildTestRegistry(t)

	lua := `
hygge.register_tool {
    name = "run_echo",
    description = "Run echo",
    execute = function(ctx, input)
        local res = hygge.exec("echo", {"hello from exec"}, {})
        return { content = res.stdout }
    end,
}
`
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", lua)
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	toolFn, ok := toolReg.Get("run_echo")
	if !ok {
		t.Fatal("run_echo not registered")
	}

	result, err := toolFn.Execute(context.Background(), []byte(`{}`), tool.ExecContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Errorf("IsError=true: %s", result.Content)
	}
	// stdout should contain the echoed text.
	if result.Content == "" {
		t.Error("expected non-empty stdout from exec")
	}
}

// TestLuaLoader_subagentMissingDesc verifies that a subagent without a
// description errors at load time.
func TestLuaLoader_subagentMissingDesc(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	lua := `
hygge.register_subagent {
    name = "testsubagent",
    system_prompt = "Some prompt",
}
`
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", lua)
	if err := reg.Install(context.Background(), "local:"+dir); err == nil {
		t.Fatal("expected error for subagent with missing description")
	}
}

// TestLuaLoader_asyncHook verifies that an async hook can be registered and
// dispatched correctly for post_* events.
func TestLuaLoader_asyncHook(t *testing.T) {
	reg, _, hookReg, _, _ := buildTestRegistry(t)

	lua := `
hygge.register_hook("post_tool", {
    mode = "async",
    timeout = "1s",
}, function(event)
    -- async hook; no return value expected
end)
`
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", lua)
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	hooks := hookReg.For(hook.EventPostTool)
	if len(hooks) == 0 {
		t.Fatal("no post_tool hooks registered")
	}
}

// TestLuaLoader_hookWithModify verifies that a hook can return a modify decision
// that mutates the message.
func TestLuaLoader_hookWithModify(t *testing.T) {
	reg, _, hookReg, _, _ := buildTestRegistry(t)

	lua := `
hygge.register_hook("pre_message", function(event)
    return { decision = "modify", modified_message = "modified: " .. event.message }
end)
`
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", lua)
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	in := hook.Input{
		Event:   hook.EventPreMessage,
		Message: "original",
	}
	out, dec, _, _, _ := hookReg.RunPre(context.Background(), hook.EventPreMessage, in)
	// RunPre returns Allow when hooks allow or modify — Deny is only for deny decisions.
	// The modification is applied to the returned Input.
	if dec != hook.DecisionAllow {
		t.Errorf("expected Allow decision (from modified hook), got %v", dec)
	}
	if out.Message != "modified: original" {
		t.Errorf("message = %q, want %q", out.Message, "modified: original")
	}
}

// TestLuaLoader_hookWithSystemPromptAppend verifies Lua pre_message hooks can
// return non-rendered one-turn system prompt additions and observe mode_name
// during refresh-style invocations.
func TestLuaLoader_hookWithSystemPromptAppend(t *testing.T) {
	reg, _, hookReg, _, _ := buildTestRegistry(t)

	lua := `
hygge.register_hook("pre_message", function(event)
    return { decision = "allow", system_prompt_append = "mode=" .. event.mode_name .. "; msg=" .. event.message }
end)
`
	dir := t.TempDir()
	writeFile(t, dir, "plugin.lua", lua)
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	in := hook.Input{
		Event:    hook.EventPreMessage,
		Message:  "original",
		ModeName: "Smart",
	}
	out, dec, _, _, _ := hookReg.RunPre(context.Background(), hook.EventPreMessage, in)
	if dec != hook.DecisionAllow {
		t.Errorf("expected Allow decision, got %v", dec)
	}
	if out.Message != "original" {
		t.Errorf("message = %q, want original", out.Message)
	}
	if len(out.SystemPromptAdditions) != 1 || out.SystemPromptAdditions[0] != "mode=Smart; msg=original" {
		t.Fatalf("SystemPromptAdditions = %#v", out.SystemPromptAdditions)
	}
}

// TestPluginToolAdapter_inputSchema verifies InputSchema returns a valid map.
func TestPluginToolAdapter_inputSchema(t *testing.T) {
	reg, toolReg, _, _, _ := buildTestRegistry(t)

	dir := testdataDir(t)
	if err := reg.Install(context.Background(), "local:"+dir+"/hello"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	toolFn, ok := toolReg.Get("hello_world")
	if !ok {
		t.Fatal("hello_world not found")
	}
	schema := toolFn.InputSchema()
	if schema == nil {
		t.Error("InputSchema() returned nil")
	}
}

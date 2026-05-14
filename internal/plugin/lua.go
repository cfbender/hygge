package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
	luar "layeh.com/gopher-luar"

	"github.com/cfbender/hygge/internal/hook"
)

// luaPlugin is the concrete Plugin implementation backed by gopher-lua.
//
// # Concurrency
//
// gopher-lua's LState is NOT safe for concurrent use.  All Lua calls go
// through luaPlugin.call which acquires mu before touching L.  This means
// concurrent tool/hook/command invocations are serialised per plugin.
// This is intentional and documented: plugin code is rarely the bottleneck.
type luaPlugin struct {
	name       string
	source     string
	scriptPath string
	manifest   Manifest

	mu   sync.Mutex // serialises all LState access
	L    *lua.LState
	host Host
}

// newLuaPlugin allocates a luaPlugin but does not yet execute the script.
func newLuaPlugin(name, source, scriptPath string, m Manifest) *luaPlugin {
	return &luaPlugin{
		name:       name,
		source:     source,
		scriptPath: scriptPath,
		manifest:   m,
	}
}

func (p *luaPlugin) Name() string       { return p.name }
func (p *luaPlugin) Source() string     { return p.source }
func (p *luaPlugin) Manifest() Manifest { return p.manifest }

// Load executes the plugin script inside a new LState.  This is the
// initialisation phase: the script runs top-to-bottom, calling
// hygge.register_* to declare its contributions.
func (p *luaPlugin) Load(_ context.Context, h Host) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.L != nil {
		return fmt.Errorf("plugin: %s: Load called twice", p.name)
	}

	p.host = h

	L := lua.NewState()
	p.L = L

	// Register the hygge global module.
	p.registerHyggeModule(L)

	// Execute the plugin script.
	if err := L.DoFile(p.scriptPath); err != nil {
		L.Close()
		p.L = nil
		return fmt.Errorf("plugin: %s: script error: %w", p.name, err)
	}

	return nil
}

// Close releases the LState.
func (p *luaPlugin) Close(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.L != nil {
		p.L.Close()
		p.L = nil
	}
	return nil
}

// call acquires the plugin mutex and invokes fn.  All calls into the LState
// must go through call.  If the plugin is closed, call returns an error.
func (p *luaPlugin) call(fn func(L *lua.LState) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.L == nil {
		return fmt.Errorf("plugin: %s: plugin is closed", p.name)
	}
	return fn(p.L)
}

// registerHyggeModule sets up the hygge global table in L.
func (p *luaPlugin) registerHyggeModule(L *lua.LState) {
	hygge := L.NewTable()

	// --- Registration functions ---

	L.SetField(hygge, "register_tool", L.NewFunction(func(L *lua.LState) int {
		return p.luaRegisterTool(L)
	}))
	L.SetField(hygge, "register_hook", L.NewFunction(func(L *lua.LState) int {
		return p.luaRegisterHook(L)
	}))
	L.SetField(hygge, "register_command", L.NewFunction(func(L *lua.LState) int {
		return p.luaRegisterCommand(L)
	}))
	L.SetField(hygge, "register_subagent", L.NewFunction(func(L *lua.LState) int {
		return p.luaRegisterSubagent(L)
	}))

	// --- Side-effect functions ---

	L.SetField(hygge, "send_message", L.NewFunction(func(L *lua.LState) int {
		sessionID := L.CheckString(1)
		role := L.CheckString(2)
		content := L.CheckString(3)
		ctx := context.Background()
		if err := p.host.SendMessage(ctx, sessionID, role, content); err != nil {
			L.RaiseError("hygge.send_message: %s", err.Error())
		}
		return 0
	}))

	L.SetField(hygge, "notify", L.NewFunction(func(L *lua.LState) int {
		msg := L.CheckString(1)
		level := L.OptString(2, "info")
		p.host.Notify(level, msg)
		return 0
	}))

	L.SetField(hygge, "log", L.NewFunction(func(L *lua.LState) int {
		level := L.CheckString(1)
		msg := L.CheckString(2)
		fields := map[string]any{}
		if tbl := L.OptTable(3, nil); tbl != nil {
			tbl.ForEach(func(k, v lua.LValue) {
				fields[k.String()] = luaToGo(v)
			})
		}
		p.host.Log(level, msg, fields)
		return 0
	}))

	L.SetField(hygge, "exec", L.NewFunction(func(L *lua.LState) int {
		return p.luaExec(L)
	}))

	// --- Config table (read-only) ---
	cfgData := p.host.Config()
	cfgTbl := goMapToLua(L, cfgData)
	L.SetField(hygge, "config", cfgTbl)

	// --- Session stub (populated per-handler; at module init it's empty) ---
	sessionTbl := L.NewTable()
	L.SetField(sessionTbl, "id", lua.LString(""))
	L.SetField(hygge, "session", sessionTbl)

	L.SetGlobal("hygge", hygge)
}

// --- Lua registration handlers ---

func (p *luaPlugin) luaRegisterTool(L *lua.LState) int {
	tbl := L.CheckTable(1)

	name := tableString(L, tbl, "name")
	if name == "" {
		L.RaiseError("hygge.register_tool: name is required")
		return 0
	}
	desc := tableString(L, tbl, "description")
	execFn := tbl.RawGetString("execute")
	if _, ok := execFn.(*lua.LFunction); !ok {
		L.RaiseError("hygge.register_tool: execute must be a function")
		return 0
	}
	luaFn := execFn.(*lua.LFunction)

	// InputSchema from the table, if provided.
	var inputSchema json.RawMessage
	if schemaVal := tbl.RawGetString("input_schema"); schemaVal != lua.LNil {
		if schemaTbl, ok := schemaVal.(*lua.LTable); ok {
			m := luaTableToMap(schemaTbl)
			b, err := json.Marshal(m)
			if err == nil {
				inputSchema = b
			}
		}
	}

	// Build a Go wrapper that calls the Lua function under the plugin mutex.
	pluginName := p.name
	pluginRef := p // captured for mutex access
	executeFn := func(_ context.Context, input json.RawMessage) (PluginToolResult, error) {
		var result PluginToolResult
		var execErr error
		callErr := pluginRef.call(func(L *lua.LState) error {
			// Build ctx table.
			ctxTbl := L.NewTable()
			// We don't have sessionID here; keep it empty.

			// Build input table from JSON.
			var inputMap map[string]any
			if len(input) > 0 {
				if err := json.Unmarshal(input, &inputMap); err == nil {
					L.SetField(ctxTbl, "input", goMapToLua(L, inputMap))
				}
			}

			// Call the Lua function.
			if err := L.CallByParam(lua.P{
				Fn:      luaFn,
				NRet:    1,
				Protect: true,
			}, ctxTbl, luarValue(L, inputMap)); err != nil {
				slog.Warn("plugin: tool execute error; failing open",
					"plugin", pluginName, "tool", name, "err", err)
				execErr = err
				return nil
			}

			ret := L.Get(-1)
			L.Pop(1)

			if retTbl, ok := ret.(*lua.LTable); ok {
				result.Content = tableString(L, retTbl, "content")
				if isErr := retTbl.RawGetString("is_error"); isErr == lua.LTrue {
					result.IsError = true
				}
			} else if ret != lua.LNil {
				result.Content = ret.String()
			}
			return nil
		})
		if callErr != nil {
			return PluginToolResult{Content: fmt.Sprintf("plugin error: %s", callErr), IsError: true}, nil
		}
		if execErr != nil {
			return PluginToolResult{Content: fmt.Sprintf("plugin error: %s", execErr), IsError: true}, nil
		}
		return result, nil
	}

	if err := p.host.RegisterTool(PluginTool{
		Name:        name,
		Description: desc,
		InputSchema: inputSchema,
		Execute:     executeFn,
	}); err != nil {
		L.RaiseError("hygge.register_tool: %s", err.Error())
	}
	return 0
}

func (p *luaPlugin) luaRegisterHook(L *lua.LState) int {
	eventStr := L.CheckString(1)
	// Second arg can be options table or function directly.
	var optsTbl *lua.LTable
	var handlerFn *lua.LFunction

	if L.GetTop() == 2 {
		// register_hook("event", function(e) ... end)
		handlerFn = L.CheckFunction(2)
	} else {
		// register_hook("event", {mode=..., timeout=...}, function(e) ... end)
		optsTbl = L.CheckTable(2)
		handlerFn = L.CheckFunction(3)
	}

	event := hook.Event(eventStr)
	// Validate.
	switch event {
	case hook.EventPreTool, hook.EventPostTool, hook.EventPreMessage, hook.EventPostMessage:
	default:
		L.RaiseError("hygge.register_hook: unknown event %q; valid: pre_tool, post_tool, pre_message, post_message", eventStr)
		return 0
	}

	mode := hook.ModeSync
	timeout := 5 * time.Second
	hookName := fmt.Sprintf("plugin:%s:%s", p.name, eventStr)

	if optsTbl != nil {
		if modeStr := tableString(L, optsTbl, "mode"); modeStr == "async" {
			mode = hook.ModeAsync
		}
		if tStr := tableString(L, optsTbl, "timeout"); tStr != "" {
			if d, err := time.ParseDuration(tStr); err == nil {
				timeout = d
			}
		}
		if nameStr := tableString(L, optsTbl, "name"); nameStr != "" {
			hookName = nameStr
		}
	}

	pluginName := p.name
	pluginRef := p
	captured := handlerFn

	handler := func(_ context.Context, in hook.Input) (hook.Action, error) {
		var action hook.Action
		callErr := pluginRef.call(func(L *lua.LState) error {
			// Build event table for Lua.
			evtTbl := L.NewTable()
			L.SetField(evtTbl, "event", lua.LString(in.Event))
			L.SetField(evtTbl, "session_id", lua.LString(in.SessionID))
			L.SetField(evtTbl, "hook_name", lua.LString(in.HookName))
			L.SetField(evtTbl, "pwd", lua.LString(in.Pwd))
			L.SetField(evtTbl, "tool_name", lua.LString(in.ToolName))
			L.SetField(evtTbl, "message", lua.LString(in.Message))
			if in.ToolInput != nil {
				var m map[string]any
				if err := json.Unmarshal(in.ToolInput, &m); err == nil {
					L.SetField(evtTbl, "tool_input", goMapToLua(L, m))
				}
			}

			if err := L.CallByParam(lua.P{
				Fn:      captured,
				NRet:    1,
				Protect: true,
			}, evtTbl); err != nil {
				slog.Warn("plugin: hook handler error; failing open",
					"plugin", pluginName, "hook", hookName, "err", err)
				return nil // fail-open
			}

			ret := L.Get(-1)
			L.Pop(1)

			if retTbl, ok := ret.(*lua.LTable); ok {
				dec := hook.Decision(tableString(L, retTbl, "decision"))
				reason := tableString(L, retTbl, "reason")
				action = hook.Action{Decision: dec, Reason: reason}
				// Modified fields.
				if modInput := retTbl.RawGetString("modified_tool_input"); modInput != lua.LNil {
					if modTbl, ok := modInput.(*lua.LTable); ok {
						m := luaTableToMap(modTbl)
						b, _ := json.Marshal(m)
						action.ModifiedToolInput = b
					}
				}
				if modMsg := retTbl.RawGetString("modified_message"); modMsg != lua.LNil {
					action.ModifiedMessage = modMsg.String()
				}
			}
			return nil
		})
		if callErr != nil {
			slog.Warn("plugin: hook dispatch error; failing open",
				"plugin", pluginName, "hook", hookName, "err", callErr)
		}
		return action, nil
	}

	if err := p.host.RegisterHook(HookRegistration{
		Name:    hookName,
		Event:   event,
		Mode:    mode,
		Timeout: timeout,
		Handler: handler,
	}); err != nil {
		L.RaiseError("hygge.register_hook: %s", err.Error())
	}
	return 0
}

func (p *luaPlugin) luaRegisterCommand(L *lua.LState) int {
	tbl := L.CheckTable(1)

	name := tableString(L, tbl, "name")
	if name == "" {
		L.RaiseError("hygge.register_command: name is required")
		return 0
	}
	desc := tableString(L, tbl, "description")
	execFn := tbl.RawGetString("execute")
	luaFn, ok := execFn.(*lua.LFunction)
	if !ok {
		L.RaiseError("hygge.register_command: execute must be a function")
		return 0
	}

	pluginName := p.name
	pluginRef := p
	captured := luaFn

	executeFn := func(_ context.Context, input string) (string, error) {
		var result string
		callErr := pluginRef.call(func(L *lua.LState) error {
			ctxTbl := L.NewTable()
			if err := L.CallByParam(lua.P{
				Fn:      captured,
				NRet:    1,
				Protect: true,
			}, ctxTbl, lua.LString(input)); err != nil {
				slog.Warn("plugin: command execute error; failing open",
					"plugin", pluginName, "command", name, "err", err)
				return nil
			}
			ret := L.Get(-1)
			L.Pop(1)
			if retTbl, ok := ret.(*lua.LTable); ok {
				result = tableString(L, retTbl, "message")
			} else if ret != lua.LNil {
				result = ret.String()
			}
			return nil
		})
		if callErr != nil {
			return "", callErr
		}
		return result, nil
	}

	if err := p.host.RegisterCommand(CommandRegistration{
		Name:        name,
		Description: desc,
		Execute:     executeFn,
	}); err != nil {
		L.RaiseError("hygge.register_command: %s", err.Error())
	}
	return 0
}

func (p *luaPlugin) luaRegisterSubagent(L *lua.LState) int {
	tbl := L.CheckTable(1)

	name := tableString(L, tbl, "name")
	if name == "" {
		L.RaiseError("hygge.register_subagent: name is required")
		return 0
	}
	desc := tableString(L, tbl, "description")
	if desc == "" {
		L.RaiseError("hygge.register_subagent: description is required")
		return 0
	}
	sysPrompt := tableString(L, tbl, "system_prompt")
	if sysPrompt == "" {
		L.RaiseError("hygge.register_subagent: system_prompt is required")
		return 0
	}
	model := tableString(L, tbl, "model")

	var tools []string
	if toolsTbl := tbl.RawGetString("tools"); toolsTbl != lua.LNil {
		if tt, ok := toolsTbl.(*lua.LTable); ok {
			tt.ForEach(func(_, v lua.LValue) {
				if s, ok := v.(lua.LString); ok {
					tools = append(tools, string(s))
				}
			})
		}
	}

	if err := p.host.RegisterSubagent(SubagentRegistration{
		Name:         name,
		Description:  desc,
		SystemPrompt: sysPrompt,
		Tools:        tools,
		Model:        model,
	}); err != nil {
		L.RaiseError("hygge.register_subagent: %s", err.Error())
	}
	return 0
}

func (p *luaPlugin) luaExec(L *lua.LState) int {
	command := L.CheckString(1)
	var args []string
	if argsTbl := L.OptTable(2, nil); argsTbl != nil {
		argsTbl.ForEach(func(_, v lua.LValue) {
			args = append(args, v.String())
		})
	}

	opts := ExecOptions{}
	if optsTbl := L.OptTable(3, nil); optsTbl != nil {
		if timeout := tableString(L, optsTbl, "timeout"); timeout != "" {
			if d, err := time.ParseDuration(timeout); err == nil {
				opts.Timeout = d
			}
		}
		if dir := tableString(L, optsTbl, "dir"); dir != "" {
			opts.Dir = dir
		}
	}

	ctx := context.Background()
	result, err := p.host.Exec(ctx, command, args, opts)
	if err != nil {
		L.RaiseError("hygge.exec: %s", err.Error())
		return 0
	}

	retTbl := L.NewTable()
	L.SetField(retTbl, "stdout", lua.LString(result.Stdout))
	L.SetField(retTbl, "stderr", lua.LString(result.Stderr))
	L.SetField(retTbl, "code", lua.LNumber(result.Code))
	L.Push(retTbl)
	return 1
}

// --- Lua↔Go conversion helpers ---

// tableString extracts a string field from a Lua table; returns "" when
// absent.
func tableString(_ *lua.LState, t *lua.LTable, key string) string {
	v := t.RawGetString(key)
	if v == lua.LNil {
		return ""
	}
	return v.String()
}

// luaToGo converts a Lua value to its closest Go equivalent.
func luaToGo(v lua.LValue) any {
	switch val := v.(type) {
	case lua.LBool:
		return bool(val)
	case lua.LNumber:
		return float64(val)
	case lua.LString:
		return string(val)
	case *lua.LTable:
		return luaTableToMap(val)
	default:
		return nil
	}
}

// luaTableToMap converts a Lua table to a map[string]any.  Integer-keyed
// tables become map["1"]=...; for proper arrays the caller should check the
// table shape.
func luaTableToMap(t *lua.LTable) map[string]any {
	m := make(map[string]any)
	t.ForEach(func(k, v lua.LValue) {
		m[k.String()] = luaToGo(v)
	})
	return m
}

// goMapToLua converts a Go map to a Lua table.
func goMapToLua(L *lua.LState, m map[string]any) *lua.LTable {
	t := L.NewTable()
	for k, v := range m {
		L.SetField(t, k, goValueToLua(L, v))
	}
	return t
}

// goValueToLua converts a Go value to a Lua value.
func goValueToLua(L *lua.LState, v any) lua.LValue {
	if v == nil {
		return lua.LNil
	}
	switch val := v.(type) {
	case bool:
		if val {
			return lua.LTrue
		}
		return lua.LFalse
	case int:
		return lua.LNumber(val)
	case int64:
		return lua.LNumber(val)
	case float64:
		return lua.LNumber(val)
	case string:
		return lua.LString(val)
	case map[string]any:
		return goMapToLua(L, val)
	case []any:
		tbl := L.NewTable()
		for i, item := range val {
			L.RawSetInt(tbl, i+1, goValueToLua(L, item))
		}
		return tbl
	default:
		return lua.LString(fmt.Sprintf("%v", val))
	}
}

// luarValue creates a luar-converted Go value for passing to Lua.  Used to
// pass structured Go objects to plugin functions.
func luarValue(L *lua.LState, v any) lua.LValue {
	if v == nil {
		return lua.LNil
	}
	// Use luar for complex types; fall back to goValueToLua for maps.
	if m, ok := v.(map[string]any); ok {
		return goMapToLua(L, m)
	}
	return luar.New(L, v)
}

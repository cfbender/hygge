-- Test fixture: registers a tool, a hook, and a command.

hygge.register_tool {
    name        = "hello_world",
    description = "Returns a greeting",
    input_schema = { type = "object", properties = { name = { type = "string" } } },
    execute = function(ctx, input)
        local who = "World"
        if input and input.name then
            who = input.name
        end
        return { content = "Hello, " .. who .. "!", is_error = false }
    end,
}

hygge.register_hook("pre_tool", {
    mode = "sync",
    timeout = "2s",
}, function(event)
    -- Block calls to a tool named "blocked_tool".
    if event.tool_name == "blocked_tool" then
        return { decision = "deny", reason = "blocked by test plugin" }
    end
    return { decision = "allow" }
end)

hygge.register_command {
    name        = "greet",
    description = "Greet the user",
    execute = function(ctx, input)
        return { message = "Hello from plugin! Input: " .. input }
    end,
}

hygge.register_subagent {
    name          = "pluginagent",
    description   = "A test sub-agent registered by a plugin",
    system_prompt = "You are a plugin-registered sub-agent. Be brief.",
    tools         = { "read", "glob" },
}

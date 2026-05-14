-- Fixture with invalid registration (missing name).
hygge.register_tool {
    description = "Missing name",
    execute = function(ctx, input)
        return { content = "ok" }
    end,
}

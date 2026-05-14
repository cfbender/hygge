-- Test fixture: tests error handling in execute.

hygge.register_tool {
    name        = "error_tool",
    description = "Raises an error",
    execute = function(ctx, input)
        error("intentional test error")
    end,
}

-- Test fixture: registers two tools — one parallelizable, one not.

hygge.register_tool {
    name           = "parallel_tool",
    description    = "A parallelizable tool",
    parallelizable = true,
    execute = function(ctx, input)
        return { content = "parallel ok" }
    end,
}

hygge.register_tool {
    name        = "serial_tool",
    description = "A non-parallelizable tool (default)",
    execute = function(ctx, input)
        return { content = "serial ok" }
    end,
}

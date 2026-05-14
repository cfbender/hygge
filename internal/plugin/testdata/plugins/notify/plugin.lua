-- Test fixture: tests send_message and notify.
-- Note: send_message will be a no-op in test (no InjectMessage wired).

hygge.notify("Plugin loaded successfully", "info")
hygge.log("info", "plugin init", { plugin = "notify-test" })

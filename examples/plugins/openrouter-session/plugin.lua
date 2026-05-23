-- openrouter-session plugin
--
-- PURPOSE
-- -------
-- This plugin logs a confirmation at the start of each turn so you can see
-- that the Go-side x-session-id injection machinery is active.
--
-- WHAT ACTUALLY INJECTS THE HEADER
-- ----------------------------------
-- HTTP header injection cannot be done from Lua hooks because Hygge's hook
-- events (pre_message, post_message, pre_tool, post_tool) fire inside the
-- agent logic, not at the HTTP transport layer.  The actual x-session-id
-- header is injected by a Go RoundTripper wired into the OpenRouter Fantasy
-- provider at construction time.  The transport reads the session ID from the
-- Go context (set by the agent's TurnContextDecorator before each Fantasy
-- turn) and resolves it to the root session ID via RootIDCache before setting
-- the header.
--
-- To enable the Go-side injection, wire ProviderBuildOptions.OpenRouterSessionCache
-- when constructing the OpenRouter provider via llm.ResolveProviderModelWith.
-- See internal/provider/openrouter/sessionheader.go and
-- internal/llm/provider_factory.go for the implementation.
--
-- WHAT THIS LUA PLUGIN DOES
-- ---------------------------
-- It registers a pre_message hook that logs the session ID at the start of
-- each turn.  This is useful for debugging: confirming which session is
-- active when a request fires, so you can correlate it with the root ID
-- visible in the OpenRouter dashboard.

hygge.register_hook("pre_message", {
    name    = "openrouter-session:pre_message",
    mode    = "sync",
    timeout = "2s",
}, function(event)
    if event.session_id ~= "" then
        hygge.log("info",
            "openrouter-session: turn starting; x-session-id will be injected by Go transport",
            { session_id = event.session_id })
    end
    return { decision = "allow" }
end)

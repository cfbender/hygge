# TODOs
- don't require top key [model], everything should live under modes except small_model
- implement pseudo-typing animation for responses, so each character fades in as they come in
- allow subagent config to per-profile, don't require subagents.toml, it can load from config.toml as well
- diagnose stuck tests that happen sometimes
- stream bash tool stdout instead of waiting to finish before displaying
- subagents should show the model/cost details per bubble like a normal agent message
- computer locking caused session to get into a bad state. sending a message treated it as queued but there was nothing running (I think main thread doesn't show working while subagent is running, could be part of the issue)

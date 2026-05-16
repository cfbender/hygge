# TODOs

- Add title, last user message, and last agent message to `hygge sessions list`
  - I want to be able to view these as an easy reference for each session when picking one to resume. Also slug seems to always be empty, maybe investigate that

- Fix bash click to expand in subagent view

- Subagent running doesn't seem to block the main thread from completing the same task. If a model says "dispatching the search agent to learn more" it seems to immediately do the same work itself as well
  - it seems to mostly do this when I click into the subagent. maybe the tool calls are leaking to the main chat?

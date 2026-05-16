# TODOs

- Subagent running doesn't seem to block the main thread from completing the same task. If a model says "dispatching the search agent to learn more" it seems to immediately do the same work itself as well
  - it seems to mostly do this when I click into the subagent. maybe the tool calls are leaking to the main chat?

- Add question tool like opencode's

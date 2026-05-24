# Changelog

All notable changes to Hygge are documented here.

Versions follow [Semantic Versioning](https://semver.org/).
This file is generated from `git log` using [git-cliff](https://git-cliff.org).
See [docs/releasing.md](docs/releasing.md) for maintainer instructions.

---

## [0.12.0] — 2026-05-24

### Bug Fixes

- **mcp:** Wire stdio stderr directly to ring buffer (#8) ([`1bc4f1c3`](../../commit/1bc4f1c38a25607b134c6b223ae10026dd3941cf))
- Dedupe replayed appended messages ([`d292d84c`](../../commit/d292d84cbe1123edb829af81425605d0599b846a))
- Keep parent transcript updating while viewing a subagent (#22) ([`dd06166f`](../../commit/dd06166fbe4f227b3a3c9c13c937d178cef8c8d9))
- Defer queued sends until assistant finalizes (#23) ([`d1cfdfda`](../../commit/d1cfdfda0c30ccfa471d9a6efe39d2514a4a7414))
- Refresh git branch display on send (#25) ([`0a179c64`](../../commit/0a179c64bdaab60acbdb4abc68760bcda8fecf91))

### Chores

- Todos ([`405a4746`](../../commit/405a4746ba0f6f34190e5d6db7fba18c2fe404b9))
- Increase mcp bootstrap timeout ([`74eef552`](../../commit/74eef552270cc93fc8526ac47f18d4bbe554529d))
- Update dependencies (#12) ([`8a25f555`](../../commit/8a25f55502bc9a2c8020bd7dc03faf0d4c4b7e2e))
- Changelog ([`2fb047b3`](../../commit/2fb047b3a3644b28b9cd45bd5edeb401024364e4))

### Features

- Persist always permissions per project (#17) ([`c40ac183`](../../commit/c40ac183416ec88d9b1163866c6297909d5499b1))
- Add built-in Hygge skill (#18) ([`5b44de97`](../../commit/5b44de9737a1b6ada9e4e58bbc7ba30ef0ef4bad))
- Hygge mcp add (#19) ([`24e1a539`](../../commit/24e1a539a14c02ea5bf67f5a3d2fdfe103214af7))
- Compact and context to envelope (#21) ([`04973664`](../../commit/04973664ab6c02e6d52a5ca4824dc18eaf38ef4c))

### Miscellaneous

- Fix dry-run Gemini onboarding (#9) ([`c04c9ec7`](../../commit/c04c9ec7dc2c824b67b85864ae371587ed87a91c))
- Fix MCP SSE and tool schema handling (#11) ([`afc725c9`](../../commit/afc725c940a0ff6bf09a71f7a3e3b61b272b7e2e))
- Remove legacy Anthropic and OpenAI provider shims (#10) ([`b3310fa9`](../../commit/b3310fa9aff747c6c271cb3ba07913c2fd420e32))
- HYGGE-8 Use Ctrl+G to leave subagent view (#15) ([`b0039c38`](../../commit/b0039c38007adcb9246706b9315bb7d764e599af))
- HYGGE-9 Remove permission edit option (#16) ([`2693c4c5`](../../commit/2693c4c54c4452083b0cf1b71527f1af4fe855e2))

### Refactoring

- Remove dead legacy code (#20) ([`c595729c`](../../commit/c595729c0b25e2df62c56afb349b2cce323db12b))

### Style

- Polish CLI inspection output (#24) ([`d2ce73f1`](../../commit/d2ce73f1c7e410123958bb66befe9b4e34529cd3))

### Tests

- Cover title model selection (#14) ([`c9e15089`](../../commit/c9e15089cc60fb70d51904ec546528d07cfc4ccb))


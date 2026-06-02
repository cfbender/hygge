# Changelog

All notable changes to Hygge are documented here.

Versions follow [Semantic Versioning](https://semver.org/).
This file is generated from `git log` using [git-cliff](https://git-cliff.org).
See [docs/releasing.md](docs/releasing.md) for maintainer instructions.

---

## [0.15.4] — 2026-06-02

### Chores

- Update dependencies (#65) ([`68fc2b78`](../../commit/68fc2b7879cfad64715d027a2086db9061affc38))
## [0.15.3] — 2026-05-28

### Chores

- Update deps ([`e8e58dcd`](../../commit/e8e58dcd8a4f043c4a2d4c09fcb91ed347435e75))
## [0.15.2] — 2026-05-28

### Features

- Support custom commands (#64) ([`27222952`](../../commit/272229520de7a7e1b4421ee22a8caf9c7842242a))
## [0.15.1] — 2026-05-28

### Features

- Compact mode (#62) ([`b0557f5b`](../../commit/b0557f5b23cf6a61cbc10e2399e3fef22c9b50aa))
## [0.15.0] — 2026-05-28

### Bug Fixes

- Route prompt generation through fantasy (#58) ([`581417fa`](../../commit/581417fab3a4c0730a7d959bc98c769d877c72e6))
- Use terminal colors for models command (#59) ([`3e8d86be`](../../commit/3e8d86be85ab27824e7004faa9326b612287c76a))
- Bound user message click zone (#60) ([`09584605`](../../commit/09584605874807ada8637ec917cd13ab58efdf75))

### Features

- Add favorite models (#55) ([`49195ceb`](../../commit/49195ceb4f419eb39bf48c4a7ac6e0cc4f8d0e7e))
- Better mode explanation (#57) ([`f1b79f24`](../../commit/f1b79f2464c433abfa4e249809e12c084841c369))
## [0.14.1] — 2026-05-27

### Bug Fixes

- Parse timestamps for expires at (#47) ([`ccacfc5b`](../../commit/ccacfc5ba13649fcd4781c9d70a5c6835a4dc599))
- Correct OpenRouter cost/context and add subagent split form (#49) ([`f2991ac0`](../../commit/f2991ac0d095968955c03a9cfff9e0ca705a4569))

### Style

- Smaller sidebar by two columns (#48) ([`06a8b239`](../../commit/06a8b239460e9352608de9a761e175d4a9f2e002))
## [0.14.0] — 2026-05-27

### Features

- Support OAuth for MCP servers (#45) ([`3d3a5529`](../../commit/3d3a552970160cd857d3f23cb9eedfc2a5abd1dc))
- Group shell permission approvals (#46) ([`d6d5617c`](../../commit/d6d5617c3ac13134ac29e2ef62a5acc6014dece3))
## [0.13.4] — 2026-05-26

### Bug Fixes

- Default profile from hygge.local.toml (#42) ([`edd19e6a`](../../commit/edd19e6a240973c14fe3783b675df03d46420ed0))

### Features

- Add nix flake (#41) ([`ca0d69d4`](../../commit/ca0d69d4fc34df38d646499beeff1806f6269bcb))
## [0.13.3] — 2026-05-25

### Bug Fixes

- Truncate permission modal tool details (#40) ([`31ed8b0b`](../../commit/31ed8b0b7ea4457a574d1ba7885ec89acd76ae55))
## [0.13.2] — 2026-05-25

### Features

- Avoid auth method pagination (#39) ([`4cf52a70`](../../commit/4cf52a70694f13351bdc212fdfa62f9adac0e365))
## [0.13.1] — 2026-05-25

### Chores

- Update go dependencies (#38) ([`8f8c45e2`](../../commit/8f8c45e2a447b2a335526c37f31e5b9b43e15f91))

### Features

- Add user message action modal (#37) ([`4601d78e`](../../commit/4601d78e883199faead0869beebb038cd5c8fcae))
## [0.13.0] — 2026-05-25

### Features

- Annotate lazy project context loading (#34) ([`99cf40ff`](../../commit/99cf40ffc9495cb5d2bf051dbe577d1eadea968a))
- Show session resume help on exit (#35) ([`46980d92`](../../commit/46980d92a63b8b58e08d51036752e968f4c36bc7))
- Support hygge.toml config files (#36) ([`9d2b52bf`](../../commit/9d2b52bfa4022c0dc6afc0014a664a4192f746ae))
## [0.12.4] — 2026-05-25

### Chores

- Update screenshot ([`b3996f1f`](../../commit/b3996f1f2b0f97ba1c83248bbc22fc608010a9bb))
- Add release script ([`46618e29`](../../commit/46618e29c2bb7a202f17c9082d80bd806b9607d2))
- Fix release script ([`3304cad8`](../../commit/3304cad89a49cea8347a3574b52d8e780b02feb8))
- Drop bump and changelog tasks ([`9aa70f9b`](../../commit/9aa70f9b44e77fb2f21b2a955a736f10eb443d88))
- Use usage params for release script ([`89c3698e`](../../commit/89c3698ea5b293e4650c7972aaeff9a8b3c4d91c))
- Allow CHANGELOG changes to bump script ([`42f1fc97`](../../commit/42f1fc978f333463141dd121853d5c60e12a79c4))

### Refactoring

- Improve config explain output (#33) ([`90dfcdcb`](../../commit/90dfcdcb60e65002408e1c2b1e0264d74342fca8))
## [0.12.3] — 2026-05-24

### Bug Fixes

- Keep slash model changes session-only (#31) ([`f8b85b9e`](../../commit/f8b85b9e67960003fa10b595bceee950a8d65c76))

### Chores

- Rectify version ([`60104be5`](../../commit/60104be598ef05efd101593f66d30869dbccf167))

### Features

- Clickable URLs (#28) ([`5def1d27`](../../commit/5def1d2746e93f6794926300a2901cded6fb0c8c))
- Profile directories (#29) ([`128cb09d`](../../commit/128cb09d98a25d383183dd7d1945038ef6296902))
- Add hygge logs command (#30) ([`0080a586`](../../commit/0080a586701dcbedc7898059b1f88adaafbc580d))

### Refactoring

- Drop top level model key (#27) ([`43b1f847`](../../commit/43b1f8479b8db1a40d2494f7913e5de5d89ff976))
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
## [0.11.0] — 2026-05-22

### Chores

- Set fps explicitly ([`468fa9e8`](../../commit/468fa9e85eb32799d33b81f5f3ff8e6c443145cb))
- Update gitignore ([`1dce3da9`](../../commit/1dce3da9f48c6d5ac91a27ca72303248d0596c56))
- Ignore .worktrees/ ([`4a746a1a`](../../commit/4a746a1a5beace24cf7323f3e783d697506105d2))

### Features

- Fog splash screen ([`07309f35`](../../commit/07309f35433c78eb96a800c27edb882e7f27b555))
- Test hygge skill ([`c9ce0190`](../../commit/c9ce0190f510daf2d6afaff5c7346885a70e9ea0))
- **ui:** Expand assistant thinking on click (#2) ([`f89670b4`](../../commit/f89670b435c5dc4a8e01c1d4b67e09fea3cb6189))
- **cli:** Add dry-run config preview (#3) ([`ad365d3e`](../../commit/ad365d3e8e1707ac66184f45c5a2f820b4e85635))
- **ui:** Split compact token usage (#5) ([`dbce188f`](../../commit/dbce188fef4499c63fff36349e11d1b6a8fd2709))
- Change spinner to fog line (#7) ([`a04e110f`](../../commit/a04e110f1cec5fcbae2f0beaf6141b878b46af99))

### Miscellaneous

- Add built-in iTerm themes (#6) ([`cbb8e787`](../../commit/cbb8e787b05c66910ae92be3304eabb22eecd12d))

### Refactoring

- Remove legacy theme package, consolidate on styles (#1) ([`4163b9e0`](../../commit/4163b9e02c2de148413b87690b4681718cb756cc))

### Style

- **ui:** Polish permission modal (#4) ([`79d8cbd1`](../../commit/79d8cbd1b5bf4233e3410fb73d4fb3b178c44bbe))
## [0.10.5] — 2026-05-21

### Features

- Unbuttoned mouse movement cancels scroll ([`3f7dac4c`](../../commit/3f7dac4c86a5cce56ea582c88525d91b5f1bc61d))
## [0.10.4] — 2026-05-20

### Features

- Scrollback limit ([`31fef863`](../../commit/31fef86356720327ba64f90b71e525b624df248d))
## [0.10.3] — 2026-05-20

### Bug Fixes

- **ui:** Bridge subagent handoff with a 'continuing…' placeholder ([`62eb1ed2`](../../commit/62eb1ed2bd2cb0f33547865501273d2d1af3a77c))
- Agent append garbled ([`c458083d`](../../commit/c458083d63f5d6906d64e2fa7efb06c37e5189fd))

### Features

- Continuation placeholder ([`b5b10f86`](../../commit/b5b10f8690626f4137abab4b7405768f70a48616))
## [0.10.2] — 2026-05-20

### Bug Fixes

- Diff truncation ([`29e981a2`](../../commit/29e981a21b63e371d4320e0db27f700d0919f14b))
## [0.10.1] — 2026-05-20

### Bug Fixes

- Hygge init colors and flow ([`f1e2d652`](../../commit/f1e2d652f310f73cd07259532636e105c6c0b327))
## [0.10.0] — 2026-05-20

### Bug Fixes

- **cli:** Accept modes-only model config ([`b5221f46`](../../commit/b5221f46d5dc95e8ab29db724f56dfc570e6ace7))
- **ui:** Buffer streaming response reveal ([`db16d007`](../../commit/db16d00788648e2c142ff3b835c44bbc76b4a77c))
- Subagents and model config ([`f2b6e7c6`](../../commit/f2b6e7c6982a7590322f8c78200ad5a6c17b0b60))
- **ui:** Show subagent model and cost metadata ([`6f8c4692`](../../commit/6f8c46928a5ce16045380a61e34888c0b1ccc32d))
- Subagent keeps main thread running ([`3310fea6`](../../commit/3310fea6725fcf934006567f1f12f6eb96acd7f6))
- Prevent animation ID collisions ([`640246b5`](../../commit/640246b558e77ccf0c9d1a4a224ab4d325a33f34))
- Subagent spinner ([`46b61378`](../../commit/46b6137829e5e21c1cee8a469fb60788c1c0248d))
- User message move on send ([`c7df86a8`](../../commit/c7df86a864203dce74218c1573d29b30374a59e0))
- Preserve side-by-side diff pairs when truncated ([`6b95147b`](../../commit/6b95147b170f147af221386a671afb6217fd3490))
- Cancel inflight send on session switch ([`f2a72cfc`](../../commit/f2a72cfc89b27a050318df52a26167a15cb0afd7))
- Prevent diff view overflow ([`fb61b0b4`](../../commit/fb61b0b4361bfc659907c82f818dfbf8275756b9))
- **ui:** Decode escaped tool targets ([`7f485ec1`](../../commit/7f485ec1bc1d8f411dff9da7b007130b2a8b5e22))
- **ui:** Show expand affordance for git diff output ([`735d5487`](../../commit/735d548711a54e99216a9205c444a8643856ada2))
- Git diff wrapping ([`ee438e98`](../../commit/ee438e98f0ad2285bdcbbd0b21dd90f728005d96))

### Features

- Hygge init ([`e0ea6e67`](../../commit/e0ea6e6743180d2cce1825ee42d15af5fb944115))
- **subagent:** Load types from config profiles ([`6db15a89`](../../commit/6db15a896409e69a04f6eebdc2385e76833f8240))
- **ui:** Fade in streaming response text ([`d3dcc4c4`](../../commit/d3dcc4c42ed78f02d5f7893a336dedfb768dba7e))
- Stream bash tool progress in UI ([`5a9bf58f`](../../commit/5a9bf58f12872375927e84ccdf74536a308265d8))
- Theme markdown rendering ([`6c5028a9`](../../commit/6c5028a951cf613ea38369674fdfb7525d3c7240))

### Refactoring

- **config:** Prefer modes for model selection ([`a64e87a4`](../../commit/a64e87a4da773e2a33b58f899bb8000a2fe2fb4c))

### Style

- Tone down diff additions ([`36031000`](../../commit/36031000602f482d743b40045948359517b5086b))
- Diff view ([`89c2422b`](../../commit/89c2422b375e7dee0e97eed78e38dcd91e446a78))

### Tests

- **mcp:** Bound fake server waits ([`1bf766bb`](../../commit/1bf766bb6569af41d20ea477bc30436df197ab25))
- **tool:** Guard subagent final response payload ([`b1c5e083`](../../commit/b1c5e083500b8e47c63439cc2b2800ca0638e5fd))
## [0.9.5] — 2026-05-20

### Bug Fixes

- **ui:** Show project-relative tool paths ([`7b034747`](../../commit/7b03474735503497fd60039fa6210761857ef24d))
- **permission:** Deny .aiexclude file access ([`f3e87989`](../../commit/f3e87989d22ade66229f7f36a186483753a06e29))

### Documentation

- Aiexclude ([`7503f0c5`](../../commit/7503f0c50dbe4f84dc5d7bf466f7af815c7e8a3d))
## [0.9.4] — 2026-05-20

### Features

- Lua plugin dir expose ([`8e86abc5`](../../commit/8e86abc5160608f01c91c7c8011e6d4897f6f7a6))
## [0.9.3] — 2026-05-20

### Chores

- Update readme ([`003677ac`](../../commit/003677ac84292826b11cf88f8096c885d551544a))
- Bump catwalk ([`1d9aac82`](../../commit/1d9aac82e8395f17897405e9b586d4434b67179c))
## [0.9.2] — 2026-05-19

### Bug Fixes

- Skip onboarding auth for configured providers ([`2c71b9da`](../../commit/2c71b9da9298c89f9af89ad07813309b3a529d07))
- Allow pasting onboarding API keys ([`7a0c948d`](../../commit/7a0c948d998cde9134ea73e695f18bacf78d9410))
- Use ansi colors in mac terminal ([`81480378`](../../commit/8148037899cb4515f3e5bb1a3e0ec1b4882d233e))

### Chores

- Update readme for mise binary install ([`8bc86c8a`](../../commit/8bc86c8aa63ab6614ac9a6af54272882d19d1c8c))
## [0.9.1] — 2026-05-18

### Bug Fixes

- Providers list ([`a4cdfeae`](../../commit/a4cdfeae0418cd8ab1161b685278f395dd1cf9c6))

### Chores

- **deps:** Bump catwalk to v0.41.0 ([`b46f865f`](../../commit/b46f865f94a331ec5580f17829bbeef7e9fb38da))
- **deps:** Bump fantasy to v0.25.0 ([`e133b5af`](../../commit/e133b5afde983fe214ace91818dee0acec2d5478))
- **deps:** Bump ultraviolet ([`ab9f081b`](../../commit/ab9f081bcb8da3785800f9dc20411634b88c461f))

### Features

- **cli:** Polish help models and provider auth ([`797cba5c`](../../commit/797cba5c6574d45c2f58b3e87941b809d9116192))
## [0.9.0] — 2026-05-18

### Bug Fixes

- **config:** Validate subagent permission mode ([`f46a4650`](../../commit/f46a46501bd3b723e05b592b50cf143a9f0411bd))
- **permission:** Apply always approvals immediately ([`8c77ab28`](../../commit/8c77ab2832532b9279ab897ef169a31f23ea6aec))
- Attachments ([`1527e21b`](../../commit/1527e21bc923582b9d0d974f6f1e53768ea9f15f))

### Chores

- Todos ([`a5bd175a`](../../commit/a5bd175a1847f21cca711b69d5f854c03a084001))

### Features

- **permission:** Show permission request rationale ([`aaaa2f7e`](../../commit/aaaa2f7efc1e71c753ad81bb432eb9ffd5592da4))

### Refactoring

- Split ui app responsibilities ([`b0a2d97e`](../../commit/b0a2d97e5f17808fd0cd678271dc2fec507665fe))

### Tests

- **permission:** Cover engine edge cases ([`ce8800b5`](../../commit/ce8800b5d63deef1f707a2f21b9e305240babf4d))
## [0.8.0] — 2026-05-18

### Features

- Webfetch ([`d08425f1`](../../commit/d08425f1892c31c1c9e5976ce8411630d1e5cf5d))
## [0.7.0] — 2026-05-18

### Bug Fixes

- Stabilize sqlite store connections ([`133a3049`](../../commit/133a3049428bdf62132c617aef88ad82eb8730f3))
- Show subagent initial prompt ([`5daa9f20`](../../commit/5daa9f203148b06cb09232f564a0842755fd43b5))
- Scroll completion palettes ([`7a8dbe86`](../../commit/7a8dbe867c1b6fd18af63062bf7ac7d600a2a552))
- Keep assistant thinking out of prompt history ([`961e4483`](../../commit/961e4483ebc608e88a3c9162ffcd403f61ba41ce))
- Apply go fix to palette scrolling ([`afafaf1f`](../../commit/afafaf1f35e3ed8a393ee90e62c184a3e1003ad4))
- Scroll completion palettes with mouse wheel ([`4abd4c6c`](../../commit/4abd4c6ce8556c80fcdb74e07f3a6f7f618f6dd2))

### Chores

- Update TODOS ([`3c8eb5fd`](../../commit/3c8eb5fddf8c8f265ebc5b893e69a1b940488dd6))
- Add todo ([`2f240b87`](../../commit/2f240b87cf514ef09e8a15c541dbefd8e74b5aff))

### Documentation

- Mark completed todos ([`6fae47af`](../../commit/6fae47afdb2dcdd04f10659c40540a57b8807952))

### Features

- Steer active turns by default ([`216f8cf2`](../../commit/216f8cf289a08d5f717071ffb5ca973da99c9ccb))
- Onboarding tui ([`5ef9b304`](../../commit/5ef9b30423977e2aa016b5a45287c7bb49424cc1))
- Queue busy sends and add steer command ([`49805817`](../../commit/498058172a1b7b07b5373c997904f6b7ae61aaa4))
- /steer ([`e3a9adb4`](../../commit/e3a9adb40d3baa058b75986f01d0a46fc30c95db))

### Refactoring

- Prepare fantasy steps explicitly ([`bb08f752`](../../commit/bb08f752073e39e47bf75633ba8f85a6c93b6689))
## [0.6.0] — 2026-05-18

### Bug Fixes

- **ui:** Remove todo notice pill ([`c4387764`](../../commit/c43877645f1b728d56567b7db9157f4cec399656))
- **ui:** Clear stale render state on resize ([`8d8da43e`](../../commit/8d8da43ec107b8c1f33eff67916f074ad73883de))
- **ui:** Flex footer metadata within width ([`e85fdfa6`](../../commit/e85fdfa69edc3d8bc1baa6d20805dba5cbb07977))
- Models show across profiles ([`ca930abb`](../../commit/ca930abb93478122f1a8c592759779c01e099a4f))

### Chores

- Clear completed todos ([`7e9f7858`](../../commit/7e9f7858eb95da687693fa323d25d49b7c3044b9))

### Features

- **ui:** Expand diff previews on click ([`649763f4`](../../commit/649763f47f5a9fe1cdef4f1f9756dfe0d43cab38))
- **ui:** Attach pasted image paths ([`9579ba07`](../../commit/9579ba0750c201a5e7e427936bedc83db395513e))
- Image pasting ([`98ebff91`](../../commit/98ebff9127b045b812077246a34c11445dad7c3e))
- Hygge onboard ([`91deef01`](../../commit/91deef01d9c72facdd37e4b7f5fad9be982aab95))
## [0.5.1] — 2026-05-18

### Bug Fixes

- Only require provider for tui ([`78668ac1`](../../commit/78668ac13251fcba67800e1c53aaa949a95a706b))
- Simplify side-by-side diff separator ([`79bf873f`](../../commit/79bf873fc6f582e4c958c1ca52aab033e86d2a4d))
- Add top spacing to message viewport ([`68516abf`](../../commit/68516abf25f353fee5107a43d83e96abeb56ee65))
- Wrap sidebar todo titles ([`6f4cc077`](../../commit/6f4cc077649df29e5a86f6866fece34be191bbc8))

### Chores

- Update readme ([`7dc906ca`](../../commit/7dc906ca10355bb07022f51f0f8787055b710199))
- Todos and footer style ([`5aafffda`](../../commit/5aafffda7b8dfb3d725cf48694460b053f7ce550))
- Update todos ([`e0070c67`](../../commit/e0070c678efdd1a7837d1722b74af41e8c291a08))

### Features

- Add side-by-side diff view ([`28e86657`](../../commit/28e86657ed2e1a014c5577d041b1e77dc3e22c44))
## [0.5.0] — 2026-05-17

### Chores

- Drop legacy loop code ([`c2d6fb73`](../../commit/c2d6fb73c45f13a82ed868aa53d9dd2eeebf0edb))

### Features

- Load plugins from config ([`2ab9bc74`](../../commit/2ab9bc7421a1e899ed36ddfce55f20a266d9c75c))
- Stub out user request wrapping ([`2c13290d`](../../commit/2c13290df4ed6183bd29a631f9c687dd177ad208))
- Add session memories ([`45763909`](../../commit/4576390970e4e2b7007c9eb76523c2ecb7f9abde))
- Add file-backed memories ([`e5a3bab9`](../../commit/e5a3bab99f71388b84721e4b9c238d36253d6156))
- Warn on unignored project memories ([`1d6c9bc6`](../../commit/1d6c9bc66667ff48ad769253e26f20cef765d930))
- Add memory tools ([`919adfe9`](../../commit/919adfe989affa130ba23ddd0d3435bf630d3d86))
- Add memory management UI ([`4206c01f`](../../commit/4206c01f3baa80bd82c936d7eb087beddf0bdea6))
- Add autonomous memory controls ([`28918f0d`](../../commit/28918f0df5911df41a4a6f44a20e50a8059d684b))
## [0.4.3] — 2026-05-17

### Bug Fixes

- Fresh install problems ([`495c6bf1`](../../commit/495c6bf1d297402fd63a8191e4bcc4c6347c35f5))
## [0.4.2] — 2026-05-16

### Chores

- Capture startup logs ([`8a8c6a44`](../../commit/8a8c6a44739a4cf8de99af1c4c85ae5cc5add2b6))

### Features

- Add hidden hook prompt context ([`aae058ac`](../../commit/aae058ac1c016cf2eac735ac043aee7989303759))
- Lua table helpers ([`fb619fa1`](../../commit/fb619fa15cac844b9172a4d366abedca00cbd3aa))
## [0.4.1] — 2026-05-16

### Chores

- Add quorum to example plugins ([`d6900386`](../../commit/d69003864b71bb52189170c8e702cd9f36d7c52e))

### Features

- Luals types ([`0761044e`](../../commit/0761044e4c6aaa9b034ca340b12d40ca9dd30b3a))
## [0.4.0] — 2026-05-16

### Bug Fixes

- Persist plugin source changes ([`930220a9`](../../commit/930220a9d8d79c011aa6924fa7a4512469fbd602))
- Unregister plugin-owned registrations ([`d185bc41`](../../commit/d185bc41a90e969892ec2cd37995263ded34cb81))
- Render expanded tool blocks standalone ([`66df9a20`](../../commit/66df9a202b456a554b9bf91c57f90f8ebd89af73))
- Title generation ([`b48e816c`](../../commit/b48e816c4cd8f212cb48d9c8494c860e60c0e878))
- Expand subagent bash tool output ([`66301343`](../../commit/66301343b24bd127d465287491754fd21cd92480))
- Keep foreground subagent events isolated ([`6d02dd58`](../../commit/6d02dd584b7f16b06f64daa96067c54ae27a075f))

### CI

- Bump actions version ([`e9e8953a`](../../commit/e9e8953a7ce0eacb6f2f4549aadb3ffa2421630f))

### Chores

- Readme pronunciation ([`651df788`](../../commit/651df78886cb5aa7d546af4e2dd9ddbeea868580))
- Move pronunciation ([`c1340500`](../../commit/c134050075aef258fd124750ecf2cab9f6309a55))
- Readme screenshot ([`cf31cb6c`](../../commit/cf31cb6c0d02e73fa6058fc7f39ecfd5ce546848))
- Mark completed todos ([`cf738eeb`](../../commit/cf738eeb2cd877b5c072885ec33690e70223355a))
- Title fixes ([`ae1be4ec`](../../commit/ae1be4ecab00e685c29bded6db5417a2a94ea432))
- Fix lint warnings ([`2c85856e`](../../commit/2c85856e6c4b4f989f9a2a54f0d20e2f86c96434))

### Features

- Generate session titles with small model ([`c1db6fd2`](../../commit/c1db6fd2c23a7540c8967150243d394253489551))
- Persist subagent parent tool ids ([`c631b5dc`](../../commit/c631b5dc6cdc33a6483995b361bbf9321352d8e0))
- Enrich sessions list output ([`553e8540`](../../commit/553e8540a2c8781ce8d37a14de5ccefe111878e1))
- Add interactive question tool ([`e8df29e1`](../../commit/e8df29e14706d8bf8223850fac276c11a30185f5))
- Question tool fixes ([`6f80bfd4`](../../commit/6f80bfd431007e9aa444b8dcf3ce95f46a0ad39f))

### Refactoring

- Share noninteractive git runner ([`bc64ff84`](../../commit/bc64ff84dff54514920a16d5459fe31fb4d9b7e5))
## [0.3.4] — 2026-05-16

### Bug Fixes

- Stderr hold and bash pipe ([`7b394db5`](../../commit/7b394db5b803ff05bbbe15b9db5be2def244696d))
- Skill discovery and usage ([`819607ba`](../../commit/819607ba144709b05f807ac4db2551cd6811ba19))
- Usage ([`ab67ef36`](../../commit/ab67ef3612ce7600b5579c554bff1c28b1f37db0))
- System prompt per mode ([`a39922d0`](../../commit/a39922d04cb96fe754b40592bf93b7ac181ad168))
- Message cutoff ([`b62efca4`](../../commit/b62efca4efe204b4b170a24ae97bd61784dfb5c8))

### CI

- Test failures ([`4f965d1c`](../../commit/4f965d1cb982eb9a9f49b421378424ecb6cfa31a))
- Bump actions version ([`c0775d00`](../../commit/c0775d003c12ba4914c68d931d3f72e43a6ec70f))

### Documentation

- Cleanup ([`fb94c32f`](../../commit/fb94c32f66fbc120677cb00122eddaa0995b346f))

### Features

- Skill name display ([`a962eb66`](../../commit/a962eb66066756ded6d86dd667839cc1a6336059))

### Miscellaneous

- Fix compaction marker hydration ordering ([`f594b28b`](../../commit/f594b28bfc068c29d08925c8237bf12684a154b7))
## [0.3.3] — 2026-05-16

### Features

- Bump script ([`e2c92198`](../../commit/e2c92198fdd5f70f07ddd19e72761705722b084b))
## [0.3.2] — 2026-05-16

### Chores

- Switch info log to debug ([`03dc8905`](../../commit/03dc8905eb8ee396b9a0659eb32a95d6cd6ce3a5))
## [0.3.1] — 2026-05-16

### Bug Fixes

- Support root go install ([`8829994c`](../../commit/8829994c46d5a833335dc47455f1629e9c0de3a8))


# Hygge

A fast, beautiful, plugin-extensible TUI.

## Build

Requires [mise](https://mise.jdx.dev) and [golangci-lint](https://golangci-lint.run/welcome/install/).

```sh
mise install          # installs the Go toolchain pinned in .mise.toml
mise run build        # compiles to ./bin/hygge
mise run test         # runs tests with race detector
mise run lint         # runs golangci-lint
mise run run          # builds and runs
```

## License

MIT — see [LICENSE](LICENSE).

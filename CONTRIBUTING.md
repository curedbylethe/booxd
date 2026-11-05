# Contributing

booxd is a Go CLI/TUI for Anna's Archive. Read [AGENTS.md](AGENTS.md) for repository rules before changing code.

## Prerequisites

- Go 1.25 or newer.
- Keep builds cgo-free with `CGO_ENABLED=0`; Android arm64 must cross-compile.
- For screenshot regeneration: tmux, Python 3, Pillow, and a monospace font.

## Build and verify

```sh
make build
make android
make test
make vet
make lint
make ci
```

`make build` writes `dist/booxd`. `make android` writes `dist/booxd-android-arm64` with cgo disabled. `make ci` runs build, vet, tests, and lint. Install golangci-lint v2.15.0 if `make lint` reports it missing:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
```

The CI workflow runs build, vet, tests, installer checks, and lint on Linux. It also builds, vets, tests, and checks the Windows installer on Windows.

## Package layout

| Package | Responsibility |
| --- | --- |
| `internal/cache` | Stores search results on disk. |
| `internal/config` | Loads configuration paths, defaults, fields, and environment overrides. |
| `internal/down` | Loads cookies and performs authenticated, resumable downloads through `internal/fetch`. |
| `internal/fetch` | Owns HTTP requests, streaming, cookies, timeouts, host serialization, retry state, and cache lookup. |
| `internal/mirror` | Probes mirrors and persists selection and cooldowns. |
| `internal/parse` | Converts HTML into search records and book details without network access. |
| `internal/tui` | Runs the tcell screen and local book navigation; the caller supplies search. |

## Releases and installer checks

Pushing a `v*` tag starts [the release workflow](.github/workflows/release.yml). It verifies, builds, and smoke-tests six targets (macOS, Linux, Windows, and Android arm64), then publishes archives, `SHA256SUMS`, `install.sh`, and `install.ps1`.

Run the installer checks locally where supported:

```sh
./install_test.sh
pwsh ./install_test.ps1
```

CI runs the shell check on Linux and the PowerShell check on Windows.

## TUI screenshots

Regenerate the committed search and detail screenshots from the running TUI:

```sh
python3 scripts/tui-screenshot.py
```

The script uses tmux and Pillow. It is a developer tool, not part of the booxd binary.

## Changes and documentation

Use Conventional Commits with a lowercase imperative summary, for example `docs(search): clarify cached output` or `fix(fetch): preserve retry state`. Update documentation in the same commit as the behavior it describes.

See [README.md](README.md) for user instructions, [docs/eink.md](docs/eink.md) for redraw rules, and [docs/termux.md](docs/termux.md) for Boox setup. [AGENTS.md](AGENTS.md) contains hard repository constraints. See the [project conventions](https://linear.app/curedbylethe/document/booxd-project-conventions-bf3f21ac3632) for issue structure and product rules.

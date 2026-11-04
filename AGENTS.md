# AGENTS.md

Hard constraints for agents and contributors working on booxd. Everything else
lives in [README.md](README.md) and the
[project conventions](https://linear.app/curedbylethe/document/booxd-project-conventions-bf3f21ac3632).

## Toolchain

- Go 1.25 or newer, as declared in `go.mod`.
- One binary, built with `CGO_ENABLED=0`.
- Android arm64 is the target that cross-compilation must preserve; no daemon or APK.

## Rules

1. Never use cgo; the binary must cross-compile for Android arm64.
2. Every HTTP request goes through `internal/fetch`; no other package builds its own HTTP client.
3. Allow one in-flight request per host, respect HTTP 429, and check the cache before fetching.
4. `internal/parse` never touches the network, and `internal/tui` never builds a request.
5. Use deliberate full-frame redraws only: no spinner, cursor blink, animation, or colour-only meaning.
6. Downloads require the site's account gate; reuse a supplied session cookie, report when it is missing, and never attempt to obtain one.
7. Run `make ci` before every commit.
8. Documentation describes what exists; update it in the same commit as the behavior it describes.
9. Use Conventional Commits: `type(scope): lowercase imperative summary`, with scope optional.

## Commands

| Command | Purpose |
| -- | -- |
| `make build` | Build `dist/booxd` without cgo. |
| `make vet` | Run Go vet on every package. |
| `make test` | Run Go tests on every package. |
| `make lint` | Run golangci-lint; print an installation command when missing. |
| `make ci` | Run build, vet, test, and lint. |
| `make clean` | Remove `dist/`. |

## Where the rest of it lives

- [README.md](README.md): current command behavior, build instructions, and package map.
- [Makefile](Makefile): local verification commands.
- [.golangci.yml](.golangci.yml): linter configuration.
- [Project conventions](https://linear.app/curedbylethe/document/booxd-project-conventions-bf3f21ac3632): authoritative issue structure and product constraints.

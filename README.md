# booxd

A terminal client for Anna's Archive, intended for Boox e-readers through Termux.
This repository is the skeleton for searching, inspecting, and fetching books.

## Status

All five commands are stubs. They print "not implemented yet" and exit 1; nothing
talks to the network. Mirror discovery, parsing, downloads, and the TUI come later.
No arguments prints usage and exits 2; an unknown command also exits 2.

## Commands

| Command | Output today |
| -- | -- |
| `booxd search` | `booxd: search is not implemented yet` |
| `booxd get` | `booxd: get is not implemented yet` |
| `booxd show` | `booxd: show is not implemented yet` |
| `booxd tui` | `booxd: tui is not implemented yet` |
| `booxd serve` | `booxd: serve is not implemented yet` |

## Building

Use Go 1.25 or newer. `make build` produces `dist/booxd` with `CGO_ENABLED=0`.

## Testing and linting

Run `make test`, `make vet`, or `make lint` separately. `make ci` runs build, vet,
test, and lint. Install the linter first:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
```

Ensure Go's binary installation directory is on `PATH`.

## Layout

These package skeletons document their future responsibilities:

- `internal/mirror`: mirror seeds, probing, selected-mirror persistence, and cooldown.
- `internal/fetch`: the single HTTP client, timeouts, per-host serialization, `per_host_interval`, retry/backoff, and cookie jar.
- `internal/parse`: HTML into records, without network access.
- `internal/cache`: disk query cache keys, timestamps, TTL, atomic writes, and sweep.
- `internal/down`: gated downloads, `.part` resume, size checks, and atomic renames using fetch.
- `internal/tui`: the tcell screen, input, and redraw using parse, cache, and mirror results.

## Documentation

See [AGENTS.md](AGENTS.md) for repository constraints and
[project conventions](https://linear.app/curedbylethe/document/booxd-project-conventions-bf3f21ac3632)
for backlog structure.

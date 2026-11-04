# booxd

A terminal client for Anna's Archive, intended for Boox e-readers through Termux.
This repository is the skeleton for searching, inspecting, and fetching books.

## Status

`search` discovers a mirror, then prints "not implemented yet" and exits 1. It
does not fetch search results yet. The other four commands remain stubs: they print
"not implemented yet" and exit 1. Parsing, downloads, and the TUI come later.
No arguments prints usage and exits 2; an unknown command also exits 2.
Requests to a host run one at a time, with at least one second between starts.
The client waits after a 429 and retries a 5xx twice. `--verbose` logs attempts.

## Commands

| Command | Output today |
| -- | -- |
| `booxd search [--verbose]` | `mirror: <url>` on stderr, then `booxd: search is not implemented yet` on stdout; exit 1 |
| `booxd get` | `booxd: get is not implemented yet` |
| `booxd show` | `booxd: show is not implemented yet` |
| `booxd tui` | `booxd: tui is not implemented yet` |
| `booxd serve` | `booxd: serve is not implemented yet` |

`booxd search --mirror URL` uses `URL` and skips discovery entirely. Put flags
before query arguments; `booxd search -h` shows search usage and exits 2.
`booxd search --verbose` logs host, status, and wait for each attempt to stderr.

Discovery probes embedded seeds in order with a five-second timeout per seed. The
first passing mirror is reused for 24 hours without probing. A 403, 404, or timeout
puts that seed on cooldown for one hour. Selection and cooldowns persist in
`booxd/mirror.json` under the platform user cache directory: `$XDG_CACHE_HOME` or
`$HOME/.cache` on Termux, and `$HOME/Library/Caches` on macOS.

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

Implemented packages:

- `internal/mirror`: mirror seeds, probing, selected-mirror persistence, and cooldown.
- `internal/fetch`: the shared HTTP client, per-host throttling, retry/backoff, and cache lookup. A cookie jar remains planned.

Package skeletons and their planned responsibilities:

- `internal/parse`: HTML into records, without network access.
- `internal/cache`: disk query cache keys, timestamps, TTL, atomic writes, and sweep.
- `internal/down`: gated downloads, `.part` resume, size checks, and atomic renames using fetch.
- `internal/tui`: the tcell screen, input, and redraw using parse, cache, and mirror results.

## Documentation

See [AGENTS.md](AGENTS.md) for repository constraints and
[project conventions](https://linear.app/curedbylethe/document/booxd-project-conventions-bf3f21ac3632)
for backlog structure.

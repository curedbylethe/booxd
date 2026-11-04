# booxd

A terminal client for Anna's Archive, intended for Boox e-readers through Termux.
This repository is the skeleton for searching, inspecting, and fetching books.

## Status

`search` loads optional configuration from `config.toml`, fetches one results
page, and prints parsed book records. `show` fetches a book page and prints its
metadata and download options. `tui` opens an interactive search screen with a
result list and local detail pane. Search supports `--page`, `--dump-html`, and a
disk cache with a 24-hour TTL. Cached output prints
`cached <age> ago` on stderr. Parsing lives in `internal/parse` and never accesses
the network. `get` downloads one book when supplied with a session cookie file.
`serve` remains a stub: it prints "not implemented yet" and exits 1.
No arguments prints usage and exits 2; an unknown command also exits 2.
Requests to a host run one at a time, with at least one second between starts.
The client waits after a 429 and retries a 5xx twice. `--verbose` logs attempts.

## Commands

| Command | Output today |
| -- | -- |
| `booxd search [--config PATH] [--mirror URL] [--page N] [--dump-html PATH] [--no-cache] [--max-age DURATION] <query>` | `mirror: <url>` on stderr when using network; one tab-separated row per book on stdout; cached rows print `cached <age> ago` on stderr; `no results` on stderr and exit 0 for a miss; exit 1 for fetch or parse failure |
| `booxd get [--config PATH] [--cookies PATH] [--mirror URL] [--verbose] [--open] <id>` | Uses the supplied session to fetch the book page and first open download option; refuses without `--cookies` and prints the book URL; progress prints whole percentages on stderr; --open opens the finished file with termux-open and a missing command prints one error line; exits 0 when saved/already present, 1 on download or open failure, 2 on usage or missing-session refusal |
| `booxd show [--config PATH] [--mirror URL] [--verbose] <id>` | nine labeled metadata lines and download options; all-gated pages print `downloads require a signed-in session`; exit 1 for config, resolve, fetch, status, or parse failure; exit 2 for usage errors |
| `booxd tui [--mono]` | Interactive search line, result table, local detail pane, and status line; Enter searches or opens details, `j`/`k` or arrows move, `/` edits query, Esc goes back, `?` shows help, and `q` quits outside query entry |
| `booxd serve` | `booxd: serve is not implemented yet` |

`booxd search --config PATH` reads `PATH` instead of the default config file.
The TUI's `--mono` flag disables colour; any non-empty `NO_COLOR` value does the same.
`booxd search --mirror URL` uses `URL` and skips discovery entirely. Put flags
before query arguments; multiple words are joined with spaces. Page defaults to 1.
`--dump-html PATH` writes the raw response body before parsing, or the cached body
on a cache hit. A missing query or
page below 1 exits 2; `booxd search -h` shows search usage and exits 2.
The query cache lives at `<cache_dir>/booxd/cache`; `--no-cache` skips reads and
`--max-age DURATION` overrides the default `24h` freshness window. If mirror
resolution or fetching fails, search serves the last cached page with its age
marker. Cache entries older than 30 days are swept at search startup.
`booxd search --verbose` logs host, status, and wait for each attempt to stderr.
It also reports waits before they begin.

## Show

`booxd show <id>` fetches `/books/<id>`; `booxd show <id>-<slug>` fetches the
slug path as given. It prints title, author, publisher, year, language, format,
size, ISBN, and pages. Missing fields print `-`. Open download options are
listed; gated options carry ` (requires signed-in session)`. If every option is
gated, it prints `downloads require a signed-in session`.

Successful detail pages are cached for 24 hours under
`<cache_dir>/booxd/cache`, keyed by `book:<id>`. A fresh cache hit makes no
request and prints no `mirror:` line. A stale entry is fetched again. `show`
does not use or obtain a cookie; it reports the account gate and exits 0.

## Get

`booxd get --cookies cookies.txt <id>` loads a Netscape cookie file and reuses
the session for the fresh book-page request and the first open download option.
The page is never read from or written to the cache. Without `--cookies`, `get`
resolves the mirror, prints the book URL, explains that a signed-in session is
needed, and exits 2. A gated page or a 401, 403, or 407 stops with one error; `get`
does not try another download option.
If the cookie file has no cookies for the resolved mirror host, `get` reports
that mismatch and suggests `--mirror` so you can select a mirror matching the
cookie domain.

Files go to `download_dir`. The transfer keeps interrupted data in `<name>.part`,
sends `Range` when resuming, checks the response size against `Content-Length`,
and renames the completed file atomically. Progress prints whole percentages,
one per line on stderr. If the server omits `Content-Length`, `get` skips progress
and the size check. The configured `request_timeout` bounds the book-page
request. Download response headers have a 30-second wait limit, but the body
transfer has no overall timeout. Existing files are kept and reported as already
downloaded.

In Termux, `--open` runs `termux-open` after saving the file or finding it already
downloaded. Android opens the default handler for that file type. If the command
is missing, `get` prints a clear error on stderr and exits 1; the file remains saved.

## Configuration

The default file is `booxd/config.toml` under the platform config directory:
`$XDG_CONFIG_HOME` or `$HOME/.config` on Termux, and `$HOME/Library/Application Support`
on macOS. `--config` and `BOOXD_CONFIG` select another file. A missing default
file uses defaults; a missing explicitly selected file is an error.

Supported fields are `mirrors` (TOML string array; embedded seeds by default),
`download_dir` (`$HOME/Downloads`), `cache_dir` (platform cache directory),
`proxy` (empty), `user_agent` (`booxd/0.1`), `request_timeout` (`30s`), and
`per_host_interval` (`1s`). Durations use Go duration strings. Proxy accepts
`http`, `https`, and `socks5` URLs. Termux users may set `download_dir` to
`$HOME/storage/downloads` after enabling shared storage.

An empty `mirrors` list keeps the embedded seeds. Environment overrides use
`BOOXD_MIRRORS` (comma-separated), `BOOXD_DOWNLOAD_DIR`, `BOOXD_CACHE_DIR`,
`BOOXD_PROXY`, `BOOXD_USER_AGENT`,
`BOOXD_REQUEST_TIMEOUT`, and `BOOXD_PER_HOST_INTERVAL`. Precedence is flag,
environment, file, then compiled default. A malformed file value prints one
line naming the file and field; a malformed environment variable names the
variable.

Discovery probes embedded seeds in order with a five-second timeout per seed. The
first passing mirror is reused for 24 hours without probing. A 403, 404, or timeout
puts that seed on cooldown for one hour. If every probe fails, the error lists
each attempted mirror with its failure reason (network error, HTTP status, or
unrecognized results page). Selection and cooldowns persist in
`booxd/mirror.json` under `cache_dir`, defaulting to the platform user cache directory: `$XDG_CACHE_HOME` or
`$HOME/.cache` on Termux, and `$HOME/Library/Caches` on macOS.

## Building

Use Go 1.25 or newer. `make build` produces `dist/booxd` with `CGO_ENABLED=0`.
`make android` produces `dist/booxd-android-arm64` for Termux on Android arm64.

## Testing and linting

Run `make test`, `make vet`, or `make lint` separately. `make ci` runs build, vet,
test, and lint. Install the linter first:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
```

Ensure Go's binary installation directory is on `PATH`.

## Layout

Implemented packages:

- `internal/config`: config path, defaults, TOML loading, and environment overrides.
- `internal/mirror`: mirror seeds, probing, selected-mirror persistence, and cooldown.
- `internal/fetch`: the shared HTTP client, streaming responses, cookie jar, per-host throttling, retry/backoff, and cache lookup. On Android, DNS uses the first valid nameserver IP in `$PREFIX/etc/resolv.conf` (default prefix `/data/data/com.termux/files/usr`), falling back to `8.8.8.8` if the file is missing or has no valid nameserver.
- `internal/cache`: disk query cache with normalized keys, atomic JSON writes, TTL reads, and expiry sweep.
- `internal/parse`: search and detail HTML into book metadata and download
  options, without network access.
- `internal/tui`: tcell input, local book navigation, and deliberate full-frame
  redraws; search runs through an injected function.

- `internal/down`: Netscape cookie loading, gated downloads, `.part` resume, size checks, progress lines, bounded redirects, and atomic renames using fetch.

## Documentation

- [docs/eink.md](docs/eink.md): the e-ink redraw policy and the reason for each rule.
- [docs/termux.md](docs/termux.md): installing booxd in Termux on the Boox, and why there is no daemon.

See [AGENTS.md](AGENTS.md) for repository constraints and
[project conventions](https://linear.app/curedbylethe/document/booxd-project-conventions-bf3f21ac3632)
for backlog structure.

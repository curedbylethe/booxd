// Package fetch owns the single HTTP client, timeouts, per-host serialization,
// per_host_interval, retry/backoff, and cookie jar; every request goes through it.
// It must not import HTML parsing or CLI code.
package fetch

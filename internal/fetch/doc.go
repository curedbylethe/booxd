// Package fetch owns the single HTTP client, streaming responses, cookie jar,
// timeouts, per-host serialization, retry/backoff, and cache lookup. Every
// request goes through it.
// It must not import HTML parsing or CLI code.
package fetch

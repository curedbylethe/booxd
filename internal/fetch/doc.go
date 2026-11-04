// Package fetch owns the single HTTP client, streaming responses, cookie jar,
// timeouts, per-host serialization, retry/backoff, and cache lookup. Every
// request goes through it.
// WithTimeout(0) disables the whole-request timeout but keeps a DefaultTimeout
// limit for response headers.
// It must not import HTML parsing or CLI code.
// It also maintains persisted 429 state.
package fetch

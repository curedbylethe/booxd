// Package update checks for a newer booxd release, remembers the last check,
// installs a downloaded release, and restarts the process.
//
// Lookup goes through internal/fetch and the answer lives in internal/cache, so
// this package builds no client, opens no store, and never writes to the
// terminal.
package update

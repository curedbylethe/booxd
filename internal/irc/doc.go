// Package irc owns the IRC connection, the parser, and DCC SEND detection.
// It performs no HTTP; every HTTP request belongs to internal/fetch.
//
// Hostname resolution on Android arm64 needs the resolver hook that
// internal/fetch owns, so a device build must pass a DialFunc.
// Call ReleaseSearch with the token Say returned to free the in-flight slot
// when a search ends without a bot reply; the slot is freed only while that
// token still owns it.
package irc

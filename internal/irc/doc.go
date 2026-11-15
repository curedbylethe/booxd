// Package irc owns the IRC connection, the parser, and DCC SEND detection.
// It performs no HTTP; every HTTP request belongs to internal/fetch.
//
// Hostname resolution on Android arm64 needs the resolver hook that
// internal/fetch owns, so a device build must pass a DialFunc.
package irc

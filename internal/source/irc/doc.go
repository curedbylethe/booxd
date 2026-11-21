// Package irc turns an @search answer and a DCC offer into books: it parses
// one result line, scans the offered archive, sends the search, and streams
// a fetched book to a .part file.
// It performs no HTTP; every HTTP request belongs to internal/fetch. It does
// not own the connection or the offer: those are internal/irc and
// internal/dcc.
package irc

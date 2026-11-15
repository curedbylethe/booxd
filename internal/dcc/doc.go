// Package dcc owns one DCC SEND offer: the value the caller dials, the parse of
// the offer arguments, the private-address fallback, and the dial itself.
// It does not own the IRC connection, the CTCP framing, or any HTTP: HTTP lives
// in internal/fetch.
// Open does not read the payload: the caller reads exactly Offer.Size bytes and
// attempts no resume.
package dcc

package irc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrSendTooSoon reports a chat send inside MinSendGap.
var ErrSendTooSoon = errors.New("irc: send too soon")

// ErrSearchPending reports a chat send while an earlier one still awaits its
// reply.
var ErrSearchPending = errors.New("irc: search pending")

// clock supplies time to the send gate. It stays unexported so the package API
// does not grow; tests set Client.clock directly.
type clock interface {
	Now() time.Time
	Wait(ctx context.Context, d time.Duration) error
}

// systemClock is the production clock.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Wait sleeps for d or until ctx is done, whichever comes first.
func (systemClock) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// gateSend admits one chat send to target and records its cost. It runs before
// the write and writes nothing itself, so a rejection never reaches the socket.
// The timestamp is set even when the write then fails, so a failing socket
// cannot be hammered.
func (c *Client) gateSend(target string) error {
	c.limitMu.Lock()
	defer c.limitMu.Unlock()
	now := c.clock.Now()
	if now.Before(c.next) {
		return fmt.Errorf("%w: %s remaining", ErrSendTooSoon, c.next.Sub(now))
	}
	// An expired slot is free again; a zero searchTimeout never expires.
	if c.searchNick != "" && (c.searchTimeout <= 0 || now.Before(c.searchUntil)) {
		return fmt.Errorf("%w: waiting on %s", ErrSearchPending, c.searchNick)
	}
	c.next = now.Add(c.minSendGap)
	c.searchUntil = time.Time{}
	if c.searchTimeout > 0 {
		c.searchUntil = now.Add(c.searchTimeout)
	}
	c.searchNick = target
	return nil
}

// releaseSearch frees the slot when nick is the one that occupies it: the bot
// answers a search with an offer, a PRIVMSG, or a NOTICE.
func (c *Client) releaseSearch(nick string) {
	c.limitMu.Lock()
	defer c.limitMu.Unlock()
	if c.searchNick != "" && strings.EqualFold(c.searchNick, nick) {
		c.searchNick = ""
		c.searchUntil = time.Time{}
	}
}

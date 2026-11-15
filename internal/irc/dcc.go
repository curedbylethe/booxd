package irc

import (
	"strings"

	"github.com/curedbylethe/booxd/internal/dcc"
)

// dccVerb starts every DCC payload; dccSend is the only one recognised here,
// and the bot sends both cases.
const (
	dccVerb = "DCC "
	dccSend = dccVerb + "SEND "
)

// handleDCC delivers a PRIVMSG or NOTICE DCC SEND as an offer. Plain chat and
// every other CTCP payload pass through untouched. A bad offer is traced and
// dropped so the session stays open.
func (c *Client) handleDCC(msg Message) {
	body := ctcpBody(msg.Trailing)
	if len(body) < len(dccSend) || !strings.EqualFold(body[:len(dccSend)], dccSend) {
		return
	}
	from := sender(msg)
	if from == "" {
		c.tracef("irc: dcc offer with no sender: %s", body)
		return
	}
	// Parse owns the SEND verb, so it gets everything after "DCC ".
	offer, err := dcc.Parse(body[len(dccVerb):])
	if err != nil {
		c.tracef("irc: dcc offer from %s: %v", from, err)
		return
	}
	offer.From = from
	if offer.Private() {
		if offer, err = offer.Resolve(msg.Host); err != nil {
			c.tracef("irc: dcc offer from %s: %v", from, err)
			return
		}
	}
	deliver(c, c.offers, offer, "dcc")
	c.releaseSearch(offer.From)
}

// ctcpBody unwraps one leading and one trailing \x01. A trailing with no
// leading marker is plain chat, never a CTCP body.
func ctcpBody(trailing string) string {
	if !strings.HasPrefix(trailing, "\x01") {
		return ""
	}
	return strings.TrimSuffix(trailing[1:], "\x01")
}

// sender is the offer author: the nick, or the first word of the prefix when
// the nick is empty.
func sender(msg Message) string {
	if msg.Nick != "" {
		return msg.Nick
	}
	fields := strings.Fields(msg.Prefix)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

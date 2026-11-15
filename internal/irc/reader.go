package irc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// ErrLineTooLong reports a server line that exceeded maxLineLen.
var ErrLineTooLong = errors.New("irc: line too long")

const (
	// maxLineLen is the longest line accepted before the newline.
	maxLineLen = 4096
	// channelBuffer is the capacity of Messages and DCC, so a brief consumer
	// stall does not drop anything.
	channelBuffer = 16
)

// readLoop owns the socket read side: one goroutine, one reader.
func (c *Client) readLoop() {
	for {
		if c.idleTimeout > 0 {
			if err := c.conn.SetReadDeadline(time.Now().Add(c.idleTimeout)); err != nil {
				c.terminate(fmt.Errorf("irc: read: %w", err))
				return
			}
		}
		line, err := c.reader.ReadString('\n')
		if err != nil {
			c.terminate(readFailure(c.idleTimeout, err))
			return
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		c.traceLine("<-", line)
		if len(line) > maxLineLen {
			c.terminate(ErrLineTooLong)
			return
		}
		msg, err := Parse(line)
		if err != nil {
			c.tracef("irc: skip: %v", err)
			continue
		}
		switch msg.Command {
		case "PING":
			if err := c.send(pong(msg)); err != nil {
				c.terminate(fmt.Errorf("irc: PONG: %w", err))
				return
			}
		case "ERROR":
			c.terminate(fmt.Errorf("irc: server ERROR: %s", msg.Trailing))
			return
		default:
			// The bot answers a search with a PRIVMSG or a NOTICE; a DCC
			// offer from the same nick releases it too.
			if msg.Command == "PRIVMSG" || msg.Command == "NOTICE" {
				c.releaseSearch(msg.Nick)
				c.handleDCC(msg)
			}
			deliver(c, c.msgs, msg, "messages")
		}
	}
}

// pong replies to a PING with the same token, keeping the trailing form when
// the token arrived as a trailing.
func pong(msg Message) string {
	if msg.Trailing != "" {
		return "PONG :" + msg.Trailing
	}
	if len(msg.Params) > 0 {
		return "PONG " + msg.Params[0]
	}
	return "PONG :"
}

// terminate records the first terminal error and shuts down without QUIT,
// because the socket is already unusable.
func (c *Client) terminate(err error) {
	c.recordErr(err)
	_ = c.shutdown(false)
}

// deliver queues one value without blocking. A full channel drops the value
// and traces it, so a slow consumer never stalls the reader. deliverMu keeps
// the send off a channel Close is closing.
func deliver[T any](c *Client, ch chan T, v T, kind string) {
	c.deliverMu.Lock()
	defer c.deliverMu.Unlock()
	select {
	case ch <- v:
	default:
		c.tracef("irc: drop: %s full", kind)
	}
}

// readFailure names the read failure and keeps os.ErrDeadlineExceeded visible
// for the idle case.
func readFailure(idle time.Duration, err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("irc: read: connection idle for %s: %w", idle, os.ErrDeadlineExceeded)
	}
	return fmt.Errorf("irc: read: %w", err)
}

// traceLine writes one raw line to Config.Trace; a nil writer stays silent.
func (c *Client) traceLine(dir, line string) {
	c.tracef("irc: %s %s", dir, line)
}

// tracef writes one trace line; a nil writer stays silent.
func (c *Client) tracef(format string, args ...any) {
	if c.trace == nil {
		return
	}
	c.traceMu.Lock()
	defer c.traceMu.Unlock()
	_, _ = fmt.Fprintf(c.trace, format+"\n", args...)
}

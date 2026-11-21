package irc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/curedbylethe/booxd/internal/dcc"
)

// ErrClosed reports a client whose connection is closed.
var ErrClosed = errors.New("irc: client closed")

// maxNickRetries is how many times a 433 may rename the nick before Dial gives up.
const maxNickRetries = 2

// Config describes one connection.
type Config struct {
	Host            string        // "irc.irchighway.net"
	Port            int           // 6667, or 6697 with TLS
	Nick            string        // requested nick; "_" is appended on a 433
	User            string        // USER field; defaults to Nick
	Channel         string        // "#ebooks"
	TLS             bool          // wrap the connection in TLS
	InsecureTLS     bool          // skip verification; irchighway is self-signed
	DialTimeout     time.Duration // covers TCP connect and TLS handshake
	RegisterTimeout time.Duration // wait for 001
	JoinTimeout     time.Duration // wait for 366
	SendTimeout     time.Duration // per-write deadline
	IdleTimeout     time.Duration // longest silence tolerated while reading; zero waits forever
	MinSendGap      time.Duration // shortest space between chat sends; zero means no gap
	SearchTimeout   time.Duration // how long one send waits for its reply; zero means no automatic release
	DialFunc        func(ctx context.Context, network, addr string) (net.Conn, error)
	Trace           io.Writer // raw line log; nil is silent
}

// Client is one registered, joined connection. Every write goes through send.
type Client struct {
	conn        net.Conn
	reader      *bufio.Reader
	writeMu     sync.Mutex
	sendTimeout time.Duration
	idleTimeout time.Duration
	nick        string
	channel     string
	msgs        chan Message
	offers      chan dcc.Offer
	done        chan struct{}
	deliverMu   sync.Mutex
	trace       io.Writer
	traceMu     sync.Mutex
	errMu       sync.Mutex
	err         error
	closed      sync.Once

	// Send discipline: one gap and one search slot, guarded by limitMu.
	clock         clock
	limitMu       sync.Mutex
	minSendGap    time.Duration
	searchTimeout time.Duration
	next          time.Time
	searchNick    string
	searchUntil   time.Time
	searchSeq     uint64
}

// Dial connects, registers the nick, and joins the channel. It returns only
// after the server confirms both with 001 and 366.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Nick == "" {
		return nil, fmt.Errorf("irc: empty nick")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dctx := ctx
	if cfg.DialTimeout > 0 {
		var cancel context.CancelFunc
		dctx, cancel = context.WithTimeout(ctx, cfg.DialTimeout)
		defer cancel()
	}

	dial := cfg.DialFunc
	if dial == nil {
		dialer := &net.Dialer{Timeout: cfg.DialTimeout}
		dial = dialer.DialContext
	}
	conn, err := dial(dctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("irc: dial %s: %w", addr, err)
	}
	if cfg.TLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.InsecureTLS})
		if err := tlsConn.HandshakeContext(dctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("irc: tls handshake with %s: %w", cfg.Host, err)
		}
		conn = tlsConn
	}

	c := &Client{
		conn:          conn,
		reader:        bufio.NewReaderSize(conn, maxLineLen+2),
		sendTimeout:   cfg.SendTimeout,
		idleTimeout:   cfg.IdleTimeout,
		minSendGap:    cfg.MinSendGap,
		searchTimeout: cfg.SearchTimeout,
		nick:          cfg.Nick,
		channel:       cfg.Channel,
		msgs:          make(chan Message, channelBuffer),
		offers:        make(chan dcc.Offer, channelBuffer),
		done:          make(chan struct{}),
		trace:         cfg.Trace,
		clock:         systemClock{},
	}
	// Cancelling during registration or join must unblock the read, not wait
	// out the timeout; the nick is not registered yet, so no QUIT is sent.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	if err := c.register(cfg); err != nil {
		stop()
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if err := c.join(cfg); err != nil {
		stop()
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if !stop() {
		_ = conn.Close()
		return nil, ctx.Err()
	}
	_ = conn.SetReadDeadline(time.Time{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-c.done:
		}
	}()
	go c.readLoop()
	return c, nil
}

// Messages carries every line the server sends, except PING, which is answered
// by the reader. The channel closes when the session ends.
func (c *Client) Messages() <-chan Message { return c.msgs }

// DCC carries recognised DCC offers. The channel closes when the session ends.
func (c *Client) DCC() <-chan dcc.Offer { return c.offers }

// Nick returns the nick the server accepted.
func (c *Client) Nick() string { return c.nick }

// Channel returns the channel the server confirmed.
func (c *Client) Channel() string { return c.channel }

// Say writes one PRIVMSG line. It returns ErrSendTooSoon inside MinSendGap and
// ErrSearchPending while an earlier send awaits its reply. On success it
// returns the token that owns the slot the send acquired; pass that token to
// ReleaseSearch to free the slot.
func (c *Client) Say(ctx context.Context, target, text string) (uint64, error) {
	return c.privmsg(ctx, target, text)
}

// CTCP writes a PRIVMSG whose payload is wrapped in \x01 markers, under the
// same gap and search slot as Say.
func (c *Client) CTCP(ctx context.Context, target, payload string) error {
	_, err := c.privmsg(ctx, target, "\x01"+payload+"\x01")
	return err
}

// Err returns the first terminal error, or ErrClosed after Close.
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// recordErr keeps the first terminal error; later calls change nothing.
func (c *Client) recordErr(err error) {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.err == nil {
		c.err = err
	}
}

// Close sends QUIT once and shuts down. A second call returns nil and writes
// nothing.
func (c *Client) Close() error { return c.shutdown(true) }

// shutdown is the single shutdown path: it closes the socket, done, Messages
// and DCC exactly once. QUIT is skipped when the socket is already dead.
func (c *Client) shutdown(quit bool) error {
	var err error
	c.closed.Do(func() {
		c.recordErr(ErrClosed)
		if quit {
			err = c.send("QUIT")
		}
		err = errors.Join(err, c.conn.Close())
		close(c.done)
		c.deliverMu.Lock()
		close(c.msgs)
		close(c.offers)
		c.deliverMu.Unlock()
	})
	return err
}

// privmsg is the only path that builds a PRIVMSG line.
func (c *Client) privmsg(ctx context.Context, target, body string) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if strings.ContainsAny(body, "\r\n") {
		return 0, fmt.Errorf("irc: refusing to send %q: CR or LF", body)
	}
	slot, err := c.gateSend(target)
	if err != nil {
		return 0, err
	}
	return slot, c.send("PRIVMSG " + target + " :" + body)
}

// send writes one complete line. It is the only writer of the connection.
func (c *Client) send(line string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.sendTimeout > 0 {
		if err := c.conn.SetWriteDeadline(time.Now().Add(c.sendTimeout)); err != nil {
			return err
		}
	}
	_, err := c.conn.Write([]byte(line + "\r\n"))
	if err == nil {
		c.traceLine("->", line)
	}
	return err
}

// read returns the next parseable line, skipping noise the parser rejects.
func (c *Client) read() (Message, error) {
	for {
		line, err := c.readLine()
		if err != nil {
			return Message{}, err
		}
		msg, err := Parse(line)
		if err != nil {
			continue
		}
		return msg, nil
	}
}

// register writes NICK and USER and waits for 001, renaming on a 433.
func (c *Client) register(cfg Config) error {
	user := cfg.User
	if user == "" {
		user = cfg.Nick
	}
	if err := c.send("NICK " + c.nick); err != nil {
		return err
	}
	if err := c.send("USER " + user + " 0 * :" + user); err != nil {
		return err
	}
	if err := c.setReadDeadline(cfg.RegisterTimeout); err != nil {
		return err
	}
	for retries := 0; ; {
		msg, err := c.read()
		if err != nil {
			return readError("register", cfg.RegisterTimeout, err)
		}
		switch msg.Command {
		case "001":
			return nil
		case "PING":
			if err := c.send(pong(msg)); err != nil {
				return err
			}
		case "433":
			if retries == maxNickRetries {
				return fmt.Errorf("irc: register: nick %q rejected after %d attempts", c.nick, maxNickRetries+1)
			}
			retries++
			c.nick += "_"
			if err := c.send("NICK " + c.nick); err != nil {
				return err
			}
		case "ERROR":
			return fmt.Errorf("irc: register: %s", msg.Trailing)
		}
	}
}

// join writes JOIN and waits for 366, naming the numeric on refusal.
func (c *Client) join(cfg Config) error {
	if err := c.send("JOIN " + cfg.Channel); err != nil {
		return err
	}
	if err := c.setReadDeadline(cfg.JoinTimeout); err != nil {
		return err
	}
	for {
		msg, err := c.read()
		if err != nil {
			return readError("join "+cfg.Channel, cfg.JoinTimeout, err)
		}
		switch msg.Command {
		case "366":
			return nil
		case "473":
			return fmt.Errorf("irc: join %s: invite only", cfg.Channel)
		case "474":
			return fmt.Errorf("irc: join %s: banned", cfg.Channel)
		case "475":
			return fmt.Errorf("irc: join %s: bad key", cfg.Channel)
		case "PING":
			if err := c.send(pong(msg)); err != nil {
				return err
			}
		case "ERROR":
			return fmt.Errorf("irc: join %s: %s", cfg.Channel, msg.Trailing)
		default:
			// A numeric that does not name our channel (422 with no MOTD)
			// does not refuse the join.
			if isFailure(msg.Command) && len(msg.Params) > 1 && strings.EqualFold(msg.Params[1], cfg.Channel) {
				return fmt.Errorf("irc: join %s: %s %s", cfg.Channel, msg.Command, msg.Trailing)
			}
		}
	}
}

// setReadDeadline bounds the next read; a zero timeout waits indefinitely.
//
// shortcut: a zero RegisterTimeout or JoinTimeout means no deadline at all;
// DefaultConfig always supplies one.
func (c *Client) setReadDeadline(d time.Duration) error {
	if d <= 0 {
		return nil
	}
	return c.conn.SetReadDeadline(time.Now().Add(d))
}

// readError turns a deadline into context.DeadlineExceeded and names the phase.
func readError(phase string, timeout time.Duration, err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("irc: %s: no reply within %s: %w", phase, timeout, context.DeadlineExceeded)
	}
	return fmt.Errorf("irc: %s: %w", phase, err)
}

// isFailure reports the 4xx and 5xx numerics that end a request.
func isFailure(command string) bool {
	return len(command) == 3 && (command[0] == '4' || command[0] == '5')
}

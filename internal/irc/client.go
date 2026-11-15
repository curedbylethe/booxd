package irc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
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
	DialFunc        func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Client is one registered, joined connection. Every write goes through send.
type Client struct {
	conn        net.Conn
	reader      *bufio.Reader
	writeMu     sync.Mutex
	sendTimeout time.Duration
	nick        string
	channel     string
	errMu       sync.Mutex
	err         error
	closed      sync.Once
}

// Dial connects, registers the nick, and joins the channel. It returns only
// after the server confirms both with 001 and 366.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Nick == "" {
		return nil, fmt.Errorf("irc: empty nick")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dctx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()

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
		conn:        conn,
		reader:      bufio.NewReader(conn),
		sendTimeout: cfg.SendTimeout,
		nick:        cfg.Nick,
		channel:     cfg.Channel,
	}
	if err := c.register(cfg); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := c.join(cfg); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	return c, nil
}

// Nick returns the nick the server accepted.
func (c *Client) Nick() string { return c.nick }

// Channel returns the channel the server confirmed.
func (c *Client) Channel() string { return c.channel }

// Say writes one PRIVMSG line.
func (c *Client) Say(ctx context.Context, target, text string) error {
	return c.privmsg(ctx, target, text)
}

// CTCP writes a PRIVMSG whose payload is wrapped in \x01 markers.
func (c *Client) CTCP(ctx context.Context, target, payload string) error {
	return c.privmsg(ctx, target, "\x01"+payload+"\x01")
}

// Err returns the terminal error, or ErrClosed after Close.
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// Close sends QUIT once, closes the socket, and records ErrClosed. A second
// call returns nil and writes nothing.
func (c *Client) Close() error {
	var err error
	c.closed.Do(func() {
		quitErr := c.send("QUIT")
		err = errors.Join(quitErr, c.conn.Close())
		c.errMu.Lock()
		if c.err == nil {
			c.err = ErrClosed
		}
		c.errMu.Unlock()
	})
	return err
}

// privmsg is the only path that builds a PRIVMSG line.
func (c *Client) privmsg(ctx context.Context, target, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.ContainsAny(body, "\r\n") {
		return fmt.Errorf("irc: refusing to send %q: CR or LF", body)
	}
	return c.send("PRIVMSG " + target + " :" + body)
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
	return err
}

// read returns the next parseable line, skipping noise the parser rejects.
func (c *Client) read() (Message, error) {
	for {
		line, err := c.reader.ReadString('\n')
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
		case "ERROR":
			return fmt.Errorf("irc: join %s: %s", cfg.Channel, msg.Trailing)
		default:
			if isFailure(msg.Command) {
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

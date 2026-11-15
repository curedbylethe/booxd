package irc

import (
	"os"
	"time"
)

// DefaultConfig returns the irchighway ebooks settings. Port 6697 answers the
// TLS handshake, and InsecureTLS is set because the server presents a
// self-signed certificate. Tracing stays off unless BOOXD_IRC_DEBUG=1.
func DefaultConfig() Config {
	cfg := Config{
		Host:            "irc.irchighway.net",
		Port:            6697,
		Nick:            "booxd",
		User:            "booxd",
		Channel:         "#ebooks",
		TLS:             true,
		InsecureTLS:     true,
		DialTimeout:     30 * time.Second,
		RegisterTimeout: 30 * time.Second,
		JoinTimeout:     30 * time.Second,
		SendTimeout:     10 * time.Second,
		IdleTimeout:     5 * time.Minute,
		MinSendGap:      10 * time.Second,
		// The bot usually answers in seconds. The timeout frees a channel
		// search whose reply never arrives, because a channel target cannot
		// be matched against the bot's nick.
		SearchTimeout: 2 * time.Minute,
	}
	if DebugEnabled() {
		cfg.Trace = os.Stderr
	}
	return cfg
}

// DebugEnabled reports whether BOOXD_IRC_DEBUG is exactly "1". It is the only
// environment read in this package.
func DebugEnabled() bool {
	return os.Getenv("BOOXD_IRC_DEBUG") == "1"
}

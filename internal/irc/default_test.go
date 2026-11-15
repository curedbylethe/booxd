package irc

import (
	"context"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/dcc"
)

// The exported API is fixed: a rename or removal breaks this file's build.
var (
	_ Config                                               = Config{}
	_ *Client                                              = (*Client)(nil)
	_ func(context.Context, Config) (*Client, error)       = Dial
	_ func(*Client) string                                 = (*Client).Nick
	_ func(*Client) string                                 = (*Client).Channel
	_ func(*Client, context.Context, string, string) error = (*Client).Say
	_ func(*Client, context.Context, string, string) error = (*Client).CTCP
	_ func(*Client) <-chan Message                         = (*Client).Messages
	_ func(*Client) <-chan dcc.Offer                       = (*Client).DCC
	_ func(*Client) error                                  = (*Client).Err
	_ func(*Client) error                                  = (*Client).Close
	_ func(string) (Message, error)                        = Parse
	_ Message                                              = Message{}
)

func TestDefaultConfig(t *testing.T) {
	// Any value but "1" keeps the trace off, whatever the developer's shell
	// already holds.
	t.Setenv("BOOXD_IRC_DEBUG", "")

	cfg := DefaultConfig()

	if cfg.Host != "irc.irchighway.net" {
		t.Errorf("Host = %q, want %q", cfg.Host, "irc.irchighway.net")
	}
	if cfg.Port != 6697 {
		t.Errorf("Port = %d, want 6697", cfg.Port)
	}
	if cfg.Channel != "#ebooks" {
		t.Errorf("Channel = %q, want %q", cfg.Channel, "#ebooks")
	}
	if !cfg.TLS {
		t.Error("TLS = false, want true")
	}
	if !cfg.InsecureTLS {
		t.Error("InsecureTLS = false, want true")
	}
	if cfg.MinSendGap != 10*time.Second {
		t.Errorf("MinSendGap = %s, want 10s", cfg.MinSendGap)
	}
	if cfg.SearchTimeout != 2*time.Minute {
		t.Errorf("SearchTimeout = %s, want 2m", cfg.SearchTimeout)
	}
	for _, d := range []struct {
		name string
		got  time.Duration
	}{
		{"DialTimeout", cfg.DialTimeout},
		{"RegisterTimeout", cfg.RegisterTimeout},
		{"JoinTimeout", cfg.JoinTimeout},
		{"SendTimeout", cfg.SendTimeout},
		{"IdleTimeout", cfg.IdleTimeout},
	} {
		if d.got <= 0 {
			t.Errorf("%s = %s, want a positive duration", d.name, d.got)
		}
	}
	if cfg.Trace != nil {
		t.Error("Trace is set with BOOXD_IRC_DEBUG off, want nil")
	}
}

func TestDebugEnabled(t *testing.T) {
	t.Setenv("BOOXD_IRC_DEBUG", "1")
	if !DebugEnabled() {
		t.Error("DebugEnabled() = false with BOOXD_IRC_DEBUG=1, want true")
	}
	if DefaultConfig().Trace == nil {
		t.Error("DefaultConfig().Trace = nil with BOOXD_IRC_DEBUG=1, want a writer")
	}

	t.Setenv("BOOXD_IRC_DEBUG", "0")
	if DebugEnabled() {
		t.Error("DebugEnabled() = true with BOOXD_IRC_DEBUG=0, want false")
	}
	if DefaultConfig().Trace != nil {
		t.Error("DefaultConfig().Trace is set with BOOXD_IRC_DEBUG=0, want nil")
	}
}

package irc

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    Message
		wantErr bool
	}{
		{
			name: "full prefix and trailing",
			line: ":nick!user@host PRIVMSG #ebooks :hello there",
			want: Message{
				Raw:      ":nick!user@host PRIVMSG #ebooks :hello there",
				Prefix:   "nick!user@host",
				Nick:     "nick",
				User:     "user",
				Host:     "host",
				Command:  "PRIVMSG",
				Params:   []string{"#ebooks"},
				Trailing: "hello there",
			},
		},
		{
			name: "nick only prefix",
			line: ":nick PRIVMSG #ebooks :hi",
			want: Message{
				Raw:      ":nick PRIVMSG #ebooks :hi",
				Prefix:   "nick",
				Nick:     "nick",
				Command:  "PRIVMSG",
				Params:   []string{"#ebooks"},
				Trailing: "hi",
			},
		},
		{
			name: "no prefix",
			line: "PRIVMSG #ebooks :hi",
			want: Message{
				Raw:      "PRIVMSG #ebooks :hi",
				Command:  "PRIVMSG",
				Params:   []string{"#ebooks"},
				Trailing: "hi",
			},
		},
		{
			name: "numeric command",
			line: ":irc.example 001 nick :Welcome to the network",
			want: Message{
				Raw:      ":irc.example 001 nick :Welcome to the network",
				Prefix:   "irc.example",
				Nick:     "irc.example",
				Command:  "001",
				Params:   []string{"nick"},
				Trailing: "Welcome to the network",
			},
		},
		{
			name: "numeric with two middle params",
			line: ":irc.example 366 nick #ebooks :End of /NAMES list.",
			want: Message{
				Raw:      ":irc.example 366 nick #ebooks :End of /NAMES list.",
				Prefix:   "irc.example",
				Nick:     "irc.example",
				Command:  "366",
				Params:   []string{"nick", "#ebooks"},
				Trailing: "End of /NAMES list.",
			},
		},
		{
			name: "notice",
			line: ":bot!x@y NOTICE nick :Searching for: dune",
			want: Message{
				Raw:      ":bot!x@y NOTICE nick :Searching for: dune",
				Prefix:   "bot!x@y",
				Nick:     "bot",
				User:     "x",
				Host:     "y",
				Command:  "NOTICE",
				Params:   []string{"nick"},
				Trailing: "Searching for: dune",
			},
		},
		{
			name: "missing trailing",
			line: ":nick PRIVMSG #ebooks",
			want: Message{
				Raw:     ":nick PRIVMSG #ebooks",
				Prefix:  "nick",
				Nick:    "nick",
				Command: "PRIVMSG",
				Params:  []string{"#ebooks"},
			},
		},
		{
			name: "empty trailing equals missing trailing",
			line: ":nick PRIVMSG #ebooks :",
			want: Message{
				Raw:     ":nick PRIVMSG #ebooks :",
				Prefix:  "nick",
				Nick:    "nick",
				Command: "PRIVMSG",
				Params:  []string{"#ebooks"},
			},
		},
		{
			name: "trailing only",
			line: "PING :irc.example",
			want: Message{
				Raw:      "PING :irc.example",
				Command:  "PING",
				Trailing: "irc.example",
			},
		},
		{
			name: "only first colon starts trailing",
			line: ":nick PRIVMSG #ebooks :has : a colon",
			want: Message{
				Raw:      ":nick PRIVMSG #ebooks :has : a colon",
				Prefix:   "nick",
				Nick:     "nick",
				Command:  "PRIVMSG",
				Params:   []string{"#ebooks"},
				Trailing: "has : a colon",
			},
		},
		{name: "spaces only", line: "   ", wantErr: true},
		{name: "empty", line: "", wantErr: true},
		{name: "empty prefix", line: ": PRIVMSG #ebooks", wantErr: true},
		{name: "missing command", line: ":nick", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.line)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %+v, want error", tt.line, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.line, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseStripsLineEnding(t *testing.T) {
	for _, line := range []string{
		"PING :irc.example",
		"PING :irc.example\n",
		"PING :irc.example\r\n",
	} {
		got, err := Parse(line)
		if err != nil {
			t.Fatalf("Parse(%q): %v", line, err)
		}
		if got.Raw != "PING :irc.example" {
			t.Errorf("Parse(%q).Raw = %q, want %q", line, got.Raw, "PING :irc.example")
		}
	}
}

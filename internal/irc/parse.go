package irc

import (
	"fmt"
	"strings"
)

// Message is one parsed IRC line.
type Message struct {
	Raw      string   // the line as received, without the trailing \r or \n
	Prefix   string   // "nick!user@host" without the leading colon; empty when absent
	Nick     string   // nick part of Prefix
	User     string   // user part of Prefix, empty when absent
	Host     string   // host part of Prefix, empty when absent
	Command  string   // "PRIVMSG", "NOTICE", "001", "366", ...
	Params   []string // middle parameters, in order, without a leading colon
	Trailing string   // text after " :", empty when absent
}

// Parse parses one IRC line. It does no I/O and keeps no state.
//
// An empty trailing ("... :") yields Trailing == "", the same as an absent
// trailing, so there is no HasTrailing field: nothing distinguishes them.
func Parse(line string) (Message, error) {
	s := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	msg := Message{Raw: s}

	if strings.TrimSpace(s) == "" {
		return Message{}, fmt.Errorf("irc: parse %q: empty line", line)
	}

	rest := s
	if strings.HasPrefix(rest, ":") {
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			return Message{}, fmt.Errorf("irc: parse %q: prefix without command", line)
		}
		msg.Prefix = rest[1:i]
		if msg.Prefix == "" {
			return Message{}, fmt.Errorf("irc: parse %q: empty prefix", line)
		}
		msg.Nick, msg.User, msg.Host = splitPrefix(msg.Prefix)
		rest = rest[i+1:]
	}

	rest = strings.TrimLeft(rest, " ")
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		msg.Command, rest = rest[:i], rest[i+1:]
	} else {
		msg.Command, rest = rest, ""
	}
	if msg.Command == "" {
		return Message{}, fmt.Errorf("irc: parse %q: missing command", line)
	}

	for rest != "" {
		rest = strings.TrimLeft(rest, " ")
		if rest == "" {
			break
		}
		if rest[0] == ':' {
			msg.Trailing = rest[1:]
			break
		}
		if i := strings.IndexByte(rest, ' '); i >= 0 {
			msg.Params, rest = append(msg.Params, rest[:i]), rest[i+1:]
		} else {
			msg.Params, rest = append(msg.Params, rest), ""
		}
	}

	return msg, nil
}

// splitPrefix fills nick, user and host; every part after the nick is optional.
func splitPrefix(prefix string) (nick, user, host string) {
	nick = prefix
	if i := strings.IndexByte(nick, '!'); i >= 0 {
		nick, user = nick[:i], nick[i+1:]
		if j := strings.IndexByte(user, '@'); j >= 0 {
			user, host = user[:j], user[j+1:]
		}
		return nick, user, host
	}
	if i := strings.IndexByte(nick, '@'); i >= 0 {
		nick, host = nick[:i], nick[i+1:]
	}
	return nick, user, host
}

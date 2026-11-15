package dcc

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Offer is one DCC SEND offer.
type Offer struct {
	From string // nick that sent the offer
	Name string // filename as sent, quotes removed
	IP   net.IP // offer address, IPv4
	Port int    // offer port
	Size int64  // byte count, -1 when the sender did not give one
}

// dialContext is a test seam; tests replace it and restore it with t.Cleanup.
var dialContext = (&net.Dialer{Timeout: 30 * time.Second}).DialContext

// Parse parses the argument text after "DCC ", for example
// `SEND "A Book.epub" 3232235777 5000 1234`.
// Read from the right: the last three fields are IP, port, and size, and
// everything between SEND and them is the name.
func Parse(args string) (Offer, error) {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return Offer{}, fmt.Errorf("dcc: empty offer")
	}
	if !strings.EqualFold(fields[0], "SEND") {
		return Offer{}, fmt.Errorf("dcc: bad verb %q", fields[0])
	}
	if len(fields) < 5 {
		return Offer{}, fmt.Errorf("dcc: offer needs name, ip, port and size, got %d fields", len(fields)-1)
	}

	name := unquote(strings.Join(fields[1:len(fields)-3], " "))
	if name == "" {
		return Offer{}, fmt.Errorf("dcc: bad name %q", name)
	}

	rawIP := fields[len(fields)-3]
	ipValue, err := strconv.ParseUint(rawIP, 10, 32)
	if err != nil {
		return Offer{}, fmt.Errorf("dcc: bad ip %q", rawIP)
	}
	ip := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(ip, uint32(ipValue))

	rawPort := fields[len(fields)-2]
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return Offer{}, fmt.Errorf("dcc: bad port %q", rawPort)
	}

	rawSize := fields[len(fields)-1]
	size, err := strconv.ParseInt(rawSize, 10, 64)
	if err != nil || size < 0 {
		return Offer{}, fmt.Errorf("dcc: bad size %q", rawSize)
	}

	return Offer{Name: name, IP: ip, Port: port, Size: size}, nil
}

// unquote removes one pair of surrounding double quotes.
func unquote(name string) string {
	if len(name) >= 2 && strings.HasPrefix(name, `"`) && strings.HasSuffix(name, `"`) {
		return name[1 : len(name)-1]
	}
	return name
}

// Private reports whether IP is in 10/8, 172.16/12, or 192.168/16.
func (o Offer) Private() bool {
	return o.IP.IsPrivate()
}

// Resolve replaces a private IP with host, and errors when the offer is
// private and host is empty or not an IP.
func (o Offer) Resolve(host string) (Offer, error) {
	if !o.Private() {
		return o, nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return Offer{}, fmt.Errorf("dcc: bad host %q for private ip %s", host, o.IP)
	}
	if v4 := ip.To4(); v4 != nil {
		o.IP = v4
	} else {
		o.IP = ip
	}
	return o, nil
}

// Open dials the offerer. The caller reads Size bytes; no resume is attempted.
func (o Offer) Open(ctx context.Context) (net.Conn, error) {
	return dialContext(ctx, "tcp4", net.JoinHostPort(o.IP.String(), strconv.Itoa(o.Port)))
}

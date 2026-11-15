package dcc

import (
	"context"
	"net"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		want    Offer
		wantErr bool
	}{
		{
			name: "quoted name with space",
			args: `SEND "A Book.epub" 3232235777 5000 1234`,
			want: Offer{Name: "A Book.epub", IP: net.IPv4(192, 168, 1, 1), Port: 5000, Size: 1234},
		},
		{
			name: "unquoted name with space",
			args: `SEND A Book.epub 3232235777 5000 1234`,
			want: Offer{Name: "A Book.epub", IP: net.IPv4(192, 168, 1, 1), Port: 5000, Size: 1234},
		},
		{
			name: "lowercase verb",
			args: `send book.epub 3232235777 5000 1234`,
			want: Offer{Name: "book.epub", IP: net.IPv4(192, 168, 1, 1), Port: 5000, Size: 1234},
		},
		{
			name: "apostrophe in name",
			args: `SEND "A Book's.epub" 3232235777 5000 1234`,
			want: Offer{Name: "A Book's.epub", IP: net.IPv4(192, 168, 1, 1), Port: 5000, Size: 1234},
		},
		{
			// 0.x lies outside 10/8, 172.16/12, and 192.168/16, so it is not private.
			name: "zero address is not private",
			args: `SEND book.epub 0 5000 1234`,
			want: Offer{Name: "book.epub", IP: net.IPv4(0, 0, 0, 0), Port: 5000, Size: 1234},
		},
		{
			name: "ten eight is private",
			args: `SEND book.epub 167772160 5000 1234`,
			want: Offer{Name: "book.epub", IP: net.IPv4(10, 0, 0, 0), Port: 5000, Size: 1234},
		},
		{
			name: "zero size",
			args: `SEND book.epub 3232235777 5000 0`,
			want: Offer{Name: "book.epub", IP: net.IPv4(192, 168, 1, 1), Port: 5000, Size: 0},
		},
		{name: "port zero", args: `SEND book.epub 3232235777 0 1234`, wantErr: true},
		{name: "negative size", args: `SEND book.epub 3232235777 5000 -1`, wantErr: true},
		{name: "missing size", args: `SEND book.epub 3232235777 5000`, wantErr: true},
		{name: "verb only", args: `SEND`, wantErr: true},
		{name: "empty", args: ``, wantErr: true},
		{name: "ip over uint32", args: `SEND book.epub 4294967296 5000 1`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %+v, want error", tt.args, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error: %v", tt.args, err)
			}
			if got.From != tt.want.From || got.Name != tt.want.Name ||
				!got.IP.Equal(tt.want.IP) || got.Port != tt.want.Port || got.Size != tt.want.Size {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestOfferPrivate(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"172.32.0.1", false},
		{"203.0.113.9", false},
		{"0.0.0.0", false},
	}
	for _, tt := range tests {
		if got := (Offer{IP: net.ParseIP(tt.ip)}).Private(); got != tt.want {
			t.Errorf("Offer{IP: %s}.Private() = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestOfferResolve(t *testing.T) {
	private := Offer{Name: "book.epub", IP: net.IPv4(192, 168, 1, 1), Port: 5000, Size: 1234}
	public := Offer{Name: "book.epub", IP: net.IPv4(203, 0, 113, 9), Port: 5000, Size: 1234}

	tests := []struct {
		name    string
		offer   Offer
		host    string
		wantIP  string
		wantErr bool
	}{
		{name: "private replaced by host", offer: private, host: "203.0.113.9", wantIP: "203.0.113.9"},
		{name: "private without host", offer: private, host: "", wantErr: true},
		{name: "private with hostname", offer: private, host: "irc.example", wantErr: true},
		{name: "public unchanged without host", offer: public, host: "", wantIP: "203.0.113.9"},
		{name: "public unchanged with host", offer: public, host: "irc.example", wantIP: "203.0.113.9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.offer.Resolve(tt.host)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Resolve(%q) = %+v, want error", tt.host, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): unexpected error: %v", tt.host, err)
			}
			if !got.IP.Equal(net.ParseIP(tt.wantIP)) {
				t.Errorf("Resolve(%q) IP = %s, want %s", tt.host, got.IP, tt.wantIP)
			}
			if got.Name != tt.offer.Name || got.Port != tt.offer.Port || got.Size != tt.offer.Size {
				t.Errorf("Resolve(%q) changed the offer: %+v", tt.host, got)
			}
		})
	}
}

func TestOfferOpen(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	var gotNetwork, gotAddr string
	prev := dialContext
	t.Cleanup(func() { dialContext = prev })
	dialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
		gotNetwork, gotAddr = network, addr
		return client, nil
	}

	offer := Offer{IP: net.IPv4(192, 168, 1, 1), Port: 5000}
	conn, err := offer.Open(context.Background())
	if err != nil {
		t.Fatalf("Open: unexpected error: %v", err)
	}
	if conn != client {
		t.Errorf("Open returned %v, want the dialed connection", conn)
	}
	if gotNetwork != "tcp4" {
		t.Errorf("Open dialed network %q, want tcp4", gotNetwork)
	}
	if gotAddr != "192.168.1.1:5000" {
		t.Errorf("Open dialed %q, want 192.168.1.1:5000", gotAddr)
	}
}

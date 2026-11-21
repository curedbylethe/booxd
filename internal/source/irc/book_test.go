package irc

import (
	"reflect"
	"strings"
	"testing"

	"github.com/curedbylethe/booxd/internal/source"
)

func TestParseLine(t *testing.T) {
	const canonical = "!artemis_serv 16d6770d2ba9 | 27 - The Last Hero - Graphic Novel.pdf ::INFO:: 49.78MB"

	tests := []struct {
		name   string
		line   string
		want   Book
		wantOK bool
	}{
		{
			name: "canonical line",
			line: canonical,
			want: Book{
				Server: "artemis_serv",
				ID:     "16d6770d2ba9",
				Name:   "27 - The Last Hero - Graphic Novel",
				Format: "pdf",
				Size:   "49.78MB",
			},
			wantOK: true,
		},
		{
			name: "uppercase extension",
			line: "!bot abc123 | Book Title.EPUB ::INFO:: 2MB",
			want: Book{
				Server: "bot",
				ID:     "abc123",
				Name:   "Book Title",
				Format: "epub",
				Size:   "2MB",
			},
			wantOK: true,
		},
		{
			name: "spaces and earlier dots in name",
			line: "!bot id | Novel 03 - The Great Gatsby (retail).epub ::INFO:: 3MB",
			want: Book{
				Server: "bot",
				ID:     "id",
				Name:   "Novel 03 - The Great Gatsby (retail)",
				Format: "epub",
				Size:   "3MB",
			},
			wantOK: true,
		},
		{
			name: "no padding around pipe",
			line: "!bot id|file.epub ::INFO:: 1MB",
			want: Book{
				Server: "bot",
				ID:     "id",
				Name:   "file",
				Format: "epub",
				Size:   "1MB",
			},
			wantOK: true,
		},
		{
			name: "extra token before pipe is dropped",
			line: "!bot id noise | file.epub ::INFO:: 1MB",
			want: Book{
				Server: "bot",
				ID:     "id",
				Name:   "file",
				Format: "epub",
				Size:   "1MB",
			},
			wantOK: true,
		},
		{
			name: "legacy hash tail truncates size",
			line: "!bot id | Book.epub ::INFO:: 5MB ::HASH:: dde5abc",
			want: Book{
				Server: "bot",
				ID:     "id",
				Name:   "Book",
				Format: "epub",
				Size:   "5MB",
			},
			wantOK: true,
		},
		{name: "no leading bang", line: "bot id | file.epub ::INFO:: 1MB"},
		{name: "no info marker", line: "!bot id | file.epub"},
		{name: "no pipe", line: "!DukeLupus John Marsden - Hamlet (mobi).rar  ::INFO::  250.45KB"},
		{name: "fewer than two tokens", line: "!bot|file.epub ::INFO:: 1MB"},
		{name: "empty info value", line: "!bot id | file.epub ::INFO:: "},
		{name: "name without dot", line: "!bot id | file ::INFO:: 1MB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLine(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("parseLine(%q) ok = %v, want %v (got %+v)", tt.line, ok, tt.wantOK, got)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseLine(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}

	t.Run("id stable across identical lines", func(t *testing.T) {
		first, ok := parseLine(canonical)
		if !ok {
			t.Fatalf("parseLine(%q) = false, want true", canonical)
		}
		second, ok := parseLine(canonical)
		if !ok {
			t.Fatalf("parseLine(%q) = false, want true", canonical)
		}
		if !reflect.DeepEqual(first, second) {
			t.Errorf("two parses of %q differ: %+v vs %+v", canonical, first, second)
		}
	})

	t.Run("id differs for same size", func(t *testing.T) {
		a, ok := parseLine("!bot aaa111 | A.epub ::INFO:: 5MB")
		if !ok {
			t.Fatal("parseLine(A) = false, want true")
		}
		b, ok := parseLine("!bot bbb222 | B.epub ::INFO:: 5MB")
		if !ok {
			t.Fatal("parseLine(B) = false, want true")
		}
		if a.ID == b.ID {
			t.Errorf("two books with size %q share ID %q", a.Size, a.ID)
		}
	})
}

func TestBookRefRoundTripsAcceptedIDs(t *testing.T) {
	for _, id := range []string{"abc-def", "abc/def", "50%off", "a:b"} {
		t.Run(id, func(t *testing.T) {
			b, ok := parseLine("!bot " + id + " |Title.epub ::INFO:: 1MB")
			if !ok {
				t.Fatal("line rejected")
			}
			ref := b.Ref()
			if err := ref.Validate(); err != nil {
				t.Fatalf("Validate(%+v) = %v", ref, err)
			}
			if got, err := source.Parse(ref.String()); err != nil || got != ref {
				t.Fatalf("Parse(%q) = %+v, %v; want %+v", ref.String(), got, err, ref)
			}
		})
	}
}

func TestBookRef(t *testing.T) {
	b := Book{Server: "bot", ID: "16d6770d2ba9", Name: "Name", Format: "epub"}
	want := source.Ref{Source: source.IRC, ID: "!bot 16d6770d2ba9 | Name.epub"}
	if got := b.Ref(); got != want {
		t.Fatalf("Ref() = %+v, want %+v", got, want)
	}
}

func TestBookFromRefRoundTrip(t *testing.T) {
	const canonical = "!artemis_serv 16d6770d2ba9 | 27 - The Last Hero - Graphic Novel.pdf ::INFO:: 49.78MB"
	const request = "!artemis_serv 16d6770d2ba9 | 27 - The Last Hero - Graphic Novel.pdf"

	b, ok := parseLine(canonical)
	if !ok {
		t.Fatalf("parseLine(%q) = false, want true", canonical)
	}
	ref := b.Ref()
	if got := b.requestLine(); got != request {
		t.Fatalf("requestLine() = %q, want %q", got, request)
	}
	if ref.Source != source.IRC || ref.ID != request {
		t.Fatalf("Ref() = %+v, want source irc with ID %q", ref, request)
	}

	// The printed form must regenerate the same book through Parse.
	printed, err := source.Parse(ref.String())
	if err != nil {
		t.Fatalf("Parse(%q): %v", ref.String(), err)
	}
	got, err := BookFromRef(printed)
	if err != nil {
		t.Fatalf("BookFromRef(%+v): %v", printed, err)
	}
	want := b
	want.Size = "" // a reference carries no size; stage 04 checks the offer's
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BookFromRef = %+v, want %+v", got, want)
	}
	if got.requestLine() != request {
		t.Fatalf("rebuilt requestLine() = %q, want %q", got.requestLine(), request)
	}

	s := got.Source()
	if s.Ref != ref || s.Title != got.Name || s.Format != got.Format || s.Size != "" || s.Author != "" || s.Year != "" {
		t.Fatalf("Source() = %+v, want ref %+v and title/format from book", s, ref)
	}

	// No padding around the pipe is normalised by requestLine, not echoed.
	unpadded := Book{Server: "bot", ID: "abc123", Name: "file", Format: "epub"}
	if got := unpadded.requestLine(); got != "!bot abc123 | file.epub" {
		t.Fatalf("requestLine() = %q, want canonical form", got)
	}
}

func TestBookFromRefRejects(t *testing.T) {
	tests := []struct {
		name string
		ref  source.Ref
	}{
		{name: "non-IRC source", ref: source.Ref{Source: source.Anna, ID: "1234"}},
		{name: "plain pack ID", ref: source.Ref{Source: source.IRC, ID: "16d6770d2ba9"}},
		{name: "embedded newline", ref: source.Ref{Source: source.IRC, ID: "!bot abc | Na\nme.epub"}},
		{name: "second line", ref: source.Ref{Source: source.IRC, ID: "!bot abc | Name.epub\n!bot def | Other.epub"}},
		{name: "server with slash", ref: source.Ref{Source: source.IRC, ID: "!bot/x abc | Name.epub"}},
		{name: "server with at", ref: source.Ref{Source: source.IRC, ID: "!@bot abc | Name.epub"}},
		{name: "missing separator", ref: source.Ref{Source: source.IRC, ID: "!bot abc Name.epub"}},
		{name: "missing extension", ref: source.Ref{Source: source.IRC, ID: "!bot abc | Name"}},
		{name: "extra token before pipe", ref: source.Ref{Source: source.IRC, ID: "!bot abc extra | Name.epub"}},
		{name: "over 300 bytes", ref: source.Ref{Source: source.IRC, ID: "!bot " + strings.Repeat("a", 301) + " | Name.epub"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BookFromRef(tt.ref)
			if err == nil {
				t.Fatalf("BookFromRef(%+v) = nil error, want rejection", tt.ref)
			}
			if !strings.Contains(err.Error(), "source irc") && !strings.Contains(err.Error(), "source:") {
				t.Fatalf("error %q does not name the source", err)
			}
		})
	}
}

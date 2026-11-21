package irc

import (
	"reflect"
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
				Raw:    canonical,
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
				Raw:    "!bot abc123 | Book Title.EPUB ::INFO:: 2MB",
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
				Raw:    "!bot id | Novel 03 - The Great Gatsby (retail).epub ::INFO:: 3MB",
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
				Raw:    "!bot id|file.epub ::INFO:: 1MB",
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
				Raw:    "!bot id | Book.epub ::INFO:: 5MB ::HASH:: dde5abc",
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

func TestBookRef(t *testing.T) {
	b := Book{ID: "16d6770d2ba9"}
	want := source.Ref{Source: source.IRC, ID: "16d6770d2ba9"}
	if got := b.Ref(); got != want || got.String() != "irc:16d6770d2ba9" {
		t.Fatalf("Ref() = %+v (%s), want %+v", got, got, want)
	}
}

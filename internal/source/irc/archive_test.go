package irc

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"reflect"
	"testing"
)

const (
	lineA = "!artemis_serv 16d6770d2ba9 | Name.epub ::INFO:: 49.78MB"
	lineB = "!bot abc123 | Other.pdf ::INFO:: 2MB"
	lineC = "!serv xyz789 | Third.mobi ::INFO:: 3MB"
)

var (
	bookA = Book{Server: "artemis_serv", ID: "16d6770d2ba9", Name: "Name", Format: "epub", Size: "49.78MB", Raw: lineA}
	bookB = Book{Server: "bot", ID: "abc123", Name: "Other", Format: "pdf", Size: "2MB", Raw: lineB}
	bookC = Book{Server: "serv", ID: "xyz789", Name: "Third", Format: "mobi", Size: "3MB", Raw: lineC}
)

// zipFixture builds an in-memory archive; directory-prefixed member names
// exercise scanning by content rather than by name.
func zipFixture(t *testing.T, members ...string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for i, body := range members {
		file, err := writer.Create(fmt.Sprintf("dir/list-%d.txt", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestScanArchive(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    []Book
		wantErr bool
	}{
		{
			name: "three lines across two members keep order",
			data: zipFixture(t, lineA+"\n"+lineB+"\n", lineC+"\n"),
			want: []Book{bookA, bookB, bookC},
		},
		{
			name: "skip header and malformed bang line",
			data: zipFixture(t, "Results:\n!not-a-result\n"+lineA+"\n"),
			want: []Book{bookA},
		},
		{
			name: "crlf lines parsed",
			data: zipFixture(t, lineA+"\r\n", lineB+"\r\n"),
			want: []Book{bookA, bookB},
		},
		{
			name:    "not a zip",
			data:    []byte("not a zip"),
			wantErr: true,
		},
		{
			name: "empty zip",
			data: zipFixture(t),
			want: []Book{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scanArchive(tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("scanArchive() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("scanArchive() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// repeatReader yields an endless stream of one byte for the cap test.
type repeatReader byte

func (r repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func TestReadOffer(t *testing.T) {
	archive := zipFixture(t, lineA+"\n")

	t.Run("declared smaller than stream", func(t *testing.T) {
		if _, err := readOffer(bytes.NewReader(archive), int64(len(archive))-1); err == nil {
			t.Fatal("readOffer() error = nil, want truncated-offer error")
		}
	})

	t.Run("payload over cap", func(t *testing.T) {
		if _, err := readOffer(repeatReader('x'), int64(maxArchiveSize)+1); err == nil {
			t.Fatal("readOffer() error = nil, want over-cap error")
		}
	})

	t.Run("zero declared rejects empty payload", func(t *testing.T) {
		if _, err := readOffer(bytes.NewReader(nil), 0); err == nil {
			t.Fatal("readOffer() error = nil, want not-a-zip error")
		}
	})

	t.Run("declared equal to stream", func(t *testing.T) {
		got, err := readOffer(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			t.Fatalf("readOffer() error = %v", err)
		}
		if want := []Book{bookA}; !reflect.DeepEqual(got, want) {
			t.Errorf("readOffer() = %+v, want %+v", got, want)
		}
	})
}

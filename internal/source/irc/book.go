package irc

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/curedbylethe/booxd/internal/source"
)

// Book is one parsed result line from an @search answer.
type Book struct {
	Server string // first token after "!", the filebot nick, e.g. "artemis_serv"
	ID     string // second token after "!", before the pipe; dedupe and cache key
	Name   string // text after "|", extension removed
	Format string // that extension, lowercased, without the dot
	Size   string // value after "::INFO::", display only, e.g. "49.78MB"
}

var (
	serverRE = regexp.MustCompile("^[A-Za-z0-9_\\-\\[\\]\\\\\\^{}|`]+$")
	idRE     = regexp.MustCompile(`^[A-Za-z0-9]+$`)
	formatRE = regexp.MustCompile(`^[a-z0-9]+$`)
)

// validate checks every field that can reach the server. Printable Name bytes
// exclude controls; the nick charset already rules out leading @ or +.
func (b Book) validate() error {
	if len(b.Server) < 1 || len(b.Server) > 64 || !serverRE.MatchString(b.Server) {
		return fmt.Errorf("source irc: invalid server %q", b.Server)
	}
	if len(b.ID) < 1 || len(b.ID) > 64 || !idRE.MatchString(b.ID) {
		return fmt.Errorf("source irc: invalid id %q", b.ID)
	}
	if len(b.Name) < 1 || len(b.Name) > 160 || strings.Contains(b.Name, "::INFO::") {
		return fmt.Errorf("source irc: invalid name %q", b.Name)
	}
	for i := 0; i < len(b.Name); i++ {
		if b.Name[i] < 0x20 || b.Name[i] == 0x7f {
			return fmt.Errorf("source irc: invalid name %q", b.Name)
		}
	}
	if len(b.Format) < 1 || len(b.Format) > 16 || !formatRE.MatchString(b.Format) {
		return fmt.Errorf("source irc: invalid format %q", b.Format)
	}
	return nil
}

// Ref returns the book's source-qualified identity. The request line is
// rebuilt from fields, so the reference is self-contained and never carries
// received bytes such as the "::INFO::"/"::HASH::" tails.
func (b Book) Ref() source.Ref { return source.Ref{Source: source.IRC, ID: b.requestLine()} }

// requestLine returns the canonical bot request form, rebuilt from fields.
func (b Book) requestLine() string {
	return "!" + b.Server + " " + b.ID + " | " + b.Name + "." + b.Format
}

// BookFromRef rebuilds a Book from a printed reference. Nothing from the
// reference is sent verbatim: each field is parsed and validated, then the
// request line is rebuilt from the result.
func BookFromRef(r source.Ref) (Book, error) {
	if r.Source != source.IRC {
		return Book{}, fmt.Errorf("source irc: cannot build book from %q reference", r.Source)
	}
	if err := r.Validate(); err != nil {
		return Book{}, err
	}
	prefix, file, ok := strings.Cut(r.ID, " | ")
	if !ok {
		return Book{}, fmt.Errorf("source irc: reference %q is not a request line", r.ID)
	}
	if !strings.HasPrefix(prefix, "!") {
		return Book{}, fmt.Errorf("source irc: invalid request prefix %q", prefix)
	}
	tokens := strings.Fields(prefix[1:])
	if len(tokens) != 2 {
		return Book{}, fmt.Errorf("source irc: invalid request prefix %q", prefix)
	}
	ext := path.Ext(file)
	if ext == "" {
		return Book{}, fmt.Errorf("source irc: file %q has no extension", file)
	}
	b := Book{
		Server: tokens[0],
		ID:     tokens[1],
		Name:   strings.TrimSuffix(file, ext),
		Format: strings.ToLower(strings.TrimPrefix(ext, ".")),
	}
	if err := b.validate(); err != nil {
		return Book{}, err
	}
	return b, nil
}

// Source returns the provider-neutral view used by display code.
func (b Book) Source() source.Book {
	return source.Book{Ref: b.Ref(), Title: b.Name, Format: b.Format, Size: b.Size}
}

// parseLine returns false for anything that is not a well-formed result line.
func parseLine(line string) (Book, bool) {
	i := strings.Index(line, "::INFO::")
	if i < 0 {
		return Book{}, false
	}

	// Cut at the first whitespace: legacy lines append " ::HASH:: …".
	sizeFields := strings.Fields(line[i+len("::INFO::"):])
	if len(sizeFields) == 0 {
		return Book{}, false
	}
	size := sizeFields[0]

	request := strings.TrimSpace(line[:i])
	if !strings.HasPrefix(request, "!") {
		return Book{}, false
	}

	body := request[1:]
	pipe := strings.Index(body, "|")
	if pipe < 0 {
		return Book{}, false
	}

	tokens := strings.Fields(body[:pipe])
	if len(tokens) < 2 {
		return Book{}, false
	}

	namePart := strings.TrimSpace(body[pipe+1:])
	ext := path.Ext(namePart)
	if ext == "" {
		return Book{}, false
	}
	format := strings.ToLower(strings.TrimPrefix(ext, "."))
	name := strings.TrimSuffix(namePart, ext)

	b := Book{
		Server: tokens[0],
		ID:     tokens[1],
		Name:   name,
		Format: format,
		Size:   size,
	}
	if b.Server == "" || b.ID == "" || b.Name == "" || b.Format == "" || b.Size == "" {
		return Book{}, false
	}
	return b, true
}

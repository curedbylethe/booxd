package irc

import (
	"path"
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
	Raw    string // the whole "!" line, including the "::INFO::" tail
}

// Ref returns the book's source-qualified identity. The wire "::HASH::" tail
// is not captured, so the hash is empty.
func (b Book) Ref() source.Ref { return source.Ref{Source: source.IRC, ID: b.ID} }

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
		Raw:    line,
	}
	if b.Server == "" || b.ID == "" || b.Name == "" || b.Format == "" || b.Size == "" || b.Raw == "" {
		return Book{}, false
	}
	return b, true
}

// requestLine returns Raw without the "::INFO::" tail, the form the bot accepts.
func (b Book) requestLine() string {
	if i := strings.Index(b.Raw, "::INFO::"); i >= 0 {
		return strings.TrimSpace(b.Raw[:i])
	}
	return b.Raw
}

package source

import (
	"fmt"
	"regexp"
	"strings"
)

// Name identifies a book provider.
type Name string

// The providers a Ref can name.
const (
	Anna     Name = "anna"     // Anna's Archive: numeric record ID
	IRC      Name = "irc"      // #ebooks offer: hex ID from the result line
	ZLibrary Name = "zlibrary" // Z-Library: numeric ID plus a hash
)

// Ref is one book's identity. ID and Hash are provider-specific strings.
type Ref struct {
	Source Name
	ID     string
	Hash   string // empty when the provider has no hash
}

// legacyPattern is the bare form accepted for Anna's Archive: an ID with an
// optional display slug.
var legacyPattern = regexp.MustCompile(`^[0-9]+(?:-[A-Za-z0-9._-]+)?$`)

// Parse reads a display form back into a Ref. A reference without ":" is an
// Anna's Archive ID, optionally followed by a slug that is dropped.
func Parse(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("source: empty reference")
	}
	left, right, qualified := strings.Cut(s, ":")
	if !qualified {
		if !legacyPattern.MatchString(s) {
			return Ref{}, fmt.Errorf("source: invalid book id %q", s)
		}
		id, _, _ := strings.Cut(s, "-")
		return Ref{Source: Anna, ID: id}, nil
	}
	name := Name(left)
	if name != Anna && name != IRC && name != ZLibrary {
		return Ref{}, fmt.Errorf("source: unknown source %q", left)
	}
	id, hash, _ := strings.Cut(right, "/")
	ref := Ref{Source: name, ID: id, Hash: hash}
	if err := ref.Validate(); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

// String returns the display form: the bare ID for Anna's Archive, otherwise
// source:id with /hash appended when there is a hash.
func (r Ref) String() string {
	if r.Source == Anna {
		return r.ID
	}
	s := string(r.Source) + ":" + r.ID
	if r.Hash != "" {
		s += "/" + r.Hash
	}
	return s
}

// Key returns the fully qualified identity, independent of the display form.
func (r Ref) Key() string {
	return string(r.Source) + "\x00" + r.ID + "\x00" + r.Hash
}

// IsZero reports whether every field is empty.
func (r Ref) IsZero() bool { return r == Ref{} }

// Validate applies the per-source rules Parse applies.
func (r Ref) Validate() error {
	switch r.Source {
	case Anna:
		if !digits(r.ID) {
			return fmt.Errorf("source: invalid book id %q", r.ID)
		}
		if r.Hash != "" {
			return fmt.Errorf("source: anna reference takes no hash")
		}
	case ZLibrary:
		if !digits(r.ID) {
			return fmt.Errorf("source: invalid book id %q", r.ID)
		}
		if r.Hash == "" {
			return fmt.Errorf("source: zlibrary reference needs a hash")
		}
		if !alnum(r.Hash) {
			return fmt.Errorf("source: invalid hash %q", r.Hash)
		}
	case IRC:
		if !alnum(r.ID) {
			return fmt.Errorf("source: invalid book id %q", r.ID)
		}
		if r.Hash != "" && !alnum(r.Hash) {
			return fmt.Errorf("source: invalid hash %q", r.Hash)
		}
	default:
		return fmt.Errorf("source: unknown source %q", string(r.Source))
	}
	return nil
}

func digits(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

func alnum(s string) bool {
	return s != "" && strings.Trim(s, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") == ""
}

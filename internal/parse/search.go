package parse

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

var (
	cardClasses     = []string{"bg-white", "rounded-lg", "shadow", "p-4", "mb-4"}
	metadataClasses = []string{"text-sm", "text-[#666]", "mt-1"}
	sizePattern     = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)? (?:B|KB|MB|GB|TB)$`)
	formats         = map[string]bool{"PDF": true, "EPUB": true, "MOBI": true, "AZW": true, "AZW3": true, "DJVU": true, "FB2": true, "LIT": true, "TXT": true, "RTF": true, "DOC": true, "DOCX": true, "HTML": true, "HTM": true, "ZIP": true, "RAR": true, "7Z": true, "CBR": true, "CBZ": true, "MP3": true, "M4B": true, "M4A": true, "OGG": true, "FLAC": true}
)

const (
	titleTag        = "h3"
	publisherPrefix = "Publisher: "
	countPrefix     = "Showing "
	countSuffix     = " on this page"
)

// Book is a search result. Missing fields are empty.
type Book struct{ ID, Slug, Title, Author, Year, Format, Size, Publisher, Language, URL string }

// Search parses a search page into book records without network access.
func Search(r io.Reader) ([]Book, error) {
	root, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parse search: %w", err)
	}
	var books []Book
	count := -1
	walk(root, func(n *html.Node) {
		if n.Type == html.TextNode {
			s := collapse(n.Data)
			if strings.HasPrefix(s, countPrefix) && strings.Contains(s, " result") && strings.Contains(s, countSuffix) {
				var v int
				if _, err := fmt.Sscanf(s, "Showing %d", &v); err == nil {
					count = v
				}
			}
		}
		if n.Type == html.ElementNode && n.Data == "div" && hasClasses(n, cardClasses) {
			books = append(books, parseCard(n))
		}
	})
	if count >= 0 && count != len(books) {
		return nil, fmt.Errorf("parse search: parsed %d of %d results", len(books), count)
	}
	// A real miss says "Showing 0 results"; no count and no cards is another page.
	if count < 0 && len(books) == 0 {
		return nil, fmt.Errorf("parse search: no result count; not a search results page")
	}
	return books, nil
}

func walk(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}
func collapse(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	walk(n, func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
	})
	return collapse(b.String())
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasClasses(n *html.Node, classes []string) bool {
	fields := strings.Fields(attr(n, "class"))
	for _, wanted := range classes {
		if !slices.Contains(fields, wanted) {
			return false
		}
	}
	return true
}
func parseCard(card *html.Node) Book {
	var b Book
	headingSeen, metadataSeen := false, false
	walk(card, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if !headingSeen && n.Data == titleTag {
			headingSeen = true
			b.Title = nodeText(n)
			found := false
			walk(n, func(a *html.Node) {
				if !found && a.Type == html.ElementNode && a.Data == "a" {
					found = true
					b.Title = nodeText(a)
					b.URL = attr(a, "href")
				}
			})
			return
		}
		if !headingSeen || n.Data != "div" || !hasClasses(n, metadataClasses) {
			return
		}
		value := nodeText(n)
		if !metadataSeen && strings.Contains(value, "·") {
			metadataSeen = true
			parseMetadata(&b, value)
			return
		}
		if metadataSeen && b.Publisher == "" && strings.HasPrefix(value, publisherPrefix) {
			b.Publisher = strings.TrimPrefix(value, publisherPrefix)
		}
	})
	if _, path, ok := strings.Cut(b.URL, "/books/"); ok {
		path, _, _ = strings.Cut(path, "?")
		path, _, _ = strings.Cut(path, "#")
		id, slug, ok := strings.Cut(path, "-")
		if ok && id != "" && strings.Trim(id, "0123456789") == "" {
			b.ID = id
			b.Slug = slug
		}
	}
	return b
}
func year(s string) bool {
	if len(s) != 4 || strings.Trim(s, "0123456789") != "" {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1000 && n <= 2099
}
func parseMetadata(b *Book, value string) {
	parts := strings.Split(value, "·")
	parts = parts[:len(parts)-1]
	for i := range parts {
		parts[i] = collapse(parts[i])
	}
	if len(parts) > 0 && !year(parts[0]) && !sizePattern.MatchString(parts[0]) && !formats[strings.ToUpper(parts[0])] {
		if !strings.EqualFold(parts[0], "unknown") && !strings.EqualFold(parts[0], "unknown author") {
			b.Author = parts[0]
		}
		parts = parts[1:]
	}
	for _, p := range parts {
		switch {
		case b.Year == "" && year(p):
			b.Year = p
		case b.Size == "" && sizePattern.MatchString(p):
			b.Size = p
		case b.Format == "" && formats[strings.ToUpper(p)]:
			b.Format = strings.ToUpper(p)
		}
	}
}

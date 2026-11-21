package parse

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/curedbylethe/booxd/internal/source"
	"golang.org/x/net/html"
)

// Detail contains metadata and download options from a book page.
type Detail struct {
	ID                                                                  source.Ref
	Title, Author, Publisher, Year, Language, Format, Size, ISBN, Pages string
	Description                                                         string
	Downloads                                                           []Download
	LoginWall                                                           bool
}

// Download describes one download option and whether it requires a session.
type Download struct {
	Label           string
	URL             string
	RequiresSession bool
}

// DetailPage parses a book detail page without network access.
func DetailPage(r io.Reader) (Detail, error) {
	root, err := html.Parse(r)
	if err != nil {
		return Detail{}, fmt.Errorf("parse detail: %w", err)
	}

	var detail Detail
	var detailsPanel, downloadsPanel, overviewPanel *html.Node
	walk(root, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "div" {
			return
		}
		switch attr(n, "id") {
		case "book-panel-details":
			if detailsPanel == nil {
				detailsPanel = n
			}
		case "book-panel-downloads":
			if downloadsPanel == nil {
				downloadsPanel = n
			}
		case "book-panel-overview":
			if overviewPanel == nil {
				overviewPanel = n
			}
		}
	})

	var table *html.Node
	if detailsPanel != nil {
		walk(detailsPanel, func(n *html.Node) {
			if table == nil && n.Type == html.ElementNode && n.Data == "table" {
				table = n
			}
		})
	}
	if table == nil {
		return Detail{}, fmt.Errorf("parse detail: no detail table")
	}

	values := make(map[string]string)
	walk(table, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "tr" {
			return
		}
		var cells []*html.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "td" {
				cells = append(cells, c)
			}
		}
		if len(cells) != 2 {
			return
		}
		label, value := collapse(nodeText(cells[0])), collapse(nodeText(cells[1]))
		if label != "" {
			values[label] = value
		}
	})
	if len(values) == 0 {
		return Detail{}, fmt.Errorf("parse detail: no detail table")
	}
	if id := values["Record ID"]; id != "" {
		detail.ID = source.Ref{Source: source.Anna, ID: id}
	}
	detail.Title = values["Title"]
	detail.Author = values["Author"]
	detail.Publisher = values["Publisher"]
	detail.Year = values["Publication year"]
	detail.Pages = values["Pages"]
	detail.Language = values["Language"]
	detail.Format = values["Format"]
	detail.Size = values["Approx. size"]
	detail.ISBN = values["ISBN"]
	if strings.EqualFold(detail.Author, "unknown author") {
		detail.Author = ""
	}
	if detail.Pages == "0" {
		detail.Pages = ""
	}
	detail.Description = description(overviewPanel)

	if downloadsPanel == nil {
		return detail, nil
	}
	walk(downloadsPanel, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "li" || !hasClasses(n, []string{"list-disc"}) {
			return
		}
		var label, href, linkText string
		walk(n, func(child *html.Node) {
			if child.Type != html.ElementNode {
				return
			}
			if label == "" && child.Data == "span" && hasClasses(child, []string{"font-medium", "text-[#444]"}) {
				label = nodeText(child)
			}
			if href == "" && child.Data == "a" {
				if candidate := attr(child, "href"); candidate != "" {
					href = candidate
					linkText = nodeText(child)
				}
			}
		})
		label = collapse(label)
		if label == "" || href == "" {
			return
		}
		detail.Downloads = append(detail.Downloads, Download{
			Label:           label,
			URL:             href,
			RequiresSession: accountLink(href) || strings.Contains(linkText, "Log in to access downloads"),
		})
	})
	detail.LoginWall = strings.Contains(nodeText(downloadsPanel), "Log in to access downloads") &&
		(len(detail.Downloads) == 0 || allRequireSession(detail.Downloads))
	return detail, nil
}

// descriptionPlaceholder is what the catalog renders instead of overview prose.
const descriptionPlaceholder = "No long-form description is available for this record in the imported catalog yet."

// description returns the overview prose with markup removed, one paragraph per
// blank-line-separated block and the placeholder treated as no description.
func description(overview *html.Node) string {
	if overview == nil {
		return ""
	}
	var prose *html.Node
	walk(overview, func(n *html.Node) {
		if prose == nil && n.Type == html.ElementNode && n.Data == "div" && hasClasses(n, []string{"prose", "max-w-none"}) {
			prose = n
		}
	})
	if prose == nil {
		return ""
	}
	var b strings.Builder
	blockText(prose, &b)
	if collapse(b.String()) == descriptionPlaceholder {
		return ""
	}
	return paragraphs(b.String())
}

var blockTags = map[string]bool{
	"br": true, "div": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "ol": true, "p": true, "ul": true,
}

func blockText(n *html.Node, b *strings.Builder) {
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
		return
	}
	if n.Type != html.ElementNode {
		return
	}
	block := blockTags[n.Data]
	if block {
		b.WriteByte('\n')
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		blockText(c, b)
	}
	if block {
		b.WriteByte('\n')
	}
}

// paragraphs collapses whitespace within each line and separates blocks by a
// blank line. Bullet lines stay on their own line inside their block.
func paragraphs(s string) string {
	var blocks, lines []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if line = collapse(line); line == "" {
			if len(lines) > 0 {
				blocks = append(blocks, strings.Join(lines, "\n"))
				lines = nil
			}
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}

func accountLink(href string) bool {
	if strings.Contains(href, "not_member") {
		return true
	}
	u, err := url.Parse(href)
	return err == nil && u.Path == "/account"
}

func allRequireSession(downloads []Download) bool {
	for _, download := range downloads {
		if !download.RequiresSession {
			return false
		}
	}
	return true
}

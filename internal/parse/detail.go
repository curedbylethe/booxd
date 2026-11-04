package parse

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Detail contains metadata and download options from a book page.
type Detail struct {
	ID, Title, Author, Publisher, Year, Language, Format, Size, ISBN, Pages string
	Downloads                                                               []Download
	LoginWall                                                               bool
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
	var detailsPanel, downloadsPanel *html.Node
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
	detail.ID = values["Record ID"]
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

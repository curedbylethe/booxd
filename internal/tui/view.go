package tui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

func draw(screen tcell.Screen, m model) {
	width, height := screen.Size()
	screen.Fill(' ', m.theme.base)
	if width < 80 || height < 24 {
		screen.PutStrStyled(0, 0, "need at least 80x24", m.theme.base)
		return
	}
	if m.help {
		drawHelp(screen, m, width, height)
		return
	}
	switch m.view {
	case stateQuery, stateResults:
		drawResults(screen, m, width, height)
	case stateDetail:
		drawDetail(screen, m, width, height)
	}
}

func drawResults(screen tcell.Screen, m model, width, height int) {
	search := "search: " + string(m.input)
	screen.PutStrStyled(0, 0, truncate(search, width), m.theme.base)
	if m.view == stateQuery {
		caretX := len("search: ") + uniseg.StringWidth(string(m.input[:m.caret]))
		if caretX < width {
			caret := ' '
			if m.caret < len(m.input) {
				caret = m.input[m.caret]
			}
			screen.SetContent(caretX, 0, caret, nil, m.theme.reverse)
		}
	}

	visible := visibleResults(height)
	for row := 0; row < visible; row++ {
		index := m.offset + row
		if index >= len(m.books) {
			break
		}
		y := row*2 + 1
		style := m.theme.base
		metadataStyle := m.theme.dim
		if index == m.selected {
			style = m.theme.reverse
			metadataStyle = m.theme.reverse
			screen.PutStrStyled(0, y, strings.Repeat(" ", width), style)
			screen.PutStrStyled(0, y+1, strings.Repeat(" ", width), style)
		}
		book := m.books[index]
		values := []string{book.Author, book.Year, book.Format, book.Size}
		metadata := make([]string, 0, len(values))
		for _, value := range values {
			if value != "" {
				metadata = append(metadata, value)
			}
		}
		screen.PutStrStyled(0, y, truncate(book.Title, width), style)
		screen.PutStrStyled(0, y+1, truncate(strings.Join(metadata, " · "), width), metadataStyle)
	}
	status := m.status
	if status == "" {
		status = "? for help"
	}
	statusStyle := m.theme.base
	if m.statusErr {
		statusStyle = m.theme.alert
	}
	screen.PutStrStyled(0, height-1, truncate(status, width), statusStyle)
}

func drawDetail(screen tcell.Screen, m model, width, height int) {
	if m.selected < len(m.books) {
		b := m.books[m.selected]
		fields := []struct{ name, value string }{
			{"title", b.Title},
			{"author", b.Author},
			{"year", b.Year},
			{"format", b.Format},
			{"size", b.Size},
			{"publisher", b.Publisher},
			{"language", b.Language},
			{"id", b.ID},
			{"url", b.URL},
		}
		for i, field := range fields {
			if i >= height-2 {
				break
			}
			label := field.name + ":"
			screen.PutStrStyled(0, i, label, m.theme.dim)
			screen.PutStrStyled(len(label)+1, i, truncate(field.value, width-len(label)-1), m.theme.base)
		}
	}
	screen.PutStrStyled(0, height-1, "esc back · q quit", m.theme.dim)
}

func drawHelp(screen tcell.Screen, m model, width, height int) {
	lines := []string{
		"/ focus search",
		"Enter submit search or open selected result",
		"j / Down move down",
		"k / Up move up",
		"Esc go back",
		"q quit",
		"? show help",
		"Ctrl-C quit",
	}
	for i, line := range lines {
		if i >= height-1 {
			break
		}
		screen.PutStrStyled(0, i, truncate(line, width), m.theme.base)
	}
	screen.PutStrStyled(0, height-1, "? or esc closes", m.theme.dim)
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if uniseg.StringWidth(s) <= width {
		return s
	}
	graphemes := uniseg.NewGraphemes(s)
	n := 0
	var out strings.Builder
	for graphemes.Next() {
		if n+graphemes.Width() > width-1 {
			break
		}
		n += graphemes.Width()
		out.WriteString(graphemes.Str())
	}
	return out.String() + "…"
}

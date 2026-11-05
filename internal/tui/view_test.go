package tui

import (
	"strings"
	"testing"

	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/gdamore/tcell/v2"
)

func TestFrameAudit(t *testing.T) {
	style := newTheme(false)
	resultFrame := func(count, selected, offset int) model {
		return model{
			view:     stateResults,
			books:    booksForTest(count),
			selected: selected,
			offset:   offset,
			height:   24,
			theme:    style,
		}
	}
	book := booksForTest(1)[0]
	book.Publisher = "Press"
	book.Language = "en"
	book.URL = "/books/0"
	sparseBook := booksForTest(1)[0]
	sparseBook.Author = ""
	sparseBook.Year = ""

	tests := []struct {
		name         string
		width        int
		height       int
		model        model
		selectedRows [2]int
		wantTitle    string
		wantMeta     string
		minimum      bool
	}{
		{name: "results three books", width: 80, height: 24, model: resultFrame(3, 1, 0), selectedRows: [2]int{3, 4}, wantTitle: "Title 00", wantMeta: "Author · 2024 · epub · 1 MB"},
		{name: "results twenty-two books scrolled", width: 80, height: 24, model: resultFrame(22, 21, 11), selectedRows: [2]int{21, 22}},
		{name: "results omit empty metadata", width: 80, height: 24, model: model{view: stateResults, books: []parse.Book{sparseBook}, selected: 0, height: 24, theme: style}, selectedRows: [2]int{1, 2}, wantMeta: "epub · 1 MB"},
		{name: "results empty", width: 80, height: 24, model: resultFrame(0, 0, 0), selectedRows: [2]int{-1, -1}},
		{name: "results error", width: 80, height: 24, model: model{view: stateResults, status: "offline", statusErr: true, height: 24, theme: style}, selectedRows: [2]int{-1, -1}},
		{name: "detail", width: 80, height: 24, model: model{view: stateDetail, books: []parse.Book{book}, height: 24, theme: style}, selectedRows: [2]int{-1, -1}},
		{name: "help", width: 80, height: 24, model: model{view: stateResults, help: true, height: 24, theme: style}, selectedRows: [2]int{-1, -1}},
		{name: "results wide", width: 100, height: 30, model: model{view: stateResults, books: booksForTest(3), height: 30, theme: style}, selectedRows: [2]int{1, 2}},
		{name: "below minimum width", width: 79, height: 24, model: model{view: stateResults, height: 24, theme: style}, selectedRows: [2]int{-1, -1}, minimum: true},
		{name: "below minimum height", width: 80, height: 23, model: model{view: stateResults, height: 23, theme: style}, selectedRows: [2]int{-1, -1}, minimum: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			screen := newSimulation(t, tt.width, tt.height)
			draw(screen, tt.model)
			screen.Show()
			cells, width, height := screen.GetContents()
			if width != tt.width || height != tt.height {
				t.Fatalf("screen size=%dx%d, want %dx%d", width, height, tt.width, tt.height)
			}
			if len(cells) != width*height {
				t.Fatalf("cell count=%d, want %d", len(cells), width*height)
			}
			for i, cell := range cells {
				if cell.Style != style.base && cell.Style != style.dim && cell.Style != style.reverse && cell.Style != style.alert {
					t.Fatalf("cell %d uses unapproved style %v", i, cell.Style)
				}
			}

			for y := 0; y < height; y++ {
				first := -1
				for x := 0; x < width; x++ {
					cell := cells[x+y*width]
					if isBlankCell(cell) {
						continue
					}
					if first < 0 {
						first = x
					}
					for _, r := range cell.Runes {
						if isBoxRune(r) {
							t.Fatalf("box-drawing rune %q at (%d,%d)", r, x, y)
						}
					}
				}
				if first > 0 {
					t.Fatalf("row %d begins at column %d, want left-aligned at column 0", y, first)
				}
			}

			for _, row := range tt.selectedRows {
				if row >= 0 {
					for x := 0; x < width; x++ {
						if got := cells[x+row*width].Style; got != style.reverse {
							t.Fatalf("selected row %d column %d style=%v, want reverse %v", row, x, got, style.reverse)
						}
					}
				}
			}

			row := func(y int) string {
				var line strings.Builder
				for x := 0; x < width; x++ {
					line.WriteString(runesOf(cells[x+y*width]))
				}
				return line.String()
			}
			if tt.wantTitle != "" && (!strings.Contains(row(1), tt.wantTitle) || strings.Contains(row(1), "TITLE")) {
				t.Fatalf("first result title row=%q", row(1))
			}
			if tt.wantMeta != "" {
				if got := strings.TrimRight(row(2), " "); got != tt.wantMeta {
					t.Fatalf("result metadata row=%q, want %q", got, tt.wantMeta)
				}
			}

			if tt.minimum {
				line := make([]string, width)
				for x := range line {
					line[x] = runesOf(cells[x])
				}
				if got := strings.TrimRight(strings.Join(line, ""), " "); got != "need at least 80x24" {
					t.Fatalf("minimum message=%q", got)
				}
				for i := width; i < len(cells); i++ {
					if !isBlankCell(cells[i]) {
						t.Fatalf("minimum frame draws below row 0 at cell %d: %q", i, runesOf(cells[i]))
					}
				}
			}
		})
	}
}

func isBlankCell(cell tcell.SimCell) bool {
	text := runesOf(cell)
	return text == "" || text == " "
}

func runesOf(cell tcell.SimCell) string {
	return string(cell.Runes)
}

func isBoxRune(r rune) bool {
	return r >= '\u2500' && r <= '\u257f' || strings.ContainsRune("+|", r)
}

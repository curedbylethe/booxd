package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/curedbylethe/booxd/internal/source"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
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
	zbook := book
	zbook.ID = source.Ref{Source: source.ZLibrary, ID: "5512", Hash: "a1b2c3"}
	zbook.URL = ""
	downloadFrame := func(text string, busy bool) model {
		return model{view: stateDetail, books: []parse.Book{zbook}, book: zbook, download: downloadState{ref: zbook.ID, text: text, busy: busy}, hasDownload: true, height: 24, theme: style}
	}
	const running = "esc cancel · j/k scroll · q quit"

	tests := []struct {
		name         string
		width        int
		height       int
		model        model
		selectedRows [2]int
		wantTitle    string
		wantMeta     string
		minimum      bool
		wantDownload string
		wantFooter   string
	}{
		{name: "results three books", width: 80, height: 24, model: resultFrame(3, 1, 0), selectedRows: [2]int{3, 4}, wantTitle: "Title 00", wantMeta: "Author · 2024 · epub · 1 MB"},
		{name: "results twenty-two books scrolled", width: 80, height: 24, model: resultFrame(22, 21, 11), selectedRows: [2]int{21, 22}},
		{name: "results omit empty metadata", width: 80, height: 24, model: model{view: stateResults, books: []parse.Book{sparseBook}, selected: 0, height: 24, theme: style}, selectedRows: [2]int{1, 2}, wantMeta: "epub · 1 MB"},
		{name: "results empty", width: 80, height: 24, model: resultFrame(0, 0, 0), selectedRows: [2]int{-1, -1}},
		{name: "results error", width: 80, height: 24, model: model{view: stateResults, status: "offline", statusErr: true, height: 24, theme: style}, selectedRows: [2]int{-1, -1}},
		{name: "detail", width: 80, height: 24, model: model{view: stateDetail, books: []parse.Book{book}, book: book, height: 24, theme: style}, selectedRows: [2]int{-1, -1}, wantDownload: "zlibrary only"},
		{name: "download idle", width: 80, height: 24, model: model{view: stateDetail, books: []parse.Book{zbook}, book: zbook, hasDownload: true, height: 24, theme: style}, selectedRows: [2]int{-1, -1}, wantDownload: "-"},
		{name: "download starting", width: 80, height: 24, model: downloadFrame("…", true), selectedRows: [2]int{-1, -1}, wantDownload: "…", wantFooter: running},
		{name: "download percent", width: 80, height: 24, model: downloadFrame(progressText(42, 100), true), selectedRows: [2]int{-1, -1}, wantDownload: "42%", wantFooter: running},
		{name: "download bytes", width: 80, height: 24, model: downloadFrame(progressText(3<<20, 0), true), selectedRows: [2]int{-1, -1}, wantDownload: "3 MiB", wantFooter: running},
		{name: "download saved", width: 80, height: 24, model: downloadFrame(downloadText("/home/you/Downloads/5512.epub", nil), false), selectedRows: [2]int{-1, -1}, wantDownload: "saved /home/you/Downloads/5512.epub"},
		{name: "download exists", width: 80, height: 24, model: downloadFrame(downloadText("", ErrExists), false), selectedRows: [2]int{-1, -1}, wantDownload: "already downloaded"},
		{name: "download session expired", width: 80, height: 24, model: downloadFrame(downloadText("", ErrSessionExpired), false), selectedRows: [2]int{-1, -1}, wantDownload: "session expired"},
		{name: "download quota", width: 80, height: 24, model: downloadFrame(downloadText("", ErrQuota), false), selectedRows: [2]int{-1, -1}, wantDownload: "quota exhausted"},
		{name: "download cancelled", width: 80, height: 24, model: downloadFrame(downloadText("", fmt.Errorf("get: %w", context.Canceled)), false), selectedRows: [2]int{-1, -1}, wantDownload: "cancelled"},
		{name: "download failed", width: 80, height: 24, model: downloadFrame(downloadText("", errors.New("offline")), false), selectedRows: [2]int{-1, -1}, wantDownload: "failed"},
		{name: "download failed with reason", width: 80, height: 24, model: downloadFrame(downloadText("", fmt.Errorf("%w: needs a session", ErrFailed)), false), selectedRows: [2]int{-1, -1}, wantDownload: "failed: needs a session"},
		{name: "download long path truncated", width: 80, height: 24, model: downloadFrame(downloadText("/"+strings.Repeat("d", 120), nil), false), selectedRows: [2]int{-1, -1}, wantDownload: "saved /" + strings.Repeat("d", 62) + "…"},
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

			if tt.wantDownload != "" {
				if got, want := strings.TrimRight(row(9), " "), "download: "+tt.wantDownload; got != want {
					t.Fatalf("download row=%q, want %q", got, want)
				}
				want := tt.wantFooter
				if want == "" {
					want = "d download · esc back · j/k scroll · q quit"
				}
				if got := strings.TrimRight(row(height-1), " "); got != want {
					t.Fatalf("detail footer=%q, want %q", got, want)
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

func TestWrap(t *testing.T) {
	long := strings.Repeat("x", 200)
	lines := wrap(long, 80)
	if len(lines) != 3 || len(lines[0]) != 80 || len(lines[1]) != 80 || len(lines[2]) != 40 {
		t.Fatalf("long word lines=%d widths=%d,%d", len(lines), len(lines[0]), len(lines[1]))
	}
	if got := wrap("one two three", 7); len(got) != 2 || got[0] != "one two" || got[1] != "three" {
		t.Fatalf("space wrap=%q", got)
	}
	if got := wrap("a\n\nb", 80); len(got) != 3 || got[0] != "a" || got[1] != "" || got[2] != "b" {
		t.Fatalf("blank line wrap=%q", got)
	}
	cjk := wrap(strings.Repeat("漢", 50), 80)
	if len(cjk) != 2 || uniseg.StringWidth(cjk[0]) != 80 || uniseg.StringWidth(cjk[1]) != 20 {
		t.Fatalf("cjk lines=%d widths=%d,%d", len(cjk), uniseg.StringWidth(cjk[0]), uniseg.StringWidth(cjk[1]))
	}
	for _, line := range wrap(strings.Repeat("漢字 words ", 30), 80) {
		if uniseg.StringWidth(line) > 80 {
			t.Fatalf("line wider than 80: %q", line)
		}
	}
}

func TestDetailLayout(t *testing.T) {
	style := newTheme(false)
	book := booksForTest(1)[0]
	book.Publisher = "Press"
	book.Language = "en"
	book.URL = "/books/0"
	m := model{view: stateDetail, books: []parse.Book{book}, book: book, description: "First line\n\nSecond paragraph", width: 80, height: 24, theme: style}
	screen := newSimulation(t, 80, 24)
	draw(screen, m)
	screen.Show()
	lines, _, _, _ := simLines(screen)
	want := []string{"title: Title 00", "author: Author", "year: 2024", "format: epub", "size: 1 MB", "publisher: Press", "language: en", "id: 0", "url: /books/0", "download: zlibrary only"}
	for i, prefix := range want {
		if !strings.HasPrefix(strings.TrimRight(lines[i], " "), prefix) {
			t.Fatalf("row %d=%q, want prefix %q", i, lines[i], prefix)
		}
	}
	if strings.TrimRight(lines[10], " ") != "" {
		t.Fatalf("row 10 should be blank: %q", lines[10])
	}
	if strings.TrimRight(lines[11], " ") != "description:" {
		t.Fatalf("label row=%q", lines[11])
	}
	if strings.TrimRight(lines[12], " ") != "First line" {
		t.Fatalf("body row 12=%q", lines[12])
	}
	if strings.TrimRight(lines[13], " ") != "" || strings.TrimRight(lines[14], " ") != "Second paragraph" {
		t.Fatalf("paragraph rows=%q %q", lines[13], lines[14])
	}
	if !strings.Contains(lines[23], "esc back · j/k scroll · q quit") {
		t.Fatalf("footer=%q", lines[23])
	}

	empty := model{view: stateDetail, books: []parse.Book{book}, book: book, width: 80, height: 24, theme: style}
	screen = newSimulation(t, 80, 24)
	draw(screen, empty)
	screen.Show()
	lines, _, _, _ = simLines(screen)
	if strings.TrimRight(lines[11], " ") != "description:" || strings.TrimRight(lines[12], " ") != "-" {
		t.Fatalf("empty description rows=%q %q", lines[11], lines[12])
	}

	scrolled := empty
	scrolled.description = strings.Join([]string{"01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12", "13"}, "\n")
	scrolled.detailOffset = 2
	screen = newSimulation(t, 80, 24)
	draw(screen, scrolled)
	screen.Show()
	lines, _, _, _ = simLines(screen)
	if strings.TrimRight(lines[12], " ") != "03" || strings.TrimRight(lines[21], " ") != "12" || strings.TrimRight(lines[22], " ") != "" {
		t.Fatalf("scrolled rows=%q %q %q", lines[12], lines[21], lines[22])
	}
}

func TestDetailSmallTerminalWithDownload(t *testing.T) {
	z := zlibBook()
	description := strings.TrimSpace(strings.Repeat("A long description of the book. ", 40))
	for _, tt := range []struct {
		name     string
		download downloadState
		row      string
		footer   string
	}{
		{"running", downloadState{ref: z.ID, text: "42%", busy: true}, "download: 42%", "esc cancel · j/k scroll · q quit"},
		{"cancelled", downloadState{ref: z.ID, text: "cancelled"}, "download: cancelled", "d download · esc back · j/k scroll · q quit"},
		{"long path", downloadState{ref: z.ID, text: "saved /" + strings.Repeat("d", 120)}, "download: saved /" + strings.Repeat("d", 62) + "…", "d download · esc back · j/k scroll · q quit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := model{view: stateDetail, books: []parse.Book{z}, book: z, download: tt.download, hasDownload: true, description: description, width: 80, height: 24, theme: newTheme(false)}
			screen := newSimulation(t, 80, 24)
			draw(screen, m)
			screen.Show()
			lines, _, _, _ := simLines(screen)
			if got := strings.TrimRight(lines[9], " "); got != tt.row {
				t.Fatalf("download row=%q, want %q", got, tt.row)
			}
			if got := strings.TrimRight(lines[23], " "); got != tt.footer {
				t.Fatalf("footer=%q, want %q", got, tt.footer)
			}
			wrapped := wrap(description, 80)
			if strings.TrimRight(lines[11], " ") != "description:" || strings.TrimRight(lines[12], " ") != wrapped[0] || strings.TrimRight(lines[21], " ") != wrapped[9] {
				t.Fatalf("description rows=%q %q %q", lines[11], lines[12], lines[21])
			}
			if strings.TrimRight(lines[22], " ") != "" {
				t.Fatalf("row above footer=%q, want blank", lines[22])
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

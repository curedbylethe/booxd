package tui

import (
	"fmt"

	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/curedbylethe/booxd/internal/source"
	"github.com/gdamore/tcell/v2"
)

type viewState int

const (
	stateQuery viewState = iota
	stateResults
	stateDetail
)

type model struct {
	view         viewState
	input        []rune
	caret        int
	query        string
	books        []parse.Book
	searchSeq    int
	page         int // the page whose rows are on screen
	source       source.Name
	book         parse.Book
	download     downloadState
	selected     int
	offset       int
	status       string
	statusErr    bool
	help         bool
	description  string
	detailOffset int
	width        int
	height       int
	hasDetail    bool
	hasDownload  bool
	theme        theme
}

// downloadState is the one download slot: the book it belongs to, the text of
// its detail row, and whether it is still running.
type downloadState struct {
	ref  source.Ref
	text string
	busy bool
}

type action struct {
	dirty    bool
	quit     bool
	search   bool
	detail   bool
	download bool
	cancel   bool
}

// downloading reports whether the shown book's download is running.
func (m model) downloading() bool {
	return m.download.busy && m.download.ref == m.book.ID
}

// showsDetail reports whether the frame shows ref's detail screen.
func (m model) showsDetail(ref source.Ref) bool {
	return !m.help && m.view == stateDetail && m.book.ID == ref
}

func nextSource(from source.Name) source.Name {
	if from == source.Anna {
		return source.ZLibrary
	}
	return source.Anna
}

func handleKey(m model, ev *tcell.EventKey) (model, action) {
	if ev.Key() == tcell.KeyCtrlC {
		return m, action{quit: true}
	}
	if m.help {
		if ev.Key() == tcell.KeyEscape || ev.Rune() == '?' {
			m.help = false
			return m, action{dirty: true}
		}
		return m, action{}
	}

	switch m.view {
	case stateQuery:
		switch ev.Key() {
		case tcell.KeyEnter:
			m.query = string(m.input)
			if m.query == "" {
				m.status = "type a query"
				m.statusErr = false
				return m, action{dirty: true}
			}
			m.view = stateResults
			return m, action{dirty: true, search: true}
		case tcell.KeyEscape:
			m.view = stateResults
			return m, action{dirty: true}
		case tcell.KeyTab:
			// The shown results must not survive into the new source: drop
			// them so a pending or failed search leaves nothing to select.
			// Bumping searchSeq also drops a search still running on the old
			// source, and its "…" status with it.
			m.source = nextSource(m.source)
			m.searchSeq++
			m.status = ""
			m.statusErr = false
			m.books = nil
			m.selected = 0
			m.offset = 0
			return m, action{dirty: true}
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if m.caret > 0 {
				m.input = append(m.input[:m.caret-1], m.input[m.caret:]...)
				m.caret--
				return m, action{dirty: true}
			}
		case tcell.KeyDelete:
			if m.caret < len(m.input) {
				m.input = append(m.input[:m.caret], m.input[m.caret+1:]...)
				return m, action{dirty: true}
			}
		case tcell.KeyLeft:
			if m.caret > 0 {
				m.caret--
				return m, action{dirty: true}
			}
		case tcell.KeyRight:
			if m.caret < len(m.input) {
				m.caret++
				return m, action{dirty: true}
			}
		case tcell.KeyHome:
			if m.caret != 0 {
				m.caret = 0
				return m, action{dirty: true}
			}
		case tcell.KeyEnd:
			if m.caret != len(m.input) {
				m.caret = len(m.input)
				return m, action{dirty: true}
			}
		case tcell.KeyRune:
			if ev.Rune() >= ' ' && ev.Rune() != 0x7f {
				m.input = append(m.input, 0)
				copy(m.input[m.caret+1:], m.input[m.caret:])
				m.input[m.caret] = ev.Rune()
				m.caret++
				return m, action{dirty: true}
			}
		}
	case stateResults:
		oldSelected := m.selected
		switch ev.Key() {
		case tcell.KeyEscape:
			m.view = stateQuery
			m.input = []rune(m.query)
			m.caret = len(m.input)
			return m, action{dirty: true}
		case tcell.KeyTab:
			// The shown results follow the shown source, so a known query is
			// searched again on the new one. Drop the old source's rows so
			// nothing of theirs stays selectable while that search runs.
			m.source = nextSource(m.source)
			m.books = nil
			m.selected = 0
			m.offset = 0
			return m, action{dirty: true, search: m.query != ""}
		case tcell.KeyEnter:
			if len(m.books) > 0 {
				m.view = stateDetail
				m.book = m.books[m.selected]
				m.description = ""
				m.detailOffset = 0
				return m, action{dirty: true, detail: m.hasDetail}
			}
		case tcell.KeyUp:
			m.selected--
		case tcell.KeyDown:
			m.selected++
		case tcell.KeyRune:
			switch ev.Rune() {
			case '/':
				m.view = stateQuery
				m.input = []rune(m.query)
				m.caret = len(m.input)
				return m, action{dirty: true}
			case 'j':
				m.selected++
			case 'k':
				m.selected--
			case 'q':
				return m, action{quit: true}
			case '?':
				m.help = true
				return m, action{dirty: true}
			}
		}
		m.selected = max(0, min(m.selected, len(m.books)-1))
		oldOffset := m.offset
		m.offset = visibleOffset(m.selected, m.offset, visibleResults(m.height), len(m.books))
		if m.selected != oldSelected || m.offset != oldOffset {
			return m, action{dirty: true}
		}
	case stateDetail:
		switch ev.Key() {
		case tcell.KeyEscape:
			// The first Esc only cancels, so the outcome stays in view.
			if m.downloading() {
				return m, action{cancel: true}
			}
			m.view = stateResults
			m.detailOffset = 0
			m.status = ""
			m.statusErr = false
			return m, action{dirty: true}
		case tcell.KeyUp:
			return scrollDetail(m, -1)
		case tcell.KeyDown:
			return scrollDetail(m, 1)
		case tcell.KeyRune:
			switch ev.Rune() {
			case '/':
				m.view = stateQuery
				m.input = []rune(m.query)
				m.caret = len(m.input)
				return m, action{dirty: true}
			case 'j':
				return scrollDetail(m, 1)
			case 'k':
				return scrollDetail(m, -1)
			case 'd':
				if m.hasDownload && m.book.ID.Source == source.ZLibrary && !m.download.busy {
					return m, action{dirty: true, download: true}
				}
			case 'q':
				return m, action{quit: true}
			case '?':
				m.help = true
				return m, action{dirty: true}
			}
		}
	}
	return m, action{}
}

// descriptionRow is the description label's row: after the ten field rows
// and one blank row.
const descriptionRow = 11

// detailBodyRows is the number of lines between the description label and the
// footer.
func detailBodyRows(height int) int {
	return max(0, height-descriptionRow-3)
}

func clampDetailOffset(m model, offset int) int {
	return max(0, min(offset, max(0, len(wrap(m.description, m.width))-detailBodyRows(m.height))))
}

func scrollDetail(m model, delta int) (model, action) {
	offset := clampDetailOffset(m, m.detailOffset+delta)
	if offset == m.detailOffset {
		return m, action{}
	}
	m.detailOffset = offset
	return m, action{dirty: true}
}

// applySearch applies the result of search seq; a result for an older query or
// source is dropped, since a newer search owns the screen.
func applySearch(m model, seq, page int, books []parse.Book, err error) (model, bool) {
	if seq != m.searchSeq {
		return m, false
	}
	return applyResults(m, page, books, err), true
}

func applyResults(m model, page int, books []parse.Book, err error) model {
	if err != nil {
		m.status = err.Error()
		m.statusErr = true
		return m
	}
	m.statusErr = false
	m.books = books
	m.page = page
	m.selected = 0
	m.offset = 0
	if len(books) == 0 {
		m.status = "no results"
	} else {
		m.status = fmt.Sprintf("%d results", len(books))
	}
	return m
}

func visibleOffset(selected, offset, visible, count int) int {
	if visible <= 0 || count == 0 {
		return 0
	}
	if selected < offset {
		offset = selected
	}
	if selected >= offset+visible {
		offset = selected - visible + 1
	}
	if max := count - visible; offset > max {
		offset = max
	}
	if offset < 0 {
		return 0
	}
	return offset
}

func visibleResults(height int) int {
	return max(0, (height-2)/2)
}

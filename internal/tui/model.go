package tui

import (
	"fmt"

	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/gdamore/tcell/v2"
)

type viewState int

const (
	stateQuery viewState = iota
	stateResults
	stateDetail
)

type model struct {
	view     viewState
	input    []rune
	caret    int
	query    string
	books    []parse.Book
	selected int
	offset   int
	status   string
	help     bool
	width    int
	height   int
}

type action struct {
	dirty  bool
	quit   bool
	search bool
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
				return m, action{dirty: true}
			}
			m.view = stateResults
			return m, action{dirty: true, search: true}
		case tcell.KeyEscape:
			m.view = stateResults
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
		case tcell.KeyEnter:
			if len(m.books) > 0 {
				m.view = stateDetail
				return m, action{dirty: true}
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
		if len(m.books) > 0 {
			if m.selected < 0 {
				m.selected = 0
			}
			if m.selected >= len(m.books) {
				m.selected = len(m.books) - 1
			}
		}
		oldOffset := m.offset
		m.offset = visibleOffset(m.selected, m.offset, m.height-3, len(m.books))
		if m.selected != oldSelected || m.offset != oldOffset {
			return m, action{dirty: true}
		}
	case stateDetail:
		switch ev.Key() {
		case tcell.KeyEscape:
			m.view = stateResults
			return m, action{dirty: true}
		case tcell.KeyRune:
			switch ev.Rune() {
			case '/':
				m.view = stateQuery
				m.input = []rune(m.query)
				m.caret = len(m.input)
				return m, action{dirty: true}
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

func applyResults(m model, books []parse.Book, err error) model {
	if err != nil {
		m.status = err.Error()
		return m
	}
	m.books = books
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
	if max := count - visible; offset > max && max > 0 {
		offset = max
	}
	if offset < 0 {
		return 0
	}
	return offset
}

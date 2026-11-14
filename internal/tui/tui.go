package tui

import (
	"context"
	"fmt"

	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/gdamore/tcell/v2"
)

// SearchFunc searches one page for query.
type SearchFunc func(ctx context.Context, query string) ([]parse.Book, error)

// DetailFunc loads one book's detail page.
type DetailFunc func(ctx context.Context, id string) (parse.Detail, error)

// Options configures the TUI.
type Options struct {
	Search SearchFunc
	Detail DetailFunc
	Screen tcell.Screen
	Mono   bool
}

// Run initializes and runs the terminal interface.
func Run(ctx context.Context, opts Options) error {
	if opts.Search == nil {
		panic("tui: Search is required")
	}
	screen := opts.Screen
	if screen == nil {
		var err error
		screen, err = tcell.NewScreen()
		if err != nil {
			return fmt.Errorf("create screen: %w", err)
		}
		if err := screen.Init(); err != nil {
			return fmt.Errorf("initialize screen: %w", err)
		}
		defer screen.Fini()
	}
	return runScreenWithTheme(ctx, screen, opts.Search, opts.Detail, opts.Mono)
}

func runScreenWithTheme(ctx context.Context, screen tcell.Screen, search SearchFunc, detail DetailFunc, mono bool) error {
	screen.HideCursor()
	width, height := screen.Size()
	m := model{view: stateQuery, width: width, height: height, hasDetail: detail != nil, theme: newTheme(mono)}
	paint(screen, m)
	for {
		event := screen.PollEvent()
		if event == nil {
			return nil
		}
		switch ev := event.(type) {
		case *tcell.EventError:
			return fmt.Errorf("%s", ev.Error())
		case *tcell.EventResize:
			m.width, m.height = screen.Size()
			m.offset = visibleOffset(m.selected, m.offset, visibleResults(m.height), len(m.books))
			m.detailOffset = clampDetailOffset(m, m.detailOffset)
			screen.Sync()
			paint(screen, m)
		case *tcell.EventKey:
			var act action
			m, act = handleKey(m, ev)
			if act.quit {
				return nil
			}
			if act.search {
				m.status = "…"
				m.statusErr = false
				paint(screen, m)
				books, err := search(ctx, m.query)
				m = applyResults(m, books, err)
				paint(screen, m)
				continue
			}
			if act.detail {
				m.status = "…"
				m.statusErr = false
				paint(screen, m)
				if m.selected < len(m.books) && m.books[m.selected].ID != "" {
					result, err := detail(ctx, m.books[m.selected].ID)
					if err != nil {
						m.status = err.Error()
						m.statusErr = true
					} else {
						m.description = result.Description
						m.status = ""
						m.statusErr = false
					}
				} else {
					m.status = ""
				}
				m.detailOffset = 0
				paint(screen, m)
				continue
			}
			if act.dirty {
				paint(screen, m)
			}
		}
	}
}

// paint is the only caller of Show. One deliberate frame per state change.
func paint(screen tcell.Screen, m model) {
	draw(screen, m)
	screen.Show()
}

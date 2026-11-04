package tui

import (
	"context"
	"fmt"

	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/gdamore/tcell/v2"
)

// SearchFunc searches one page for query.
type SearchFunc func(ctx context.Context, query string) ([]parse.Book, error)

// Options configures the TUI.
type Options struct {
	Search SearchFunc
	Screen tcell.Screen
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
	return runScreen(ctx, screen, opts.Search)
}

func runScreen(ctx context.Context, screen tcell.Screen, search SearchFunc) error {
	screen.HideCursor()
	_, height := screen.Size()
	m := model{view: stateQuery, height: height}
	draw(screen, m)
	screen.Show()
	for {
		event := screen.PollEvent()
		if event == nil {
			return nil
		}
		switch ev := event.(type) {
		case *tcell.EventError:
			return fmt.Errorf("%s", ev.Error())
		case *tcell.EventResize:
			_, m.height = screen.Size()
			m.offset = visibleOffset(m.selected, m.offset, m.height-3, len(m.books))
			screen.Sync()
			draw(screen, m)
			screen.Show()
		case *tcell.EventKey:
			var act action
			m, act = handleKey(m, ev)
			if act.quit {
				return nil
			}
			if act.search {
				m.status = "…"
				draw(screen, m)
				screen.Show()
				books, err := search(ctx, m.query)
				m = applyResults(m, books, err)
				draw(screen, m)
				screen.Show()
				continue
			}
			if act.dirty {
				draw(screen, m)
				screen.Show()
			}
		}
	}
}

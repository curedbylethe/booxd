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
	return runScreenWithTheme(ctx, screen, opts.Search, opts.Mono)
}

func runScreen(ctx context.Context, screen tcell.Screen, search SearchFunc) error {
	return runScreenWithTheme(ctx, screen, search, false)
}

func runScreenWithTheme(ctx context.Context, screen tcell.Screen, search SearchFunc, mono bool) error {
	screen.HideCursor()
	_, height := screen.Size()
	m := model{view: stateQuery, height: height, theme: newTheme(mono)}
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
			_, m.height = screen.Size()
			m.offset = visibleOffset(m.selected, m.offset, m.height-3, len(m.books))
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

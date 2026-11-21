package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/curedbylethe/booxd/internal/source"
	"github.com/gdamore/tcell/v2"
)

// SearchFunc searches one page of from for query.
type SearchFunc func(ctx context.Context, from source.Name, query string) ([]parse.Book, error)

// DetailFunc loads one book's detail page.
type DetailFunc func(ctx context.Context, ref source.Ref) (parse.Detail, error)

// DownloadFunc saves ref's file and returns its path. progress may be called
// from any goroutine; a total of zero or less means the size is unknown. ctx
// is cancelled when the user cancels the download or quits.
type DownloadFunc func(ctx context.Context, ref source.Ref, progress func(done, total int64)) (string, error)

// Download outcomes a DownloadFunc reports. The download row shows the text of
// ErrExists, ErrSessionExpired and ErrQuota as is. An error wrapping ErrFailed
// is shown whole, so its text must hold no link or cookie. An error wrapping
// context.Canceled reads "cancelled"; any other error reads "failed".
var (
	ErrExists         = errors.New("already downloaded")
	ErrSessionExpired = errors.New("session expired")
	ErrQuota          = errors.New("quota exhausted")
	ErrFailed         = errors.New("failed")
)

// Options configures the TUI.
type Options struct {
	Search   SearchFunc
	Detail   DetailFunc
	Download DownloadFunc
	// Source is the provider selected at start; empty means anna.
	Source source.Name
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
	return run(ctx, screen, opts, false)
}

// update carries finished work back to the UI goroutine, the only goroutine
// that changes the model. apply reports whether the visible frame changed.
type update struct {
	tcell.EventTime
	apply func(model) (model, bool)
}

// run is the event loop. Work runs on its own goroutine and posts an update;
// with inline set, work runs in place and its updates apply before the next
// key, which keeps scripted tests deterministic.
func run(ctx context.Context, screen tcell.Screen, opts Options, inline bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var queued []tcell.Event
	post := func(ev tcell.Event) {
		// The queue is full only while the loop is busy; retry until it
		// drains or the loop has returned.
		for screen.PostEvent(ev) != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	start := func(work func()) { go work() }
	if inline {
		post = func(ev tcell.Event) { queued = append(queued, ev) }
		start = func(work func()) { work() }
	}

	screen.HideCursor()
	width, height := screen.Size()
	from := opts.Source
	if from == "" {
		from = source.Anna
	}
	m := model{view: stateQuery, width: width, height: height, source: from, hasDetail: opts.Detail != nil, hasDownload: opts.Download != nil, theme: newTheme(opts.Mono)}
	paint(screen, m)
	// cancelDownload stops the one running download; only this goroutine
	// touches it.
	cancelDownload := context.CancelFunc(func() {})
	for {
		var event tcell.Event
		if len(queued) > 0 {
			event, queued = queued[0], queued[1:]
		} else {
			event = screen.PollEvent()
		}
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
		case *update:
			var dirty bool
			if m, dirty = ev.apply(m); dirty {
				paint(screen, m)
			}
		case *tcell.EventKey:
			var act action
			m, act = handleKey(m, ev)
			switch {
			case act.quit:
				return nil
			case act.search:
				m.searchSeq++
				m.status = "…"
				m.statusErr = false
				paint(screen, m)
				seq, from, query := m.searchSeq, m.source, m.query
				start(func() {
					books, err := opts.Search(ctx, from, query)
					post(&update{apply: func(m model) (model, bool) { return applySearch(m, seq, books, err) }})
				})
			case act.detail:
				m.status = "…"
				m.statusErr = false
				paint(screen, m)
				ref := m.book.ID
				if ref.IsZero() {
					m.status = ""
					paint(screen, m)
					continue
				}
				start(func() {
					result, err := opts.Detail(ctx, ref)
					post(&update{apply: func(m model) (model, bool) {
						if m.view != stateDetail || m.book.ID != ref {
							return m, false
						}
						if err != nil {
							m.status = err.Error()
							m.statusErr = true
						} else {
							m.description = result.Description
							m.status = ""
							m.statusErr = false
						}
						m.detailOffset = 0
						return m, !m.help
					}})
				})
			case act.download:
				ref := m.book.ID
				m.download = downloadState{ref: ref, text: "…", busy: true}
				paint(screen, m)
				dctx, dcancel := context.WithCancel(ctx)
				cancelDownload = dcancel
				start(func() {
					defer dcancel()
					// Only the download goroutine touches last, so a step is
					// posted, and a frame drawn, only when the row text changes.
					last := "…"
					progress := func(done, total int64) {
						text := progressText(done, total)
						if text == last {
							return
						}
						last = text
						post(&update{apply: func(m model) (model, bool) {
							if !m.download.busy || m.download.ref != ref {
								return m, false
							}
							m.download.text = text
							return m, m.showsDetail(ref)
						}})
					}
					path, err := opts.Download(dctx, ref, progress)
					text := downloadText(path, err)
					post(&update{apply: func(m model) (model, bool) {
						m.download = downloadState{ref: ref, text: text}
						return m, m.showsDetail(ref)
					}})
				})
			case act.cancel:
				// The row changes when the download returns, not before.
				cancelDownload()
			case act.dirty:
				paint(screen, m)
			}
		}
	}
}

// progressText is the download row while bytes arrive: whole percent when the
// total is known, otherwise whole MiB. A known total advances one step per
// whole percent or per MiB, whichever is coarser, so a large file still moves
// without redrawing on every write.
func progressText(done, total int64) string {
	if total > 0 {
		if done >= total {
			return "100%"
		}
		step := max(total/100, 1<<20)
		return fmt.Sprintf("%d%%", min(done/step*step*100/total, 100))
	}
	return fmt.Sprintf("%d MiB", done>>20)
}

// downloadText is the download row once a download ends.
func downloadText(path string, err error) string {
	if err == nil {
		return "saved " + path
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	for _, known := range []error{ErrExists, ErrSessionExpired, ErrQuota} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	if errors.Is(err, ErrFailed) {
		return err.Error()
	}
	return ErrFailed.Error()
}

// paint is the only caller of Show. One deliberate frame per state change.
func paint(screen tcell.Screen, m model) {
	draw(screen, m)
	screen.Show()
}

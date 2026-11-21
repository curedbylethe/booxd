package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/curedbylethe/booxd/internal/source"
	"github.com/gdamore/tcell/v2"
)

func TestRunScreenDrawsElevenRowsAndHidesCursor(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	books := booksForTest(20)
	books[0].Title = strings.Repeat("Long title ", 8)
	books[1].Title = "漢字e\u0301"
	calls := 0
	err := runScreen(context.Background(), sim, func(context.Context, source.Name, string) ([]parse.Book, error) {
		calls++
		return books, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("search calls=%d", calls)
	}
	lines, cells, width, height := simLines(sim)
	if width != 80 || height != 24 {
		t.Fatalf("size=%dx%d", width, height)
	}
	if !strings.HasPrefix(lines[0], "search [anna]:     x") || strings.Contains(lines[1], "TITLE") {
		t.Fatalf("search and first result lines: %q %q", lines[0], lines[1])
	}
	for i := 0; i < 11; i++ {
		want := fmt.Sprintf("Title %02d", i)
		if i == 0 {
			if len([]rune(lines[1])) != 80 || !strings.HasSuffix(lines[1], "…") {
				t.Fatalf("long title was not truncated at panel width: %q", lines[1])
			}
		} else if i == 1 && (!strings.Contains(lines[3], "漢") || !strings.Contains(lines[3], "字") || !strings.Contains(lines[3], "e\u0301")) {
			t.Fatalf("wide and combining title missing from %q", lines[3])
		}
		titleRow := 1 + i*2
		if i > 1 && !strings.Contains(lines[titleRow], want) {
			t.Fatalf("row %d missing from %q", i, lines[titleRow])
		}
		if !strings.Contains(lines[titleRow+1], "Author · 2024 · epub · 1 MB") {
			t.Fatalf("metadata row %d missing fields: %q", i, lines[titleRow+1])
		}
	}
	if !strings.Contains(lines[23], "20 results") {
		t.Fatalf("status=%q", lines[23])
	}
	for _, y := range []int{1, 2} {
		_, _, attrs := cells[y*width].Style.Decompose()
		if attrs&tcell.AttrReverse == 0 {
			t.Fatalf("selected row %d is not reversed", y)
		}
	}
	_, _, visible := sim.GetCursor()
	if visible {
		t.Fatal("cursor is visible")
	}
}

func TestRunScreenKeysDoNotSearchAgain(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, '?', tcell.ModNone)
	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, '/', tcell.ModNone)
	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	calls := 0
	if err := runScreen(context.Background(), sim, func(context.Context, source.Name, string) ([]parse.Book, error) {
		calls++
		return booksForTest(2), nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("search calls=%d, want 1", calls)
	}
}

func TestRunScreenDetailUsesCallback(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	searches, details := 0, 0
	book := parse.Book{ID: annaRef("42"), Title: "A title", Author: "An author", Year: "2024", Format: "epub", Size: "1 MB", Publisher: "Press", Language: "en", URL: "/books/42"}
	var gotRef source.Ref
	detail := func(_ context.Context, ref source.Ref) (parse.Detail, error) {
		details++
		gotRef = ref
		return parse.Detail{Description: "First line"}, nil
	}
	if err := runScreenWithTheme(context.Background(), sim, func(context.Context, source.Name, string) ([]parse.Book, error) {
		searches++
		return []parse.Book{book}, nil
	}, detail, false); err != nil {
		t.Fatal(err)
	}
	lines, _, _, _ := simLines(sim)
	if searches != 1 || details != 1 {
		t.Fatalf("search calls=%d detail calls=%d", searches, details)
	}
	if gotRef != book.ID {
		t.Fatalf("detail ref=%+v, want %+v", gotRef, book.ID)
	}
	if !strings.Contains(lines[0], "title: A title") || !strings.Contains(lines[8], "url: /books/42") {
		t.Fatalf("detail metadata=%q", lines[:9])
	}
	if strings.TrimRight(lines[11], " ") != "description:" || strings.TrimRight(lines[12], " ") != "First line" {
		t.Fatalf("description rows=%q %q", lines[11], lines[12])
	}
	if strings.TrimRight(lines[23], " ") != "d download · esc back · j/k scroll · q quit" {
		t.Fatalf("footer=%q", lines[23])
	}
}

func TestRunScreenDetailLoadingAndError(t *testing.T) {
	book := func() []parse.Book {
		return []parse.Book{{ID: annaRef("42"), Title: "A title", Author: "An author", Year: "2024", Format: "epub", Size: "1 MB", Publisher: "Press", Language: "en", URL: "/books/42"}}
	}
	open := func(sim tcell.SimulationScreen) {
		pressRune(sim, 'x')
		sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
		sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
		sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	}

	loading := newSimulation(t, 80, 24)
	open(loading)
	var status string
	detail := func(context.Context, source.Ref) (parse.Detail, error) {
		lines, _, _, _ := simLines(loading)
		status = strings.TrimRight(lines[23], " ")
		return parse.Detail{Description: "First line"}, nil
	}
	if err := runScreenWithTheme(context.Background(), loading, func(context.Context, source.Name, string) ([]parse.Book, error) {
		return book(), nil
	}, detail, false); err != nil {
		t.Fatal(err)
	}
	if status != "…" {
		t.Fatalf("loading status=%q, want %q", status, "…")
	}

	failing := newSimulation(t, 80, 24)
	open(failing)
	err := runScreenWithTheme(context.Background(), failing, func(context.Context, source.Name, string) ([]parse.Book, error) {
		return book(), nil
	}, func(context.Context, source.Ref) (parse.Detail, error) {
		return parse.Detail{}, errors.New("offline")
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	lines, cells, width, _ := simLines(failing)
	if !strings.Contains(lines[0], "title: A title") || strings.TrimRight(lines[12], " ") != "-" {
		t.Fatalf("metadata or fallback lost: %q %q", lines[0], lines[12])
	}
	if strings.TrimRight(lines[23], " ") != "offline" {
		t.Fatalf("error footer=%q", lines[23])
	}
	if cells[23*width].Style != newTheme(false).alert {
		t.Fatalf("error footer style=%v, want alert", cells[23*width].Style)
	}
}

func TestRunScreenDetailNilCallbackAndReopen(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	if err := runScreen(context.Background(), sim, func(context.Context, source.Name, string) ([]parse.Book, error) {
		return []parse.Book{{ID: annaRef("42"), Title: "A title"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	lines, _, _, _ := simLines(sim)
	if strings.TrimRight(lines[12], " ") != "-" || !strings.Contains(lines[23], "esc back · j/k scroll · q quit") {
		t.Fatalf("nil callback fallback=%q footer=%q", lines[12], lines[23])
	}

	reopen := newSimulation(t, 80, 24)
	pressRune(reopen, 'x')
	reopen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	reopen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	reopen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	reopen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	reopen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	calls := 0
	if err := runScreenWithTheme(context.Background(), reopen, func(context.Context, source.Name, string) ([]parse.Book, error) {
		return []parse.Book{{ID: annaRef("42"), Title: "A title"}}, nil
	}, func(context.Context, source.Ref) (parse.Detail, error) {
		calls++
		return parse.Detail{Description: "Again"}, nil
	}, false); err != nil {
		t.Fatal(err)
	}
	lines, _, _, _ = simLines(reopen)
	if calls != 2 || strings.TrimRight(lines[12], " ") != "Again" {
		t.Fatalf("reopen calls=%d row=%q", calls, lines[12])
	}
}

func TestRunScreenFailureKeepsPreviousResults(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, '/', tcell.ModNone)
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	calls := 0
	err := runScreen(context.Background(), sim, func(context.Context, source.Name, string) ([]parse.Book, error) {
		calls++
		if calls == 1 {
			return []parse.Book{{ID: annaRef("old"), Title: "Previous result"}}, nil
		}
		return nil, errors.New("offline")
	})
	if err != nil {
		t.Fatal(err)
	}
	lines, _, _, _ := simLines(sim)
	if calls != 2 || !strings.Contains(lines[1], "Previous result") || !strings.Contains(lines[23], "offline") {
		t.Fatalf("calls=%d result=%q status=%q", calls, lines[1], lines[23])
	}
}

func TestRunScreenResizeKeepsSelection(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	for i := 0; i < 3; i++ {
		sim.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	}
	sim.SetSize(100, 30)
	if err := sim.PostEvent(tcell.NewEventResize(100, 30)); err != nil {
		t.Fatal(err)
	}
	sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	if err := runScreen(context.Background(), sim, func(context.Context, source.Name, string) ([]parse.Book, error) {
		return booksForTest(20), nil
	}); err != nil {
		t.Fatal(err)
	}
	lines, cells, width, height := simLines(sim)
	if width != 100 || height != 30 || !strings.Contains(lines[7], "Title 03") {
		t.Fatalf("resize or selection lost: %dx%d row=%q", width, height, lines[7])
	}
	_, _, attrs := cells[7*width].Style.Decompose()
	if attrs&tcell.AttrReverse == 0 {
		t.Fatal("selected row is not reversed after resize")
	}

	small := newSimulation(t, 80, 24)
	pressRune(small, 'x')
	small.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	small.SetSize(79, 24)
	if err := small.PostEvent(tcell.NewEventResize(79, 24)); err != nil {
		t.Fatal(err)
	}
	small.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	if err := runScreen(context.Background(), small, func(context.Context, source.Name, string) ([]parse.Book, error) {
		return booksForTest(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	smallLines, _, smallWidth, smallHeight := simLines(small)
	if smallWidth != 79 || smallHeight != 24 || !strings.Contains(smallLines[0], "need at least 80x24") {
		t.Fatalf("small resize frame=%dx%d row=%q", smallWidth, smallHeight, smallLines[0])
	}
}

func TestRunScreenReturnsEventError(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	if err := sim.PostEvent(tcell.NewEventError(errors.New("screen failed"))); err != nil {
		t.Fatal(err)
	}
	err := runScreen(context.Background(), sim, func(context.Context, source.Name, string) ([]parse.Book, error) { return nil, nil })
	if err == nil || !strings.Contains(err.Error(), "screen failed") {
		t.Fatalf("error=%v", err)
	}
}

func TestFrameCount(t *testing.T) {
	queryResults := func() []tcell.Event {
		return []tcell.Event{
			tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone),
			tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
		}
	}
	queryWithJ := func(count int) []tcell.Event {
		events := queryResults()
		for i := 0; i < count; i++ {
			events = append(events, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
		}
		return events
	}
	runes := func(value string) []tcell.Event {
		events := make([]tcell.Event, 0, len(value))
		for _, r := range value {
			events = append(events, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
		}
		return events
	}
	key := func(k tcell.Key, r rune) tcell.Event {
		return tcell.NewEventKey(k, r, tcell.ModNone)
	}
	theme := newTheme(false)
	tests := []struct {
		name          string
		before        []tcell.Event
		target        []tcell.Event
		baseShows     int
		targetShows   int
		wantCalls     int
		cellX         int
		cellY         int
		wantRune      rune
		wantCellStyle tcell.Style
	}{
		{name: "runScreen starts", baseShows: 1, wantRune: 's', wantCellStyle: theme.base},
		{name: "j from row 0", before: queryResults(), target: []tcell.Event{key(tcell.KeyRune, 'j')}, baseShows: 4, targetShows: 1, wantCalls: 1, cellY: 3, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "k at row 0 is a no-op", before: queryResults(), target: []tcell.Event{key(tcell.KeyRune, 'k')}, baseShows: 4, wantCalls: 1, cellY: 1, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "j at row 19 is a no-op", before: queryWithJ(19), target: []tcell.Event{key(tcell.KeyRune, 'j')}, baseShows: 23, targetShows: 0, wantCalls: 1, cellY: 21, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "Down at row 19 is a no-op", before: queryWithJ(19), target: []tcell.Event{key(tcell.KeyDown, 0)}, baseShows: 23, targetShows: 0, wantCalls: 1, cellY: 21, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "Up at row 0 is a no-op", before: queryResults(), target: []tcell.Event{key(tcell.KeyUp, 0)}, baseShows: 4, targetShows: 0, wantCalls: 1, cellY: 1, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "x while help is open is a no-op", before: append(queryResults(), key(tcell.KeyRune, '?')), target: []tcell.Event{key(tcell.KeyRune, 'x')}, baseShows: 5, targetShows: 0, wantCalls: 1, wantRune: '/', wantCellStyle: theme.base},
		{name: "j in detail is a no-op", before: append(queryResults(), key(tcell.KeyEnter, 0)), target: []tcell.Event{key(tcell.KeyRune, 'j')}, baseShows: 5, targetShows: 0, wantCalls: 1, wantRune: 't', wantCellStyle: theme.dim},
		{name: "/ from results", before: queryResults(), target: []tcell.Event{key(tcell.KeyRune, '/')}, baseShows: 4, targetShows: 1, wantCalls: 1, wantRune: 's', wantCellStyle: theme.base},
		{name: "Esc from query to results", target: []tcell.Event{key(tcell.KeyEscape, 0)}, baseShows: 1, targetShows: 1, wantRune: 's', wantCellStyle: theme.base},
		{name: "Esc from results to query", before: queryResults(), target: []tcell.Event{key(tcell.KeyEscape, 0)}, baseShows: 4, targetShows: 1, wantCalls: 1, wantRune: 's', wantCellStyle: theme.base},
		{name: "Enter submits dune", before: runes("dune"), target: []tcell.Event{key(tcell.KeyEnter, 0)}, baseShows: 5, targetShows: 2, wantCalls: 1, cellY: 23, wantRune: '2', wantCellStyle: theme.base},
		{name: "Enter with an empty query", target: []tcell.Event{key(tcell.KeyEnter, 0)}, baseShows: 1, targetShows: 1, cellY: 23, wantRune: 't', wantCellStyle: theme.base},
		{name: "? then ?", before: queryResults(), target: []tcell.Event{key(tcell.KeyRune, '?'), key(tcell.KeyRune, '?')}, baseShows: 4, targetShows: 2, wantCalls: 1, cellY: 1, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "Enter from results opens detail", before: queryResults(), target: []tcell.Event{key(tcell.KeyEnter, 0)}, baseShows: 4, targetShows: 1, wantCalls: 1, wantRune: 't', wantCellStyle: theme.dim},
		{name: "same-size resize redraws once", before: queryResults(), target: []tcell.Event{tcell.NewEventResize(80, 24)}, baseShows: 4, targetShows: 1, wantCalls: 1, wantRune: 's', wantCellStyle: theme.base},
		{name: "q quits without another frame", before: queryResults(), target: []tcell.Event{key(tcell.KeyRune, 'q')}, baseShows: 4, wantCalls: 1, cellY: 1, wantRune: 'T', wantCellStyle: theme.reverse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			screen := newCountingScreen(t, 80, 24)
			events := append(append([]tcell.Event{}, tt.before...), tt.target...)
			events = append(events, key(tcell.KeyCtrlC, 0))
			screen.scriptEvents = events
			calls := 0
			search := func(context.Context, source.Name, string) ([]parse.Book, error) {
				calls++
				return booksForTest(20), nil
			}
			if err := runScreen(context.Background(), screen, search); err != nil {
				t.Fatal(err)
			}
			if screen.postErr != nil {
				t.Fatalf("script event: %v", screen.postErr)
			}
			if want := tt.baseShows + tt.targetShows; screen.shows != want {
				t.Fatalf("Show calls=%d, want %d", screen.shows, want)
			}
			if calls != tt.wantCalls {
				t.Fatalf("search calls=%d, want %d", calls, tt.wantCalls)
			}
			cells, width, _ := screen.GetContents()
			cell := cells[tt.cellY*width+tt.cellX]
			if len(cell.Runes) == 0 || cell.Runes[0] != tt.wantRune {
				t.Fatalf("cell (%d,%d)=%q, want first rune %q", tt.cellX, tt.cellY, string(cell.Runes), tt.wantRune)
			}
			if cell.Style != tt.wantCellStyle {
				t.Fatalf("cell (%d,%d) style=%v, want %v", tt.cellX, tt.cellY, cell.Style, tt.wantCellStyle)
			}
		})
	}
}

func TestInFlightStatus(t *testing.T) {
	screen := newCountingScreen(t, 80, 24)
	screen.scriptEvents = []tcell.Event{
		tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone),
	}
	calls := 0
	var status, previousRow string
	search := func(context.Context, source.Name, string) ([]parse.Book, error) {
		calls++
		if calls == 1 {
			return []parse.Book{{ID: annaRef("old"), Title: "Previous result"}}, nil
		}
		lines, _, _, _ := simLines(screen)
		status = strings.TrimRight(lines[23], " ")
		previousRow = strings.TrimRight(lines[1], " ")
		return nil, nil
	}
	if err := runScreen(context.Background(), screen, search); err != nil {
		t.Fatal(err)
	}
	if screen.postErr != nil {
		t.Fatalf("script event: %v", screen.postErr)
	}
	if calls != 2 {
		t.Fatalf("search calls=%d, want 2", calls)
	}
	if status != "…" {
		t.Fatalf("in-flight status=%q, want %q", status, "…")
	}
	if previousRow != "Previous result" {
		t.Fatalf("in-flight result row=%q, want previous result", previousRow)
	}
}

func TestCursorHidden(t *testing.T) {
	for _, tt := range []struct {
		name     string
		moveLeft bool
		wantX    int
		wantRune rune
	}{
		{name: "inside input", moveLeft: true, wantX: len("search [zlibrary]: ") + 2, wantRune: 'c'},
		{name: "at end of input", wantX: len("search [zlibrary]: ") + 3, wantRune: ' '},
	} {
		t.Run(tt.name, func(t *testing.T) {
			screen := newCountingScreen(t, 80, 24)
			screen.scriptEvents = []tcell.Event{
				tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone),
			}
			if tt.moveLeft {
				screen.scriptEvents = append(screen.scriptEvents, tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone))
			}
			screen.scriptEvents = append(screen.scriptEvents, tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone))
			if err := runScreen(context.Background(), screen, func(context.Context, source.Name, string) ([]parse.Book, error) { return nil, nil }); err != nil {
				t.Fatal(err)
			}
			if screen.postErr != nil {
				t.Fatalf("script event: %v", screen.postErr)
			}
			if screen.hides != 1 || screen.showCursor != 0 {
				t.Fatalf("HideCursor calls=%d ShowCursor calls=%d, want 1 and 0", screen.hides, screen.showCursor)
			}
			cells, width, _ := screen.GetContents()
			cell := cells[tt.wantX]
			if len(cell.Runes) == 0 || cell.Runes[0] != tt.wantRune {
				t.Fatalf("caret cell=%q, want %q", string(cell.Runes), tt.wantRune)
			}
			if cell.Style != newTheme(false).reverse {
				t.Fatalf("caret style=%v, want reverse %v", cell.Style, newTheme(false).reverse)
			}
			_, _, visible := screen.GetCursor()
			if visible {
				t.Fatal("terminal cursor is visible")
			}
			if width != 80 {
				t.Fatalf("screen width=%d, want 80", width)
			}
		})
	}
}

func TestRunUsesMonoOption(t *testing.T) {
	screen := newSimulation(t, 80, 24)
	screen.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	search := func(context.Context, source.Name, string) ([]parse.Book, error) {
		return nil, errors.New("offline")
	}
	// Run searches off the UI goroutine, so quit only once the error is drawn.
	hooked := &hookScreen{SimulationScreen: screen, onShow: func(lines []string) {
		if strings.HasPrefix(lines[23], "offline") {
			screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
		}
	}}
	if err := Run(context.Background(), Options{Search: search, Screen: hooked, Mono: true}); err != nil {
		t.Fatal(err)
	}
	_, height := screen.Size()
	cells, width, _ := screen.GetContents()
	status := cells[(height-1)*width]
	if want := newTheme(true).alert; status.Style != want {
		t.Fatalf("mono status style=%v, want %v", status.Style, want)
	}
	if status.Style != strip(status.Style) {
		t.Fatal("Run with Mono=true rendered a colored status")
	}
}

func runScreen(ctx context.Context, screen tcell.Screen, search SearchFunc) error {
	return runScreenWithTheme(ctx, screen, search, nil, false)
}

func runScreenWithTheme(ctx context.Context, screen tcell.Screen, search SearchFunc, detail DetailFunc, mono bool) error {
	return run(ctx, screen, Options{Search: search, Detail: detail, Mono: mono}, true)
}

// hookScreen hands each shown frame to onShow on the UI goroutine and reports
// each event that work posts on posted once it is queued, so tests of Run's
// background work can react without racing the loop.
type hookScreen struct {
	tcell.SimulationScreen
	onShow func(lines []string)
	posted chan struct{}
}

func (s *hookScreen) Show() {
	s.SimulationScreen.Show()
	if s.onShow != nil {
		lines, _, _, _ := simLines(s.SimulationScreen)
		s.onShow(lines)
	}
}

func (s *hookScreen) PostEvent(ev tcell.Event) error {
	err := s.SimulationScreen.PostEvent(ev)
	if err == nil && s.posted != nil {
		s.posted <- struct{}{}
	}
	return err
}

type countingScreen struct {
	tcell.SimulationScreen
	shows, hides, showCursor int
	scriptEvents             []tcell.Event
	scriptIndex              int
	postErr                  error
}

func (s *countingScreen) Show() {
	s.shows++
	s.SimulationScreen.Show()
	if s.scriptIndex < len(s.scriptEvents) {
		s.postNextScriptEvent()
	}
	if s.scriptIndex == len(s.scriptEvents)-1 {
		s.postNextScriptEvent()
	}
}

func (s *countingScreen) postNextScriptEvent() {
	if s.scriptIndex >= len(s.scriptEvents) || s.postErr != nil {
		return
	}
	s.postErr = s.PostEvent(s.scriptEvents[s.scriptIndex])
	if s.postErr == nil {
		s.scriptIndex++
	}
}

func (s *countingScreen) HideCursor() {
	s.hides++
	s.SimulationScreen.HideCursor()
}

func (s *countingScreen) ShowCursor(x, y int) {
	s.showCursor++
	s.SimulationScreen.ShowCursor(x, y)
}

func newCountingScreen(t *testing.T, width, height int) *countingScreen {
	t.Helper()
	return &countingScreen{SimulationScreen: newSimulation(t, width, height)}
}

func newSimulation(t *testing.T, width, height int) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(width, height)
	t.Cleanup(screen.Fini)
	return screen
}

func pressRune(screen tcell.SimulationScreen, r rune) {
	screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
}

func booksForTest(count int) []parse.Book {
	books := make([]parse.Book, count)
	for i := range books {
		books[i] = parse.Book{ID: annaRef(fmt.Sprint(i)), Title: fmt.Sprintf("Title %02d", i), Author: "Author", Year: "2024", Format: "epub", Size: "1 MB"}
	}
	return books
}

func simLines(screen tcell.SimulationScreen) ([]string, []tcell.SimCell, int, int) {
	cells, width, height := screen.GetContents()
	lines := make([]string, height)
	for row := 0; row < height; row++ {
		var line strings.Builder
		for col := 0; col < width; col++ {
			runes := cells[row*width+col].Runes
			if len(runes) == 0 {
				line.WriteByte(' ')
			} else {
				line.WriteString(string(runes))
			}
		}
		lines[row] = line.String()
	}
	return lines, cells, width, height
}

func zlibBook() parse.Book {
	return parse.Book{ID: source.Ref{Source: source.ZLibrary, ID: "5512", Hash: "a1b2c3"}, Title: "Kindred", Format: "epub"}
}

func TestSelectSourceCycles(t *testing.T) {
	tab := tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
	m := model{view: stateQuery, source: source.Anna}
	for _, want := range []source.Name{source.ZLibrary, source.Anna, source.ZLibrary} {
		var act action
		m, act = handleKey(m, tab)
		if m.source != want || act != (action{dirty: true}) {
			t.Fatalf("query Tab: source=%q action=%+v, want %q", m.source, act, want)
		}
	}
	for _, tt := range []struct {
		name string
		m    model
		want source.Name
		act  action
	}{
		{"results with a query searches again", model{view: stateResults, source: source.Anna, query: "dune"}, source.ZLibrary, action{dirty: true, search: true}},
		{"results without a query", model{view: stateResults, source: source.ZLibrary}, source.Anna, action{dirty: true}},
		{"detail ignores Tab", model{view: stateDetail, source: source.Anna}, source.Anna, action{}},
		{"help ignores Tab", model{view: stateResults, source: source.Anna, help: true}, source.Anna, action{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, act := handleKey(tt.m, tab)
			if got.source != tt.want || act != tt.act {
				t.Fatalf("source=%q action=%+v, want %q %+v", got.source, act, tt.want, tt.act)
			}
		})
	}
}

func TestSelectSourceShownAsTextWithoutShift(t *testing.T) {
	for _, tt := range []struct {
		from   source.Name
		prefix string
	}{
		{source.Anna, "search [anna]:     "},
		{source.ZLibrary, "search [zlibrary]: "},
	} {
		t.Run(string(tt.from), func(t *testing.T) {
			screen := newSimulation(t, 80, 24)
			draw(screen, model{view: stateQuery, source: tt.from, input: []rune("dune"), caret: 4, height: 24, theme: newTheme(false)})
			screen.Show()
			lines, cells, width, _ := simLines(screen)
			if !strings.HasPrefix(lines[0], tt.prefix+"dune") {
				t.Fatalf("search row=%q, want prefix %q", lines[0], tt.prefix+"dune")
			}
			if caret := cells[len("search [zlibrary]: dune")]; caret.Style != newTheme(false).reverse {
				t.Fatalf("caret style=%v, want reverse", caret.Style)
			}
			if got := strings.TrimRight(lines[23], " "); got != "Tab source · ? for help" {
				t.Fatalf("status=%q", got)
			}
			if width != 80 {
				t.Fatalf("width=%d", width)
			}
		})
	}
}

func TestSelectSourceStartsAndReachesSearch(t *testing.T) {
	for _, tt := range []struct {
		name  string
		start source.Name
		tab   bool
		want  source.Name
	}{
		{"default is anna", "", false, source.Anna},
		{"option sets zlibrary", source.ZLibrary, false, source.ZLibrary},
		{"Tab switches before search", "", true, source.ZLibrary},
		{"Tab switches back", source.ZLibrary, true, source.Anna},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sim := newSimulation(t, 80, 24)
			if tt.tab {
				sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
			}
			pressRune(sim, 'x')
			sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
			var got source.Name
			search := func(_ context.Context, from source.Name, _ string) ([]parse.Book, error) {
				got = from
				return nil, nil
			}
			if err := run(context.Background(), sim, Options{Search: search, Source: tt.start}, true); err != nil {
				t.Fatal(err)
			}
			lines, _, _, _ := simLines(sim)
			if got != tt.want || !strings.HasPrefix(lines[0], "search ["+string(tt.want)+"]:") {
				t.Fatalf("search source=%q row=%q, want %q", got, lines[0], tt.want)
			}
		})
	}
}

func TestSearchInFlightKeepsInputAndDropsStaleResult(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var mu sync.Mutex
	var queries []string
	search := func(_ context.Context, from source.Name, query string) ([]parse.Book, error) {
		mu.Lock()
		queries = append(queries, string(from)+":"+query)
		mu.Unlock()
		if query == "a" {
			<-release
			return []parse.Book{{ID: annaRef("stale"), Title: "Stale result"}}, nil
		}
		return []parse.Book{zlibBook()}, nil
	}
	posted := make(chan struct{}, 4)
	hooked := &hookScreen{SimulationScreen: sim, posted: posted}
	step := 0
	hooked.onShow = func(lines []string) {
		switch {
		case step == 0 && strings.TrimRight(lines[23], " ") == "…":
			// The first search is still running: edit the query, switch
			// source and search again.
			step = 1
			sim.InjectKey(tcell.KeyRune, '/', tcell.ModNone)
			sim.InjectKey(tcell.KeyBackspace, 0, tcell.ModNone)
			sim.InjectKey(tcell.KeyRune, 'b', tcell.ModNone)
			sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
			sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
		case step == 1 && strings.Contains(lines[1], "Kindred"):
			step = 2
			go func() {
				<-posted // the second search's result, already drawn
				unblock()
				<-posted // the first search's result, now queued
				sim.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
			}()
		}
	}
	timeout := time.AfterFunc(5*time.Second, func() { sim.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone) })
	defer timeout.Stop()
	pressRune(sim, 'a')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	if err := Run(context.Background(), Options{Search: search, Screen: hooked}); err != nil {
		t.Fatal(err)
	}
	lines, _, _, _ := simLines(sim)
	mu.Lock()
	defer mu.Unlock()
	// Both searches run at once, so either may record its query first.
	slices.Sort(queries)
	if step != 2 || strings.Join(queries, ",") != "anna:a,zlibrary:b" {
		t.Fatalf("step=%d queries=%q", step, queries)
	}
	if !strings.HasPrefix(lines[0], "search [zlibrary]: b") || !strings.Contains(lines[1], "Kindred") || strings.Contains(strings.Join(lines, "\n"), "Stale") {
		t.Fatalf("stale result drawn or fresh one lost: %q %q", lines[0], lines[1])
	}
}

func TestSearchStaleResultIsDropped(t *testing.T) {
	m := model{view: stateResults, searchSeq: 2, books: []parse.Book{zlibBook()}, status: "…"}
	got, dirty := applySearch(m, 1, []parse.Book{{ID: annaRef("old")}}, nil)
	if dirty || got.books[0].ID != zlibBook().ID || got.status != "…" {
		t.Fatalf("stale result applied: dirty=%t books=%v status=%q", dirty, got.books, got.status)
	}
	got, dirty = applySearch(m, 2, []parse.Book{{ID: annaRef("new")}}, nil)
	if !dirty || got.books[0].ID != annaRef("new") || got.status != "1 results" {
		t.Fatalf("current result not applied: dirty=%t books=%v status=%q", dirty, got.books, got.status)
	}
}

// openDetail scripts a search for x and opening its first result.
func openDetail(sim tcell.SimulationScreen, keys ...rune) {
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	for _, r := range keys {
		pressRune(sim, r)
	}
	pressRune(sim, 'q')
}

func TestDownloadCallsCallbackWithRef(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	openDetail(sim, 'd')
	calls := 0
	var got source.Ref
	var during string
	download := func(_ context.Context, ref source.Ref, progress func(done, total int64)) (string, error) {
		calls++
		got = ref
		lines, _, _, _ := simLines(sim)
		during = strings.TrimRight(lines[9], " ")
		progress(1, 2)
		return "/books/5512.epub", nil
	}
	search := func(context.Context, source.Name, string) ([]parse.Book, error) { return []parse.Book{zlibBook()}, nil }
	if err := run(context.Background(), sim, Options{Search: search, Download: download}, true); err != nil {
		t.Fatal(err)
	}
	lines, _, _, _ := simLines(sim)
	if calls != 1 || got != zlibBook().ID {
		t.Fatalf("download calls=%d ref=%+v", calls, got)
	}
	if during != "download: …" || strings.TrimRight(lines[9], " ") != "download: saved /books/5512.epub" {
		t.Fatalf("download row during=%q after=%q", during, lines[9])
	}
	if strings.TrimRight(lines[23], " ") != "d download · esc back · j/k scroll · q quit" {
		t.Fatalf("footer=%q", lines[23])
	}
}

func TestDownloadAnnaDetailOffersNone(t *testing.T) {
	screen := newCountingScreen(t, 80, 24)
	screen.scriptEvents = []tcell.Event{
		tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone),
	}
	download := func(context.Context, source.Ref, func(int64, int64)) (string, error) {
		t.Fatal("download called for an anna book")
		return "", nil
	}
	search := func(context.Context, source.Name, string) ([]parse.Book, error) { return booksForTest(1), nil }
	if err := run(context.Background(), screen, Options{Search: search, Download: download}, true); err != nil {
		t.Fatal(err)
	}
	if screen.postErr != nil {
		t.Fatalf("script event: %v", screen.postErr)
	}
	// start, x, searching, results, detail: d draws nothing.
	if screen.shows != 5 {
		t.Fatalf("Show calls=%d, want 5", screen.shows)
	}
	lines, _, _, _ := simLines(screen)
	if got := strings.TrimRight(lines[9], " "); got != "download: zlibrary only" {
		t.Fatalf("download row=%q", got)
	}
}

func TestDownloadKey(t *testing.T) {
	d := tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone)
	z := zlibBook()
	for _, tt := range []struct {
		name string
		m    model
		act  action
	}{
		{"zlibrary detail starts", model{view: stateDetail, book: z, hasDownload: true}, action{dirty: true, download: true}},
		{"zlibrary detail retries after an outcome", model{view: stateDetail, book: z, hasDownload: true, download: downloadState{ref: z.ID, text: "failed"}}, action{dirty: true, download: true}},
		{"busy is a no-op", model{view: stateDetail, book: z, hasDownload: true, download: downloadState{ref: z.ID, text: "4%", busy: true}}, action{}},
		{"anna detail is a no-op", model{view: stateDetail, book: booksForTest(1)[0], hasDownload: true}, action{}},
		{"no callback is a no-op", model{view: stateDetail, book: z}, action{}},
		{"results ignore d", model{view: stateResults, books: []parse.Book{z}, hasDownload: true}, action{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, act := handleKey(tt.m, d); act != tt.act {
				t.Fatalf("action=%+v, want %+v", act, tt.act)
			}
		})
	}
	other := zlibBook()
	other.ID.ID = "7731"
	m := model{view: stateDetail, book: other, download: downloadState{ref: z.ID, text: "4%", busy: true}}
	if got := downloadRow(m); got != "-" {
		t.Fatalf("another book's row=%q, want -", got)
	}
	esc := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
	if got, act := handleKey(m, esc); got.view != stateResults || act != (action{dirty: true}) {
		t.Fatalf("Esc on another book: view=%v action=%+v, want back", got.view, act)
	}
	m.book = z
	if got, act := handleKey(m, esc); got.view != stateDetail || act != (action{cancel: true}) {
		t.Fatalf("Esc on the running book: view=%v action=%+v, want cancel", got.view, act)
	}
}

func TestDownloadOutcomeText(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want string
	}{
		{nil, "saved /books/5512.epub"},
		{ErrExists, "already downloaded"},
		{ErrSessionExpired, "session expired"},
		{fmt.Errorf("wrapped: %w", ErrQuota), "quota exhausted"},
		{fmt.Errorf("%w: downloads need a signed-in session", ErrFailed), "failed: downloads need a signed-in session"},
		{errors.New("Get https://host.test/dl?token=secret: refused"), "failed"},
		{context.Canceled, "cancelled"},
		{fmt.Errorf("%w: %w", ErrFailed, context.Canceled), "cancelled"},
	} {
		if got := downloadText("/books/5512.epub", tt.err); got != tt.want {
			t.Errorf("downloadText(%v)=%q, want %q", tt.err, got, tt.want)
		}
	}
}

func TestProgressText(t *testing.T) {
	for _, tt := range []struct {
		done, total int64
		want        string
	}{
		{0, 1000, "0%"},
		{9, 1000, "0%"},
		{10, 1000, "1%"},
		{1000, 1000, "100%"},
		{2000, 1000, "100%"},
		{0, 0, "0 MiB"},
		{1<<20 - 1, -1, "0 MiB"},
		{5 << 20, 0, "5 MiB"},
	} {
		if got := progressText(tt.done, tt.total); got != tt.want {
			t.Errorf("progressText(%d, %d)=%q, want %q", tt.done, tt.total, got, tt.want)
		}
	}
}

func TestProgressFramesChangeAtCoarseSteps(t *testing.T) {
	for _, tt := range []struct {
		name   string
		report func(progress func(done, total int64))
		frames int
	}{
		// 1001 reports, 101 distinct percents, then the outcome.
		{"known total", func(progress func(done, total int64)) {
			for i := int64(0); i <= 1000; i++ {
				progress(i, 1000)
			}
		}, 102},
		// 21 reports a quarter MiB apart: 0 to 5 MiB, then the outcome.
		{"unknown total", func(progress func(done, total int64)) {
			for i := int64(0); i <= 20; i++ {
				progress(i<<18, -1)
			}
		}, 7},
	} {
		t.Run(tt.name, func(t *testing.T) {
			screen := newCountingScreen(t, 80, 24)
			screen.scriptEvents = []tcell.Event{
				tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
				tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone),
				tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone),
			}
			before := 0
			download := func(_ context.Context, _ source.Ref, progress func(done, total int64)) (string, error) {
				before = screen.shows
				tt.report(progress)
				return "/books/5512.epub", nil
			}
			search := func(context.Context, source.Name, string) ([]parse.Book, error) { return []parse.Book{zlibBook()}, nil }
			if err := run(context.Background(), screen, Options{Search: search, Download: download}, true); err != nil {
				t.Fatal(err)
			}
			if screen.postErr != nil {
				t.Fatalf("script event: %v", screen.postErr)
			}
			if got := screen.shows - before; got != tt.frames {
				t.Fatalf("frames during download=%d, want %d", got, tt.frames)
			}
			lines, _, _, _ := simLines(screen)
			if got := strings.TrimRight(lines[9], " "); got != "download: saved /books/5512.epub" {
				t.Fatalf("download row=%q", got)
			}
		})
	}
}

func TestDownloadCancel(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	second := zlibBook()
	second.ID.ID, second.Title = "7731", "Parable"
	searches := 0
	search := func(context.Context, source.Name, string) ([]parse.Book, error) {
		searches++
		return []parse.Book{zlibBook(), second}, nil
	}
	observed := make(chan error, 1)
	download := func(ctx context.Context, _ source.Ref, _ func(done, total int64)) (string, error) {
		<-ctx.Done()
		observed <- ctx.Err()
		return "", fmt.Errorf("get: %w", ctx.Err())
	}
	const running = "esc cancel · j/k scroll · q quit"
	step := 0
	var failure string
	esc := func() { sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone) }
	hooked := &hookScreen{SimulationScreen: sim}
	hooked.onShow = func(lines []string) {
		row := func(y int) string { return strings.TrimRight(lines[y], " ") }
		switch {
		case step == 0 && strings.Contains(lines[3], "Parable"):
			step = 1
			pressRune(sim, 'j')
			sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			pressRune(sim, 'd')
		case step == 1 && row(9) == "download: …":
			if row(23) != running {
				failure = "running footer=" + row(23)
			}
			step = 2
			esc()
		case step == 2 && row(9) == "download: cancelled":
			if row(0) != "title: Parable" || row(23) != "d download · esc back · j/k scroll · q quit" {
				failure = "after cancel title=" + row(0) + " footer=" + row(23)
			}
			step = 3
			esc()
		case step == 3 && strings.HasPrefix(lines[0], "search [zlibrary]: x"):
			_, cells, width, _ := simLines(sim)
			if !strings.Contains(lines[1], "Kindred") || !strings.Contains(lines[3], "Parable") || cells[3*width].Style != newTheme(false).reverse {
				failure = "results after back=" + lines[1] + " | " + lines[3]
			}
			step = 4
			sim.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
		}
	}
	timeout := time.AfterFunc(5*time.Second, func() { sim.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone) })
	defer timeout.Stop()
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	if err := Run(context.Background(), Options{Search: search, Download: download, Source: source.ZLibrary, Screen: hooked}); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
	if step != 4 || searches != 1 {
		t.Fatalf("step=%d searches=%d, want 4 and 1", step, searches)
	}
	select {
	case err := <-observed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("download ctx err=%v, want context.Canceled", err)
		}
	default:
		t.Fatal("download did not observe ctx.Done")
	}
}

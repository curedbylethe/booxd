package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/gdamore/tcell/v2"
)

func TestRunScreenDrawsTwentyRowsAndHidesCursor(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	books := booksForTest(20)
	books[0].Title = strings.Repeat("Long title ", 8)
	books[1].Title = "漢字e\u0301"
	calls := 0
	err := runScreen(context.Background(), sim, func(context.Context, string) ([]parse.Book, error) {
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
	if !strings.Contains(lines[0], "search: x") || !strings.Contains(lines[1], "TITLE") {
		t.Fatalf("header lines: %q %q", lines[0], lines[1])
	}
	for i := 0; i < 20; i++ {
		want := fmt.Sprintf("Title %02d", i)
		if i == 0 {
			want = "…"
		} else if i == 1 {
			if !strings.Contains(lines[i+2], "漢") || !strings.Contains(lines[i+2], "字") || !strings.Contains(lines[i+2], "e\u0301") {
				t.Fatalf("wide and combining title missing from %q", lines[i+2])
			}
			continue
		}
		if !strings.Contains(lines[i+2], want) {
			t.Fatalf("row %d missing from %q", i, lines[i+2])
		}
	}
	if !strings.Contains(lines[2][:40], "…") {
		t.Fatalf("long title was not truncated at column width: %q", lines[2][:34])
	}
	if !strings.Contains(lines[23], "20 results") {
		t.Fatalf("status=%q", lines[23])
	}
	_, _, attrs := cells[2*width].Style.Decompose()
	if attrs&tcell.AttrReverse == 0 {
		t.Fatal("selected row is not reversed")
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
	if err := runScreen(context.Background(), sim, func(context.Context, string) ([]parse.Book, error) {
		calls++
		return booksForTest(2), nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("search calls=%d, want 1", calls)
	}
}

func TestRunScreenDetailIsLocal(t *testing.T) {
	sim := newSimulation(t, 80, 24)
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	calls := 0
	book := parse.Book{ID: "42", Title: "A title", Author: "An author", Year: "2024", Format: "epub", Size: "1 MB", Publisher: "Press", Language: "en", URL: "/books/42"}
	if err := runScreen(context.Background(), sim, func(context.Context, string) ([]parse.Book, error) {
		calls++
		return []parse.Book{book}, nil
	}); err != nil {
		t.Fatal(err)
	}
	lines, _, _, _ := simLines(sim)
	if calls != 1 || !strings.Contains(lines[0], "title: A title") || !strings.Contains(lines[8], "url: /books/42") || !strings.Contains(lines[23], "esc back · q quit") {
		t.Fatalf("calls=%d detail=%q status=%q", calls, lines[:9], lines[23])
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
	err := runScreen(context.Background(), sim, func(context.Context, string) ([]parse.Book, error) {
		calls++
		if calls == 1 {
			return []parse.Book{{ID: "old", Title: "Previous result"}}, nil
		}
		return nil, errors.New("offline")
	})
	if err != nil {
		t.Fatal(err)
	}
	lines, _, _, _ := simLines(sim)
	if calls != 2 || !strings.Contains(lines[2], "Previous result") || !strings.Contains(lines[23], "offline") {
		t.Fatalf("calls=%d result=%q status=%q", calls, lines[2], lines[23])
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
	if err := runScreen(context.Background(), sim, func(context.Context, string) ([]parse.Book, error) {
		return booksForTest(20), nil
	}); err != nil {
		t.Fatal(err)
	}
	lines, cells, width, height := simLines(sim)
	if width != 100 || height != 30 || !strings.Contains(lines[5], "Title 03") {
		t.Fatalf("resize or selection lost: %dx%d row=%q", width, height, lines[5])
	}
	_, _, attrs := cells[5*width].Style.Decompose()
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
	if err := runScreen(context.Background(), small, func(context.Context, string) ([]parse.Book, error) {
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
	err := runScreen(context.Background(), sim, func(context.Context, string) ([]parse.Book, error) { return nil, nil })
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
		{name: "k at row 0 is a no-op", before: queryResults(), target: []tcell.Event{key(tcell.KeyRune, 'k')}, baseShows: 4, wantCalls: 1, cellY: 2, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "j at row 19 is a no-op", before: queryWithJ(19), target: []tcell.Event{key(tcell.KeyRune, 'j')}, baseShows: 23, targetShows: 0, wantCalls: 1, cellY: 21, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "Down at row 19 is a no-op", before: queryWithJ(19), target: []tcell.Event{key(tcell.KeyDown, 0)}, baseShows: 23, targetShows: 0, wantCalls: 1, cellY: 21, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "Up at row 0 is a no-op", before: queryResults(), target: []tcell.Event{key(tcell.KeyUp, 0)}, baseShows: 4, targetShows: 0, wantCalls: 1, cellY: 2, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "x while help is open is a no-op", before: append(queryResults(), key(tcell.KeyRune, '?')), target: []tcell.Event{key(tcell.KeyRune, 'x')}, baseShows: 5, targetShows: 0, wantCalls: 1, wantRune: '/', wantCellStyle: theme.base},
		{name: "j in detail is a no-op", before: append(queryResults(), key(tcell.KeyEnter, 0)), target: []tcell.Event{key(tcell.KeyRune, 'j')}, baseShows: 5, targetShows: 0, wantCalls: 1, wantRune: 't', wantCellStyle: theme.dim},
		{name: "/ from results", before: queryResults(), target: []tcell.Event{key(tcell.KeyRune, '/')}, baseShows: 4, targetShows: 1, wantCalls: 1, wantRune: 's', wantCellStyle: theme.base},
		{name: "Esc from query to results", target: []tcell.Event{key(tcell.KeyEscape, 0)}, baseShows: 1, targetShows: 1, wantRune: 's', wantCellStyle: theme.base},
		{name: "Esc from results to query", before: queryResults(), target: []tcell.Event{key(tcell.KeyEscape, 0)}, baseShows: 4, targetShows: 1, wantCalls: 1, wantRune: 's', wantCellStyle: theme.base},
		{name: "Enter submits dune", before: runes("dune"), target: []tcell.Event{key(tcell.KeyEnter, 0)}, baseShows: 5, targetShows: 2, wantCalls: 1, cellY: 23, wantRune: '2', wantCellStyle: theme.base},
		{name: "Enter with an empty query", target: []tcell.Event{key(tcell.KeyEnter, 0)}, baseShows: 1, targetShows: 1, cellY: 23, wantRune: 't', wantCellStyle: theme.base},
		{name: "? then ?", before: queryResults(), target: []tcell.Event{key(tcell.KeyRune, '?'), key(tcell.KeyRune, '?')}, baseShows: 4, targetShows: 2, wantCalls: 1, cellY: 2, wantRune: 'T', wantCellStyle: theme.reverse},
		{name: "Enter from results opens detail", before: queryResults(), target: []tcell.Event{key(tcell.KeyEnter, 0)}, baseShows: 4, targetShows: 1, wantCalls: 1, wantRune: 't', wantCellStyle: theme.dim},
		{name: "same-size resize redraws once", before: queryResults(), target: []tcell.Event{tcell.NewEventResize(80, 24)}, baseShows: 4, targetShows: 1, wantCalls: 1, wantRune: 's', wantCellStyle: theme.base},
		{name: "q quits without another frame", before: queryResults(), target: []tcell.Event{key(tcell.KeyRune, 'q')}, baseShows: 4, wantCalls: 1, cellY: 2, wantRune: 'T', wantCellStyle: theme.reverse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			screen := newCountingScreen(t, 80, 24)
			events := append(append([]tcell.Event{}, tt.before...), tt.target...)
			events = append(events, key(tcell.KeyCtrlC, 0))
			screen.scriptEvents = events
			calls := 0
			search := func(context.Context, string) ([]parse.Book, error) {
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
	search := func(context.Context, string) ([]parse.Book, error) {
		calls++
		if calls == 1 {
			return []parse.Book{{ID: "old", Title: "Previous result"}}, nil
		}
		lines, _, _, _ := simLines(screen)
		status = strings.TrimRight(lines[23], " ")
		previousRow = strings.TrimRight(lines[2], " ")
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
		{name: "inside input", moveLeft: true, wantX: len("search: ") + 2, wantRune: 'c'},
		{name: "at end of input", wantX: len("search: ") + 3, wantRune: ' '},
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
			if err := runScreen(context.Background(), screen, func(context.Context, string) ([]parse.Book, error) { return nil, nil }); err != nil {
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

func TestEveryCellUsesAThemeStyle(t *testing.T) {
	screen := newSimulation(t, 80, 24)
	theme := newTheme(false)
	m := model{
		view:      stateResults,
		books:     booksForTest(3),
		selected:  1,
		status:    "offline",
		statusErr: true,
		height:    24,
		theme:     theme,
	}
	draw(screen, m)
	cells, _, _ := screen.GetContents()
	for i, cell := range cells {
		if cell.Style != theme.base && cell.Style != theme.dim && cell.Style != theme.reverse && cell.Style != theme.alert {
			t.Fatalf("cell %d uses unapproved style %v", i, cell.Style)
		}
	}
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
		books[i] = parse.Book{ID: fmt.Sprint(i), Title: fmt.Sprintf("Title %02d", i), Author: "Author", Year: "2024", Format: "epub", Size: "1 MB"}
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

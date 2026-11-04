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
	sim := newSimulation(t, 100, 30)
	pressRune(sim, 'x')
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	for i := 0; i < 3; i++ {
		sim.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	}
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

package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/curedbylethe/booxd/internal/parse"
	"github.com/curedbylethe/booxd/internal/source"
	"github.com/gdamore/tcell/v2"
)

func TestHandleKeyQuery(t *testing.T) {
	tests := []struct {
		name  string
		key   tcell.Key
		r     rune
		input string
		caret int
		want  string
		at    int
		a     action
	}{
		{"insert", tcell.KeyRune, 'x', "ab", 1, "axb", 2, action{dirty: true}},
		{"backspace", tcell.KeyBackspace, 0, "abc", 2, "ac", 1, action{dirty: true}},
		{"backspace2", tcell.KeyBackspace2, 0, "abc", 2, "ac", 1, action{dirty: true}},
		{"delete", tcell.KeyDelete, 0, "abc", 1, "ac", 1, action{dirty: true}},
		{"left", tcell.KeyLeft, 0, "abc", 2, "abc", 1, action{dirty: true}},
		{"right", tcell.KeyRight, 0, "abc", 1, "abc", 2, action{dirty: true}},
		{"home", tcell.KeyHome, 0, "abc", 2, "abc", 0, action{dirty: true}},
		{"end", tcell.KeyEnd, 0, "abc", 1, "abc", 3, action{dirty: true}},
		{"backspace no-op", tcell.KeyBackspace, 0, "abc", 0, "abc", 0, action{}},
		{"delete no-op", tcell.KeyDelete, 0, "abc", 3, "abc", 3, action{}},
		{"left no-op", tcell.KeyLeft, 0, "abc", 0, "abc", 0, action{}},
		{"right no-op", tcell.KeyRight, 0, "abc", 3, "abc", 3, action{}},
		{"home no-op", tcell.KeyHome, 0, "abc", 0, "abc", 0, action{}},
		{"end no-op", tcell.KeyEnd, 0, "abc", 3, "abc", 3, action{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model{view: stateQuery, input: []rune(tt.input), caret: tt.caret, height: 24}
			got, act := handleKey(m, tcell.NewEventKey(tt.key, tt.r, tcell.ModNone))
			if string(got.input) != tt.want || got.caret != tt.at || act != tt.a {
				t.Fatalf("got input %q caret %d action %+v", string(got.input), got.caret, act)
			}
		})
	}
}

func TestEmptyQueryHintClearsErrorStyle(t *testing.T) {
	m := model{view: stateQuery, status: "offline", statusErr: true}
	got, act := handleKey(m, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if got.status != "type a query" || got.statusErr || act != (action{dirty: true}) {
		t.Fatalf("status=%q statusErr=%t action=%+v", got.status, got.statusErr, act)
	}
}

func TestHandleKeyStates(t *testing.T) {
	book := parse.Book{ID: annaRef("1")}
	tests := []struct {
		name  string
		m     model
		key   tcell.Key
		r     rune
		view  viewState
		help  bool
		query string
		at    int
		act   action
	}{
		{"submit", model{view: stateQuery, input: []rune("abc"), caret: 3}, tcell.KeyEnter, 0, stateResults, false, "abc", 3, action{dirty: true, search: true}},
		{"empty submit", model{view: stateQuery}, tcell.KeyEnter, 0, stateQuery, false, "", 0, action{dirty: true}},
		{"query escape", model{view: stateQuery}, tcell.KeyEscape, 0, stateResults, false, "", 0, action{dirty: true}},
		{"results slash", model{view: stateResults, query: "abc"}, tcell.KeyRune, '/', stateQuery, false, "abc", 3, action{dirty: true}},
		{"detail slash", model{view: stateDetail, query: "abc"}, tcell.KeyRune, '/', stateQuery, false, "abc", 3, action{dirty: true}},
		{"results escape", model{view: stateResults, query: "abc"}, tcell.KeyEscape, 0, stateQuery, false, "abc", 3, action{dirty: true}},
		{"detail escape", model{view: stateDetail}, tcell.KeyEscape, 0, stateResults, false, "", 0, action{dirty: true}},
		{"results enter", model{view: stateResults, books: []parse.Book{book}}, tcell.KeyEnter, 0, stateDetail, false, "", 0, action{dirty: true}},
		{"empty results enter", model{view: stateResults}, tcell.KeyEnter, 0, stateResults, false, "", 0, action{}},
		{"results help", model{view: stateResults}, tcell.KeyRune, '?', stateResults, true, "", 0, action{dirty: true}},
		{"detail help", model{view: stateDetail}, tcell.KeyRune, '?', stateDetail, true, "", 0, action{dirty: true}},
		{"help close question", model{view: stateResults, help: true}, tcell.KeyRune, '?', stateResults, false, "", 0, action{dirty: true}},
		{"help close escape", model{view: stateResults, help: true}, tcell.KeyEscape, 0, stateResults, false, "", 0, action{dirty: true}},
		{"help ignores key", model{view: stateResults, help: true}, tcell.KeyRune, 'q', stateResults, true, "", 0, action{}},
		{"query q inserts", model{view: stateQuery}, tcell.KeyRune, 'q', stateQuery, false, "", 1, action{dirty: true}},
		{"results q quits", model{view: stateResults}, tcell.KeyRune, 'q', stateResults, false, "", 0, action{quit: true}},
		{"detail q quits", model{view: stateDetail}, tcell.KeyRune, 'q', stateDetail, false, "", 0, action{quit: true}},
		{"ctrl c quits", model{view: stateQuery}, tcell.KeyCtrlC, 0, stateQuery, false, "", 0, action{quit: true}},
		{"up no-op", model{view: stateResults, books: []parse.Book{book}}, tcell.KeyUp, 0, stateResults, false, "", 0, action{}},
		{"down moves", model{view: stateResults, books: []parse.Book{book, book}}, tcell.KeyDown, 0, stateResults, false, "", 0, action{dirty: true}},
		{"up moves", model{view: stateResults, books: []parse.Book{book, book}, selected: 1}, tcell.KeyUp, 0, stateResults, false, "", 0, action{dirty: true}},
		{"j empty no-op", model{view: stateResults}, tcell.KeyRune, 'j', stateResults, false, "", 0, action{}},
		{"k empty no-op", model{view: stateResults}, tcell.KeyRune, 'k', stateResults, false, "", 0, action{}},
		{"detail j no-op", model{view: stateDetail, books: []parse.Book{book}}, tcell.KeyRune, 'j', stateDetail, false, "", 0, action{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, act := handleKey(tt.m, tcell.NewEventKey(tt.key, tt.r, tcell.ModNone))
			if got.view != tt.view || got.help != tt.help || got.query != tt.query || got.caret != tt.at || act != tt.act {
				t.Fatalf("got model {view:%d help:%v query:%q caret:%d} action %+v", got.view, got.help, got.query, got.caret, act)
			}
		})
	}
}

func TestSelectionClampsAndOffsetFollows(t *testing.T) {
	books := make([]parse.Book, 30)
	for i := range books {
		books[i].ID = annaRef(string(rune('a' + i)))
	}
	m := model{view: stateResults, books: books, height: 24}
	for i := 0; i < 40; i++ {
		m, _ = handleKey(m, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	}
	if m.selected != 29 || m.offset != 19 {
		t.Fatalf("bottom selection=%d offset=%d", m.selected, m.offset)
	}
	for i := 0; i < 40; i++ {
		m, _ = handleKey(m, tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone))
	}
	if m.selected != 0 || m.offset != 0 {
		t.Fatalf("top selection=%d offset=%d", m.selected, m.offset)
	}
}

func TestVisibleResults(t *testing.T) {
	for _, tt := range []struct{ height, want int }{{24, 11}, {30, 14}, {23, 10}, {0, 0}, {1, 0}} {
		if got := visibleResults(tt.height); got != tt.want {
			t.Errorf("visibleResults(%d)=%d, want %d", tt.height, got, tt.want)
		}
	}
}

func TestVisibleOffsetResetsWhenAllRowsFit(t *testing.T) {
	if got := visibleOffset(7, 5, 57, 30); got != 0 {
		t.Fatalf("visibleOffset() = %d, want 0", got)
	}
}

func TestApplyResultsFailureKeepsRows(t *testing.T) {
	old := []parse.Book{{ID: annaRef("old")}}
	m := model{view: stateResults, books: old, selected: 0}
	m = applyResults(m, []parse.Book{{ID: annaRef("new")}}, errors.New("offline"))
	if m.books[0].ID != annaRef("old") || m.status != "offline" {
		t.Fatalf("got books=%v status=%q", m.books, m.status)
	}
}

func TestTabSwitchClearsStaleResults(t *testing.T) {
	books := []parse.Book{{ID: annaRef("old")}}
	tab := tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)

	m := model{view: stateResults, books: books, selected: 2, offset: 3, query: "q"}
	m, a := handleKey(m, tab)
	if m.books != nil || m.selected != 0 || m.offset != 0 {
		t.Fatalf("results view: books=%v selected=%d offset=%d", m.books, m.selected, m.offset)
	}
	if a != (action{dirty: true, search: true}) {
		t.Fatalf("results view: action=%+v", a)
	}

	m = model{view: stateQuery, books: books, selected: 2, offset: 3, query: "q"}
	m, a = handleKey(m, tab)
	if m.books != nil || m.selected != 0 || m.offset != 0 {
		t.Fatalf("query view: books=%v selected=%d offset=%d", m.books, m.selected, m.offset)
	}
	if a != (action{dirty: true}) {
		t.Fatalf("query view: action=%+v", a)
	}
}

func TestTabOnQueryScreenDropsInFlightSearch(t *testing.T) {
	m := model{view: stateQuery, searchSeq: 1, query: "q", status: "…"}
	m, _ = handleKey(m, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	m, applied := applySearch(m, 1, []parse.Book{{ID: annaRef("stale")}}, nil)
	if applied || m.books != nil {
		t.Fatalf("anna result applied after switching source: applied=%t books=%v", applied, m.books)
	}
	if m.status != "" {
		t.Fatalf("status=%q, want it cleared with the dropped search", m.status)
	}
}

func TestDetailScrollClamps(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i))
	}
	m := model{view: stateDetail, books: []parse.Book{{ID: annaRef("1")}}, description: strings.Join(lines, "\n"), width: 80, height: 24}
	j := tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone)
	k := tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone)
	for i := 0; i < 40; i++ {
		m, _ = handleKey(m, j)
	}
	if m.detailOffset != 20 {
		t.Fatalf("bottom offset=%d, want 20", m.detailOffset)
	}
	for i := 0; i < 40; i++ {
		m, _ = handleKey(m, k)
	}
	if m.detailOffset != 0 {
		t.Fatalf("top offset=%d, want 0", m.detailOffset)
	}
	if _, act := handleKey(m, k); act != (action{}) {
		t.Fatalf("k at top action=%+v, want none", act)
	}
	m.detailOffset = 20
	if _, act := handleKey(m, j); act != (action{}) {
		t.Fatalf("j at bottom action=%+v, want none", act)
	}
}

func TestDetailScrollNoOpWhenDescriptionFits(t *testing.T) {
	m := model{view: stateDetail, books: []parse.Book{{ID: annaRef("1")}}, description: "short", width: 80, height: 24}
	for _, key := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
	} {
		got, act := handleKey(m, key)
		if got.detailOffset != 0 || act != (action{}) {
			t.Fatalf("offset=%d action=%+v, want 0 and none", got.detailOffset, act)
		}
	}
}

func TestDetailOffsetResetsOnEnterAndLeave(t *testing.T) {
	m := model{view: stateResults, books: []parse.Book{{ID: annaRef("1")}}, description: "stale", detailOffset: 5}
	got, _ := handleKey(m, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if got.view != stateDetail || got.detailOffset != 0 || got.description != "" {
		t.Fatalf("enter detail offset=%d description=%q", got.detailOffset, got.description)
	}
	m = model{view: stateDetail, books: []parse.Book{{ID: annaRef("1")}}, description: "text", detailOffset: 5}
	got, _ = handleKey(m, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if got.view != stateResults || got.detailOffset != 0 {
		t.Fatalf("leave detail offset=%d", got.detailOffset)
	}
}

func TestDetailEscapeClearsStatus(t *testing.T) {
	m := model{view: stateDetail, books: []parse.Book{{ID: annaRef("1")}}, description: "text", status: "offline", statusErr: true}
	got, act := handleKey(m, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if got.view != stateResults || !act.dirty {
		t.Fatalf("view=%v action=%+v", got.view, act)
	}
	if got.status != "" || got.statusErr {
		t.Fatalf("status=%q err=%v, want cleared", got.status, got.statusErr)
	}
}

func TestClampDetailOffsetOnResize(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i))
	}
	m := model{view: stateDetail, description: strings.Join(lines, "\n"), width: 80, height: 24, detailOffset: 20}
	if got := clampDetailOffset(m, m.detailOffset); got != 20 {
		t.Fatalf("offset at 80x24=%d, want 20", got)
	}
	m.height = 30
	if got := clampDetailOffset(m, m.detailOffset); got != 14 {
		t.Fatalf("offset after grow=%d, want 14", got)
	}
	m.height = 24
	m.width = 80
	if got := clampDetailOffset(m, 100); got != 20 {
		t.Fatalf("excessive offset=%d, want 20", got)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		width int
		want  string
	}{
		{"abc", 0, ""},
		{"abc", -1, ""},
		{"abc", 3, "abc"},
		{"abc", 2, "a…"},
		{"漢字a", 3, "漢…"},
		{"e\u0301x", 1, "…"},
		{"e\u0301x", 2, "e\u0301x"},
		{"", 1, ""},
	}
	for _, tt := range tests {
		if got := truncate(tt.input, tt.width); got != tt.want {
			t.Errorf("truncate(%q, %d)=%q, want %q", tt.input, tt.width, got, tt.want)
		}
	}
	if got := truncate(strings.Repeat("x", 100), 10); got != "xxxxxxxxx…" {
		t.Fatalf("long truncate=%q", got)
	}
}

func annaRef(id string) source.Ref { return source.Ref{Source: source.Anna, ID: id} }

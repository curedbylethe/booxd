package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestNewThemeModes(t *testing.T) {
	for _, mono := range []bool{true, false} {
		theme := newTheme(mono)
		if theme.base != tcell.StyleDefault {
			t.Errorf("base = %v, want default", theme.base)
		}
		if theme.dim != tcell.StyleDefault.Dim(true) {
			t.Errorf("dim = %v, want dim default", theme.dim)
		}
		if theme.reverse != tcell.StyleDefault.Reverse(true) {
			t.Errorf("reverse = %v, want reversed default", theme.reverse)
		}
		wantAlert := tcell.StyleDefault.Bold(true)
		if !mono {
			wantAlert = wantAlert.Foreground(tcell.ColorRed)
		}
		if theme.alert != wantAlert {
			t.Errorf("alert = %v, want %v", theme.alert, wantAlert)
		}
	}
}

func TestThemeParity(t *testing.T) {
	mono, colour := newTheme(true), newTheme(false)
	for name, styles := range map[string][2]tcell.Style{
		"base":    {mono.base, colour.base},
		"dim":     {mono.dim, colour.dim},
		"reverse": {mono.reverse, colour.reverse},
		"alert":   {mono.alert, colour.alert},
	} {
		if strip(styles[0]) != strip(styles[1]) {
			t.Errorf("%s attributes differ between modes", name)
		}
	}
	if mono.alert != strip(mono.alert) {
		t.Fatal("mono alert contains colour")
	}
}

func TestMonoStatusIsObservable(t *testing.T) {
	mono, colour := drawThemeStatus(t, true), drawThemeStatus(t, false)
	monoStatus := mono[23*80]
	colourStatus := colour[23*80]
	if monoStatus.Style != strip(monoStatus.Style) {
		t.Fatal("mono status contains colour")
	}
	if colourStatus.Style == strip(colourStatus.Style) {
		_, foreground, background := colourStatus.Style.Decompose()
		t.Fatalf("colour status has no colour: fg=%v bg=%v style=%#v", foreground, background, colourStatus.Style)
	}
}

func TestMonoFrameParity(t *testing.T) {
	mono, colour := drawThemeStatus(t, true), drawThemeStatus(t, false)
	if len(mono) != len(colour) {
		t.Fatalf("frame cell counts differ: %d != %d", len(mono), len(colour))
	}
	for i := range mono {
		if len(mono[i].Runes) != len(colour[i].Runes) {
			t.Fatalf("cell %d rune counts differ", i)
		}
		for j := range mono[i].Runes {
			if mono[i].Runes[j] != colour[i].Runes[j] {
				t.Fatalf("cell %d rune %d differs: %q != %q", i, j, mono[i].Runes[j], colour[i].Runes[j])
			}
		}
		if strip(mono[i].Style) != strip(colour[i].Style) {
			t.Fatalf("cell %d attributes differ", i)
		}
	}
}

func strip(style tcell.Style) tcell.Style {
	return style.Foreground(tcell.ColorDefault).Background(tcell.ColorDefault)
}

func drawThemeStatus(t *testing.T, mono bool) []tcell.SimCell {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(80, 24)
	m := model{view: stateResults, height: 24, status: "offline", statusErr: true, theme: newTheme(mono)}
	draw(screen, m)
	screen.Show()
	cells, _, _ := screen.GetContents()
	return cells
}

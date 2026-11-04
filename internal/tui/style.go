package tui

import "github.com/gdamore/tcell/v2"

type theme struct {
	base    tcell.Style
	dim     tcell.Style
	reverse tcell.Style
	alert   tcell.Style
}

func newTheme(mono bool) theme {
	base := tcell.StyleDefault
	alert := base.Bold(true)
	if !mono {
		alert = alert.Foreground(tcell.ColorRed)
	}
	return theme{
		base:    base,
		dim:     base.Dim(true),
		reverse: base.Reverse(true),
		alert:   alert,
	}
}

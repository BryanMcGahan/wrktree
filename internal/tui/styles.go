package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#6B4FD8", Dark: "#A48CFF"}
	colGreen  = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#56D364"}
	colRed    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#FF7B72"}
	colYellow = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#E3B341"}
	colCyan   = lipgloss.AdaptiveColor{Light: "#0969DA", Dark: "#79C0FF"}
	colDim    = lipgloss.AdaptiveColor{Light: "#8C959F", Dark: "#6E7681"}
	colSel    = lipgloss.AdaptiveColor{Light: "#E7E1FF", Dark: "#2D2650"}
	colMark   = lipgloss.AdaptiveColor{Light: "#DDF4FF", Dark: "#1C3A5E"}

	sTitle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	sBold    = lipgloss.NewStyle().Bold(true)
	sDim     = lipgloss.NewStyle().Foreground(colDim)
	sGreen   = lipgloss.NewStyle().Foreground(colGreen)
	sRed     = lipgloss.NewStyle().Foreground(colRed)
	sYellow  = lipgloss.NewStyle().Foreground(colYellow)
	sCyan    = lipgloss.NewStyle().Foreground(colCyan)
	sAccent  = lipgloss.NewStyle().Foreground(colAccent)
	sSelRow  = lipgloss.NewStyle().Background(colSel)
	sMarkRow = lipgloss.NewStyle().Background(colMark)
	sKey     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	sErr     = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	sOK      = lipgloss.NewStyle().Foreground(colGreen)

	sBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colDim).Padding(0, 1)
	sFocusBox = sBox.BorderForeground(colAccent)
	sButton   = lipgloss.NewStyle().Padding(0, 2).Foreground(colDim).Border(lipgloss.RoundedBorder()).BorderForeground(colDim)
	sButtonOn = sButton.Foreground(colAccent).BorderForeground(colAccent).Bold(true)
)

// help renders "key action" pairs as a hint line.
func help(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sKey.Render(pairs[i])+" "+sDim.Render(pairs[i+1]))
	}
	return strings.Join(parts, sDim.Render("  ·  "))
}

// truncate shortens s to w display cells, adding an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > w-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// pad right-pads s to w cells (truncating if needed).
func pad(s string, w int) string {
	s = truncate(s, w)
	if n := w - lipgloss.Width(s); n > 0 {
		s += strings.Repeat(" ", n)
	}
	return s
}

// selRow renders plain text as a highlighted row of width w.
func selRow(s string, w int) string {
	return sSelRow.Foreground(colAccent).Bold(true).Render(pad(s, w))
}

// clampLines keeps at most h lines of s.
func clampLines(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h && h >= 0 {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

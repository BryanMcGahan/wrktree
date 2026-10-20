package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	colBar    = lipgloss.AdaptiveColor{Light: "#EEEBF7", Dark: "#24213A"}
	colBarFg  = lipgloss.AdaptiveColor{Light: "#24213A", Dark: "#E6E1FF"}
	colRule   = lipgloss.AdaptiveColor{Light: "#D0D7DE", Dark: "#3A3F4B"}

	sRule    = lipgloss.NewStyle().Foreground(colRule)
	sSection = lipgloss.NewStyle().Bold(true).Foreground(colBarFg)
	sColHead = lipgloss.NewStyle().Foreground(colDim).Bold(true)

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

// truncate shortens s (which may contain ANSI styling) to w cells.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// headerBar renders a full-width title bar: a badge, a title and right-aligned info.
func headerBar(w int, title, info, right string) string {
	badge := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(colAccent).Padding(0, 1).Render("wrk")
	bar := lipgloss.NewStyle().Background(colBar)
	left := badge + bar.Render(" ") + bar.Bold(true).Foreground(colBarFg).Render(title)
	if info != "" {
		left += bar.Foreground(colDim).Render("  " + info)
	}
	r := ""
	if right != "" {
		r = bar.Foreground(colDim).Render(right + " ")
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(r)
	if gap < 1 {
		left = truncate(left, w-lipgloss.Width(r)-1)
		gap = w - lipgloss.Width(left) - lipgloss.Width(r)
	}
	return left + bar.Render(strings.Repeat(" ", max(gap, 0))) + r
}

// footer renders a separator line followed by key hints.
func footer(w int, keys string) string {
	return sRule.Render(strings.Repeat("─", w)) + "\n " + truncate(keys, w-2)
}

// section renders a labelled divider: "── Label ──────".
func section(label string, w int) string {
	head := sRule.Render("── ") + sSection.Render(label) + " "
	return head + sRule.Render(strings.Repeat("─", max(w-lipgloss.Width(head), 0)))
}

// panel draws a rounded box of outer size w×h with title set into the top border.
func panel(title, body string, w, h int, focused bool) string {
	bc := lipgloss.NewStyle().Foreground(colDim)
	ts := sSection
	if focused {
		bc = lipgloss.NewStyle().Foreground(colAccent)
		ts = ts.Foreground(colAccent)
	}
	inner := w - 2
	var top string
	if title != "" {
		t := truncate(title, inner-4)
		top = bc.Render("╭─ ") + ts.Render(t) + bc.Render(" "+strings.Repeat("─", max(inner-lipgloss.Width(t)-3, 0))+"╮")
	} else {
		top = bc.Render("╭" + strings.Repeat("─", inner) + "╮")
	}
	lines := strings.Split(body, "\n")
	out := []string{top}
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, bc.Render("│")+" "+pad(l, inner-2)+" "+bc.Render("│"))
	}
	out = append(out, bc.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(out, "\n")
}

// frame lays out a screen: header bar, body (padded to fill), footer.
func frame(e *env, header, body, keys string) string {
	h := e.height - 1 // last line is the app status line
	top := header + "\n"
	bot := footer(e.width, keys)
	avail := h - lipgloss.Height(header) - lipgloss.Height(bot)
	body = clampLines(body, avail)
	if n := avail - lipgloss.Height(body); n > 0 {
		body += strings.Repeat("\n", n)
	}
	return top + body + "\n" + bot
}

// indent prefixes every line with n spaces.
func indent(s string, n int) string {
	p := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = p + l
		}
	}
	return strings.Join(lines, "\n")
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

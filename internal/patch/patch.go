// Package patch parses unified diffs and builds partial patches from selected
// lines, enabling line-level staging and unstaging.
package patch

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind is the type of a diff line.
type Kind byte

const (
	Context Kind = ' '
	Add     Kind = '+'
	Del     Kind = '-'
	NoEOL   Kind = '\\' // "\ No newline at end of file"
)

type Line struct {
	Kind Kind
	Text string // without the leading marker
}

// IsChange reports whether the line is an addition or deletion.
func (l Line) IsChange() bool { return l.Kind == Add || l.Kind == Del }

type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Section            string // text after the closing @@
	Lines              []Line
}

// File is the diff of a single file.
type File struct {
	Header  []string // lines before the first hunk (diff --git, index, ---, +++ ...)
	Hunks   []Hunk
	Binary  bool
	NewFile bool
	Deleted bool
}

// Parse parses the diff of a single file. Multi-file diffs are not supported.
func Parse(diff string) (*File, error) {
	f := &File{}
	lines := strings.Split(diff, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	i := 0
	for ; i < len(lines) && !strings.HasPrefix(lines[i], "@@"); i++ {
		l := lines[i]
		f.Header = append(f.Header, l)
		switch {
		case strings.HasPrefix(l, "new file mode"):
			f.NewFile = true
		case strings.HasPrefix(l, "deleted file mode"):
			f.Deleted = true
		case strings.HasPrefix(l, "Binary files ") || l == "GIT binary patch":
			f.Binary = true
		}
	}
	for i < len(lines) {
		h, err := parseHunkHeader(lines[i])
		if err != nil {
			return nil, err
		}
		i++
		for ; i < len(lines) && !strings.HasPrefix(lines[i], "@@"); i++ {
			l := lines[i]
			if l == "" {
				// Some tools strip the trailing space of empty context lines.
				h.Lines = append(h.Lines, Line{Kind: Context})
				continue
			}
			k := Kind(l[0])
			switch k {
			case Context, Add, Del, NoEOL:
				h.Lines = append(h.Lines, Line{Kind: k, Text: l[1:]})
			default:
				return nil, fmt.Errorf("unexpected diff line %q", l)
			}
		}
		f.Hunks = append(f.Hunks, h)
	}
	return f, nil
}

func parseHunkHeader(s string) (Hunk, error) {
	var h Hunk
	// @@ -a,b +c,d @@ section
	rest, ok := strings.CutPrefix(s, "@@ ")
	if !ok {
		return h, fmt.Errorf("bad hunk header %q", s)
	}
	ranges, section, ok := strings.Cut(rest, " @@")
	if !ok {
		return h, fmt.Errorf("bad hunk header %q", s)
	}
	h.Section = strings.TrimPrefix(section, " ")
	parts := strings.Fields(ranges)
	if len(parts) != 2 {
		return h, fmt.Errorf("bad hunk header %q", s)
	}
	var err error
	if h.OldStart, h.OldCount, err = parseRange(parts[0], '-'); err != nil {
		return h, err
	}
	if h.NewStart, h.NewCount, err = parseRange(parts[1], '+'); err != nil {
		return h, err
	}
	return h, nil
}

func parseRange(s string, prefix byte) (start, count int, err error) {
	if len(s) == 0 || s[0] != prefix {
		return 0, 0, fmt.Errorf("bad range %q", s)
	}
	a, b, has := strings.Cut(s[1:], ",")
	if start, err = strconv.Atoi(a); err != nil {
		return
	}
	count = 1
	if has {
		count, err = strconv.Atoi(b)
	}
	return
}

// Pos identifies a line within a File.
type Pos struct{ Hunk, Line int }

// Selection is a set of selected change lines.
type Selection map[Pos]bool

// ChangeCount returns the number of +/- lines in the file.
func (f *File) ChangeCount() int {
	n := 0
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.IsChange() {
				n++
			}
		}
	}
	return n
}

// Build creates a patch containing only the selected changes.
//
// When reverse is false the patch is meant for `git apply --cached` (staging
// lines from the working-tree diff). When reverse is true it is meant for
// `git apply --cached -R` (unstaging lines from the staged diff).
//
// Returns "" if nothing is selected.
func (f *File) Build(sel Selection, reverse bool) string {
	if f.Binary {
		return ""
	}
	selCount := 0
	for p, ok := range sel {
		if ok && p.Hunk < len(f.Hunks) && p.Line < len(f.Hunks[p.Hunk].Lines) && f.Hunks[p.Hunk].Lines[p.Line].IsChange() {
			selCount++
		}
	}
	if selCount == 0 {
		return ""
	}
	partial := selCount < f.ChangeCount()

	var body strings.Builder
	delta := 0 // cumulative (new - old) line shift of emitted hunks
	for hi, h := range f.Hunks {
		out, changed := h.filter(hi, sel, reverse)
		if !changed {
			continue
		}
		oldN, newN := 0, 0
		for _, l := range out {
			switch l.Kind {
			case Context:
				oldN++
				newN++
			case Del:
				oldN++
			case Add:
				newN++
			}
		}
		// The side that matches the index is unchanged (old side when staging,
		// new side when unstaging); the other side shifts by the net line
		// change of the hunks emitted before it.
		var oldStart, newStart int
		if reverse {
			newStart = h.NewStart
			oldStart = shift(newStart, newN, oldN, -delta)
		} else {
			oldStart = h.OldStart
			newStart = shift(oldStart, oldN, newN, delta)
		}
		delta += newN - oldN
		fmt.Fprintf(&body, "@@ -%s +%s @@", fmtRange(oldStart, oldN), fmtRange(newStart, newN))
		if h.Section != "" {
			body.WriteString(" " + h.Section)
		}
		body.WriteString("\n")
		for _, l := range out {
			body.WriteByte(byte(l.Kind))
			body.WriteString(l.Text)
			body.WriteByte('\n')
		}
	}
	if body.Len() == 0 {
		return ""
	}
	return f.header(partial, reverse) + body.String()
}

// shift converts a hunk start on one side to the other. In unified diffs a
// zero-length range names the line before the (empty) range.
func shift(start, fromN, toN, d int) int {
	s := start + d
	if fromN == 0 {
		s++
	}
	if toN == 0 {
		s--
	}
	if s < 0 {
		s = 0
	}
	return s
}

// filter keeps the selected changes of hunk hi and neutralises the rest:
// staging drops unselected additions and turns unselected deletions into
// context; unstaging does the opposite.
func (h Hunk) filter(hi int, sel Selection, reverse bool) ([]Line, bool) {
	var out []Line
	changed := false
	keptPrev := true
	selectedAddAfter := func(li int) bool {
		for j := li + 1; j < len(h.Lines); j++ {
			if h.Lines[j].Kind == Add && sel[Pos{hi, j}] {
				return true
			}
		}
		return false
	}
	for li, l := range h.Lines {
		selected := sel[Pos{hi, li}]
		switch l.Kind {
		case Context:
			out = append(out, l)
			keptPrev = true
		case Add:
			switch {
			case selected:
				out = append(out, l)
				changed = true
				keptPrev = true
			case reverse:
				out = append(out, Line{Kind: Context, Text: l.Text})
				keptPrev = true
			default:
				keptPrev = false
			}
		case Del:
			switch {
			case selected:
				out = append(out, l)
				changed = true
				keptPrev = true
			case reverse:
				keptPrev = false
			case li+1 < len(h.Lines) && h.Lines[li+1].Kind == NoEOL && selectedAddAfter(li):
				// The old last line lacks a newline but selected lines are
				// appended after it: it must be re-added with a newline.
				out = append(out, l, h.Lines[li+1], Line{Kind: Add, Text: l.Text})
				keptPrev = false // its marker has already been emitted
			default:
				out = append(out, Line{Kind: Context, Text: l.Text})
				keptPrev = true
			}
		case NoEOL:
			if keptPrev {
				out = append(out, l)
			}
		}
	}
	return fixNoEOL(out), changed
}

// fixNoEOL removes "\ No newline" markers that no longer follow the final
// line of either side, which can happen once lines are dropped.
func fixNoEOL(lines []Line) []Line {
	var out []Line
	for i, l := range lines {
		if l.Kind == NoEOL {
			if i == 0 || lines[i-1].Kind == NoEOL {
				continue
			}
			// A marker is only valid if the previous line is the last line of
			// its side within this hunk.
			prev := lines[i-1]
			last := true
			for _, nx := range lines[i+1:] {
				if nx.Kind == Context || nx.Kind == prev.Kind || (prev.Kind == Context && nx.IsChange()) {
					last = false
					break
				}
			}
			if !last {
				continue
			}
		}
		out = append(out, l)
	}
	return out
}

func fmtRange(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// header rewrites file headers for partial patches of new/deleted files.
func (f *File) header(partial, reverse bool) string {
	var b strings.Builder
	path := f.path()
	for _, l := range f.Header {
		switch {
		case strings.HasPrefix(l, "index "):
			continue // object ids are not needed and would be wrong for partial patches
		case partial && !reverse && f.Deleted && strings.HasPrefix(l, "deleted file mode"):
			continue // deleting only some lines leaves the file in place
		case partial && !reverse && f.Deleted && strings.HasPrefix(l, "+++ "):
			l = "+++ b/" + path
		case partial && reverse && f.NewFile && strings.HasPrefix(l, "new file mode"):
			continue // unstaging some lines keeps the file in the index
		case partial && reverse && f.NewFile && strings.HasPrefix(l, "--- "):
			l = "--- a/" + path
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

func (f *File) path() string {
	for _, l := range f.Header {
		if p, ok := strings.CutPrefix(l, "+++ b/"); ok {
			return p
		}
	}
	for _, l := range f.Header {
		if p, ok := strings.CutPrefix(l, "--- a/"); ok {
			return p
		}
	}
	return ""
}

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

// ---------------------------------------------------------------- confirm --

type option struct {
	label  string
	key    string
	run    func() tea.Cmd // nil means cancel
	danger bool
}

// confirm is a modal dialog with a few buttons.
type confirm struct {
	e     *env
	title string
	body  string
	opts  []option
	cur   int
}

func newConfirm(e *env, title, body string, opts ...option) *confirm {
	return &confirm{e: e, title: title, body: body, opts: opts}
}

func (c *confirm) Init() tea.Cmd { return nil }

func (c *confirm) choose(i int) tea.Cmd {
	o := c.opts[i]
	if o.run == nil {
		return pop(nil)
	}
	return tea.Sequence(pop(nil), o.run())
}

func (c *confirm) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q":
			return c, pop(nil)
		case "left", "h", "shift+tab":
			c.cur = (c.cur + len(c.opts) - 1) % len(c.opts)
		case "right", "l", "tab":
			c.cur = (c.cur + 1) % len(c.opts)
		case "enter":
			return c, c.choose(c.cur)
		default:
			for i, o := range c.opts {
				if o.key != "" && k.String() == o.key {
					return c, c.choose(i)
				}
			}
		}
	}
	return c, nil
}

func (c *confirm) View() string {
	var btns []string
	for i, o := range c.opts {
		label := o.label
		if o.key != "" {
			label = fmt.Sprintf("%s (%s)", o.label, o.key)
		}
		st := sButton
		if i == c.cur {
			st = sButtonOn
			if o.danger {
				st = st.Foreground(colRed).BorderForeground(colRed)
			}
		}
		btns = append(btns, st.Render(label))
	}
	maxW := c.e.width - 8
	if maxW > 90 {
		maxW = 90
	}
	body := lipgloss.NewStyle().Width(maxW).Render(c.body)
	content := lipgloss.JoinVertical(lipgloss.Left,
		sTitle.Render(c.title), "", body, "",
		lipgloss.JoinHorizontal(lipgloss.Top, btns...),
		sDim.Render("←/→ choose · enter confirm · esc cancel"),
	)
	return lipgloss.Place(c.e.width, c.e.height-1, lipgloss.Center, lipgloss.Center, sFocusBox.Render(content))
}

// ----------------------------------------------------------------- picker --

type pickItem struct {
	title string
	desc  string
	value any
}

type pickLoadedMsg struct {
	p     *picker
	items []pickItem
	err   error
}

// picker is a fuzzy-filterable list, optionally loaded asynchronously.
type picker struct {
	e        *env
	title    string
	items    []pickItem
	filtered []int
	input    textinput.Model
	cur      int
	loading  bool
	load     func() ([]pickItem, error)
	onPick   func(pickItem) tea.Cmd
	onCancel func() tea.Cmd
	err      error
	spin     spinner.Model
	// allowCustom lets enter submit the typed text when nothing matches.
	allowCustom bool
}

func newPicker(e *env, title string, load func() ([]pickItem, error), onPick func(pickItem) tea.Cmd) *picker {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = "type to filter"
	ti.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = sAccent
	return &picker{e: e, title: title, load: load, onPick: onPick, input: ti, loading: true, spin: sp}
}

func (p *picker) Init() tea.Cmd {
	load := p.load
	return tea.Batch(p.spin.Tick, textinput.Blink, func() tea.Msg {
		items, err := load()
		return pickLoadedMsg{p: p, items: items, err: err}
	})
}

func (p *picker) refilter() {
	q := p.input.Value()
	p.filtered = p.filtered[:0]
	if q == "" {
		for i := range p.items {
			p.filtered = append(p.filtered, i)
		}
	} else {
		keys := make([]string, len(p.items))
		for i, it := range p.items {
			keys[i] = it.title + "  " + it.desc
		}
		for _, m := range fuzzy.Find(q, keys) {
			p.filtered = append(p.filtered, m.Index)
		}
	}
	if p.cur >= len(p.filtered) {
		p.cur = len(p.filtered) - 1
	}
	if p.cur < 0 {
		p.cur = 0
	}
}

func (p *picker) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case pickLoadedMsg:
		if msg.p != p {
			return p, nil
		}
		p.loading = false
		p.items, p.err = msg.items, msg.err
		p.refilter()
		return p, nil
	case spinner.TickMsg:
		if !p.loading {
			return p, nil
		}
		var cmd tea.Cmd
		p.spin, cmd = p.spin.Update(msg)
		return p, cmd
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			if p.onCancel != nil {
				return p, tea.Sequence(pop(nil), p.onCancel())
			}
			return p, pop(nil)
		case "up", "ctrl+p", "ctrl+k":
			if p.cur > 0 {
				p.cur--
			}
			return p, nil
		case "down", "ctrl+n", "ctrl+j":
			if p.cur < len(p.filtered)-1 {
				p.cur++
			}
			return p, nil
		case "enter":
			if len(p.filtered) > 0 {
				it := p.items[p.filtered[p.cur]]
				return p, tea.Sequence(pop(nil), p.onPick(it))
			}
			if p.allowCustom && strings.TrimSpace(p.input.Value()) != "" {
				v := strings.TrimSpace(p.input.Value())
				return p, tea.Sequence(pop(nil), p.onPick(pickItem{title: v, value: v}))
			}
			return p, nil
		}
	}
	var cmd tea.Cmd
	old := p.input.Value()
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != old {
		p.cur = 0
		p.refilter()
	}
	return p, cmd
}

func (p *picker) View() string {
	var b strings.Builder
	b.WriteString(sTitle.Render(p.title) + "\n\n")
	b.WriteString(p.input.View() + "\n\n")
	listH := p.e.height - 8
	switch {
	case p.loading:
		b.WriteString(p.spin.View() + " loading…\n")
	case p.err != nil:
		b.WriteString(sErr.Render(errText(p.err)) + "\n")
	case len(p.filtered) == 0:
		if p.allowCustom && p.input.Value() != "" {
			b.WriteString(sDim.Render("no matches — enter to use ") + sBold.Render(p.input.Value()) + "\n")
		} else {
			b.WriteString(sDim.Render("no matches") + "\n")
		}
	default:
		start := 0
		if p.cur >= listH {
			start = p.cur - listH + 1
		}
		for i := start; i < len(p.filtered) && i < start+listH; i++ {
			it := p.items[p.filtered[i]]
			w := p.e.width - 3
			var line string
			if i == p.cur {
				line = selRow("▸ "+it.title+"  "+it.desc, w)
			} else {
				line = "  " + truncate(it.title, w-2)
				if rest := w - lipgloss.Width(line) - 2; it.desc != "" && rest > 0 {
					line += "  " + sDim.Render(truncate(it.desc, rest))
				}
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n" + help("↑/↓", "move", "enter", "select", "esc", "back"))
	return lipgloss.NewStyle().Padding(0, 1).Render(b.String())
}

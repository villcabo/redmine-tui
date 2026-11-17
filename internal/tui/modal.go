package tui

import (
	"time"

	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// modal is a dialog drawn over the current screen. It owns every key while open.
type modal interface {
	view(e env) string
	help(e env) string
	handleKey(m *Model, msg tea.KeyPressMsg) tea.Cmd
}

// forwarder is implemented by modals that need non-key messages, such as cursor blinks.
type forwarder interface {
	forward(msg tea.Msg) tea.Cmd
}

func newInput(prompt, placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = prompt
	in.Placeholder = placeholder
	return in
}

// inputView draws a text input. The stock view paints the cursor over the first rune of
// the placeholder, which hides it; here the cursor sits on its own cell and the
// placeholder is drawn whole after it.
func inputView(in textinput.Model) string {
	if in.Value() != "" || in.Placeholder == "" {
		return in.View()
	}
	placeholder, width := in.Placeholder, in.Width()
	in.Placeholder = ""
	in.SetWidth(0)
	st := in.Styles().Blurred.Placeholder
	if in.Focused() {
		st = in.Styles().Focused.Placeholder
	}
	view := in.View() + st.Render(placeholder)
	if pad := width - lipgloss.Width(view); pad > 0 {
		view += strings.Repeat(" ", pad)
	}
	return view
}

func boxed(e env, title, body string, width int) string {
	inner := e.st.boxTitle.Render(title) + "\n\n" + body
	return e.st.box.Width(width).Render(inner)
}

func dialogWidth(e env, want int) int { return max(30, min(want, e.w-4)) }

// ---- text input -------------------------------------------------------

// inputModal asks for one line of text. onSubmit returns a message to show when the
// value is rejected; an empty message closes the dialog.
type inputModal struct {
	title    string
	input    textinput.Model
	errText  string
	hint     string
	onSubmit func(m *Model, value string) (tea.Cmd, string)
}

func (i *inputModal) view(e env) string {
	body := inputView(i.input)
	if i.hint != "" {
		body += "\n" + e.st.dim.Render(i.hint)
	}
	if i.errText != "" {
		body += "\n" + e.st.danger.Render(i.errText)
	}
	return boxed(e, i.title, body, dialogWidth(e, 64))
}

func (i *inputModal) help(e env) string { return e.t("help.input") }

func (i *inputModal) handleKey(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.modal = nil
		return nil
	case "enter":
		cmd, errText := i.onSubmit(m, strings.TrimSpace(i.input.Value()))
		if errText != "" {
			i.errText = errText
			return nil
		}
		if m.modal == modal(i) {
			m.modal = nil
		}
		return cmd
	}
	var cmd tea.Cmd
	i.input, cmd = i.input.Update(msg)
	i.errText = ""
	return cmd
}

func (i *inputModal) forward(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	i.input, cmd = i.input.Update(msg)
	return cmd
}

// ---- picker -----------------------------------------------------------

type pickItem struct {
	label string
	value any
}

// pickerModal lets the user choose one item, optionally narrowing a long list by typing.
// With remote set, typing asks Redmine instead of filtering the loaded items.
type pickerModal struct {
	title     string
	note      string
	items     []pickItem
	filterOn  bool
	filter    textinput.Model
	remote    func(seq int, query string) tea.Cmd
	seq       int
	cursor    int
	top       int
	emptyText string
	back      modal // shown again when the picker is cancelled
	onPick    func(m *Model, it pickItem) tea.Cmd
}

func newPicker(e env, title string, items []pickItem, onPick func(m *Model, it pickItem) tea.Cmd) *pickerModal {
	return &pickerModal{title: title, items: items, onPick: onPick, emptyText: e.t("picker.empty")}
}

func (p *pickerModal) withFilter(e env) *pickerModal {
	p.filterOn = true
	p.filter = newInput("/ ", e.t("picker.filter"))
	p.filter.Focus()
	return p
}

func (p *pickerModal) visible() []pickItem {
	if !p.filterOn || p.remote != nil {
		return p.items
	}
	q := lower(p.filter.Value())
	if q == "" {
		return p.items
	}
	var out []pickItem
	for _, it := range p.items {
		if strings.Contains(lower(it.label), q) {
			out = append(out, it)
		}
	}
	return out
}

const pickerRows = 12

func (p *pickerModal) rows(e env) int { return max(3, min(pickerRows, e.h-12)) }

func (p *pickerModal) view(e env) string {
	vis := p.visible()
	var b strings.Builder
	width := lipgloss.Width(p.title)
	for _, it := range vis {
		width = max(width, lipgloss.Width(it.label)+4)
	}
	if p.filterOn || p.note != "" {
		width = max(width, 48)
	}
	width = dialogWidth(e, width+4)
	if p.note != "" {
		b.WriteString(e.st.danger.Width(width-4).Render(p.note) + "\n")
	}
	if p.filterOn {
		p.filter.SetWidth(width - 8)
		b.WriteString(inputView(p.filter) + "\n")
	}
	rows := p.rows(e)
	if p.cursor < p.top {
		p.top = p.cursor
	}
	if p.cursor >= p.top+rows {
		p.top = p.cursor - rows + 1
	}
	end := min(len(vis), p.top+rows)
	if len(vis) == 0 {
		b.WriteString(e.st.dim.Render(p.emptyText))
	}
	for i := p.top; i < end; i++ {
		line := "  " + e.st.text.Render(vis[i].label)
		if i == p.cursor {
			line = e.st.cursor.Render("▸ ") + e.st.selected.Render(vis[i].label)
		}
		b.WriteString(truncate(line, width-4) + "\n")
	}
	body := strings.TrimRight(b.String(), "\n")
	if len(vis) > rows {
		body += "\n" + e.st.dim.Render(e.t("picker.more", p.cursor+1, len(vis)))
	}
	return boxed(e, p.title, body, width)
}

func (p *pickerModal) help(e env) string {
	if p.filterOn {
		return e.t("help.picker_filter")
	}
	return e.t("help.picker")
}

func (p *pickerModal) handleKey(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	vis := p.visible()
	move := func(delta int) {
		p.cursor = max(0, min(p.cursor+delta, len(vis)-1))
	}
	switch key {
	case "esc":
		m.modal = p.back
		return nil
	case "enter":
		if p.cursor >= 0 && p.cursor < len(vis) {
			it := vis[p.cursor]
			m.modal = nil
			return p.onPick(m, it)
		}
		return nil
	case "up", "ctrl+p":
		move(-1)
		return nil
	case "down", "ctrl+n":
		move(1)
		return nil
	case "pgup":
		move(-p.rows(m.env()))
		return nil
	case "pgdown":
		move(p.rows(m.env()))
		return nil
	case "home":
		p.cursor = 0
		return nil
	case "end":
		p.cursor = max(0, len(vis)-1)
		return nil
	}
	if !p.filterOn {
		switch key {
		case "k":
			move(-1)
		case "j":
			move(1)
		}
		return nil
	}
	var cmd tea.Cmd
	before := p.filter.Value()
	p.filter, cmd = p.filter.Update(msg)
	if p.filter.Value() != before {
		p.cursor, p.top = 0, 0
		if p.remote != nil {
			p.seq++
			return tea.Batch(cmd, debounceCmd(p.seq, p.filter.Value()))
		}
	}
	return cmd
}

func (p *pickerModal) forward(msg tea.Msg) tea.Cmd {
	if !p.filterOn {
		return nil
	}
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(msg)
	return cmd
}

func debounceCmd(seq int, query string) tea.Cmd {
	return tea.Tick(debounceDelay, func(time.Time) tea.Msg { return pickerQueryMsg{seq: seq, query: query} })
}

// ---- confirmation -----------------------------------------------------

// confirmModal shows the exact change about to be written. Anything but the "yes"
// key cancels.
type confirmModal struct {
	text  string
	onYes func(m *Model) tea.Cmd
}

func (c *confirmModal) view(e env) string {
	w := dialogWidth(e, 90)
	body := lipgloss.NewStyle().Width(w - 4).Render(c.text)
	return boxed(e, e.t("confirm.title"), body, w)
}

func (c *confirmModal) help(e env) string { return e.t("help.confirm") }

func (c *confirmModal) handleKey(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	m.modal = nil
	if strings.EqualFold(msg.String(), m.tr.T("confirm.yes_key")) {
		return c.onYes(m)
	}
	return nil
}

// ---- message ----------------------------------------------------------

// messageModal shows a message that any key dismisses.
type messageModal struct {
	title  string
	text   string
	danger bool
	onDone func(m *Model) tea.Cmd
}

func (mm *messageModal) view(e env) string {
	w := dialogWidth(e, 72)
	style := e.st.text
	if mm.danger {
		style = e.st.danger
	}
	return boxed(e, mm.title, style.Width(w-4).Render(mm.text), w)
}

func (mm *messageModal) help(e env) string { return e.t("help.any_key") }

func (mm *messageModal) handleKey(m *Model, _ tea.KeyPressMsg) tea.Cmd {
	m.modal = nil
	if mm.onDone != nil {
		return mm.onDone(m)
	}
	return nil
}

// ---- loading ----------------------------------------------------------

// loadingModal covers the screen while a fetch needed by the next dialog runs.
// Esc cancels it: its result is then ignored.
type loadingModal struct {
	text  string
	token int
}

func (l *loadingModal) view(e env) string {
	return e.st.box.Padding(0, 2).Render(e.st.dim.Render(l.text + "…"))
}

func (l *loadingModal) help(e env) string { return e.t("help.loading") }

func (l *loadingModal) handleKey(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" {
		m.modal = nil
		m.awaitToken++
	}
	return nil
}

// ---- multi-line text --------------------------------------------------

// textareaModal edits multi-line text inside the app, used when no editor is available.
type textareaModal struct {
	title    string
	area     textarea.Model
	onSubmit func(m *Model, text string) tea.Cmd
}

func newTextarea(e env, title string, onSubmit func(m *Model, text string) tea.Cmd) *textareaModal {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.SetWidth(max(20, min(70, e.w-8)))
	ta.SetHeight(max(4, min(12, e.h-12)))
	ta.Focus()
	return &textareaModal{title: title, area: ta, onSubmit: onSubmit}
}

func (t *textareaModal) view(e env) string {
	return boxed(e, t.title, t.area.View(), dialogWidth(e, 76))
}

func (t *textareaModal) help(e env) string { return e.t("help.textarea") }

func (t *textareaModal) handleKey(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.modal = nil
		return nil
	case "ctrl+s":
		text := strings.TrimSpace(t.area.Value())
		m.modal = nil
		return t.onSubmit(m, text)
	}
	var cmd tea.Cmd
	t.area, cmd = t.area.Update(msg)
	return cmd
}

func (t *textareaModal) forward(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	t.area, cmd = t.area.Update(msg)
	return cmd
}

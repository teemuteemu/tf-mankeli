package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/ui/theme"
)

// output is a window showing Terraform's output as it applies. It stays open
// once Terraform finishes, so the result can be read, until the user closes it.
type output struct {
	title string
	text  string
	body  viewport.Model
	done  bool
	err   error
}

func newOutput(title string) *output {
	return &output{title: title, body: viewport.New()}
}

// outputMsg carries a chunk of Terraform's output, and the channel the next
// chunk arrives on.
type outputMsg struct {
	text string
	ch   <-chan string
}

// waitForOutput delivers the next chunk written to ch, until it is closed.
func waitForOutput(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		text, ok := <-ch
		if !ok {
			return nil
		}
		return outputMsg{text: text, ch: ch}
	}
}

// chanWriter sends everything written to it on the channel. Terraform's
// stdout and stderr may write at the same time, which a channel allows.
type chanWriter chan<- string

func (w chanWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

// append adds a chunk of output, following it if the window was scrolled to the end.
func (o *output) append(text string) {
	follow := o.body.AtBottom()
	o.text += text
	o.render()
	if follow {
		o.body.GotoBottom()
	}
}

// render wraps the output to the window's width.
func (o *output) render() {
	text := strings.TrimRight(o.text, "\n")
	if w := o.body.Width(); w > 0 {
		text = lipgloss.NewStyle().Width(w).Render(text)
	}
	o.body.SetContent(text)
}

// updateOutput handles keys while the output window is shown. Closing it
// while Terraform runs leaves the apply going, with the status line telling.
func (m model) updateOutput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Close):
		m.output = nil
		return m, nil
	}
	var cmd tea.Cmd
	m.output.body, cmd = m.output.body.Update(msg)
	return m, cmd
}

// outputSize is the output window's size, borders included: most of the
// terminal, leaving the resources around it in view.
func (m model) outputSize() (width, height int) {
	return max(m.width*4/5, 0), max(m.height*3/4, 0)
}

// overlayOutput draws the output window centered on top of base.
func (m model) overlayOutput(base string) string {
	window := m.outputView()
	width, height := m.outputSize()
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(window).X((m.width-width)/2).Y((m.height-height)/2).Z(1),
	).Render()
}

// outputView is a window with Terraform's output in a scrollable body.
func (m model) outputView() string {
	o := m.output
	status := theme.Help.Render(m.running)
	switch {
	case o.done && o.err != nil:
		status = theme.Error.Render("Failed.")
	case o.done:
		status = theme.Help.Render("Done.")
	}
	help := helpLine(keys.Scroll, keys.Close, keys.Quit)
	lines := []string{
		theme.Title.Render(o.title),
		"",
		o.body.View(),
		"",
		status,
		help,
	}
	return theme.Pane.
		BorderForeground(theme.ActiveColor).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

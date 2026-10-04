package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/ui/theme"
)

// failure returns the error worth showing, most recent first, with a title
// completing the sentence "<title> failed". It returns a nil error when
// nothing has failed.
func (m model) failure() (string, error) {
	switch {
	case m.actionErr != nil:
		// The error already names the action, e.g. "applying: …".
		return "Terraform", m.actionErr
	case m.err != nil:
		return "Reading the state", m.err
	case m.planErr != nil:
		return "Plan", m.planErr
	}
	return "", nil
}

// showErrorPane reports whether the error pane takes the table's place.
func (m model) showErrorPane() bool {
	_, err := m.failure()
	return err != nil && !m.errHidden
}

// refreshError renders the current failure into the error pane, wrapped to its
// width. A failure the user hasn't seen yet reopens the pane and scrolls it
// back to the top; re-rendering the same one after a resize leaves both alone.
func (m *model) refreshError() {
	title, err := m.failure()
	if err == nil {
		m.errText = ""
		return
	}

	text := strings.TrimSpace(err.Error())
	if text != m.errText {
		m.errText = text
		m.errHidden = false
		m.errPane.GotoTop()
	}
	if w := m.errPane.Width(); w > 0 {
		text = lipgloss.NewStyle().Width(w).Render(text)
	}
	m.errPane.SetContent(theme.Error.Bold(true).Render(title+" failed") + "\n\n" + text)
}

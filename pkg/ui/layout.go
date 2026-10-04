package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/ui/theme"
)

// layout fits the panes to the terminal, leaving room for their borders and
// the status and help lines. With details open, the panes split the height.
func (m *model) layout() {
	const (
		borderSize   = 2 // one cell on each side
		footerHeight = 2 // status and help lines
	)

	paneWidth := max(m.width-borderSize, 0)
	available := max(m.height-footerHeight-borderSize, 0)
	tableHeight := available
	if m.showDetails {
		available = max(available-borderSize, 0)
		tableHeight = available / 2
		m.details.SetWidth(paneWidth)
		m.details.SetHeight(available - tableHeight)
	}

	m.table.SetWidth(paneWidth)
	m.table.SetHeight(tableHeight)
	m.table.SetColumns(scaleColumns(m.table.Columns(), paneWidth))

	// The error pane stands in for the table, so it gets the same box.
	m.errPane.SetWidth(paneWidth)
	m.errPane.SetHeight(tableHeight)
	m.refreshError()

	if m.confirm != nil {
		const (
			dialogPadding = 2 // one cell left and right
			dialogLines   = 5 // title, summary, help and the blank lines between
		)
		m.confirm.body.SetWidth(max(paneWidth-dialogPadding, 0))
		m.confirm.body.SetHeight(max(m.height-borderSize-dialogLines, 0))
	}

	if m.prompt != nil {
		const (
			dialogPadding = 2 // one cell left and right
			promptWidth   = 4 // the input's own "ID: " prompt
		)
		m.prompt.input.SetWidth(max(paneWidth-dialogPadding-promptWidth, 0))
	}
}

// paneStyle highlights the focused pane's border when both panes are shown.
func (m model) paneStyle(p pane) lipgloss.Style {
	if m.showDetails && m.focus == p {
		return theme.Pane.BorderForeground(theme.ActiveColor)
	}
	return theme.Pane
}

package ui

import (
	"slices"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
	"github.com/teemuteemu/tf-mankeli/pkg/ui/detail"
)

// changes returns the planned changes, or none if there is no plan.
func (m model) changes() []tfstate.Change {
	if m.plan == nil {
		return nil
	}
	return m.plan.Changes
}

// busy reports whether Terraform is running, so no other run should start.
func (m model) busy() bool {
	return m.loading || m.planning || m.running != "" || m.targeting != ""
}

// selected returns the resource under the cursor, or the one open in the
// details pane when that is focused.
func (m model) selected() (tfstate.Resource, bool) {
	i := m.table.Cursor()
	if m.focus == detailsPane {
		i = m.indexOf(m.detailsAddress)
	}
	if i < 0 || i >= len(m.rows) {
		return tfstate.Resource{}, false
	}
	return m.rows[i], true
}

// inState reports whether address is in the state, rather than only planned.
func (m model) inState(address string) bool {
	return slices.ContainsFunc(m.state, func(r tfstate.Resource) bool { return r.Address == address })
}

func (m model) indexOf(address string) int {
	return slices.IndexFunc(m.rows, func(r tfstate.Resource) bool { return r.Address == address })
}

func (m *model) clearOutcome() {
	m.actionErr = nil
	m.notice = ""
	m.refreshError()
}

// rebuild merges the state with the plan and refreshes the table and the
// details pane, keeping the cursor on the same resource.
func (m *model) rebuild() {
	selected := ""
	if i := m.table.Cursor(); i >= 0 && i < len(m.rows) {
		selected = m.rows[i].Address
	}

	m.rows = tfstate.Merge(m.state, m.changes())
	cursor := m.indexOf(selected)
	if cursor < 0 {
		cursor = min(m.table.Cursor(), len(m.rows)-1)
	}
	m.table.SetRows(toRows(m.rows, cursor))
	if cursor >= 0 {
		m.table.SetCursor(cursor)
	}
	if i := m.indexOf(m.detailsAddress); m.showDetails && i >= 0 {
		m.details.SetContent(detail.Render(m.rows[i]))
	}
	m.refreshError()
}

// openDetails shows the selected resource in the details pane and focuses it.
func (m *model) openDetails() {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.rows) {
		return
	}
	m.detailsAddress = m.rows[i].Address
	m.details.SetContent(detail.Render(m.rows[i]))
	m.details.GotoTop()
	m.showDetails = true
	m.setFocus(detailsPane)
	m.layout()
}

func (m *model) setFocus(p pane) {
	m.focus = p
	if p == tablePane {
		m.table.Focus()
	} else {
		m.table.Blur()
	}
}

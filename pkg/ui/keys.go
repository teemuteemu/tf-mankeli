package ui

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
)

// handleKey runs the action a key is bound to. A dialog takes every key while
// it is open; keys bound to nothing scroll whichever pane is showing.
func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		return m.updateConfirm(msg)
	}
	if m.prompt != nil {
		return m.updatePrompt(msg)
	}

	switch {
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Plan):
		return m.planAll()
	case key.Matches(msg, keys.ApplyAll):
		return m.applyAll()
	case key.Matches(msg, keys.Apply):
		return m.applySelected()
	case key.Matches(msg, keys.Destroy):
		return m.destroySelected()
	case key.Matches(msg, keys.Remove):
		return m.removeSelected()
	case key.Matches(msg, keys.Import):
		return m.importSelected()
	case key.Matches(msg, keys.Taint):
		return m.taintSelected()
	case key.Matches(msg, keys.Error):
		// Toggle the full error back on after dismissing it.
		if _, err := m.failure(); err != nil {
			m.errHidden = !m.errHidden
			return m, nil
		}
	case key.Matches(msg, keys.Details):
		if m.focus == tablePane {
			m.openDetails()
			return m, nil
		}
	case key.Matches(msg, keys.Focus):
		if m.showDetails {
			if m.focus == tablePane {
				m.setFocus(detailsPane)
			} else {
				m.setFocus(tablePane)
			}
			return m, nil
		}
	case key.Matches(msg, keys.Close):
		// The error covers the table, so dismiss it before closing details.
		if m.showErrorPane() {
			m.errHidden = true
			return m, nil
		}
		if m.showDetails {
			m.showDetails = false
			m.setFocus(tablePane)
			m.layout()
			return m, nil
		}
	}

	var cmd tea.Cmd
	if m.focus == detailsPane {
		m.details, cmd = m.details.Update(msg)
		return m, cmd
	}
	// The error pane stands in for the table, so it takes the table's keys.
	if m.showErrorPane() {
		m.errPane, cmd = m.errPane.Update(msg)
		return m, cmd
	}
	return m.updateTable(msg)
}

// target picks the resource an action applies to and reports whether the
// action may run at all: not while Terraform is busy, and not with nothing
// selected. It clears the last outcome, making room for this action's.
func (m model) target() (model, tfstate.Resource, bool) {
	if m.busy() {
		return m, tfstate.Resource{}, false
	}
	m.clearOutcome()
	r, ok := m.selected()
	return m, r, ok
}

// quit leaves, unless Terraform is still running.
func (m model) quit() (tea.Model, tea.Cmd) {
	// Quitting would leave Terraform changing things with nobody watching.
	if m.running != "" {
		m.notice = "Terraform is still running; quit once it has finished."
		return m, nil
	}
	return m, tea.Quit
}

// planAll reads the state and plans every change again.
func (m model) planAll() (tea.Model, tea.Cmd) {
	// Ignore repeats while Terraform runs, so results can't arrive out of order.
	if m.busy() {
		return m, nil
	}
	m.clearOutcome()
	m.loading, m.planning = true, true
	m.err, m.planErr = nil, nil
	m.refreshError()
	return m, m.replan()
}

// applyAll asks to apply every planned change.
func (m model) applyAll() (tea.Model, tea.Cmd) {
	if m.busy() {
		return m, nil
	}
	m.clearOutcome()
	if !m.plan.HasChanges() {
		m.notice = "No changes to apply."
		return m, nil
	}
	m.confirm = confirmPlan("Apply all changes?", "apply", "", m.plan, false)
	m.layout()
	return m, nil
}

// applySelected asks to apply the planned change of the selected resource.
func (m model) applySelected() (tea.Model, tea.Cmd) {
	m, r, ok := m.target()
	if !ok {
		return m, nil
	}
	if r.Action() == tfstate.NoOp {
		m.notice = fmt.Sprintf("No changes to apply for %s.", r.Address)
		return m, nil
	}
	// Plan again for just this resource, so the dialog shows exactly what
	// will be applied, including resources it depends on.
	m.targeting, m.targetDestroy = r.Address, false
	return m, planTarget(m.dir, r.Address, false)
}

// destroySelected asks to destroy the selected resource.
func (m model) destroySelected() (tea.Model, tea.Cmd) {
	m, r, ok := m.target()
	if !ok {
		return m, nil
	}
	switch {
	case !m.inState(r.Address):
		m.notice = fmt.Sprintf("%s doesn't exist yet.", r.Address)
		return m, nil
	case r.Mode == "data":
		m.notice = "Data sources can't be destroyed."
		return m, nil
	}
	// Plan the destroy first, so the dialog shows exactly what will be
	// destroyed, including resources that depend on this one.
	m.targeting, m.targetDestroy = r.Address, true
	return m, planTarget(m.dir, r.Address, true)
}

// removeSelected asks to drop the selected resource from the state.
func (m model) removeSelected() (tea.Model, tea.Cmd) {
	m, r, ok := m.target()
	if !ok {
		return m, nil
	}
	if !m.inState(r.Address) {
		m.notice = fmt.Sprintf("%s isn't in the state.", r.Address)
		return m, nil
	}
	m.confirm = confirmRemove(r)
	m.layout()
	return m, nil
}

// importSelected asks for the ID of the object to import as the selected resource.
func (m model) importSelected() (tea.Model, tea.Cmd) {
	m, r, ok := m.target()
	if !ok {
		return m, nil
	}
	switch {
	case m.inState(r.Address):
		m.notice = fmt.Sprintf("%s is already in the state.", r.Address)
		return m, nil
	case r.Mode == "data":
		m.notice = "Data sources can't be imported."
		return m, nil
	}
	var cmd tea.Cmd
	m.prompt, cmd = promptImport(r)
	m.layout()
	return m, cmd
}

// taintSelected taints the selected resource, or untaints an already tainted one.
func (m model) taintSelected() (tea.Model, tea.Cmd) {
	m, r, ok := m.target()
	if !ok {
		return m, nil
	}
	switch {
	case !m.inState(r.Address):
		m.notice = fmt.Sprintf("%s doesn't exist yet.", r.Address)
		return m, nil
	case r.Mode == "data":
		m.notice = "Data sources can't be tainted."
		return m, nil
	}
	if r.Tainted {
		m.running = fmt.Sprintf("Untainting %s…", r.Address)
		return m, untaint(m.dir, r.Address)
	}
	m.running = fmt.Sprintf("Tainting %s…", r.Address)
	return m, taint(m.dir, r.Address)
}

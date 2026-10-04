package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
	"github.com/teemuteemu/tf-mankeli/pkg/ui/theme"
)

// statusView shows what Terraform is doing, the outcome of the last action
// and a summary of the plan like the last line of `terraform plan`.
func (m model) statusView() string {
	width := max(m.width-2, 0)
	style := theme.Help.MaxWidth(width)

	switch {
	case m.running != "":
		return style.Render(m.running)
	case m.targeting != "" && m.targetDestroy:
		return style.Render(fmt.Sprintf("Planning destruction of %s…", m.targeting))
	case m.targeting != "":
		return style.Render(fmt.Sprintf("Planning changes for %s…", m.targeting))
	}

	// The error pane shows the failure in full, so name it here and no more.
	// Once dismissed, squeeze it onto this line so it isn't lost entirely.
	if title, err := m.failure(); err != nil {
		text := title + " failed."
		if m.errHidden {
			text = title + " failed: " + strings.Join(strings.Fields(err.Error()), " ")
		}
		return theme.Error.MaxWidth(width).Render(text)
	}

	status := ""
	switch {
	case m.loading:
		status = "Reading state and planning…"
	case m.planning:
		status = "Planning…"
	default:
		status = summarize(m.changes())
	}
	if m.notice != "" {
		status = m.notice + " " + status
	}
	return style.Render(status)
}

// summarize counts changes like the last line of `terraform plan`, adding
// how many outputs change.
func summarize(changes []tfstate.Change) string {
	add, change, destroy, outputs := 0, 0, 0, 0
	for _, c := range changes {
		if c.Mode == tfstate.OutputMode {
			if c.Action != tfstate.NoOp {
				outputs++
			}
			continue
		}
		switch c.Action {
		case tfstate.Create:
			add++
		case tfstate.Update:
			change++
		case tfstate.Delete:
			destroy++
		case tfstate.DeleteThenCreate, tfstate.CreateThenDelete:
			add++
			destroy++
		}
	}
	plan := "No changes."
	if add+change+destroy > 0 {
		plan = fmt.Sprintf("Plan: %d to add, %d to change, %d to destroy.", add, change, destroy)
	} else if outputs > 0 {
		plan = "No resource changes."
	}
	if outputs > 0 {
		plan += fmt.Sprintf(" Outputs: %d to change.", outputs)
	}
	return plan
}

// helpView lists the keys that do something right now, built from the same
// bindings handleKey matches against.
func (m model) helpView() string {
	if m.focus == detailsPane {
		line := append([]key.Binding{keys.Scroll}, keys.actionKeysFor(m.selected())...)
		line = append(line, withDesc(keys.Focus, "table"), keys.Close, keys.Plan, keys.Quit)
		return helpLine(line...)
	}
	// The error pane covers the table, so none of the table's keys apply.
	if m.showErrorPane() {
		return helpLine(keys.Scroll, withDesc(keys.Close, "dismiss"), keys.Plan, keys.Quit)
	}

	line := append([]key.Binding{keys.Details}, keys.actionKeysFor(m.selected())...)
	line = append(line, keys.Plan)
	if _, err := m.failure(); err != nil {
		line = append(line, keys.Error)
	}
	if m.showDetails {
		line = append(line, withDesc(keys.Focus, "details"), keys.Close)
	}
	// The table documents its own movement keys.
	return m.table.HelpView() + theme.Help.Render(" • ") + helpLine(line...)
}

// Package theme holds the colors and styles the UI is drawn with, so that
// every pane, dialog and line of text looks the same.
package theme

import (
	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
)

// Colors the styles below are built from.
var (
	BorderColor = lipgloss.Color("240")
	ActiveColor = lipgloss.Color("62")
	ErrorColor  = lipgloss.Color("196")
)

var (
	// Pane is the border drawn around the table, the details pane and the dialogs.
	Pane = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(BorderColor)

	// Message is a full-screen notice shown instead of the panes.
	Message = lipgloss.NewStyle().Padding(1, 2)

	// Title heads a dialog.
	Title = lipgloss.NewStyle().Bold(true)

	// Help is for the key hints and any other secondary text.
	Help = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	// Error is for anything that failed.
	Error = lipgloss.NewStyle().Foreground(ErrorColor)
)

// ActionStyle colors an action the way Terraform's plan output does.
func ActionStyle(a tfstate.Action) lipgloss.Style {
	s := lipgloss.NewStyle()
	switch a {
	case tfstate.Create:
		return s.Foreground(lipgloss.Color("42"))
	case tfstate.Delete:
		return s.Foreground(ErrorColor)
	case tfstate.Update:
		return s.Foreground(lipgloss.Color("220"))
	case tfstate.DeleteThenCreate, tfstate.CreateThenDelete:
		return s.Foreground(lipgloss.Color("170"))
	case tfstate.Read:
		return s.Foreground(lipgloss.Color("39"))
	}
	return s
}

// DescribeAction completes the sentence "<address> will be …".
func DescribeAction(a tfstate.Action) string {
	switch a {
	case tfstate.Create:
		return "created"
	case tfstate.Delete:
		return "destroyed"
	case tfstate.Update:
		return "updated in-place"
	case tfstate.DeleteThenCreate, tfstate.CreateThenDelete:
		return "replaced"
	case tfstate.Read:
		return "read"
	case tfstate.Other:
		return "changed"
	}
	return ""
}

// TableStyles is the table's own styling, with a header rule matching Pane's border.
func TableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(BorderColor).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	return s
}

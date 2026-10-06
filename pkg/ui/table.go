package ui

import (
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
	"github.com/teemuteemu/tf-mankeli/pkg/ui/theme"
)

// column is a table column with either a fixed width or a relative share
// (weight) of the remaining terminal width.
type column struct {
	title  string
	width  int
	weight int
}

var columns = []column{
	{title: "", width: 3}, // planned action
	{title: "", width: 7}, // tainted marker
	{title: "Address", weight: 3},
	{title: "Type", weight: 3},
	{title: "Name", weight: 2},
	{title: "Module", weight: 2},
	{title: "Provider", weight: 3},
	{title: "Mode", weight: 1},
}

// toRows builds the table rows, coloring each by its planned action. The
// selected row is left uncolored: colors inside its cells would break the
// selection highlight.
func toRows(resources []tfstate.Resource, selected int) []table.Row {
	rows := make([]table.Row, len(resources))
	for i, r := range resources {
		tainted := ""
		if r.Tainted {
			tainted = "tainted"
		}
		row := table.Row{string(r.Action()), tainted, r.Address, r.Type, r.Name, r.Module, r.Provider, r.Mode}
		if i != selected && r.Action() != tfstate.NoOp {
			style := theme.ActionStyle(r.Action())
			for j, cell := range row {
				if cell != "" {
					row[j] = style.Render(cell)
				}
			}
		}
		rows[i] = row
	}
	return rows
}

// updateTable passes msg to the table, which owns cursor movement.
func (m model) updateTable(msg tea.Msg) (tea.Model, tea.Cmd) {
	cursor := m.table.Cursor()
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	// Move the uncolored row along with the selection.
	if m.table.Cursor() != cursor {
		m.table.SetRows(toRows(m.rows, m.table.Cursor()))
	}
	return m, cmd
}

// scaleColumns gives fixed-width columns their width and splits the rest of
// width between the other columns by weight; the last of them takes the remainder.
func scaleColumns(cols []table.Column, width int) []table.Column {
	const cellPad = 2 // default cell style pads one cell left and right

	fixed, totalWeight, lastWeighted := 0, 0, 0
	for i, c := range columns {
		if c.width > 0 {
			fixed += c.width
		} else {
			totalWeight += c.weight
			lastWeighted = i
		}
	}

	available := max(width-cellPad*len(cols)-fixed, 0)
	remaining := available
	for i, c := range columns {
		switch {
		case c.width > 0:
			cols[i].Width = c.width
		case i == lastWeighted:
			cols[i].Width = remaining
		default:
			cols[i].Width = available * c.weight / totalWeight
			remaining -= cols[i].Width
		}
	}
	return cols
}

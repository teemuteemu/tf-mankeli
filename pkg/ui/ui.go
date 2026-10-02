// Package ui renders a full-screen, terminal-sized table.
package ui

import (
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

// columnWeights sets each column's relative share of the terminal width.
var columnWeights = []int{1, 4, 4, 3}

var totalWeight = func() int {
	sum := 0
	for _, w := range columnWeights {
		sum += w
	}
	return sum
}()

type model struct {
	table table.Model
}

// Run shows the given columns and rows in a table and blocks until the user quits.
func Run(columns []table.Column, rows []table.Row) error {
	_, err := tea.NewProgram(model{newTable(columns, rows)}).Run()
	return err
}

func newTable(columns []table.Column, rows []table.Row) table.Model {
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(s)
	return t
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			if m.table.Focused() {
				m.table.Blur()
			} else {
				m.table.Focus()
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			return m, tea.Batch(
				tea.Printf("Let's go to %s!", m.table.SelectedRow()[1]),
			)
		}
	}
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// resize fits the table to the terminal, leaving room for the border and help line.
func (m *model) resize(width, height int) {
	const (
		borderSize = 2 // one cell on each side
		helpHeight = 1
		cellPad    = 2 // default cell style pads one cell left and right
	)

	tableWidth := max(width-borderSize, 0)
	m.table.SetWidth(tableWidth)
	m.table.SetHeight(max(height-borderSize-helpHeight, 0))

	// Scale columns proportionally to their weights; the last column takes the remainder.
	cols := m.table.Columns()
	available := max(tableWidth-cellPad*len(cols), len(cols))
	used := 0
	for i := range cols {
		if i == len(cols)-1 {
			cols[i].Width = available - used
			break
		}
		cols[i].Width = available * columnWeights[i] / totalWeight
		used += cols[i].Width
	}
	m.table.SetColumns(cols)
}

func (m model) View() tea.View {
	v := tea.NewView(baseStyle.Render(m.table.View()) + "\n  " + m.table.HelpView())
	v.AltScreen = true
	return v
}

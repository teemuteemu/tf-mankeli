// Package ui renders Terraform state as a full-screen, terminal-sized table.
package ui

import (
	"context"
	"fmt"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
)

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

var messageStyle = lipgloss.NewStyle().Padding(1, 2)

// column is a table column and its relative share of the terminal width.
type column struct {
	title  string
	weight int
}

var columns = []column{
	{"Address", 4},
	{"Type", 3},
	{"Name", 2},
	{"Module", 2},
	{"Provider", 3},
	{"Mode", 1},
}

var totalWeight = func() int {
	sum := 0
	for _, c := range columns {
		sum += c.weight
	}
	return sum
}()

// stateLoadedMsg carries the result of reading the state.
type stateLoadedMsg struct {
	resources []tfstate.Resource
	err       error
}

type model struct {
	dir     string
	table   table.Model
	loading bool
	err     error
}

// Run shows the state of the Terraform working directory dir and blocks until the user quits.
func Run(dir string) error {
	_, err := tea.NewProgram(newModel(dir)).Run()
	return err
}

func newModel(dir string) model {
	cols := make([]table.Column, len(columns))
	for i, c := range columns {
		cols[i] = table.Column{Title: c.title, Width: len(c.title)}
	}

	t := table.New(
		table.WithColumns(cols),
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

	return model{dir: dir, table: t, loading: true}
}

func (m model) Init() tea.Cmd { return loadState(m.dir) }

// loadState reads the state in the background so the UI stays responsive.
func loadState(dir string) tea.Cmd {
	return func() tea.Msg {
		resources, err := tfstate.Load(context.Background(), dir)
		return stateLoadedMsg{resources: resources, err: err}
	}
}

func toRows(resources []tfstate.Resource) []table.Row {
	rows := make([]table.Row, len(resources))
	for i, r := range resources {
		rows[i] = table.Row{r.Address, r.Type, r.Name, r.Module, r.Provider, r.Mode}
	}
	return rows
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case stateLoadedMsg:
		m.loading = false
		m.err = msg.err
		m.table.SetRows(toRows(msg.resources))
		return m, nil
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
		cols[i].Width = available * columns[i].weight / totalWeight
		used += cols[i].Width
	}
	m.table.SetColumns(cols)
}

func (m model) View() tea.View {
	var content string
	switch {
	case m.loading:
		content = messageStyle.Render(fmt.Sprintf("Loading Terraform state from %s…", m.dir))
	case m.err != nil:
		content = messageStyle.Render(fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err))
	case len(m.table.Rows()) == 0:
		content = messageStyle.Render("No resources in state.\n\nPress q to quit.")
	default:
		content = baseStyle.Render(m.table.View()) + "\n  " + m.table.HelpView()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

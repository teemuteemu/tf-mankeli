// Package ui renders Terraform state as a full-screen, terminal-sized table.
package ui

import (
	"context"
	"encoding/json"
	"fmt"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
)

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

var activeBorderColor = lipgloss.Color("62")

var (
	messageStyle = lipgloss.NewStyle().Padding(1, 2)
	titleStyle   = lipgloss.NewStyle().Bold(true)
	helpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

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

// pane identifies which pane receives key presses.
type pane int

const (
	tablePane pane = iota
	detailsPane
)

type model struct {
	dir       string
	table     table.Model
	details   viewport.Model
	resources []tfstate.Resource // in the same order as the table rows

	showDetails bool
	focus       pane
	width       int
	height      int

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

	return model{dir: dir, table: t, details: viewport.New(), loading: true}
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
		m.resources = msg.resources
		m.table.SetRows(toRows(msg.resources))
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if m.focus == tablePane {
				m.openDetails()
				return m, nil
			}
		case "tab":
			if m.showDetails {
				if m.focus == tablePane {
					m.setFocus(detailsPane)
				} else {
					m.setFocus(tablePane)
				}
				return m, nil
			}
		case "esc":
			if m.showDetails {
				m.showDetails = false
				m.setFocus(tablePane)
				m.layout()
				return m, nil
			}
		}
		if m.focus == detailsPane {
			m.details, cmd = m.details.Update(msg)
			return m, cmd
		}
	}
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// openDetails shows the selected resource in the details pane and focuses it.
func (m *model) openDetails() {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.resources) {
		return
	}
	m.details.SetContent(renderDetails(m.resources[i]))
	m.details.GotoTop()
	m.showDetails = true
	m.setFocus(detailsPane)
	m.layout()
}

func renderDetails(r tfstate.Resource) string {
	attributes, err := json.MarshalIndent(r.Attributes, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error rendering attributes: %v", err)
	}
	return titleStyle.Render(r.Address) + "\n\n" + string(attributes)
}

func (m *model) setFocus(p pane) {
	m.focus = p
	if p == tablePane {
		m.table.Focus()
	} else {
		m.table.Blur()
	}
}

// layout fits the panes to the terminal, leaving room for their borders and
// the help line. With details open, the table and details split the height.
func (m *model) layout() {
	const (
		borderSize = 2 // one cell on each side
		helpHeight = 1
	)

	paneWidth := max(m.width-borderSize, 0)
	available := max(m.height-helpHeight-borderSize, 0)
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
}

// scaleColumns sizes columns proportionally to their weights so they fill
// width; the last column takes the remainder.
func scaleColumns(cols []table.Column, width int) []table.Column {
	const cellPad = 2 // default cell style pads one cell left and right

	available := max(width-cellPad*len(cols), len(cols))
	used := 0
	for i := range cols {
		if i == len(cols)-1 {
			cols[i].Width = available - used
			break
		}
		cols[i].Width = available * columns[i].weight / totalWeight
		used += cols[i].Width
	}
	return cols
}

// paneStyle highlights the focused pane's border when both panes are shown.
func (m model) paneStyle(p pane) lipgloss.Style {
	if m.showDetails && m.focus == p {
		return baseStyle.BorderForeground(activeBorderColor)
	}
	return baseStyle
}

func (m model) helpView() string {
	if m.focus == detailsPane {
		return helpStyle.Render("↑/↓ scroll • tab table • esc close • q quit")
	}
	extra := "enter details"
	if m.showDetails {
		extra += " • tab details • esc close"
	}
	return m.table.HelpView() + helpStyle.Render(" • "+extra)
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
		content = m.paneStyle(tablePane).Render(m.table.View())
		if m.showDetails {
			content = lipgloss.JoinVertical(lipgloss.Left,
				content,
				m.paneStyle(detailsPane).Render(m.details.View()),
			)
		}
		content += "\n  " + m.helpView()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

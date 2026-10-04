// Package ui renders Terraform state and its planned changes as a full-screen,
// terminal-sized table.
package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

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
	helpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

// actionStyle colors an action the way Terraform's plan output does.
func actionStyle(a tfstate.Action) lipgloss.Style {
	s := lipgloss.NewStyle()
	switch a {
	case tfstate.Create:
		return s.Foreground(lipgloss.Color("42"))
	case tfstate.Delete:
		return s.Foreground(lipgloss.Color("196"))
	case tfstate.Update:
		return s.Foreground(lipgloss.Color("220"))
	case tfstate.DeleteThenCreate, tfstate.CreateThenDelete:
		return s.Foreground(lipgloss.Color("170"))
	case tfstate.Read:
		return s.Foreground(lipgloss.Color("39"))
	}
	return s
}

// describeAction completes the sentence "<address> will be …".
func describeAction(a tfstate.Action) string {
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

// column is a table column with either a fixed width or a relative share
// (weight) of the remaining terminal width.
type column struct {
	title  string
	width  int
	weight int
}

var columns = []column{
	{title: "", width: 3}, // planned action
	{title: "Address", weight: 4},
	{title: "Type", weight: 3},
	{title: "Name", weight: 2},
	{title: "Module", weight: 2},
	{title: "Provider", weight: 3},
	{title: "Mode", weight: 1},
}

// stateLoadedMsg carries the result of reading the state.
type stateLoadedMsg struct {
	resources []tfstate.Resource
	err       error
}

// planLoadedMsg carries the result of planning.
type planLoadedMsg struct {
	changes []tfstate.Change
	err     error
}

// pane identifies which pane receives key presses.
type pane int

const (
	tablePane pane = iota
	detailsPane
)

type model struct {
	dir     string
	table   table.Model
	details viewport.Model

	state   []tfstate.Resource
	changes []tfstate.Change
	rows    []tfstate.Resource // state merged with the plan, in table row order

	showDetails    bool
	detailsAddress string // resource shown in the details pane
	focus          pane
	width          int
	height         int

	loading  bool
	err      error
	planning bool
	planErr  error
}

// Run shows the state and planned changes of the Terraform working directory
// dir and blocks until the user quits. The state is read and the plan made
// before the UI starts, so it opens with the plan ready. A plan failure is
// shown in the UI; a state failure is returned.
func Run(dir string) error {
	fmt.Printf("Reading Terraform state and planning in %s…\n", dir)

	var state stateLoadedMsg
	var plan planLoadedMsg
	var wg sync.WaitGroup
	wg.Go(func() { state = readState(dir) })
	wg.Go(func() { plan = readPlan(dir) })
	wg.Wait()

	if state.err != nil {
		return state.err
	}

	var m tea.Model = newModel(dir)
	m, _ = m.Update(state)
	m, _ = m.Update(plan)
	_, err := tea.NewProgram(m).Run()
	return err
}

func newModel(dir string) model {
	cols := make([]table.Column, len(columns))
	for i, c := range columns {
		cols[i] = table.Column{Title: c.title, Width: max(c.width, len(c.title))}
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

	return model{dir: dir, table: t, details: viewport.New(), loading: true, planning: true}
}

func (m model) Init() tea.Cmd { return nil }

// refresh reads the state and plans again in parallel, so the new state shows
// up while the slower plan is still running.
func (m model) refresh() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return readState(m.dir) },
		func() tea.Msg { return readPlan(m.dir) },
	)
}

func readState(dir string) stateLoadedMsg {
	resources, err := tfstate.Load(context.Background(), dir)
	return stateLoadedMsg{resources: resources, err: err}
}

func readPlan(dir string) planLoadedMsg {
	changes, err := tfstate.Plan(context.Background(), dir)
	return planLoadedMsg{changes: changes, err: err}
}

// toRows builds the table rows, coloring each by its planned action. The
// selected row is left uncolored: colors inside its cells would break the
// selection highlight.
func toRows(resources []tfstate.Resource, selected int) []table.Row {
	rows := make([]table.Row, len(resources))
	for i, r := range resources {
		row := table.Row{string(r.Action()), r.Address, r.Type, r.Name, r.Module, r.Provider, r.Mode}
		if i != selected && r.Action() != tfstate.NoOp {
			style := actionStyle(r.Action())
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

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case stateLoadedMsg:
		m.loading = false
		m.err = msg.err
		m.state = msg.resources
		m.rebuild()
		return m, nil
	case planLoadedMsg:
		m.planning = false
		m.planErr = msg.err
		m.changes = msg.changes
		m.rebuild()
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "ctrl+r":
			// Ignore repeats while a refresh is running, so results can't arrive out of order.
			if m.loading || m.planning {
				return m, nil
			}
			m.loading, m.planning = true, true
			m.err, m.planErr = nil, nil
			return m, m.refresh()
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
	cursor := m.table.Cursor()
	m.table, cmd = m.table.Update(msg)
	// Move the uncolored row along with the selection.
	if m.table.Cursor() != cursor {
		m.table.SetRows(toRows(m.rows, m.table.Cursor()))
	}
	return m, cmd
}

// rebuild merges the state with the plan and refreshes the table and the
// details pane, keeping the cursor on the same resource.
func (m *model) rebuild() {
	selected := ""
	if i := m.table.Cursor(); i >= 0 && i < len(m.rows) {
		selected = m.rows[i].Address
	}

	m.rows = tfstate.Merge(m.state, m.changes)
	cursor := m.indexOf(selected)
	if cursor < 0 {
		cursor = min(m.table.Cursor(), len(m.rows)-1)
	}
	m.table.SetRows(toRows(m.rows, cursor))
	if cursor >= 0 {
		m.table.SetCursor(cursor)
	}
	if i := m.indexOf(m.detailsAddress); m.showDetails && i >= 0 {
		m.details.SetContent(renderDetails(m.rows[i]))
	}
}

func (m model) indexOf(address string) int {
	return slices.IndexFunc(m.rows, func(r tfstate.Resource) bool { return r.Address == address })
}

// openDetails shows the selected resource in the details pane and focuses it.
func (m *model) openDetails() {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.rows) {
		return
	}
	m.detailsAddress = m.rows[i].Address
	m.details.SetContent(renderDetails(m.rows[i]))
	m.details.GotoTop()
	m.showDetails = true
	m.setFocus(detailsPane)
	m.layout()
}

// renderDetails shows a resource's attributes, marking planned changes the
// way Terraform's plan output does.
func renderDetails(r tfstate.Resource) string {
	action := r.Action()
	title := r.Address
	if action != tfstate.NoOp {
		title = fmt.Sprintf("%s %s will be %s", action, r.Address, describeAction(action))
	}

	before, after := r.Attributes, r.Attributes
	if r.Change != nil && action != tfstate.NoOp {
		before, after = r.Change.Before, r.Change.After
	}

	return actionStyle(action).Bold(true).Render(title) + "\n\n" +
		strings.Join(diffLines(before, after), "\n")
}

// diffLines lists every attribute, prefixed with + if added, - if removed or
// ~ if changed. Attributes that are null both before and after are left out.
func diffLines(before, after map[string]any) []string {
	keys := make(map[string]bool, len(before)+len(after))
	keyWidth := 0
	for _, attrs := range []map[string]any{before, after} {
		for k := range attrs {
			keys[k] = true
			keyWidth = max(keyWidth, len(k))
		}
	}

	var lines []string
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		b, a := before[k], after[k]
		switch {
		case b == nil && a == nil:
			continue
		case b == nil:
			lines = append(lines, actionStyle(tfstate.Create).Render(
				fmt.Sprintf("+ %-*s = %s", keyWidth, k, formatValue(a))))
		case a == nil:
			lines = append(lines, actionStyle(tfstate.Delete).Render(
				fmt.Sprintf("- %-*s = %s", keyWidth, k, formatValue(b))))
		case reflect.DeepEqual(a, b):
			lines = append(lines, fmt.Sprintf("  %-*s = %s", keyWidth, k, formatValue(a)))
		default:
			lines = append(lines, actionStyle(tfstate.Update).Render(
				fmt.Sprintf("~ %-*s = %s → %s", keyWidth, k, formatValue(b), formatValue(a))))
		}
	}
	return lines
}

// formatValue renders a value as compact JSON, leaving placeholders unquoted.
func formatValue(v any) string {
	if s, ok := v.(string); ok && (s == tfstate.SensitivePlaceholder || s == tfstate.UnknownPlaceholder) {
		return s
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(buf.String(), "\n")
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

// paneStyle highlights the focused pane's border when both panes are shown.
func (m model) paneStyle(p pane) lipgloss.Style {
	if m.showDetails && m.focus == p {
		return baseStyle.BorderForeground(activeBorderColor)
	}
	return baseStyle
}

// statusView summarizes the plan like the last line of `terraform plan`.
func (m model) statusView() string {
	style := helpStyle.MaxWidth(max(m.width-2, 0))
	switch {
	case m.loading:
		return style.Render("Refreshing state and plan…")
	case m.planning:
		return style.Render("Planning…")
	case m.planErr != nil:
		// Terraform errors span several lines; squeeze them onto the status line.
		return errorStyle.MaxWidth(max(m.width-2, 0)).
			Render("Plan failed: " + strings.Join(strings.Fields(m.planErr.Error()), " "))
	}

	add, change, destroy := 0, 0, 0
	for _, c := range m.changes {
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
	if add+change+destroy == 0 {
		return style.Render("No changes.")
	}
	return style.Render(fmt.Sprintf("Plan: %d to add, %d to change, %d to destroy.", add, change, destroy))
}

func (m model) helpView() string {
	if m.focus == detailsPane {
		return helpStyle.Render("↑/↓ scroll • tab table • esc close • ctrl+r refresh • q quit")
	}
	extra := "enter details • ctrl+r refresh"
	if m.showDetails {
		extra += " • tab details • esc close"
	}
	return m.table.HelpView() + helpStyle.Render(" • "+extra)
}

func (m model) View() tea.View {
	var content string
	switch {
	// On a refresh, keep showing the previous rows until the new state arrives.
	case m.loading && len(m.rows) == 0:
		content = messageStyle.Render(fmt.Sprintf("Loading Terraform state from %s…", m.dir))
	case m.err != nil:
		content = messageStyle.Render(fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err))
	case len(m.rows) == 0 && !m.planning && m.planErr == nil:
		content = messageStyle.Render("No resources in state or plan.\n\nPress q to quit.")
	default:
		content = m.paneStyle(tablePane).Render(m.table.View())
		if m.showDetails {
			content = lipgloss.JoinVertical(lipgloss.Left,
				content,
				m.paneStyle(detailsPane).Render(m.details.View()),
			)
		}
		content += "\n  " + m.statusView() + "\n  " + m.helpView()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

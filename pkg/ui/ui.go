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
	titleStyle   = lipgloss.NewStyle().Bold(true)
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
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

// planLoadedMsg carries the result of planning all changes.
type planLoadedMsg struct {
	plan *tfstate.SavedPlan
	err  error
}

// targetPlanMsg carries the result of planning the changes of one resource.
type targetPlanMsg struct {
	address string
	plan    *tfstate.SavedPlan
	err     error
}

// appliedMsg carries the result of applying a plan.
type appliedMsg struct {
	err error
}

// confirmation is a plan waiting for the user to confirm applying it.
type confirmation struct {
	title    string
	plan     *tfstate.SavedPlan
	targeted bool           // the plan was made only for this confirmation
	body     viewport.Model // every planned change with its attribute diff
}

func newConfirmation(title string, plan *tfstate.SavedPlan, targeted bool) *confirmation {
	var changes []string
	if targeted && countChanges(plan.Changes) > 1 {
		changes = append(changes, helpStyle.Render("Includes the resources it depends on."))
	}
	for _, c := range plan.Changes {
		if c.Action != tfstate.NoOp {
			changes = append(changes, renderDetails(tfstate.Resource{Address: c.Address, Change: &c}))
		}
	}

	body := viewport.New()
	body.SetContent(strings.Join(changes, "\n\n"))
	return &confirmation{title: title, plan: plan, targeted: targeted, body: body}
}

func countChanges(changes []tfstate.Change) int {
	n := 0
	for _, c := range changes {
		if c.Action != tfstate.NoOp {
			n++
		}
	}
	return n
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

	state []tfstate.Resource
	plan  *tfstate.SavedPlan // nil until planned, or if planning failed
	rows  []tfstate.Resource // state merged with the plan, in table row order

	showDetails    bool
	detailsAddress string // resource shown in the details pane
	focus          pane
	width          int
	height         int

	loading  bool
	err      error
	planning bool
	planErr  error

	confirm   *confirmation // shown as a dialog while non-nil
	targeting string        // address being planned for a targeted apply
	applying  bool
	actionErr error  // failure of the last targeted plan or apply
	notice    string // outcome of the last action, shown on the status line
}

// changes returns the planned changes, or none if there is no plan.
func (m model) changes() []tfstate.Change {
	if m.plan == nil {
		return nil
	}
	return m.plan.Changes
}

// busy reports whether Terraform is running, so no other run should start.
func (m model) busy() bool {
	return m.loading || m.planning || m.applying || m.targeting != ""
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
		plan.plan.Discard()
		return state.err
	}

	var m tea.Model = newModel(dir)
	m, _ = m.Update(state)
	m, _ = m.Update(plan)
	final, err := tea.NewProgram(m).Run()

	// Plan files hold sensitive values; don't leave them behind.
	if fm, ok := final.(model); ok {
		fm.plan.Discard()
		if fm.confirm != nil {
			fm.confirm.plan.Discard()
		}
	}
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

// replan reads the state and plans again in parallel, so the new state shows
// up while the slower plan is still running.
func (m model) replan() tea.Cmd {
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
	plan, err := tfstate.Plan(context.Background(), dir)
	return planLoadedMsg{plan: plan, err: err}
}

func planTarget(dir, address string) tea.Cmd {
	return func() tea.Msg {
		plan, err := tfstate.Plan(context.Background(), dir, address)
		return targetPlanMsg{address: address, plan: plan, err: err}
	}
}

func apply(plan *tfstate.SavedPlan) tea.Cmd {
	return func() tea.Msg {
		return appliedMsg{err: plan.Apply(context.Background())}
	}
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
		m.plan.Discard()
		m.plan = msg.plan
		m.rebuild()
		return m, nil
	case targetPlanMsg:
		m.targeting = ""
		switch {
		case msg.err != nil:
			m.actionErr = msg.err
		case !msg.plan.HasChanges():
			msg.plan.Discard()
			m.notice = fmt.Sprintf("No changes to apply for %s.", msg.address)
		default:
			m.confirm = newConfirmation(fmt.Sprintf("Apply changes for %s?", msg.address), msg.plan, true)
			m.layout()
		}
		return m, nil
	case appliedMsg:
		m.applying = false
		if msg.err != nil {
			m.actionErr = msg.err
		} else {
			m.notice = "Applied."
		}
		// Even a failed apply may have changed some resources, so plan again either way.
		m.loading, m.planning = true, true
		m.err, m.planErr = nil, nil
		return m, m.replan()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.KeyPressMsg:
		if m.confirm != nil {
			return m.updateConfirm(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			// Quitting would leave Terraform applying with nobody watching.
			if m.applying {
				m.notice = "Apply in progress; quit once it has finished."
				return m, nil
			}
			return m, tea.Quit
		case "P", "shift+p":
			// Ignore repeats while Terraform runs, so results can't arrive out of order.
			if m.busy() {
				return m, nil
			}
			m.clearOutcome()
			m.loading, m.planning = true, true
			m.err, m.planErr = nil, nil
			return m, m.replan()
		case "A", "shift+a":
			if m.busy() {
				return m, nil
			}
			m.clearOutcome()
			if !m.plan.HasChanges() {
				m.notice = "No changes to apply."
				return m, nil
			}
			m.confirm = newConfirmation("Apply all changes?", m.plan, false)
			m.layout()
			return m, nil
		case "a":
			if m.busy() {
				return m, nil
			}
			m.clearOutcome()
			i := m.table.Cursor()
			if m.focus == detailsPane {
				i = m.indexOf(m.detailsAddress)
			}
			if i < 0 || i >= len(m.rows) {
				return m, nil
			}
			r := m.rows[i]
			if r.Action() == tfstate.NoOp {
				m.notice = fmt.Sprintf("No changes to apply for %s.", r.Address)
				return m, nil
			}
			// Plan again for just this resource, so the dialog shows exactly what
			// will be applied, including resources it depends on.
			m.targeting = r.Address
			return m, planTarget(m.dir, r.Address)
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

// updateConfirm handles keys while the confirmation dialog is shown.
func (m model) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		plan := m.confirm.plan
		m.confirm = nil
		m.applying = true
		return m, apply(plan)
	case "n", "esc", "q", "ctrl+c":
		if m.confirm.targeted {
			m.confirm.plan.Discard()
		}
		m.confirm = nil
		m.notice = "Apply canceled."
		return m, nil
	}
	var cmd tea.Cmd
	m.confirm.body, cmd = m.confirm.body.Update(msg)
	return m, cmd
}

func (m *model) clearOutcome() {
	m.actionErr = nil
	m.notice = ""
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

	if m.confirm != nil {
		const (
			dialogPadding = 2 // one cell left and right
			dialogLines   = 5 // title, summary, help and the blank lines between
		)
		m.confirm.body.SetWidth(max(paneWidth-dialogPadding, 0))
		m.confirm.body.SetHeight(max(m.height-borderSize-dialogLines, 0))
	}
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

// statusView shows what Terraform is doing, the outcome of the last action
// and a summary of the plan like the last line of `terraform plan`.
func (m model) statusView() string {
	width := max(m.width-2, 0)
	style := helpStyle.MaxWidth(width)
	// Terraform errors span several lines; squeeze them onto the status line.
	failed := func(what string, err error) string {
		return errorStyle.MaxWidth(width).Render(what + " failed: " + strings.Join(strings.Fields(err.Error()), " "))
	}

	switch {
	case m.applying:
		return style.Render("Applying…")
	case m.targeting != "":
		return style.Render(fmt.Sprintf("Planning changes for %s…", m.targeting))
	case m.actionErr != nil:
		return failed("Apply", m.actionErr)
	}

	status := ""
	switch {
	case m.loading:
		status = "Reading state and planning…"
	case m.planning:
		status = "Planning…"
	case m.planErr != nil:
		return failed("Plan", m.planErr)
	default:
		status = summarize(m.changes())
	}
	if m.notice != "" {
		status = m.notice + " " + status
	}
	return style.Render(status)
}

// summarize counts changes like the last line of `terraform plan`.
func summarize(changes []tfstate.Change) string {
	add, change, destroy := 0, 0, 0
	for _, c := range changes {
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
		return "No changes."
	}
	return fmt.Sprintf("Plan: %d to add, %d to change, %d to destroy.", add, change, destroy)
}

func (m model) helpView() string {
	if m.focus == detailsPane {
		return helpStyle.Render("↑/↓ scroll • a apply • shift+a apply all • tab table • esc close • shift+p plan • q quit")
	}
	extra := "enter details • a apply • shift+a apply all • shift+p plan"
	if m.showDetails {
		extra += " • tab details • esc close"
	}
	return m.table.HelpView() + helpStyle.Render(" • "+extra)
}

// confirmView is a full-screen dialog showing every change that confirming
// would apply, with its attribute diff, in a scrollable body.
func (m model) confirmView() string {
	lines := []string{
		titleStyle.Render(m.confirm.title),
		"",
		m.confirm.body.View(),
		"",
		summarize(m.confirm.plan.Changes),
		helpStyle.Render("↑/↓ scroll • y apply • n cancel"),
	}
	return baseStyle.
		BorderForeground(activeBorderColor).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (m model) View() tea.View {
	var content string
	switch {
	case m.confirm != nil:
		content = m.confirmView()
	// While planning again, keep showing the previous rows until the new state arrives.
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

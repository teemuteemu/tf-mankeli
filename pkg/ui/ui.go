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
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
)

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

var (
	activeBorderColor = lipgloss.Color("62")
	errorColor        = lipgloss.Color("196")
)

var (
	messageStyle = lipgloss.NewStyle().Padding(1, 2)
	titleStyle   = lipgloss.NewStyle().Bold(true)
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle   = lipgloss.NewStyle().Foreground(errorColor)
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
	{title: "", width: 7}, // tainted marker
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

// targetPlanMsg carries the result of planning changes for one resource.
type targetPlanMsg struct {
	address string
	destroy bool
	plan    *tfstate.SavedPlan
	err     error
}

// actionDoneMsg carries the result of applying a plan or removing a
// resource from the state.
type actionDoneMsg struct {
	done string // notice to show on success
	err  error
}

// confirmation is an action waiting for the user to confirm it: either
// applying a plan or removing a resource from the state.
type confirmation struct {
	title   string
	verb    string // what pressing y does, for the help line
	summary string
	body    viewport.Model // details of everything the action changes

	plan     *tfstate.SavedPlan // the plan to apply, or nil
	targeted bool               // the plan was made only for this confirmation
	remove   string             // address to remove from the state, if plan is nil
}

// confirmPlan asks to apply plan, showing every planned change with its
// attribute diff. note is shown first when the plan changes more than one resource.
func confirmPlan(title, verb, note string, plan *tfstate.SavedPlan, targeted bool) *confirmation {
	var sections []string
	if note != "" && countChanges(plan.Changes) > 1 {
		sections = append(sections, helpStyle.Render(note))
	}
	for _, c := range plan.Changes {
		if c.Action != tfstate.NoOp {
			sections = append(sections, renderDetails(tfstate.Resource{Address: c.Address, Change: &c}))
		}
	}
	return newConfirmation(confirmation{
		title:    title,
		verb:     verb,
		summary:  summarize(plan.Changes),
		plan:     plan,
		targeted: targeted,
	}, sections)
}

// confirmRemove asks to remove r from the state, showing its current attributes.
func confirmRemove(r tfstate.Resource) *confirmation {
	sections := []string{
		helpStyle.Render("Terraform will forget this resource. The real resource is not destroyed,\n" +
			"and a later plan will offer to create it again if it is still in the configuration."),
		renderDetails(tfstate.Resource{Address: r.Address, Attributes: r.Attributes}),
	}
	return newConfirmation(confirmation{
		title:   fmt.Sprintf("Remove %s from state?", r.Address),
		verb:    "remove from state",
		summary: "1 to remove from state.",
		remove:  r.Address,
	}, sections)
}

func newConfirmation(c confirmation, sections []string) *confirmation {
	c.body = viewport.New()
	c.body.SetContent(strings.Join(sections, "\n\n"))
	return &c
}

// prompt asks the user to type a value, and runs an action with it once the
// value is submitted.
type prompt struct {
	title   string
	help    string
	verb    string // what pressing enter does, for the help line
	address string // resource the value applies to
	input   textinput.Model
}

// promptImport asks for the ID of the object to import as the resource r.
func promptImport(r tfstate.Resource) (*prompt, tea.Cmd) {
	in := textinput.New()
	in.Prompt = "ID: "
	in.Placeholder = "the ID the provider identifies the existing object by"
	cmd := in.Focus()

	return &prompt{
		title: fmt.Sprintf("Import %s", r.Address),
		help: "Terraform will read the existing object with this ID and record it as this\n" +
			"resource. No new resource is created. The ID format is the provider's own;\n" +
			"see the resource type's import documentation.",
		verb:    "import",
		address: r.Address,
		input:   in,
	}, cmd
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

	// The error pane shows the current failure in full, taking the table's place.
	errPane   viewport.Model
	errHidden bool   // dismissed by the user; the status line still shows the failure
	errText   string // the failure in the pane, to tell a new one from a resize

	confirm       *confirmation // shown as a dialog while non-nil
	prompt        *prompt       // shown as a dialog while non-nil
	targeting     string        // address being planned for a targeted apply or destroy
	targetDestroy bool          // the targeted plan destroys
	running       string        // status text while applying or removing from state
	actionErr     error         // failure of the last targeted plan, apply or removal
	notice        string        // outcome of the last action, shown on the status line
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

	return model{
		dir:      dir,
		table:    t,
		details:  viewport.New(),
		errPane:  viewport.New(),
		loading:  true,
		planning: true,
	}
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

// planTarget plans applying, or with destroy destroying, just one resource.
func planTarget(dir, address string, destroy bool) tea.Cmd {
	return func() tea.Msg {
		planFn := tfstate.Plan
		if destroy {
			planFn = tfstate.PlanDestroy
		}
		plan, err := planFn(context.Background(), dir, address)
		return targetPlanMsg{address: address, destroy: destroy, plan: plan, err: err}
	}
}

func apply(plan *tfstate.SavedPlan) tea.Cmd {
	return func() tea.Msg {
		return actionDoneMsg{done: "Applied.", err: plan.Apply(context.Background())}
	}
}

func taint(dir, address string) tea.Cmd {
	return func() tea.Msg {
		err := tfstate.Taint(context.Background(), dir, address)
		return actionDoneMsg{done: fmt.Sprintf("Tainted %s.", address), err: err}
	}
}

func untaint(dir, address string) tea.Cmd {
	return func() tea.Msg {
		err := tfstate.Untaint(context.Background(), dir, address)
		return actionDoneMsg{done: fmt.Sprintf("Untainted %s.", address), err: err}
	}
}

func importResource(dir, address, id string) tea.Cmd {
	return func() tea.Msg {
		err := tfstate.Import(context.Background(), dir, address, id)
		return actionDoneMsg{done: fmt.Sprintf("Imported %s.", address), err: err}
	}
}

func removeFromState(dir, address string) tea.Cmd {
	return func() tea.Msg {
		err := tfstate.RemoveFromState(context.Background(), dir, address)
		return actionDoneMsg{done: fmt.Sprintf("Removed %s from state.", address), err: err}
	}
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
			m.refreshError()
		case !msg.plan.HasChanges():
			msg.plan.Discard()
			m.notice = fmt.Sprintf("No changes for %s.", msg.address)
		case msg.destroy:
			m.confirm = confirmPlan(fmt.Sprintf("Destroy %s?", msg.address), "destroy",
				"Includes the resources that depend on it.", msg.plan, true)
			m.layout()
		default:
			m.confirm = confirmPlan(fmt.Sprintf("Apply changes for %s?", msg.address), "apply",
				"Includes the resources it depends on.", msg.plan, true)
			m.layout()
		}
		return m, nil
	case actionDoneMsg:
		m.running = ""
		if msg.err != nil {
			m.actionErr = msg.err
		} else {
			m.notice = msg.done
		}
		// Even a failed apply may have changed some resources, so plan again either way.
		m.loading, m.planning = true, true
		m.err, m.planErr = nil, nil
		m.refreshError()
		return m, m.replan()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.KeyPressMsg:
		if m.confirm != nil {
			return m.updateConfirm(msg)
		}
		if m.prompt != nil {
			return m.updatePrompt(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			// Quitting would leave Terraform changing things with nobody watching.
			if m.running != "" {
				m.notice = "Terraform is still running; quit once it has finished."
				return m, nil
			}
			return m, tea.Quit
		case "p":
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
			m.confirm = confirmPlan("Apply all changes?", "apply", "", m.plan, false)
			m.layout()
			return m, nil
		case "a":
			if m.busy() {
				return m, nil
			}
			m.clearOutcome()
			r, ok := m.selected()
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
		case "D", "shift+d":
			if m.busy() {
				return m, nil
			}
			m.clearOutcome()
			r, ok := m.selected()
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
		case "d":
			if m.busy() {
				return m, nil
			}
			m.clearOutcome()
			r, ok := m.selected()
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
		case "i":
			if m.busy() {
				return m, nil
			}
			m.clearOutcome()
			r, ok := m.selected()
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
		case "t":
			if m.busy() {
				return m, nil
			}
			m.clearOutcome()
			r, ok := m.selected()
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
		case "e":
			// Toggle the full error back on after dismissing it.
			if _, err := m.failure(); err != nil {
				m.errHidden = !m.errHidden
				return m, nil
			}
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
		if m.focus == detailsPane {
			m.details, cmd = m.details.Update(msg)
			return m, cmd
		}
		// The error pane stands in for the table, so it takes the table's keys.
		if m.showErrorPane() {
			m.errPane, cmd = m.errPane.Update(msg)
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
		c := m.confirm
		m.confirm = nil
		if c.plan == nil {
			m.running = fmt.Sprintf("Removing %s from state…", c.remove)
			return m, removeFromState(m.dir, c.remove)
		}
		m.running = "Applying…"
		return m, apply(c.plan)
	case "n", "esc", "q", "ctrl+c":
		if m.confirm.targeted {
			m.confirm.plan.Discard()
		}
		m.confirm = nil
		m.notice = "Canceled."
		return m, nil
	}
	var cmd tea.Cmd
	m.confirm.body, cmd = m.confirm.body.Update(msg)
	return m, cmd
}

// updatePrompt handles keys while the value prompt is shown. Every key that
// isn't enter or escape goes to the text input, so that letters bound to
// actions in the table are typed rather than acted on.
func (m model) updatePrompt(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		p := m.prompt
		id := strings.TrimSpace(p.input.Value())
		// Submitting nothing would import an empty ID; keep asking instead.
		if id == "" {
			return m, nil
		}
		m.prompt = nil
		m.running = fmt.Sprintf("Importing %s…", p.address)
		return m, importResource(m.dir, p.address, id)
	case "esc", "ctrl+c":
		m.prompt = nil
		m.notice = "Canceled."
		return m, nil
	}
	var cmd tea.Cmd
	m.prompt.input, cmd = m.prompt.input.Update(msg)
	return m, cmd
}

func (m *model) clearOutcome() {
	m.actionErr = nil
	m.notice = ""
	m.refreshError()
}

// failure returns the error worth showing, most recent first, with a title
// completing the sentence "<title> failed". It returns a nil error when
// nothing has failed.
func (m model) failure() (string, error) {
	switch {
	case m.actionErr != nil:
		// The error already names the action, e.g. "applying: …".
		return "Terraform", m.actionErr
	case m.err != nil:
		return "Reading the state", m.err
	case m.planErr != nil:
		return "Plan", m.planErr
	}
	return "", nil
}

// showErrorPane reports whether the error pane takes the table's place.
func (m model) showErrorPane() bool {
	_, err := m.failure()
	return err != nil && !m.errHidden
}

// refreshError renders the current failure into the error pane, wrapped to its
// width. A failure the user hasn't seen yet reopens the pane and scrolls it
// back to the top; re-rendering the same one after a resize leaves both alone.
func (m *model) refreshError() {
	title, err := m.failure()
	if err == nil {
		m.errText = ""
		return
	}

	text := strings.TrimSpace(err.Error())
	if text != m.errText {
		m.errText = text
		m.errHidden = false
		m.errPane.GotoTop()
	}
	if w := m.errPane.Width(); w > 0 {
		text = lipgloss.NewStyle().Width(w).Render(text)
	}
	m.errPane.SetContent(errorStyle.Bold(true).Render(title+" failed") + "\n\n" + text)
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
	m.refreshError()
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

	header := actionStyle(action).Bold(true).Render(title)
	if r.Tainted {
		header += "\n" + helpStyle.Render("Tainted: it will be replaced on the next apply.")
	}
	return header + "\n\n" + strings.Join(diffLines(before, after), "\n")
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

	// The error pane stands in for the table, so it gets the same box.
	m.errPane.SetWidth(paneWidth)
	m.errPane.SetHeight(tableHeight)
	m.refreshError()

	if m.confirm != nil {
		const (
			dialogPadding = 2 // one cell left and right
			dialogLines   = 5 // title, summary, help and the blank lines between
		)
		m.confirm.body.SetWidth(max(paneWidth-dialogPadding, 0))
		m.confirm.body.SetHeight(max(m.height-borderSize-dialogLines, 0))
	}

	if m.prompt != nil {
		const (
			dialogPadding = 2 // one cell left and right
			promptWidth   = 4 // the input's own "ID: " prompt
		)
		m.prompt.input.SetWidth(max(paneWidth-dialogPadding-promptWidth, 0))
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
		return errorStyle.MaxWidth(width).Render(text)
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
		return helpStyle.Render("↑/↓ scroll • a apply • shift+a apply all • d remove from state • shift+d destroy • i import • t taint/untaint • tab table • esc close • p plan • q quit")
	}
	// The error pane covers the table, so none of the table's keys apply.
	if m.showErrorPane() {
		return helpStyle.Render("↑/↓ scroll • esc dismiss • p plan • q quit")
	}
	extra := "enter details • a apply • shift+a apply all • d remove from state • shift+d destroy • i import • t taint/untaint • p plan"
	if _, err := m.failure(); err != nil {
		extra += " • e error"
	}
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
		m.confirm.summary,
		helpStyle.Render(fmt.Sprintf("↑/↓ scroll • y %s • n cancel", m.confirm.verb)),
	}
	return baseStyle.
		BorderForeground(activeBorderColor).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// promptView is a dialog asking for a single value, with the text input focused.
func (m model) promptView() string {
	lines := []string{
		titleStyle.Render(m.prompt.title),
		"",
		helpStyle.Render(m.prompt.help),
		"",
		m.prompt.input.View(),
		"",
		helpStyle.Render(fmt.Sprintf("enter %s • esc cancel", m.prompt.verb)),
	}
	return baseStyle.
		BorderForeground(activeBorderColor).
		Padding(0, 1).
		Width(max(m.width-2, 0)).
		Render(strings.Join(lines, "\n"))
}

func (m model) View() tea.View {
	var content string
	switch {
	case m.confirm != nil:
		content = m.confirmView()
	case m.prompt != nil:
		content = m.promptView()
	// While planning again, keep showing the previous rows until the new state arrives.
	case m.loading && len(m.rows) == 0 && !m.showErrorPane():
		content = messageStyle.Render(fmt.Sprintf("Loading Terraform state from %s…", m.dir))
	case len(m.rows) == 0 && !m.planning && !m.showErrorPane():
		content = messageStyle.Render("No resources in state or plan.\n\nPress q to quit.")
	default:
		// A failure replaces the table, keeping the status and help lines in view.
		if m.showErrorPane() {
			content = m.paneStyle(tablePane).BorderForeground(errorColor).Render(m.errPane.View())
		} else {
			content = m.paneStyle(tablePane).Render(m.table.View())
		}
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

// Package ui renders Terraform state and its planned changes as a full-screen,
// terminal-sized table.
package ui

import (
	"fmt"
	"sync"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
	"github.com/teemuteemu/tf-mankeli/pkg/ui/theme"
)

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
	t.SetStyles(theme.TableStyles())

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

// Update routes each message to the part of the model it concerns. Anything
// not handled here goes to the table, which owns cursor movement.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		return m.handleKey(msg)
	}
	return m.updateTable(msg)
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
		content = theme.Message.Render(fmt.Sprintf("Loading Terraform state from %s…", m.dir))
	case len(m.rows) == 0 && !m.planning && !m.showErrorPane():
		content = theme.Message.Render("No resources in state or plan.\n\nPress q to quit.")
	default:
		// A failure replaces the table, keeping the status and help lines in view.
		if m.showErrorPane() {
			content = m.paneStyle(tablePane).BorderForeground(theme.ErrorColor).Render(m.errPane.View())
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

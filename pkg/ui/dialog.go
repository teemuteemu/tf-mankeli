package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
	"github.com/teemuteemu/tf-mankeli/pkg/ui/detail"
	"github.com/teemuteemu/tf-mankeli/pkg/ui/theme"
)

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
		sections = append(sections, theme.Help.Render(note))
	}
	for _, c := range plan.Changes {
		if c.Action != tfstate.NoOp {
			sections = append(sections, detail.Render(tfstate.Resource{Address: c.Address, Change: &c}))
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
		theme.Help.Render("Terraform will forget this resource. The real resource is not destroyed,\n" +
			"and a later plan will offer to create it again if it is still in the configuration."),
		detail.Render(tfstate.Resource{Address: r.Address, Attributes: r.Attributes}),
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

func countChanges(changes []tfstate.Change) int {
	n := 0
	for _, c := range changes {
		if c.Action != tfstate.NoOp {
			n++
		}
	}
	return n
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

// updateConfirm handles keys while the confirmation dialog is shown.
func (m model) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Confirm):
		c := m.confirm
		m.confirm = nil
		if c.plan == nil {
			m.running = fmt.Sprintf("Removing %s from state…", c.remove)
			return m, removeFromState(m.dir, c.remove)
		}
		m.running = "Applying…"
		return m, apply(c.plan)
	case key.Matches(msg, keys.Cancel):
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
	switch {
	case key.Matches(msg, keys.Submit):
		p := m.prompt
		id := strings.TrimSpace(p.input.Value())
		// Submitting nothing would import an empty ID; keep asking instead.
		if id == "" {
			return m, nil
		}
		m.prompt = nil
		m.running = fmt.Sprintf("Importing %s…", p.address)
		return m, importResource(m.dir, p.address, id)
	case key.Matches(msg, keys.CancelInput):
		m.prompt = nil
		m.notice = "Canceled."
		return m, nil
	}
	var cmd tea.Cmd
	m.prompt.input, cmd = m.prompt.input.Update(msg)
	return m, cmd
}

// confirmView is a full-screen dialog showing every change that confirming
// would apply, with its attribute diff, in a scrollable body.
func (m model) confirmView() string {
	lines := []string{
		theme.Title.Render(m.confirm.title),
		"",
		m.confirm.body.View(),
		"",
		m.confirm.summary,
		helpLine(keys.Scroll, withDesc(keys.Confirm, m.confirm.verb), keys.Cancel),
	}
	return theme.Pane.
		BorderForeground(theme.ActiveColor).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// promptView is a dialog asking for a single value, with the text input focused.
func (m model) promptView() string {
	lines := []string{
		theme.Title.Render(m.prompt.title),
		"",
		theme.Help.Render(m.prompt.help),
		"",
		m.prompt.input.View(),
		"",
		helpLine(withDesc(keys.Submit, m.prompt.verb), keys.CancelInput),
	}
	return theme.Pane.
		BorderForeground(theme.ActiveColor).
		Padding(0, 1).
		Width(max(m.width-2, 0)).
		Render(strings.Join(lines, "\n"))
}

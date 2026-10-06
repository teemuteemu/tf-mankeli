package ui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
)

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

// actionDoneMsg carries the result of applying a plan, importing a resource
// or removing one from the state.
type actionDoneMsg struct {
	done string // notice to show on success
	err  error
}

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

// apply applies plan, streaming Terraform's output as outputMsgs before the
// final actionDoneMsg.
func apply(plan *tfstate.SavedPlan) tea.Cmd {
	ch := make(chan string, 64)
	run := func() tea.Msg {
		err := plan.Apply(context.Background(), chanWriter(ch))
		close(ch)
		return actionDoneMsg{done: "Applied.", err: err}
	}
	return tea.Batch(run, waitForOutput(ch))
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

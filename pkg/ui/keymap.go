package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
	"github.com/teemuteemu/tf-mankeli/pkg/ui/theme"
)

// keyMap is every key the UI binds. The help text lives with the key it
// describes, so the status bar can't drift from what the keys actually do:
// handleKey matches against these bindings and helpView renders them.
type keyMap struct {
	Quit     key.Binding
	Plan     key.Binding
	Apply    key.Binding
	ApplyAll key.Binding
	Remove   key.Binding
	Destroy  key.Binding
	Import   key.Binding
	Taint    key.Binding
	Error    key.Binding
	Details  key.Binding
	Focus    key.Binding
	Close    key.Binding

	// Scroll is never matched: the table and the viewports bind the arrow
	// keys themselves. It is here to be listed in the help.
	Scroll key.Binding

	// Dialog keys. Confirm and Submit are described by the action they carry
	// out, so their help text is filled in with withDesc.
	Confirm     key.Binding
	Cancel      key.Binding
	Submit      key.Binding
	CancelInput key.Binding
}

var keys = keyMap{
	Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	Plan:     key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "plan")),
	Apply:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "apply")),
	ApplyAll: key.NewBinding(key.WithKeys("A", "shift+a"), key.WithHelp("shift+a", "apply all")),
	Remove:   key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "remove from state")),
	Destroy:  key.NewBinding(key.WithKeys("D", "shift+d"), key.WithHelp("shift+d", "destroy")),
	Import:   key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "import")),
	Taint:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "taint/untaint")),
	Error:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "error")),
	Details:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "details")),
	Focus:    key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch pane")),
	Close:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
	Scroll:   key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "scroll")),

	Confirm:     key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "confirm")),
	Cancel:      key.NewBinding(key.WithKeys("n", "esc", "q", "ctrl+c"), key.WithHelp("n", "cancel")),
	Submit:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit")),
	CancelInput: key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "cancel")),
}

// actionKeys are the keys that act on the selected resource, in help order.
func (k keyMap) actionKeys() []key.Binding {
	return []key.Binding{k.Apply, k.ApplyAll, k.Remove, k.Destroy, k.Import, k.Taint}
}

// actionKeysFor are the action keys that work on r. Outputs can't be
// targeted, so only applying everything changes them.
func (k keyMap) actionKeysFor(r tfstate.Resource, ok bool) []key.Binding {
	if ok && r.IsOutput() {
		return []key.Binding{k.ApplyAll}
	}
	return k.actionKeys()
}

// withDesc copies a binding with different help text, for keys whose meaning
// depends on what is on screen: esc closes the details but dismisses an error,
// and y applies one dialog but destroys in another.
func withDesc(b key.Binding, desc string) key.Binding {
	b.SetHelp(b.Help().Key, desc)
	return b
}

// helpLine renders bindings as the "key desc • key desc" hints under the panes.
func helpLine(bindings ...key.Binding) string {
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		if h := b.Help(); b.Enabled() && h.Desc != "" {
			parts = append(parts, h.Key+" "+h.Desc)
		}
	}
	return theme.Help.Render(strings.Join(parts, " • "))
}

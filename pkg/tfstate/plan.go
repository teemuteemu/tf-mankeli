package tfstate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// Action is a planned change, written as Terraform's plan symbol.
type Action string

const (
	NoOp             Action = ""
	Create           Action = "+"
	Delete           Action = "-"
	Update           Action = "~"
	DeleteThenCreate Action = "-/+"
	CreateThenDelete Action = "+/-"
	Read             Action = "<="
	// Other is an action this app doesn't recognize, such as one added in a newer Terraform.
	Other Action = "?"
)

// Change is the planned change of a single resource.
type Change struct {
	Address  string
	Mode     string
	Type     string
	Name     string
	Module   string
	Provider string
	Action   Action
	// Before and After hold the attribute values before and after the change, with
	// sensitive values and values not known until apply replaced by placeholders.
	// Before is nil for creations and After is nil for deletions.
	Before map[string]any
	After  map[string]any
}

// SavedPlan is a plan kept on disk, so that exactly the changes it shows can
// be applied. The plan file holds sensitive values in clear text: call Apply
// or Discard once it is no longer needed.
type SavedPlan struct {
	Changes []Change

	tf  *tfexec.Terraform
	tmp string // temporary directory holding the plan file
}

// Plan runs `terraform plan` in the working directory dir and returns the
// planned change of every resource. With targets, the plan is limited to
// those resource addresses and the resources they depend on.
func Plan(ctx context.Context, dir string, targets ...string) (*SavedPlan, error) {
	return plan(ctx, dir, false, targets)
}

// PlanDestroy plans destroying the real resources at the given addresses,
// along with the resources that depend on them. Without targets, it plans
// destroying everything.
func PlanDestroy(ctx context.Context, dir string, targets ...string) (*SavedPlan, error) {
	return plan(ctx, dir, true, targets)
}

// RemoveFromState makes Terraform forget the resource at address without
// destroying the real resource, like `terraform state rm`.
func RemoveFromState(ctx context.Context, dir, address string) error {
	tf, err := newTerraform(dir)
	if err != nil {
		return err
	}
	if err := tf.StateRm(ctx, address); err != nil {
		return fmt.Errorf("removing %s from state: %w", address, err)
	}
	return nil
}

// Import brings the existing object with the given id into the state as the
// resource at address, like `terraform import`. The resource must already be
// in the configuration and must not be in the state yet. No real resource is
// created: Terraform only reads the object and records it.
func Import(ctx context.Context, dir, address, id string) error {
	tf, err := newTerraform(dir)
	if err != nil {
		return err
	}
	if err := tf.Import(ctx, address, id); err != nil {
		return fmt.Errorf("importing %s: %w", address, err)
	}
	return nil
}

// Taint marks the resource at address to be replaced on the next apply, like
// `terraform taint`. Only the state changes until then.
func Taint(ctx context.Context, dir, address string) error {
	tf, err := newTerraform(dir)
	if err != nil {
		return err
	}
	if err := tf.Taint(ctx, address); err != nil {
		return fmt.Errorf("tainting %s: %w", address, err)
	}
	return nil
}

// Untaint removes the mark Taint sets, like `terraform untaint`.
func Untaint(ctx context.Context, dir, address string) error {
	tf, err := newTerraform(dir)
	if err != nil {
		return err
	}
	if err := tf.Untaint(ctx, address); err != nil {
		return fmt.Errorf("untainting %s: %w", address, err)
	}
	return nil
}

func plan(ctx context.Context, dir string, destroy bool, targets []string) (*SavedPlan, error) {
	tf, err := newTerraform(dir)
	if err != nil {
		return nil, err
	}

	tmp, err := os.MkdirTemp("", "tf-mankeli-")
	if err != nil {
		return nil, err
	}
	p := &SavedPlan{tf: tf, tmp: tmp}

	// Don't hold the state lock while planning: it can take a while. Applying
	// takes the lock and refuses the plan if the state changed in between.
	opts := []tfexec.PlanOption{tfexec.Out(p.file()), tfexec.Lock(false), tfexec.Destroy(destroy)}
	for _, target := range targets {
		opts = append(opts, tfexec.Target(target))
	}
	if _, err := tf.Plan(ctx, opts...); err != nil {
		p.Discard()
		return nil, fmt.Errorf("planning: %w", err)
	}
	plan, err := tf.ShowPlanFile(ctx, p.file())
	if err != nil {
		p.Discard()
		return nil, fmt.Errorf("reading plan: %w", err)
	}

	p.Changes = changesOf(plan)
	return p, nil
}

// Apply applies the plan and then discards it.
func (p *SavedPlan) Apply(ctx context.Context) error {
	defer p.Discard()
	if err := p.tf.Apply(ctx, tfexec.DirOrPlan(p.file())); err != nil {
		return fmt.Errorf("applying: %w", err)
	}
	return nil
}

// Discard deletes the plan file. It is safe to call more than once, and on nil.
func (p *SavedPlan) Discard() {
	if p != nil {
		os.RemoveAll(p.tmp)
	}
}

// HasChanges reports whether applying the plan would change anything.
func (p *SavedPlan) HasChanges() bool {
	return p != nil && slices.ContainsFunc(p.Changes, func(c Change) bool { return c.Action != NoOp })
}

func (p *SavedPlan) file() string {
	return filepath.Join(p.tmp, "tfplan")
}

func changesOf(plan *tfjson.Plan) []Change {
	var changes []Change
	for _, rc := range plan.ResourceChanges {
		// Deposed objects share their address with the current object; skip them.
		if rc.Change == nil || rc.DeposedKey != "" {
			continue
		}
		after := mask(rc.Change.After, rc.Change.AfterSensitive, SensitivePlaceholder)
		changes = append(changes, Change{
			Address:  rc.Address,
			Mode:     string(rc.Mode),
			Type:     rc.Type,
			Name:     rc.Name,
			Module:   rc.ModuleAddress,
			Provider: trimProvider(rc.ProviderName),
			Action:   actionOf(rc.Change.Actions),
			Before:   toMap(mask(rc.Change.Before, rc.Change.BeforeSensitive, SensitivePlaceholder)),
			After:    toMap(markUnknown(after, rc.Change.AfterUnknown)),
		})
	}
	return changes
}

func actionOf(actions tfjson.Actions) Action {
	switch {
	case actions.NoOp():
		return NoOp
	case actions.Create():
		return Create
	case actions.Delete():
		return Delete
	case actions.Update():
		return Update
	case actions.DestroyBeforeCreate():
		return DeleteThenCreate
	case actions.CreateBeforeDestroy():
		return CreateThenDelete
	case actions.Read():
		return Read
	}
	return Other
}

// markUnknown sets the values that unknown flags to UnknownPlaceholder.
// Unlike mask, it also adds flagged keys missing from value, since the plan
// leaves values not known until apply out of After entirely.
func markUnknown(value, unknown any) any {
	switch u := unknown.(type) {
	case bool:
		if u {
			return UnknownPlaceholder
		}
	case map[string]any:
		v, _ := value.(map[string]any)
		out := make(map[string]any, len(v))
		for key, val := range v {
			out[key] = val
		}
		for key, uk := range u {
			out[key] = markUnknown(v[key], uk)
		}
		return out
	case []any:
		v, _ := value.([]any)
		out := make([]any, max(len(v), len(u)))
		copy(out, v)
		for i, ui := range u {
			var vi any
			if i < len(v) {
				vi = v[i]
			}
			out[i] = markUnknown(vi, ui)
		}
		return out
	}
	return value
}

// Merge attaches each planned change to its resource. Resources the plan will
// create, and so aren't in the state yet, are appended at the end.
func Merge(resources []Resource, changes []Change) []Resource {
	merged := slices.Clone(resources)
	index := make(map[string]int, len(merged))
	for i, r := range merged {
		index[r.Address] = i
	}
	for _, c := range changes {
		if i, ok := index[c.Address]; ok {
			merged[i].Change = &c
			continue
		}
		merged = append(merged, Resource{
			Address:  c.Address,
			Mode:     c.Mode,
			Type:     c.Type,
			Name:     c.Name,
			Module:   c.Module,
			Provider: c.Provider,
			Change:   &c,
		})
	}
	return merged
}

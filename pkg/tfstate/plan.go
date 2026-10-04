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

// Plan runs `terraform plan` in the working directory dir and returns the
// planned change of every resource.
func Plan(ctx context.Context, dir string) ([]Change, error) {
	tf, err := newTerraform(dir)
	if err != nil {
		return nil, err
	}

	// The plan file holds sensitive values in clear text, so keep it only as long as needed.
	tmp, err := os.MkdirTemp("", "tf-mankeli-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	planFile := filepath.Join(tmp, "tfplan")

	// Don't hold the state lock: this plan is only viewed and can take a while.
	if _, err := tf.Plan(ctx, tfexec.Out(planFile), tfexec.Lock(false)); err != nil {
		return nil, fmt.Errorf("planning: %w", err)
	}
	plan, err := tf.ShowPlanFile(ctx, planFile)
	if err != nil {
		return nil, fmt.Errorf("reading plan: %w", err)
	}

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
	return changes, nil
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

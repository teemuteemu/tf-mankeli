// Package tfstate reads Terraform state and plans through terraform-exec.
package tfstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// binaries are the executables tried, in order, to run Terraform.
var binaries = []string{"terraform", "tofu"}

// Placeholders that replace attribute values which can't be shown.
const (
	SensitivePlaceholder = "(sensitive)"
	UnknownPlaceholder   = "(known after apply)"
)

// Resource is a single resource or data source in the state or the plan.
type Resource struct {
	Address  string
	Mode     string // "managed" or "data"
	Type     string
	Name     string
	Module   string // empty for the root module
	Provider string
	// Attributes holds the resource's values in the state, with sensitive ones
	// replaced by SensitivePlaceholder. It is nil for resources not yet created.
	Attributes map[string]any
	// Change is the resource's planned change, or nil when no plan is attached.
	Change *Change
}

// Action returns the resource's planned action, or NoOp when no plan is attached.
func (r Resource) Action() Action {
	if r.Change == nil {
		return NoOp
	}
	return r.Change.Action
}

// Load returns every resource in the state of the Terraform working directory dir.
// The directory must already be initialized with `terraform init`.
func Load(ctx context.Context, dir string) ([]Resource, error) {
	tf, err := newTerraform(dir)
	if err != nil {
		return nil, err
	}

	state, err := tf.Show(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading state: %w", err)
	}
	// A working directory with no state yet has no values.
	if state == nil || state.Values == nil || state.Values.RootModule == nil {
		return nil, nil
	}

	var resources []Resource
	if err := collect(state.Values.RootModule, &resources); err != nil {
		return nil, err
	}
	return resources, nil
}

func newTerraform(dir string) (*tfexec.Terraform, error) {
	execPath, err := findBinary()
	if err != nil {
		return nil, err
	}
	tf, err := tfexec.NewTerraform(dir, execPath)
	if err != nil {
		return nil, fmt.Errorf("setting up %s: %w", execPath, err)
	}
	return tf, nil
}

func findBinary() (string, error) {
	for _, name := range binaries {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("neither terraform nor tofu found in PATH")
}

func trimProvider(name string) string {
	return strings.TrimPrefix(name, "registry.terraform.io/")
}

// collect appends the resources of module and all its descendants to out.
func collect(module *tfjson.StateModule, out *[]Resource) error {
	for _, r := range module.Resources {
		attributes, err := maskSensitive(r.AttributeValues, r.SensitiveValues)
		if err != nil {
			return fmt.Errorf("reading sensitive values of %s: %w", r.Address, err)
		}
		*out = append(*out, Resource{
			Address:    r.Address,
			Mode:       string(r.Mode),
			Type:       r.Type,
			Name:       r.Name,
			Module:     module.Address,
			Provider:   trimProvider(r.ProviderName),
			Attributes: attributes,
		})
	}
	for _, child := range module.ChildModules {
		if err := collect(child, out); err != nil {
			return err
		}
	}
	return nil
}

// maskSensitive replaces the values that sensitiveJSON marks as sensitive.
func maskSensitive(values map[string]any, sensitiveJSON json.RawMessage) (map[string]any, error) {
	if len(sensitiveJSON) == 0 {
		return values, nil
	}
	var sensitive any
	if err := json.Unmarshal(sensitiveJSON, &sensitive); err != nil {
		return nil, err
	}
	return toMap(mask(values, sensitive, SensitivePlaceholder)), nil
}

// mask replaces the parts of value that marks flags with placeholder.
// marks mirrors the shape of value, with true at each flagged leaf.
func mask(value, marks any, placeholder string) any {
	switch m := marks.(type) {
	case bool:
		if m {
			return placeholder
		}
	case map[string]any:
		if v, ok := value.(map[string]any); ok {
			out := make(map[string]any, len(v))
			for key, val := range v {
				out[key] = mask(val, m[key], placeholder)
			}
			return out
		}
	case []any:
		if v, ok := value.([]any); ok {
			out := make([]any, len(v))
			for i, val := range v {
				var mi any
				if i < len(m) {
					mi = m[i]
				}
				out[i] = mask(val, mi, placeholder)
			}
			return out
		}
	}
	return value
}

func toMap(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

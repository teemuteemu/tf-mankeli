// Package tfstate reads Terraform state and plans through terraform-exec.
package tfstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"
)

// binaries are the executables tried, in order, to run Terraform.
var binaries = []string{"terraform", "tofu"}

// Placeholders that replace attribute values which can't be shown.
const (
	SensitivePlaceholder = "(sensitive)"
	UnknownPlaceholder   = "(known after apply)"
)

// OutputMode is the Mode of root module outputs, which are listed alongside
// resources. An output's Attributes hold its value under OutputValueKey.
const (
	OutputMode     = "output"
	OutputValueKey = "value"
)

// Resource is a single resource, data source or root module output in the
// state or the plan.
type Resource struct {
	Address  string
	Mode     string // "managed", "data" or OutputMode
	Type     string
	Name     string
	Module   string // empty for the root module
	Provider string
	// Tainted reports whether the resource is marked to be replaced on the next apply.
	Tainted bool
	// Attributes holds the resource's values in the state, with sensitive ones
	// replaced by SensitivePlaceholder. It is nil for resources not yet created.
	Attributes map[string]any
	// Change is the resource's planned change, or nil when no plan is attached.
	Change *Change
}

// IsOutput reports whether r is a root module output rather than a resource.
func (r Resource) IsOutput() bool {
	return r.Mode == OutputMode
}

// Action returns the resource's planned action, or NoOp when no plan is attached.
func (r Resource) Action() Action {
	if r.Change == nil {
		return NoOp
	}
	return r.Change.Action
}

// Load returns every resource in the state of the Terraform working directory
// dir, followed by the root module outputs.
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
	return append(resources, outputsOf(state.Values.Outputs)...), nil
}

// outputsOf lists the root module outputs by name, masking sensitive ones.
func outputsOf(outputs map[string]*tfjson.StateOutput) []Resource {
	var out []Resource
	for _, name := range slices.Sorted(maps.Keys(outputs)) {
		o := outputs[name]
		value := o.Value
		if o.Sensitive {
			value = SensitivePlaceholder
		}
		typ := ""
		if o.Type != cty.NilType {
			typ = o.Type.FriendlyName()
		}
		out = append(out, Resource{
			Address:    outputAddress(name),
			Mode:       OutputMode,
			Type:       typ,
			Name:       name,
			Attributes: map[string]any{OutputValueKey: value},
		})
	}
	return out
}

func outputAddress(name string) string {
	return OutputMode + "." + name
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
			Tainted:    r.Tainted,
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

// Package tfstate reads Terraform state through `terraform show -json`.
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

// binaries are the executables tried, in order, to read the state.
var binaries = []string{"terraform", "tofu"}

// Resource is a single resource or data source in the state.
type Resource struct {
	Address  string
	Mode     string // "managed" or "data"
	Type     string
	Name     string
	Module   string // empty for the root module
	Provider string
	// Attributes holds the resource's values, with sensitive ones replaced by SensitivePlaceholder.
	Attributes map[string]any
}

// SensitivePlaceholder replaces sensitive attribute values.
const SensitivePlaceholder = "(sensitive)"

// Load returns every resource in the state of the Terraform working directory dir.
// The directory must already be initialized with `terraform init`.
func Load(ctx context.Context, dir string) ([]Resource, error) {
	execPath, err := findBinary()
	if err != nil {
		return nil, err
	}

	tf, err := tfexec.NewTerraform(dir, execPath)
	if err != nil {
		return nil, fmt.Errorf("setting up %s: %w", execPath, err)
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

func findBinary() (string, error) {
	for _, name := range binaries {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("neither terraform nor tofu found in PATH")
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
			Provider:   strings.TrimPrefix(r.ProviderName, "registry.terraform.io/"),
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
// sensitiveJSON mirrors the shape of values, with true at each sensitive leaf.
func maskSensitive(values map[string]any, sensitiveJSON json.RawMessage) (map[string]any, error) {
	if len(sensitiveJSON) == 0 {
		return values, nil
	}
	var sensitive any
	if err := json.Unmarshal(sensitiveJSON, &sensitive); err != nil {
		return nil, err
	}
	masked, _ := mask(values, sensitive).(map[string]any)
	return masked, nil
}

func mask(value, sensitive any) any {
	switch s := sensitive.(type) {
	case bool:
		if s {
			return SensitivePlaceholder
		}
	case map[string]any:
		if v, ok := value.(map[string]any); ok {
			out := make(map[string]any, len(v))
			for key, val := range v {
				out[key] = mask(val, s[key])
			}
			return out
		}
	case []any:
		if v, ok := value.([]any); ok {
			out := make([]any, len(v))
			for i, val := range v {
				var si any
				if i < len(s) {
					si = s[i]
				}
				out[i] = mask(val, si)
			}
			return out
		}
	}
	return value
}

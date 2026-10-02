// Package tfstate reads Terraform state through `terraform show -json`.
package tfstate

import (
	"context"
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
}

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
	collect(state.Values.RootModule, &resources)
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
func collect(module *tfjson.StateModule, out *[]Resource) {
	for _, r := range module.Resources {
		*out = append(*out, Resource{
			Address:  r.Address,
			Mode:     string(r.Mode),
			Type:     r.Type,
			Name:     r.Name,
			Module:   module.Address,
			Provider: strings.TrimPrefix(r.ProviderName, "registry.terraform.io/"),
		})
	}
	for _, child := range module.ChildModules {
		collect(child, out)
	}
}

// Package detail renders a single resource: the attributes it has now, and
// how a planned change would alter them.
package detail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/teemuteemu/tf-mankeli/pkg/tfstate"
	"github.com/teemuteemu/tf-mankeli/pkg/ui/theme"
)

// Render shows a resource's attributes, marking planned changes the way
// Terraform's plan output does.
func Render(r tfstate.Resource) string {
	action := r.Action()
	title := r.Address
	if action != tfstate.NoOp {
		title = fmt.Sprintf("%s %s will be %s", action, r.Address, theme.DescribeAction(action))
	}

	before, after := r.Attributes, r.Attributes
	if r.Change != nil && action != tfstate.NoOp {
		before, after = r.Change.Before, r.Change.After
	}

	header := theme.ActionStyle(action).Bold(true).Render(title)
	if r.Tainted {
		header += "\n" + theme.Help.Render("Tainted: it will be replaced on the next apply.")
	}
	return header + "\n\n" + strings.Join(diffLines(before, after), "\n")
}

// diffLines lists every attribute, prefixed with + if added, - if removed or
// ~ if changed. Attributes that are null both before and after are left out.
func diffLines(before, after map[string]any) []string {
	keys := make(map[string]bool, len(before)+len(after))
	keyWidth := 0
	for _, attrs := range []map[string]any{before, after} {
		for k := range attrs {
			keys[k] = true
			keyWidth = max(keyWidth, len(k))
		}
	}

	var lines []string
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		b, a := before[k], after[k]
		switch {
		case b == nil && a == nil:
			continue
		case b == nil:
			lines = append(lines, theme.ActionStyle(tfstate.Create).Render(
				fmt.Sprintf("+ %-*s = %s", keyWidth, k, formatValue(a))))
		case a == nil:
			lines = append(lines, theme.ActionStyle(tfstate.Delete).Render(
				fmt.Sprintf("- %-*s = %s", keyWidth, k, formatValue(b))))
		case reflect.DeepEqual(a, b):
			lines = append(lines, fmt.Sprintf("  %-*s = %s", keyWidth, k, formatValue(a)))
		default:
			lines = append(lines, theme.ActionStyle(tfstate.Update).Render(
				fmt.Sprintf("~ %-*s = %s → %s", keyWidth, k, formatValue(b), formatValue(a))))
		}
	}
	return lines
}

// formatValue renders a value as compact JSON, leaving placeholders unquoted.
func formatValue(v any) string {
	if s, ok := v.(string); ok && (s == tfstate.SensitivePlaceholder || s == tfstate.UnknownPlaceholder) {
		return s
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

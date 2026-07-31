// Package ui renders EnvSetup's terminal output: scan results, plans,
// and validation drift. It depends only on internal/core so it can be
// reused by every command without pulling in project or engine internals.
package ui

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

// PrintScan renders the resources found by `envsetup scan`, grouped by
// type. When verbose is true, each resource's attributes are also printed.
func PrintScan(w io.Writer, found map[string][]core.Resource, verbose bool) {
	if len(found) == 0 {
		fmt.Fprintln(w, "No providers registered yet -- nothing to scan.")
		return
	}

	total := 0
	for _, typ := range sortedTypeKeys(found) {
		resources := sortedResources(found[typ])
		if len(resources) == 0 {
			continue
		}
		total += len(resources)

		fmt.Fprintf(w, "%s (%d found)\n", typ, len(resources))
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "  ID\tCONFIDENCE\tSOURCE")
		for _, r := range resources {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", r.ID, r.Confidence, r.Provenance.Source)
		}
		tw.Flush()

		if verbose {
			for _, r := range resources {
				for _, k := range sortedAttrKeys(r.Attributes) {
					fmt.Fprintf(w, "    %s.%s: %v\n", r.ID, k, r.Attributes[k])
				}
			}
		}
		fmt.Fprintln(w)
	}

	if total == 0 {
		fmt.Fprintln(w, "No resources found.")
	}
}

// PrintPlan renders a plan/apply preview: one line per action, colorized by
// kind, followed by a create/update/delete summary.
func PrintPlan(w io.Writer, actions []core.Action) {
	if len(actions) == 0 {
		fmt.Fprintln(w, "No changes. The workstation matches the project.")
		return
	}

	var create, update, del int
	for _, a := range sortedActions(actions) {
		line := fmt.Sprintf("%s.%s: %s", a.ResourceType, a.ResourceID, a.Description)
		switch a.Kind {
		case core.ActionCreate:
			create++
			fmt.Fprintln(w, green("  + "+line))
		case core.ActionUpdate:
			update++
			fmt.Fprintln(w, yellow("  ~ "+line))
		case core.ActionDelete:
			del++
			fmt.Fprintln(w, red("  - "+line))
		}
	}

	fmt.Fprintf(w, "\nPlan: %d to create, %d to update, %d to delete.\n", create, update, del)
}

// PrintValidation renders drift found by `envsetup validate`.
func PrintValidation(w io.Writer, results []core.ValidationResult) {
	drifted := 0
	for _, r := range sortedValidationResults(results) {
		if !r.Drifted {
			continue
		}
		drifted++
		fmt.Fprintln(w, yellow(fmt.Sprintf("  ~ %s.%s: %s", r.ResourceType, r.ResourceID, r.Detail)))
	}

	if drifted == 0 {
		fmt.Fprintln(w, "No drift detected. The workstation matches the project.")
		return
	}
	fmt.Fprintf(w, "\n%d resource(s) drifted from the project.\n", drifted)
}

// PrintDoctor renders the findings from `envsetup doctor`.
func PrintDoctor(w io.Writer, diagnoses []core.Diagnosis) {
	if len(diagnoses) == 0 {
		fmt.Fprintln(w, "No issues found.")
		return
	}

	for _, d := range sortedDiagnoses(diagnoses) {
		if d.ResourceID != "" {
			fmt.Fprintln(w, yellow(fmt.Sprintf("  ! %s.%s: %s", d.ResourceType, d.ResourceID, d.Message)))
			continue
		}
		fmt.Fprintln(w, yellow(fmt.Sprintf("  ! %s: %s", d.ResourceType, d.Message)))
	}

	fmt.Fprintf(w, "\n%d issue(s) found.\n", len(diagnoses))
}

func sortedTypeKeys(found map[string][]core.Resource) []string {
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedAttrKeys(attrs map[string]any) []string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedResources(resources []core.Resource) []core.Resource {
	sorted := make([]core.Resource, len(resources))
	copy(sorted, resources)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	return sorted
}

func sortedActions(actions []core.Action) []core.Action {
	sorted := make([]core.Action, len(actions))
	copy(sorted, actions)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ResourceType != sorted[j].ResourceType {
			return sorted[i].ResourceType < sorted[j].ResourceType
		}
		return sorted[i].ResourceID < sorted[j].ResourceID
	})
	return sorted
}

func sortedValidationResults(results []core.ValidationResult) []core.ValidationResult {
	sorted := make([]core.ValidationResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ResourceType != sorted[j].ResourceType {
			return sorted[i].ResourceType < sorted[j].ResourceType
		}
		return sorted[i].ResourceID < sorted[j].ResourceID
	})
	return sorted
}

func sortedDiagnoses(diagnoses []core.Diagnosis) []core.Diagnosis {
	sorted := make([]core.Diagnosis, len(diagnoses))
	copy(sorted, diagnoses)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ResourceType != sorted[j].ResourceType {
			return sorted[i].ResourceType < sorted[j].ResourceType
		}
		return sorted[i].ResourceID < sorted[j].ResourceID
	})
	return sorted
}

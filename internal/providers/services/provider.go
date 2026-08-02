// Package services implements the "services" resource type: enabled
// systemd units, both user-scope and system-scope. Only enablement
// (persistent, boot-time configuration) is tracked -- starting or stopping
// a live service is deliberately out of scope, since running state isn't
// "configuration".
package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/sudo"
)

// scopes are the two systemd instances EnvSetup tracks enabled units for.
var scopes = []string{"user", "system"}

// Provider discovers and reconciles enabled systemd units.
type Provider struct {
	run commandRunner
}

// New returns a Provider that shells out to the real systemctl binary.
func New() *Provider {
	return &Provider{run: execCommand}
}

// newWithRunner is used by tests to inject fixture systemctl output.
func newWithRunner(run commandRunner) *Provider {
	return &Provider{run: run}
}

func (p *Provider) Type() string { return "services" }

// Discover lists enabled unit files for both scopes. A scope that fails to
// list (e.g. no systemd user session running, or system scope unavailable
// in a sandboxed environment) is treated as "nothing found" rather than a
// hard error, so one unavailable scope never blocks discovery of the
// other -- or of unrelated resource types.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	var resources []core.Resource

	for _, scope := range scopes {
		out, err := p.run(ctx, "systemctl", listArgs(scope)...)
		if err != nil {
			continue
		}

		for _, line := range splitLines(out) {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			unit := fields[0]

			resources = append(resources, core.Resource{
				Type:       p.Type(),
				ID:         resourceID(scope, unit),
				Attributes: map[string]any{"scope": scope},
				Provenance: core.Provenance{Source: "systemd", Origin: scope},
				Confidence: core.ConfidenceHigh,
			})
		}
	}

	return resources, nil
}

func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, len(resources))
	for i, r := range resources {
		out[i] = core.ProjectResource{ID: r.ID, Attributes: map[string]any{"scope": r.Attributes["scope"]}}
	}
	return out, nil
}

// Plan diffs desired enabled units against currently-enabled ones by
// presence only -- there's no partial/intermediate state to update, a unit
// is either enabled or it isn't.
func (p *Provider) Plan(ctx context.Context, desired []core.ProjectResource, current []core.Resource) ([]core.Action, error) {
	currentByID := make(map[string]core.Resource, len(current))
	for _, r := range current {
		currentByID[r.ID] = r
	}
	desiredByID := make(map[string]core.ProjectResource, len(desired))
	for _, r := range desired {
		desiredByID[r.ID] = r
	}

	var actions []core.Action
	for id, d := range desiredByID {
		if _, exists := currentByID[id]; exists {
			continue
		}
		scope, _ := d.Attributes["scope"].(string)
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: id, Kind: core.ActionCreate,
			Description: fmt.Sprintf("enable %s (%s)", unitNameFromID(id), scope),
			Attributes:  map[string]any{"scope": scope},
		})
	}
	for id, c := range currentByID {
		if _, exists := desiredByID[id]; exists {
			continue
		}
		scope, _ := c.Attributes["scope"].(string)
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: id, Kind: core.ActionDelete,
			Description: fmt.Sprintf("disable %s (%s)", unitNameFromID(id), scope),
			Attributes:  map[string]any{"scope": scope},
		})
	}

	return actions, nil
}

// Apply enables or disables the unit. System-scope changes run via sudo --
// that's the visible flag that they require elevated privileges; user
// -scope changes never do, and running an enable/disable as root for the
// wrong scope would silently operate on the wrong systemd instance.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	scope, _ := action.Attributes["scope"].(string)
	unit := unitNameFromID(action.ResourceID)

	var verb string
	switch action.Kind {
	case core.ActionCreate:
		verb = "enable"
	case core.ActionDelete:
		verb = "disable"
	default:
		return nil
	}

	if scope == "system" {
		name, args := sudo.Wrap("systemctl", verb, unit)
		_, err := p.run(ctx, name, args...)
		return err
	}
	_, err := p.run(ctx, "systemctl", "--user", verb, unit)
	return err
}

// Validate re-discovers enabled units and reports any desired unit that
// isn't currently enabled.
func (p *Provider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	current, err := p.Discover(ctx, core.SystemContext{})
	if err != nil {
		return nil, err
	}

	enabled := make(map[string]bool, len(current))
	for _, r := range current {
		enabled[r.ID] = true
	}

	results := make([]core.ValidationResult, 0, len(desired))
	for _, d := range desired {
		if enabled[d.ID] {
			results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: false})
			continue
		}
		results = append(results, core.ValidationResult{
			ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "not enabled",
		})
	}

	return results, nil
}

func listArgs(scope string) []string {
	args := []string{"list-unit-files", "--state=enabled", "--no-legend", "--plain"}
	if scope == "user" {
		return append([]string{"--user"}, args...)
	}
	return args
}

func resourceID(scope, unit string) string {
	return scope + "/" + unit
}

func unitNameFromID(id string) string {
	_, unit, found := strings.Cut(id, "/")
	if !found {
		return id
	}
	return unit
}

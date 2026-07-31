// Package flatpak implements the "flatpaks" resource type: user-scope
// Flatpak application installs, alongside pacman/AUR in the packages
// provider.
package flatpak

import (
	"context"
	"fmt"
	"strings"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

// defaultOrigin is used when a hand-authored desired entry omits the
// remote to install from. flathub is the near-universal default remote,
// mirroring how the packages provider defaults empty provenance to
// "pacman".
const defaultOrigin = "flathub"

// Provider discovers and reconciles user-scope Flatpak app installs.
type Provider struct {
	run commandRunner
}

// New returns a Provider that shells out to the real flatpak binary.
func New() *Provider {
	return &Provider{run: execCommand}
}

// newWithRunner is used by tests to inject fixture flatpak output.
func newWithRunner(run commandRunner) *Provider {
	return &Provider{run: run}
}

func (p *Provider) Type() string { return "flatpaks" }

// Discover lists installed Flatpak applications (excluding runtimes --
// dependencies, not user-facing installs, the same reasoning as packages
// only tracking explicit installs). If flatpak isn't installed at all, or
// listing fails for any reason, Discover reports nothing found rather than
// erroring, so a non-Flatpak system doesn't break scan/plan for every
// other resource type.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	out, err := p.run(ctx, "flatpak", "list", "--app", "--columns=application,origin")
	if err != nil {
		return nil, nil
	}

	var resources []core.Resource
	for _, line := range splitLines(out) {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		id, origin := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		if id == "" {
			continue
		}

		resources = append(resources, core.Resource{
			Type:       p.Type(),
			ID:         id,
			Attributes: map[string]any{"origin": origin},
			Provenance: core.Provenance{Source: "flatpak", Origin: origin},
			Confidence: core.ConfidenceHigh,
		})
	}

	return resources, nil
}

func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, len(resources))
	for i, r := range resources {
		out[i] = core.ProjectResource{ID: r.ID, Attributes: map[string]any{"origin": r.Attributes["origin"]}}
	}
	return out, nil
}

// Plan diffs desired app IDs against currently-installed ones by presence
// only -- no version pinning, same reasoning as packages.
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
		origin, _ := d.Attributes["origin"].(string)
		if origin == "" {
			origin = defaultOrigin
		}
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: id, Kind: core.ActionCreate,
			Description: fmt.Sprintf("install %s (%s)", id, origin),
			Attributes:  map[string]any{"origin": origin},
		})
	}
	for id := range currentByID {
		if _, exists := desiredByID[id]; exists {
			continue
		}
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: id, Kind: core.ActionDelete,
			Description: fmt.Sprintf("uninstall %s", id),
		})
	}

	return actions, nil
}

// Apply always targets --user scope: it never needs root or a polkit
// authentication dialog, unlike --system (flatpak's own default), which
// would make Apply inconsistent with every other provider's
// no-interactive-prompts behavior.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	switch action.Kind {
	case core.ActionCreate:
		origin, _ := action.Attributes["origin"].(string)
		if origin == "" {
			origin = defaultOrigin
		}
		_, err := p.run(ctx, "flatpak", "install", "--user", "--noninteractive", "-y", origin, action.ResourceID)
		return err

	case core.ActionDelete:
		_, err := p.run(ctx, "flatpak", "uninstall", "--user", "--noninteractive", "-y", action.ResourceID)
		return err

	default:
		return nil
	}
}

// Validate re-discovers installed apps and reports any desired app that
// isn't currently installed.
func (p *Provider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	current, err := p.Discover(ctx, core.SystemContext{})
	if err != nil {
		return nil, err
	}

	installed := make(map[string]bool, len(current))
	for _, r := range current {
		installed[r.ID] = true
	}

	results := make([]core.ValidationResult, 0, len(desired))
	for _, d := range desired {
		if installed[d.ID] {
			results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: false})
			continue
		}
		results = append(results, core.ValidationResult{
			ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "not installed",
		})
	}

	return results, nil
}

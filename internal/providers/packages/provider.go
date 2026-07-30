// Package packages implements the "packages" resource type: explicitly
// installed pacman and AUR packages. It is the first real core.Provider,
// proving the engine's Discover/Export/Plan/Apply/Validate pipeline against
// a real package manager.
package packages

import (
	"context"
	"fmt"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

// Provider discovers and reconciles explicitly-installed pacman/AUR
// packages.
type Provider struct {
	run       commandRunner
	aurHelper string // "yay", "paru", or "" if none detected
}

// New returns a Provider that shells out to the real pacman/AUR-helper
// binaries on PATH.
func New() *Provider {
	return &Provider{run: execCommand, aurHelper: detectAURHelper()}
}

// newWithRunner is used by tests to inject fixture command output instead
// of invoking real pacman/AUR-helper binaries.
func newWithRunner(run commandRunner, aurHelper string) *Provider {
	return &Provider{run: run, aurHelper: aurHelper}
}

func (p *Provider) Type() string { return "packages" }

// Discover lists explicitly-installed packages only -- dependency-only
// installs don't represent user intent. Packages pacman reports as
// "foreign" (not in the official sync databases) are heuristically tagged
// as AUR-sourced with medium confidence, since "foreign" also covers
// manually-built local packages that aren't really from the AUR. Everything
// else comes from the official repos, tagged with high confidence.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	explicitOut, err := p.run(ctx, "pacman", "-Qqe")
	if err != nil {
		return nil, fmt.Errorf("packages: listing explicitly installed packages: %w", err)
	}

	foreignOut, err := p.run(ctx, "pacman", "-Qqm")
	if err != nil {
		return nil, fmt.Errorf("packages: listing foreign packages: %w", err)
	}
	foreign := make(map[string]bool)
	for _, name := range splitLines(foreignOut) {
		foreign[name] = true
	}

	names := splitLines(explicitOut)
	resources := make([]core.Resource, 0, len(names))
	for _, name := range names {
		if foreign[name] {
			resources = append(resources, core.Resource{
				Type:       p.Type(),
				ID:         name,
				Attributes: map[string]any{"provenance": "aur"},
				Provenance: core.Provenance{Source: "aur"},
				Confidence: core.ConfidenceMedium,
			})
			continue
		}
		resources = append(resources, core.Resource{
			Type:       p.Type(),
			ID:         name,
			Attributes: map[string]any{"provenance": "pacman"},
			Provenance: core.Provenance{Source: "pacman"},
			Confidence: core.ConfidenceHigh,
		})
	}

	return resources, nil
}

func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, len(resources))
	for i, r := range resources {
		out[i] = core.ProjectResource{
			ID:         r.ID,
			Attributes: map[string]any{"provenance": r.Attributes["provenance"]},
		}
	}
	return out, nil
}

// Plan diffs desired packages against currently-installed explicit
// packages. There is no version comparison in this first pass -- presence
// is all that's tracked, avoiding the footgun of pinning exact versions in
// a reproducibility tool meant to work across time.
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
		provenance, _ := d.Attributes["provenance"].(string)
		if provenance == "" {
			provenance = "pacman"
		}
		actions = append(actions, core.Action{
			ResourceType: p.Type(),
			ResourceID:   id,
			Kind:         core.ActionCreate,
			Description:  fmt.Sprintf("install %s (%s)", id, provenance),
			Attributes:   map[string]any{"provenance": provenance},
		})
	}
	for id := range currentByID {
		if _, exists := desiredByID[id]; exists {
			continue
		}
		actions = append(actions, core.Action{
			ResourceType: p.Type(),
			ResourceID:   id,
			Kind:         core.ActionDelete,
			Description:  fmt.Sprintf("remove %s", id),
		})
	}

	return actions, nil
}

// Apply installs via the AUR helper when the package's provenance is "aur",
// otherwise via pacman directly. pacman itself needs root, so plain pacman
// calls are prefixed with sudo; AUR helpers manage their own sudo
// invocations internally and must not be run as root.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	switch action.Kind {
	case core.ActionCreate:
		provenance, _ := action.Attributes["provenance"].(string)
		if provenance == "aur" {
			if p.aurHelper == "" {
				return fmt.Errorf("packages: no AUR helper (yay or paru) found in PATH, cannot install %s", action.ResourceID)
			}
			_, err := p.run(ctx, p.aurHelper, "-S", "--noconfirm", action.ResourceID)
			return err
		}
		_, err := p.run(ctx, "sudo", "pacman", "-S", "--noconfirm", action.ResourceID)
		return err

	case core.ActionDelete:
		_, err := p.run(ctx, "sudo", "pacman", "-R", "--noconfirm", action.ResourceID)
		return err

	default:
		return nil
	}
}

// Validate re-discovers installed packages and reports any desired package
// that's missing. It never proposes removing packages the user installed
// by hand -- that's what re-running scan/export is for, not validate.
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

// Package gitrepos implements the "git_repos" resource type: git-cloned
// repositories (plugin managers, tool checkouts) found one level deep
// inside a small set of well-known container directories under $HOME.
package gitrepos

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

// Provider discovers and reconciles git-cloned repositories under homeDir.
type Provider struct {
	homeDir string
	run     commandRunner
}

// New returns a Provider that shells out to the real git binary on PATH.
func New() *Provider {
	home, _ := os.UserHomeDir()
	return &Provider{homeDir: home, run: execCommand}
}

// newWithRunner is used by tests to inject fixture command output instead
// of invoking the real git binary.
func newWithRunner(homeDir string, run commandRunner) *Provider {
	return &Provider{homeDir: homeDir, run: run}
}

func (p *Provider) Type() string { return "git_repos" }

// Discover checks each of KnownContainers for immediate subdirectories
// containing a .git directory with an "origin" remote configured.
// Subdirectories without an origin remote are skipped -- there's nowhere to
// re-clone them from, so EnvSetup can't reproduce them and won't guess.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	var resources []core.Resource

	for _, container := range KnownContainers {
		containerPath := filepath.Join(p.homeDir, container)

		entries, err := os.ReadDir(containerPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("git_repos: reading %s: %w", containerPath, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			repoPath := filepath.Join(containerPath, entry.Name())
			if info, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil || !info.IsDir() {
				continue
			}

			remoteOut, err := p.run(ctx, "git", "-C", repoPath, "remote", "get-url", "origin")
			if err != nil {
				continue
			}
			remote := strings.TrimSpace(remoteOut)

			refOut, err := p.run(ctx, "git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
			if err != nil {
				return nil, fmt.Errorf("git_repos: reading HEAD ref for %s: %w", repoPath, err)
			}
			ref := strings.TrimSpace(refOut)

			id := filepath.Join(container, entry.Name())
			resources = append(resources, core.Resource{
				Type:       p.Type(),
				ID:         id,
				Attributes: map[string]any{"remote": remote, "ref": ref},
				Provenance: core.Provenance{Source: "git", Origin: remote},
				Confidence: core.ConfidenceHigh,
			})
		}
	}

	return resources, nil
}

// Export records each repo's remote. A ref is deliberately not set by
// default -- pinning is opt-in, the user's choice, added by hand-editing
// the project's git_repos.yaml.
func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, len(resources))
	for i, r := range resources {
		out[i] = core.ProjectResource{
			ID:         r.ID,
			Attributes: map[string]any{"remote": r.Attributes["remote"]},
		}
	}
	return out, nil
}

// Plan diffs desired repos against current ones. A path that already has a
// git repo with a different remote is flagged as a conflict rather than
// auto-fixed -- Apply refuses to touch it. A path with no repo at all is
// treated as "clone missing", whether it's genuinely empty or occupied by
// something that isn't a git repo; git clone's own safety check refuses the
// latter on its own.
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
		remote, _ := d.Attributes["remote"].(string)
		c, exists := currentByID[id]

		switch {
		case !exists:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionCreate,
				Description: fmt.Sprintf("clone %s -> %s", remote, id),
				Attributes:  map[string]any{"remote": remote},
			})

		case c.Attributes["remote"] != remote:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionUpdate,
				Description: fmt.Sprintf("CONFLICT: %s already exists with remote %v, wanted %s -- resolve manually", id, c.Attributes["remote"], remote),
				Attributes:  map[string]any{"conflict": true},
			})

		default:
			desiredRef, _ := d.Attributes["ref"].(string)
			if desiredRef == "" {
				continue
			}
			currentRef, _ := c.Attributes["ref"].(string)
			if currentRef != desiredRef {
				actions = append(actions, core.Action{
					ResourceType: p.Type(), ResourceID: id, Kind: core.ActionUpdate,
					Description: fmt.Sprintf("checkout %s (currently %s)", desiredRef, currentRef),
					Attributes:  map[string]any{"ref": desiredRef},
				})
			}
		}
	}
	for id := range currentByID {
		if _, exists := desiredByID[id]; exists {
			continue
		}
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: id, Kind: core.ActionDelete,
			Description: fmt.Sprintf("remove %s", id),
		})
	}

	return actions, nil
}

// Apply clones missing repos, checks out a pinned ref, or removes a repo --
// refusing outright for a flagged conflict, and refusing to remove a repo
// with uncommitted changes rather than risk losing work.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	targetPath := filepath.Join(p.homeDir, action.ResourceID)

	switch action.Kind {
	case core.ActionCreate:
		remote, _ := action.Attributes["remote"].(string)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("git_repos: creating %s: %w", filepath.Dir(targetPath), err)
		}
		_, err := p.run(ctx, "git", "clone", remote, targetPath)
		return err

	case core.ActionUpdate:
		if conflict, _ := action.Attributes["conflict"].(bool); conflict {
			return fmt.Errorf("git_repos: %s already exists with a different remote -- resolve manually (remove it, or update the project's desired remote)", action.ResourceID)
		}
		ref, _ := action.Attributes["ref"].(string)
		_, err := p.run(ctx, "git", "-C", targetPath, "checkout", ref)
		return err

	case core.ActionDelete:
		statusOut, err := p.run(ctx, "git", "-C", targetPath, "status", "--porcelain")
		if err == nil && strings.TrimSpace(statusOut) != "" {
			return fmt.Errorf("git_repos: %s has uncommitted changes, refusing to remove automatically", action.ResourceID)
		}
		if err := os.RemoveAll(targetPath); err != nil {
			return fmt.Errorf("git_repos: removing %s: %w", targetPath, err)
		}
		return nil

	default:
		return nil
	}
}

// Validate confirms each desired repo exists at its path with a matching
// remote, and (if pinned) the expected ref checked out.
func (p *Provider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	results := make([]core.ValidationResult, 0, len(desired))

	for _, d := range desired {
		targetPath := filepath.Join(p.homeDir, d.ID)

		if info, err := os.Stat(filepath.Join(targetPath, ".git")); err != nil || !info.IsDir() {
			results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "missing"})
			continue
		}

		remoteOut, err := p.run(ctx, "git", "-C", targetPath, "remote", "get-url", "origin")
		if err != nil {
			results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "no origin remote configured"})
			continue
		}
		desiredRemote, _ := d.Attributes["remote"].(string)
		if strings.TrimSpace(remoteOut) != desiredRemote {
			results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "remote differs"})
			continue
		}

		if desiredRef, _ := d.Attributes["ref"].(string); desiredRef != "" {
			refOut, err := p.run(ctx, "git", "-C", targetPath, "rev-parse", "--abbrev-ref", "HEAD")
			if err == nil && strings.TrimSpace(refOut) != desiredRef {
				results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "ref differs"})
				continue
			}
		}

		results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: false})
	}

	return results, nil
}

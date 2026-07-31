// Package dotfiles implements the "dotfiles" resource type: tracked
// configuration files, identified by a curated allowlist of well-known
// paths under $HOME, with their actual content stored in the project's
// files/ tree (byte-for-byte, git-diffable) rather than just referenced by
// path.
package dotfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/project"
)

// defaultStrategy is used for every resource Export produces. "copy" is the
// conservative default (a symlink into the project directory is surprising
// if that directory ever moves or is deleted); users can hand-edit a
// resource's strategy to "symlink" in the project's dotfiles.yaml.
const defaultStrategy = "copy"

// Provider discovers and reconciles tracked dotfiles under homeDir.
type Provider struct {
	homeDir string
}

// New returns a Provider rooted at the current user's home directory.
func New() *Provider {
	home, _ := os.UserHomeDir()
	return &Provider{homeDir: home}
}

// newWithHome is used by tests to point Discover/Export/Apply/Validate at a
// throwaway directory instead of the real $HOME.
func newWithHome(homeDir string) *Provider {
	return &Provider{homeDir: homeDir}
}

func (p *Provider) Type() string { return "dotfiles" }

// Discover checks each of KnownPaths for existence under homeDir and reports
// the ones that exist, each carrying a content hash used for later diffing.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	var resources []core.Resource

	for _, rel := range KnownPaths {
		target := filepath.Join(p.homeDir, rel)

		info, err := os.Lstat(target)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("dotfiles: checking %s: %w", target, err)
		}
		if info.IsDir() {
			continue
		}

		content, err := os.ReadFile(target)
		if err != nil {
			return nil, fmt.Errorf("dotfiles: reading %s: %w", target, err)
		}

		resources = append(resources, core.Resource{
			Type:       p.Type(),
			ID:         rel,
			Attributes: map[string]any{"content_hash": hashContent(content)},
			Provenance: core.Provenance{Source: "local-file", Origin: target},
			Confidence: core.ConfidenceHigh,
		})
	}

	return resources, nil
}

// Export copies each discovered dotfile's live content into the project's
// files/ tree and records its hash and apply strategy.
func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, 0, len(resources))

	for _, r := range resources {
		content, err := os.ReadFile(filepath.Join(p.homeDir, r.ID))
		if err != nil {
			return nil, fmt.Errorf("dotfiles: reading %s: %w", r.ID, err)
		}

		destPath := filepath.Join(project.FilesDir(projectDir), r.ID)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return nil, fmt.Errorf("dotfiles: creating %s: %w", filepath.Dir(destPath), err)
		}
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			return nil, fmt.Errorf("dotfiles: writing %s: %w", destPath, err)
		}

		out = append(out, core.ProjectResource{
			ID: r.ID,
			Attributes: map[string]any{
				"content_hash": hashContent(content),
				"strategy":     defaultStrategy,
			},
		})
	}

	return out, nil
}

// Plan diffs desired resources against current ones by content hash only --
// it never re-reads files itself. Any hash mismatch surfaces as an update,
// whether it came from external drift or an intentional local edit; the
// existing plan-then-confirm flow in `apply` is the safeguard against
// clobbering unreviewed changes.
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
		strategy, _ := d.Attributes["strategy"].(string)
		if strategy == "" {
			strategy = defaultStrategy
		}

		c, exists := currentByID[id]
		switch {
		case !exists:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionCreate,
				Description: fmt.Sprintf("create %s (%s)", id, strategy),
				Attributes:  map[string]any{"strategy": strategy},
			})
		case c.Attributes["content_hash"] != d.Attributes["content_hash"]:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionUpdate,
				Description: fmt.Sprintf("update %s (content differs)", id),
				Attributes:  map[string]any{"strategy": strategy},
			})
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

// Apply writes or symlinks the desired content into place under homeDir,
// per the action's strategy, or removes the target for a delete.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	targetPath := filepath.Join(p.homeDir, action.ResourceID)

	switch action.Kind {
	case core.ActionCreate, core.ActionUpdate:
		strategy, _ := action.Attributes["strategy"].(string)
		if strategy == "" {
			strategy = defaultStrategy
		}
		srcPath := filepath.Join(project.FilesDir(projectDir), action.ResourceID)

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("dotfiles: creating %s: %w", filepath.Dir(targetPath), err)
		}

		if strategy == "symlink" {
			if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("dotfiles: removing existing %s: %w", targetPath, err)
			}
			if err := os.Symlink(srcPath, targetPath); err != nil {
				return fmt.Errorf("dotfiles: symlinking %s -> %s: %w", targetPath, srcPath, err)
			}
			return nil
		}

		content, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("dotfiles: reading %s: %w", srcPath, err)
		}
		if err := os.WriteFile(targetPath, content, 0o644); err != nil {
			return fmt.Errorf("dotfiles: writing %s: %w", targetPath, err)
		}
		return nil

	case core.ActionDelete:
		if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("dotfiles: removing %s: %w", targetPath, err)
		}
		return nil

	default:
		return nil
	}
}

// Validate re-hashes each desired dotfile's live content and compares it
// against the hash recorded at export time.
func (p *Provider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	results := make([]core.ValidationResult, 0, len(desired))

	for _, d := range desired {
		targetPath := filepath.Join(p.homeDir, d.ID)

		content, err := os.ReadFile(targetPath)
		if err != nil {
			if os.IsNotExist(err) {
				results = append(results, core.ValidationResult{
					ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "missing",
				})
				continue
			}
			return nil, fmt.Errorf("dotfiles: reading %s: %w", targetPath, err)
		}

		if hashContent(content) != d.Attributes["content_hash"] {
			results = append(results, core.ValidationResult{
				ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "content differs",
			})
			continue
		}

		results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: false})
	}

	return results, nil
}

func hashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Doctor reports symlinked dotfiles whose target no longer exists -- e.g.
// the project's files/ tree entry was deleted by hand, or the project
// directory itself moved without updating the symlink.
func (p *Provider) Doctor(ctx context.Context, projectDir string, desired []core.ProjectResource) ([]core.Diagnosis, error) {
	var diagnoses []core.Diagnosis

	for _, d := range desired {
		strategy, _ := d.Attributes["strategy"].(string)
		if strategy != "symlink" {
			continue
		}

		targetPath := filepath.Join(p.homeDir, d.ID)
		linkDest, err := os.Readlink(targetPath)
		if err != nil {
			// Not a symlink, or missing entirely -- Validate already
			// reports plain drift for that; doctor's job is the more
			// specific "symlink exists but points nowhere" case.
			continue
		}

		if _, statErr := os.Stat(linkDest); statErr != nil {
			diagnoses = append(diagnoses, core.Diagnosis{
				ResourceType: p.Type(), ResourceID: d.ID,
				Message: fmt.Sprintf("symlink target %s does not exist (broken symlink)", linkDest),
			})
		}
	}

	return diagnoses, nil
}

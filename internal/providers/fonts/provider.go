// Package fonts implements the "fonts" resource type: manually-installed
// font files under $HOME's user-level font directories, tracked
// byte-for-byte in the project's files/ tree, the same content-hash-based
// pattern as the dotfiles provider.
package fonts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/project"
)

// Provider discovers and reconciles manually-installed font files under
// homeDir.
type Provider struct {
	homeDir string
	run     commandRunner
}

// New returns a Provider rooted at the current user's home directory,
// shelling out to the real fc-cache binary to refresh the font cache.
func New() *Provider {
	home, _ := os.UserHomeDir()
	return &Provider{homeDir: home, run: execCommand}
}

// newWithRunner is used by tests to point Discover/Export/Apply/Validate at
// a throwaway directory and inject fixture fc-cache behavior.
func newWithRunner(homeDir string, run commandRunner) *Provider {
	return &Provider{homeDir: homeDir, run: run}
}

func (p *Provider) Type() string { return "fonts" }

// Discover recursively walks each of KnownDirs (fonts are commonly
// organized into per-family subdirectories) and reports every file with a
// recognized font extension.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	var resources []core.Resource

	for _, root := range KnownDirs {
		rootPath := filepath.Join(p.homeDir, root)

		info, err := os.Stat(rootPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("fonts: checking %s: %w", rootPath, err)
		}
		if !info.IsDir() {
			continue
		}

		walkErr := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !extensions[strings.ToLower(filepath.Ext(d.Name()))] {
				return nil
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("fonts: reading %s: %w", path, err)
			}

			rel, err := filepath.Rel(p.homeDir, path)
			if err != nil {
				return fmt.Errorf("fonts: resolving relative path for %s: %w", path, err)
			}

			resources = append(resources, core.Resource{
				Type:       p.Type(),
				ID:         rel,
				Attributes: map[string]any{"content_hash": hashContent(content)},
				Provenance: core.Provenance{Source: "local-file", Origin: path},
				Confidence: core.ConfidenceHigh,
			})
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	return resources, nil
}

// Export copies each discovered font's content into the project's files/
// tree and records its hash.
func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, 0, len(resources))

	for _, r := range resources {
		content, err := os.ReadFile(filepath.Join(p.homeDir, r.ID))
		if err != nil {
			return nil, fmt.Errorf("fonts: reading %s: %w", r.ID, err)
		}

		destPath := filepath.Join(project.FilesDir(projectDir), r.ID)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return nil, fmt.Errorf("fonts: creating %s: %w", filepath.Dir(destPath), err)
		}
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			return nil, fmt.Errorf("fonts: writing %s: %w", destPath, err)
		}

		out = append(out, core.ProjectResource{
			ID:         r.ID,
			Attributes: map[string]any{"content_hash": hashContent(content)},
		})
	}

	return out, nil
}

// Plan diffs desired resources against current ones by content hash only,
// the same as dotfiles -- no filesystem access here.
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
		c, exists := currentByID[id]
		switch {
		case !exists:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionCreate,
				Description: fmt.Sprintf("install font %s", id),
			})
		case c.Attributes["content_hash"] != d.Attributes["content_hash"]:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionUpdate,
				Description: fmt.Sprintf("update font %s (content differs)", id),
			})
		}
	}
	for id := range currentByID {
		if _, exists := desiredByID[id]; exists {
			continue
		}
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: id, Kind: core.ActionDelete,
			Description: fmt.Sprintf("remove font %s", id),
		})
	}

	return actions, nil
}

// Apply writes or removes the target font file, then refreshes the font
// cache so the change takes effect immediately.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	targetPath := filepath.Join(p.homeDir, action.ResourceID)

	switch action.Kind {
	case core.ActionCreate, core.ActionUpdate:
		srcPath := filepath.Join(project.FilesDir(projectDir), action.ResourceID)

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("fonts: creating %s: %w", filepath.Dir(targetPath), err)
		}
		content, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("fonts: reading %s: %w", srcPath, err)
		}
		if err := os.WriteFile(targetPath, content, 0o644); err != nil {
			return fmt.Errorf("fonts: writing %s: %w", targetPath, err)
		}
		return p.refreshCache(ctx)

	case core.ActionDelete:
		if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("fonts: removing %s: %w", targetPath, err)
		}
		return p.refreshCache(ctx)

	default:
		return nil
	}
}

// refreshCache rebuilds the fontconfig cache for the user-level font
// directories, so an applied change is picked up without a logout/restart.
func (p *Provider) refreshCache(ctx context.Context) error {
	args := make([]string, 0, len(KnownDirs)+1)
	args = append(args, "-f")
	for _, root := range KnownDirs {
		args = append(args, filepath.Join(p.homeDir, root))
	}
	_, err := p.run(ctx, "fc-cache", args...)
	return err
}

// Validate re-hashes each desired font's live content and compares it
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
			return nil, fmt.Errorf("fonts: reading %s: %w", targetPath, err)
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

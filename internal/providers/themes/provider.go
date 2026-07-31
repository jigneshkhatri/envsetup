// Package themes implements the "themes" resource type: GTK/icon/cursor
// themes that were manually installed (downloaded and extracted) rather
// than installed via a package -- themes already installed via a package
// are covered by the packages provider instead.
//
// Unlike fonts (one resource per file), a theme is a whole directory tree
// -- a GTK theme or icon theme can be dozens to hundreds of files -- so
// each theme is one resource: the entire tree hashed and copied as a unit,
// the same "dir kind" model the dotfiles provider uses for grouped
// .config/<app> directories.
package themes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/project"
)

// Provider discovers and reconciles manually-installed themes under
// homeDir.
type Provider struct {
	homeDir string
}

// New returns a Provider rooted at the current user's home directory.
func New() *Provider {
	home, _ := os.UserHomeDir()
	return &Provider{homeDir: home}
}

// newWithHome is used by tests to point Discover/Export/Apply/Validate at
// a throwaway directory instead of the real $HOME.
func newWithHome(homeDir string) *Provider {
	return &Provider{homeDir: homeDir}
}

func (p *Provider) Type() string { return "themes" }

// Discover treats each immediate subdirectory of a KnownContainers entry
// as a candidate theme. Confidence is deliberately Medium -- a directory
// living in the right place isn't verified to be a well-formed theme, and
// a large downloaded theme is exactly the kind of thing worth a second
// look before committing it to the project. Medium confidence means these
// go through export's existing interactive review flow.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	var resources []core.Resource

	for _, container := range KnownContainers {
		containerPath := filepath.Join(p.homeDir, container)

		entries, err := os.ReadDir(containerPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("themes: reading %s: %w", containerPath, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() || isPrivate(entry) {
				continue
			}

			themePath := filepath.Join(containerPath, entry.Name())
			files, err := walkTheme(themePath)
			if err != nil {
				return nil, fmt.Errorf("themes: walking %s: %w", themePath, err)
			}
			if len(files) == 0 {
				continue
			}

			resources = append(resources, core.Resource{
				Type: p.Type(),
				ID:   filepath.Join(container, entry.Name()),
				Attributes: map[string]any{
					"content_hash": hashTree(files),
					"file_count":   len(files),
				},
				Provenance: core.Provenance{Source: "local-file", Origin: themePath},
				Confidence: core.ConfidenceMedium,
			})
		}
	}

	return resources, nil
}

// Export copies each discovered theme's whole tree into the project's
// files/ tree and records the combined hash.
func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, 0, len(resources))

	for _, r := range resources {
		srcRoot := filepath.Join(p.homeDir, r.ID)
		files, err := walkTheme(srcRoot)
		if err != nil {
			return nil, fmt.Errorf("themes: reading %s: %w", r.ID, err)
		}

		destRoot := filepath.Join(project.FilesDir(projectDir), r.ID)
		for rel, content := range files {
			destPath := filepath.Join(destRoot, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return nil, fmt.Errorf("themes: creating %s: %w", filepath.Dir(destPath), err)
			}
			if err := os.WriteFile(destPath, content, 0o644); err != nil {
				return nil, fmt.Errorf("themes: writing %s: %w", destPath, err)
			}
		}

		out = append(out, core.ProjectResource{
			ID: r.ID,
			Attributes: map[string]any{
				"content_hash": hashTree(files),
				"file_count":   len(files),
			},
		})
	}

	return out, nil
}

// Plan diffs desired themes against current ones by their combined
// content hash only -- it never re-reads the filesystem itself.
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
				Description: fmt.Sprintf("install theme %s", id),
			})
		case c.Attributes["content_hash"] != d.Attributes["content_hash"]:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionUpdate,
				Description: fmt.Sprintf("update theme %s (content differs)", id),
			})
		}
	}
	for id := range currentByID {
		if _, exists := desiredByID[id]; exists {
			continue
		}
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: id, Kind: core.ActionDelete,
			Description: fmt.Sprintf("remove theme %s", id),
		})
	}

	return actions, nil
}

// Apply copies the whole desired tree into place, or removes it entirely
// for a delete.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	targetPath := filepath.Join(p.homeDir, action.ResourceID)

	switch action.Kind {
	case core.ActionCreate, core.ActionUpdate:
		srcPath := filepath.Join(project.FilesDir(projectDir), action.ResourceID)
		if err := copyTree(srcPath, targetPath); err != nil {
			return fmt.Errorf("themes: copying %s: %w", action.ResourceID, err)
		}
		return nil

	case core.ActionDelete:
		if err := os.RemoveAll(targetPath); err != nil {
			return fmt.Errorf("themes: removing %s: %w", targetPath, err)
		}
		return nil

	default:
		return nil
	}
}

// Validate re-hashes each desired theme's live tree and compares it
// against the hash recorded at export time.
func (p *Provider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	results := make([]core.ValidationResult, 0, len(desired))

	for _, d := range desired {
		targetPath := filepath.Join(p.homeDir, d.ID)

		info, err := os.Stat(targetPath)
		if err != nil || !info.IsDir() {
			results = append(results, core.ValidationResult{
				ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "missing",
			})
			continue
		}

		files, err := walkTheme(targetPath)
		if err != nil {
			return nil, fmt.Errorf("themes: reading %s: %w", targetPath, err)
		}
		if hashTree(files) != d.Attributes["content_hash"] {
			results = append(results, core.ValidationResult{
				ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "content differs",
			})
			continue
		}

		results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: false})
	}

	return results, nil
}

// hashTree combines every file's content into one deterministic digest,
// keyed by each file's path relative to the tree root so the hash is
// stable regardless of map/walk iteration order.
func hashTree(files map[string][]byte) string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		fileHash := sha256.Sum256(files[k])
		h.Write(fileHash[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// walkTheme recursively reads root up to maxThemeWalkDepth levels deep,
// skipping excludedThemeDirNames and anything private-mode (owner-only
// permissions -- not expected in a theme, but skipped defensively for
// consistency with the dotfiles provider). Returns a map of
// path-relative-to-root (forward-slash separated) to file content.
func walkTheme(root string) (map[string][]byte, error) {
	files := make(map[string][]byte)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				return nil
			}
			return err
		}
		if path == root {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		depth := strings.Count(rel, string(filepath.Separator)) + 1

		if d.IsDir() {
			if excludedThemeDirNames[d.Name()] || isPrivate(d) {
				return filepath.SkipDir
			}
			if depth >= maxThemeWalkDepth {
				return filepath.SkipDir
			}
			return nil
		}

		if isPrivate(d) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files rather than failing the whole walk
		}
		files[filepath.ToSlash(rel)] = content
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

// copyTree copies every file under srcRoot into destRoot, mirroring
// structure and creating directories as needed.
func copyTree(srcRoot, destRoot string) error {
	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(srcRoot, path)
		if relErr != nil {
			return relErr
		}
		destPath := filepath.Join(destRoot, rel)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0o755)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destPath, content, 0o644)
	})
}

// isPrivate reports whether an entry's permissions restrict it to the
// owner only -- see the dotfiles provider for the full rationale.
func isPrivate(d fs.DirEntry) bool {
	info, err := d.Info()
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o077 == 0
}

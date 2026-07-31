// Package dotfiles implements the "dotfiles" resource type: tracked
// configuration files under $HOME, with their actual content stored in the
// project's files/ tree (byte-for-byte, git-diffable) rather than just
// referenced by path.
//
// Discovery is a blanket scan bounded by exclusion lists (known.go) rather
// than an inclusion allowlist: every top-level $HOME dotfile, plus a
// filtered walk of each .config/<app> directory. This scales without a
// hand-maintained list of every tool's config path, but it means the
// exclusion lists carry real safety weight -- see known.go for what's
// excluded and why (credentials, history, browser/chat session state).
//
// Resources come in two shapes:
//   - "file": a single file (a top-level $HOME dotfile, or a file sitting
//     directly in .config/). High confidence -- the convention is
//     unambiguous.
//   - "dir": a whole .config/<app> directory, tracked as one resource with
//     one combined content hash, rather than one resource per file --
//     otherwise `export` would prompt once per file inside a single app's
//     config. Medium confidence -- a directory in the right place isn't
//     verified to be well-formed config.
package dotfiles

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

// defaultStrategy is used for every "file" kind resource Export produces.
// "copy" is the conservative default (a symlink into the project directory
// is surprising if that directory ever moves or is deleted); users can
// hand-edit a resource's strategy to "symlink" in the project's
// dotfiles.yaml. "dir" kind resources (whole .config/<app> trees) don't
// support a symlink strategy at all -- copy only.
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

// Discover blanket-scans top-level $HOME dotfiles and .config/*, applying
// the exclusion lists in known.go. Never modifies the system.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	var resources []core.Resource

	homeEntries, err := os.ReadDir(p.homeDir)
	if err != nil {
		return nil, fmt.Errorf("dotfiles: reading %s: %w", p.homeDir, err)
	}
	for _, entry := range homeEntries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, ".") || ExcludedHomeFiles[name] || isPrivate(entry) {
			continue
		}

		target := filepath.Join(p.homeDir, name)
		content, err := os.ReadFile(target)
		if err != nil {
			continue // e.g. permission denied -- skip rather than fail the whole scan
		}

		resources = append(resources, core.Resource{
			Type:       p.Type(),
			ID:         name,
			Attributes: map[string]any{"kind": "file", "content_hash": hashContent(content)},
			Provenance: core.Provenance{Source: "local-file", Origin: target},
			Confidence: core.ConfidenceHigh,
		})
	}

	configRoot := filepath.Join(p.homeDir, ".config")
	configEntries, err := os.ReadDir(configRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("dotfiles: reading %s: %w", configRoot, err)
	}

	for _, entry := range configEntries {
		name := entry.Name()
		id := filepath.Join(".config", name)

		if !entry.IsDir() {
			if isPrivate(entry) {
				continue
			}
			target := filepath.Join(configRoot, name)
			content, err := os.ReadFile(target)
			if err != nil {
				continue
			}
			resources = append(resources, core.Resource{
				Type:       p.Type(),
				ID:         id,
				Attributes: map[string]any{"kind": "file", "content_hash": hashContent(content)},
				Provenance: core.Provenance{Source: "local-file", Origin: target},
				Confidence: core.ConfidenceHigh,
			})
			continue
		}

		if ExcludedConfigApps[name] || isPrivate(entry) {
			continue
		}

		appRoot := filepath.Join(configRoot, name)
		files, err := walkFilteredTree(appRoot, maxConfigWalkDepth)
		if err != nil {
			return nil, fmt.Errorf("dotfiles: walking %s: %w", appRoot, err)
		}
		if len(files) == 0 {
			continue
		}

		resources = append(resources, core.Resource{
			Type: p.Type(),
			ID:   id,
			Attributes: map[string]any{
				"kind":         "dir",
				"content_hash": hashTree(files),
				"file_count":   len(files),
			},
			Provenance: core.Provenance{Source: "local-file", Origin: appRoot},
			Confidence: core.ConfidenceMedium,
		})
	}

	return resources, nil
}

// Export copies each discovered dotfile's live content into the project's
// files/ tree and records its hash (and, for "file" kind, apply strategy).
func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, 0, len(resources))

	for _, r := range resources {
		if kind, _ := r.Attributes["kind"].(string); kind == "dir" {
			srcRoot := filepath.Join(p.homeDir, r.ID)
			files, err := walkFilteredTree(srcRoot, maxConfigWalkDepth)
			if err != nil {
				return nil, fmt.Errorf("dotfiles: reading %s: %w", r.ID, err)
			}

			destRoot := filepath.Join(project.FilesDir(projectDir), r.ID)
			for rel, content := range files {
				destPath := filepath.Join(destRoot, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
					return nil, fmt.Errorf("dotfiles: creating %s: %w", filepath.Dir(destPath), err)
				}
				if err := os.WriteFile(destPath, content, 0o644); err != nil {
					return nil, fmt.Errorf("dotfiles: writing %s: %w", destPath, err)
				}
			}

			out = append(out, core.ProjectResource{
				ID: r.ID,
				Attributes: map[string]any{
					"kind":         "dir",
					"content_hash": hashTree(files),
					"file_count":   len(files),
				},
			})
			continue
		}

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
				"kind":         "file",
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
		kind, _ := d.Attributes["kind"].(string)
		if kind == "" {
			kind = "file"
		}

		attrs := map[string]any{"kind": kind}
		if kind == "file" {
			strategy, _ := d.Attributes["strategy"].(string)
			if strategy == "" {
				strategy = defaultStrategy
			}
			attrs["strategy"] = strategy
		}

		c, exists := currentByID[id]
		switch {
		case !exists:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionCreate,
				Description: fmt.Sprintf("create %s (%s)", id, kind),
				Attributes:  attrs,
			})
		case c.Attributes["content_hash"] != d.Attributes["content_hash"]:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionUpdate,
				Description: fmt.Sprintf("update %s (content differs)", id),
				Attributes:  attrs,
			})
		}
	}
	for id, c := range currentByID {
		if _, exists := desiredByID[id]; exists {
			continue
		}
		kind, _ := c.Attributes["kind"].(string)
		if kind == "" {
			kind = "file"
		}
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: id, Kind: core.ActionDelete,
			Description: fmt.Sprintf("remove %s", id),
			Attributes:  map[string]any{"kind": kind},
		})
	}

	return actions, nil
}

// Apply writes, symlinks, or copies the desired content into place under
// homeDir per the action's kind and (for "file" kind) strategy, or removes
// the target for a delete.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	targetPath := filepath.Join(p.homeDir, action.ResourceID)
	kind, _ := action.Attributes["kind"].(string)
	if kind == "" {
		kind = "file"
	}

	switch action.Kind {
	case core.ActionCreate, core.ActionUpdate:
		srcPath := filepath.Join(project.FilesDir(projectDir), action.ResourceID)

		if kind == "dir" {
			if err := copyTree(srcPath, targetPath); err != nil {
				return fmt.Errorf("dotfiles: copying %s: %w", action.ResourceID, err)
			}
			return nil
		}

		strategy, _ := action.Attributes["strategy"].(string)
		if strategy == "" {
			strategy = defaultStrategy
		}

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
		if kind == "dir" {
			if err := os.RemoveAll(targetPath); err != nil {
				return fmt.Errorf("dotfiles: removing %s: %w", targetPath, err)
			}
			return nil
		}
		if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("dotfiles: removing %s: %w", targetPath, err)
		}
		return nil

	default:
		return nil
	}
}

// Validate re-hashes each desired dotfile's live content (or, for "dir"
// kind, the whole tree) and compares it against the hash recorded at
// export time.
func (p *Provider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	results := make([]core.ValidationResult, 0, len(desired))

	for _, d := range desired {
		kind, _ := d.Attributes["kind"].(string)
		if kind == "" {
			kind = "file"
		}
		targetPath := filepath.Join(p.homeDir, d.ID)

		if kind == "dir" {
			info, err := os.Stat(targetPath)
			if err != nil || !info.IsDir() {
				results = append(results, core.ValidationResult{
					ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "missing",
				})
				continue
			}
			files, err := walkFilteredTree(targetPath, maxConfigWalkDepth)
			if err != nil {
				return nil, fmt.Errorf("dotfiles: reading %s: %w", targetPath, err)
			}
			if hashTree(files) != d.Attributes["content_hash"] {
				results = append(results, core.ValidationResult{
					ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "content differs",
				})
				continue
			}
			results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: false})
			continue
		}

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

// Doctor reports symlinked dotfiles whose target no longer exists -- e.g.
// the project's files/ tree entry was deleted by hand, or the project
// directory itself moved without updating the symlink. Only "file" kind
// resources can use the symlink strategy, so "dir" kind resources are
// naturally skipped here.
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

func hashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// isPrivate reports whether an entry's permissions restrict it to the
// owner only (no group or other access) -- a strong, general signal that
// it holds something sensitive (a credential, a session cookie, a private
// key) rather than shareable configuration. This is deliberately broader
// than the named exclusion lists: real-world verification turned up files
// like $HOME/.claude.json (an OAuth-bearing session file) and
// .config/pulse/cookie using exactly this permission pattern, which no
// fixed list would have anticipated.
func isPrivate(d fs.DirEntry) bool {
	info, err := d.Info()
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o077 == 0
}

// hashTree combines every file's content into one deterministic digest for
// a whole directory tree, keyed by each file's path relative to the tree
// root so the hash is stable across re-walks regardless of map iteration
// order.
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

// walkFilteredTree recursively reads root, up to maxDepth levels deep,
// skipping anything matching the exclusion lists in known.go. Returns a
// map of path-relative-to-root (forward-slash separated, for deterministic
// cross-platform hashing) to file content.
func walkFilteredTree(root string, maxDepth int) (map[string][]byte, error) {
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
			if excludedConfigDirNames[d.Name()] || strings.HasPrefix(d.Name(), "Cache") || isPrivate(d) {
				return filepath.SkipDir
			}
			if depth >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}

		if excludedConfigExtensions[strings.ToLower(filepath.Ext(d.Name()))] || excludedConfigFilenames[d.Name()] || isPrivate(d) {
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

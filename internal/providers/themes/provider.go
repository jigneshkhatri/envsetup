// Package themes implements the "themes" resource type: GTK/icon/cursor/
// SDDM themes that were manually installed (downloaded and extracted)
// rather than installed via a package -- themes already installed via a
// package are covered by the packages provider instead.
//
// Unlike fonts (one resource per file), a theme is a whole directory tree
// -- a GTK theme or icon theme can be dozens to hundreds of files -- so
// each theme is one resource: the entire tree hashed and copied as a unit,
// the same "dir kind" model the dotfiles provider uses for grouped
// .config/<app> directories.
//
// Themes come from two scopes: user-space (under $HOME, no privilege
// needed) and system-wide (under /usr/share, root-owned -- Apply needs
// sudo). System-wide discovery additionally filters out anything pacman
// already owns (via `pacman -Qo`), since a package-shipped theme is
// already reproducible through the packages provider; only what pacman
// doesn't know about is a real gap worth tracking here. For SDDM themes
// specifically, Discover also records whether a theme is the currently
// active one, and Apply can (re-)activate it -- otherwise reproducing the
// theme's files wouldn't actually reproduce the login screen using it.
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
// homeDir (user scope) and under SystemContainers, resolved against
// systemRoot (system scope).
type Provider struct {
	homeDir    string
	systemRoot string // "/" in production; a throwaway directory in tests
	run        commandRunner
}

// New returns a Provider rooted at the current user's home directory and
// the real filesystem root, shelling out to the real pacman/sudo binaries.
func New() *Provider {
	home, _ := os.UserHomeDir()
	return &Provider{homeDir: home, systemRoot: "/", run: execCommand}
}

// newWithHome is used by tests that only exercise user-scope behavior.
// System-scope discovery is pointed at a directory that doesn't exist (a
// fixed subdirectory of homeDir, itself always a fresh t.TempDir() in
// these tests), so it finds nothing -- never touching the real
// filesystem's /usr/share, and never needing a commandRunner.
func newWithHome(homeDir string) *Provider {
	return &Provider{homeDir: homeDir, systemRoot: filepath.Join(homeDir, ".no-system-root")}
}

// newWithRoots is used by tests exercising system-scope behavior, pointing
// both user-scope and system-scope discovery at throwaway directories
// instead of the real filesystem, with a fixture commandRunner standing in
// for pacman/sudo.
func newWithRoots(homeDir, systemRoot string, run commandRunner) *Provider {
	return &Provider{homeDir: homeDir, systemRoot: systemRoot, run: run}
}

// systemPath resolves a conceptually-absolute system path (e.g.
// "/usr/share/themes") against systemRoot -- "/" in production, giving
// back the same absolute path; a throwaway directory in tests.
func (p *Provider) systemPath(abs string) string {
	return filepath.Join(p.systemRoot, strings.TrimPrefix(abs, "/"))
}

func (p *Provider) Type() string { return "themes" }

// Discover treats each immediate subdirectory of a KnownContainers or
// SystemContainers entry as a candidate theme. Confidence is deliberately
// Medium -- a directory living in the right place isn't verified to be a
// well-formed theme, and a large downloaded theme is exactly the kind of
// thing worth a second look before committing it to the project. Medium
// confidence means these go through export's existing interactive review
// flow.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	var resources []core.Resource

	for _, container := range KnownContainers {
		containerPath := filepath.Join(p.homeDir, container)

		found, err := discoverContainer(containerPath, func(name string) string {
			return filepath.Join(container, name)
		})
		if err != nil {
			return nil, err
		}
		for _, r := range found {
			r.Attributes["scope"] = "user"
			resources = append(resources, r)
		}
	}

	active := p.activeSDDMTheme(ctx)

	for _, container := range SystemContainers {
		found, err := discoverContainer(p.systemPath(container), func(name string) string {
			return filepath.Join(container, name)
		})
		if err != nil {
			return nil, err
		}

		for _, r := range found {
			owned, err := p.pacmanOwns(ctx, r.ID)
			if err != nil {
				return nil, fmt.Errorf("themes: checking ownership of %s: %w", r.ID, err)
			}
			if owned {
				continue // already reproducible via the packages provider
			}

			r.Attributes["scope"] = "system"
			if container == sddmThemesContainer && active != "" && filepath.Base(r.ID) == active {
				r.Attributes["active"] = true
			}
			resources = append(resources, r)
		}
	}

	return resources, nil
}

// discoverContainer lists containerPath's immediate subdirectories and
// returns one candidate Resource per non-empty, non-private theme tree.
// idFor computes the resource ID for a given subdirectory name.
func discoverContainer(containerPath string, idFor func(name string) string) ([]core.Resource, error) {
	entries, err := os.ReadDir(containerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("themes: reading %s: %w", containerPath, err)
	}

	var resources []core.Resource
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
			Type: "themes",
			ID:   idFor(entry.Name()),
			Attributes: map[string]any{
				"content_hash": hashTree(files),
				"file_count":   len(files),
			},
			Provenance: core.Provenance{Source: "local-file", Origin: themePath},
			Confidence: core.ConfidenceMedium,
		})
	}
	return resources, nil
}

// resolvePath returns the live filesystem path for a resource ID: user
// -scope IDs are relative to homeDir, system-scope IDs are already
// absolute.
func (p *Provider) resolvePath(id string, attrs map[string]any) string {
	if scope, _ := attrs["scope"].(string); scope == "system" {
		return p.systemPath(id)
	}
	return filepath.Join(p.homeDir, id)
}

// Export copies each discovered theme's whole tree into the project's
// files/ tree and records the combined hash.
func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, 0, len(resources))

	for _, r := range resources {
		srcRoot := p.resolvePath(r.ID, r.Attributes)
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

		attrs := map[string]any{
			"content_hash": hashTree(files),
			"file_count":   len(files),
		}
		if scope, _ := r.Attributes["scope"].(string); scope == "system" {
			attrs["scope"] = "system"
		}
		if active, _ := r.Attributes["active"].(bool); active {
			attrs["active"] = true
		}

		out = append(out, core.ProjectResource{ID: r.ID, Attributes: attrs})
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

		scope, _ := d.Attributes["scope"].(string)
		attrs := map[string]any{"scope": scope}
		if active, _ := d.Attributes["active"].(bool); active {
			attrs["active"] = true
		}

		wantsActive, _ := d.Attributes["active"].(bool)
		isActive, _ := c.Attributes["active"].(bool)

		switch {
		case !exists:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionCreate,
				Description: fmt.Sprintf("install theme %s", id),
				Attributes:  attrs,
			})
		case c.Attributes["content_hash"] != d.Attributes["content_hash"]:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionUpdate,
				Description: fmt.Sprintf("update theme %s (content differs)", id),
				Attributes:  attrs,
			})
		case wantsActive && !isActive:
			// Content already matches -- the only thing to reconcile is
			// which theme SDDM currently has selected. Desired not
			// wanting activation is never itself a reason to act: the
			// absence of "active" isn't a request to deactivate anything.
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionUpdate,
				Description: fmt.Sprintf("activate theme %s", id),
				Attributes:  attrs,
			})
		}
	}
	for id, c := range currentByID {
		if _, exists := desiredByID[id]; exists {
			continue
		}
		scope, _ := c.Attributes["scope"].(string)
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: id, Kind: core.ActionDelete,
			Description: fmt.Sprintf("remove theme %s", id),
			Attributes:  map[string]any{"scope": scope},
		})
	}

	return actions, nil
}

// Apply copies the whole desired tree into place (via sudo for
// system-scope resources), or removes it entirely for a delete. When the
// applied resource is flagged active, it also (re-)activates it as SDDM's
// current theme.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	scope, _ := action.Attributes["scope"].(string)
	targetPath := p.resolvePath(action.ResourceID, action.Attributes)

	switch action.Kind {
	case core.ActionCreate, core.ActionUpdate:
		srcPath := filepath.Join(project.FilesDir(projectDir), action.ResourceID)

		var err error
		if scope == "system" {
			err = p.copyTreeSudo(ctx, srcPath, targetPath)
		} else {
			err = copyTree(srcPath, targetPath)
		}
		if err != nil {
			return fmt.Errorf("themes: copying %s: %w", action.ResourceID, err)
		}

		if active, _ := action.Attributes["active"].(bool); active {
			if err := p.activateSDDMTheme(ctx, filepath.Base(action.ResourceID)); err != nil {
				return fmt.Errorf("themes: activating %s: %w", action.ResourceID, err)
			}
		}
		return nil

	case core.ActionDelete:
		if scope == "system" {
			_, err := p.run(ctx, "sudo", "rm", "-rf", targetPath)
			if err != nil {
				return fmt.Errorf("themes: removing %s: %w", targetPath, err)
			}
			return nil
		}
		if err := os.RemoveAll(targetPath); err != nil {
			return fmt.Errorf("themes: removing %s: %w", targetPath, err)
		}
		return nil

	default:
		return nil
	}
}

// Validate re-hashes each desired theme's live tree and compares it
// against the hash recorded at export time. A theme flagged active is
// also checked against SDDM's actual current theme.
func (p *Provider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	var active string
	activeChecked := false

	results := make([]core.ValidationResult, 0, len(desired))
	for _, d := range desired {
		targetPath := p.resolvePath(d.ID, d.Attributes)

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

		if wantActive, _ := d.Attributes["active"].(bool); wantActive {
			if !activeChecked {
				active = p.activeSDDMTheme(ctx)
				activeChecked = true
			}
			if active != filepath.Base(d.ID) {
				results = append(results, core.ValidationResult{
					ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "not the active SDDM theme",
				})
				continue
			}
		}

		results = append(results, core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: false})
	}

	return results, nil
}

// pacmanOwns reports whether path is owned by an installed package.
// `pacman -Qo` exits non-zero with "No package owns <path>" when nothing
// does -- that failure is the signal itself, not an error condition.
func (p *Provider) pacmanOwns(ctx context.Context, path string) (bool, error) {
	_, err := p.run(ctx, "pacman", "-Qo", path)
	return err == nil, nil
}

// activeSDDMTheme returns the currently configured SDDM theme name, or ""
// if none can be determined. SDDM merges /etc/sddm.conf with every
// /etc/sddm.conf.d/*.conf fragment in lexical order, with later files
// winning -- this reads them in that same order.
func (p *Provider) activeSDDMTheme(ctx context.Context) string {
	confFile := p.systemPath(sddmConfFile)
	confDir := p.systemPath(sddmConfDir)

	var candidates []string
	if _, err := os.Stat(confFile); err == nil {
		candidates = append(candidates, confFile)
	}
	matches, _ := filepath.Glob(filepath.Join(confDir, "*.conf"))
	sort.Strings(matches)
	candidates = append(candidates, matches...)

	theme := ""
	for _, path := range candidates {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if t, ok := parseThemeCurrent(content); ok {
			theme = t
		}
	}
	return theme
}

// parseThemeCurrent extracts Current= from a [Theme] section of an SDDM
// (INI-format) config file.
func parseThemeCurrent(content []byte) (string, bool) {
	inTheme := false
	theme := ""
	found := false

	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inTheme = strings.EqualFold(strings.Trim(line, "[]"), "Theme")
			continue
		}
		if !inTheme {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok && strings.TrimSpace(key) == "Current" {
			theme = strings.TrimSpace(value)
			found = true
		}
	}

	return theme, found
}

// activateSDDMTheme selects themeName as SDDM's current theme by writing
// a dedicated, EnvSetup-owned config fragment -- never an existing
// sddm.conf.d file that might hold unrelated settings someone else wrote.
// Content is staged in a temp file first since we can't write directly to
// a root-owned path.
func (p *Provider) activateSDDMTheme(ctx context.Context, themeName string) error {
	tmp, err := os.CreateTemp("", "envsetup-sddm-theme-*.conf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(fmt.Sprintf("[Theme]\nCurrent=%s\n", themeName)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if _, err := p.run(ctx, "sudo", "mkdir", "-p", p.systemPath(sddmConfDir)); err != nil {
		return err
	}
	_, err = p.run(ctx, "sudo", "cp", tmp.Name(), p.systemPath(sddmActivationFile))
	return err
}

// copyTreeSudo replaces dest with a copy of src, entirely via sudo, for
// system-scope (root-owned) theme paths. dest is removed first so an
// update never leaves stale files behind or nests src inside an
// already-existing dest (a classic `cp -r` footgun).
func (p *Provider) copyTreeSudo(ctx context.Context, src, dest string) error {
	if _, err := p.run(ctx, "sudo", "rm", "-rf", dest); err != nil {
		return err
	}
	_, err := p.run(ctx, "sudo", "cp", "-r", src, dest)
	return err
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

// Package systemconfigs implements the "system_configs" resource type:
// system-wide (/etc) configuration files that were customized after
// install. Two independent signals feed Discover:
//
//   - Files pacman itself reports as locally modified from a package's
//     shipped default (e.g. a customized /etc/nginx/nginx.conf) -- read via
//     `pacman -Qii`, authoritative package metadata, not a heuristic.
//   - Files placed by hand inside a known "drop-in" directory (known.go's
//     KnownDropInDirs) -- a package can expect a directory like
//     /etc/sddm.conf.d to exist without ever shipping a file inside it, so
//     anything found there is by definition never in pacman's backup-file
//     list. Filtered to pacman-unowned files only, via the same ownership
//     check the themes provider uses for system-wide theme directories.
//
// Together these still only cover known locations, not a blanket /etc
// walk: most of /etc is machine-generated state (see ExcludedPaths) that
// would be actively harmful to reproduce on another machine.
package systemconfigs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/pacman"
	"github.com/jigneshkhatri/envsetup/internal/project"
	"github.com/jigneshkhatri/envsetup/internal/sudo"
)

// Provider discovers and reconciles pacman-tracked, locally-modified
// system configuration files under /etc, plus hand-placed files in known
// drop-in directories.
type Provider struct {
	run        commandRunner
	systemRoot string
}

// New returns a Provider that shells out to the real pacman/sudo binaries
// and scans the real filesystem root.
func New() *Provider {
	return &Provider{run: execCommand, systemRoot: "/"}
}

// newWithRunner is used by tests that only exercise the pacman-backup-file
// path, which never touches KnownDropInDirs. systemRoot is pointed at a
// guaranteed-nonexistent path so the drop-in scan silently finds nothing,
// rather than reading the real host filesystem.
func newWithRunner(run commandRunner) *Provider {
	return &Provider{run: run, systemRoot: "/nonexistent-envsetup-test-root"}
}

// newWithRoot is used by tests that exercise the drop-in directory scan:
// systemRoot stands in for "/", so KnownDropInDirs entries resolve under a
// throwaway test directory instead of the real /etc.
func newWithRoot(run commandRunner, systemRoot string) *Provider {
	return &Provider{run: run, systemRoot: systemRoot}
}

// systemPath resolves an absolute system path (e.g. "/etc/sddm.conf.d")
// against systemRoot -- an identity mapping in production (systemRoot is
// "/"), a test-directory redirect otherwise.
func (p *Provider) systemPath(abs string) string {
	return filepath.Join(p.systemRoot, strings.TrimPrefix(abs, "/"))
}

func (p *Provider) Type() string { return "system_configs" }

// Discover combines two passes. First, `pacman -Qii` across every
// installed package, reporting only "Backup Files" entries pacman marks
// [modified] -- and not on ExcludedPaths (known.go). A path pacman itself
// can't read (state "[unreadable]", e.g. /etc/shadow when not running as
// root) is skipped the same way: EnvSetup deliberately never runs as root
// itself, so anything requiring root just to read is out of reach here.
// Second, each of KnownDropInDirs (known.go) for hand-placed files no
// package ever shipped -- see the package doc comment.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	out, err := p.run(ctx, "pacman", "-Qii")
	if err != nil {
		return nil, fmt.Errorf("system_configs: listing package info: %w", err)
	}

	var resources []core.Resource
	for _, bf := range parseBackupFiles(out) {
		if bf.state != "modified" || ExcludedPaths[bf.path] {
			continue
		}

		content, err := os.ReadFile(bf.path)
		if err != nil {
			continue // e.g. permission denied without root -- skip rather than fail the scan
		}

		resources = append(resources, core.Resource{
			Type:       p.Type(),
			ID:         bf.path,
			Attributes: map[string]any{"content_hash": hashContent(content)},
			Provenance: core.Provenance{Source: "pacman", Origin: bf.pkg},
			Confidence: core.ConfidenceHigh,
		})
	}

	for _, dir := range KnownDropInDirs {
		entries, err := os.ReadDir(p.systemPath(dir))
		if err != nil {
			continue // directory doesn't exist on this machine -- fine
		}

		for _, entry := range entries {
			if !entry.Type().IsRegular() {
				continue // skip subdirectories (e.g. per-unit foo.service.d) and symlinks (e.g. systemctl's *.wants entries)
			}

			id := filepath.Join(dir, entry.Name())
			if ExcludedPaths[id] || pacman.Owns(ctx, p.run, id) {
				continue // package-owned -- already reproducible via the packages provider
			}

			content, err := os.ReadFile(p.systemPath(id))
			if err != nil {
				continue // e.g. permission denied without root -- skip rather than fail the scan
			}

			resources = append(resources, core.Resource{
				Type:       p.Type(),
				ID:         id,
				Attributes: map[string]any{"content_hash": hashContent(content)},
				Provenance: core.Provenance{Source: "local-file", Origin: dir},
				Confidence: core.ConfidenceHigh,
			})
		}
	}

	return resources, nil
}

// Export copies each modified config's live content into the project's
// files/ tree, mirroring its /etc path.
func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, 0, len(resources))

	for _, r := range resources {
		content, err := os.ReadFile(r.ID)
		if err != nil {
			return nil, fmt.Errorf("system_configs: reading %s: %w", r.ID, err)
		}

		destPath := filepath.Join(project.FilesDir(projectDir), r.ID)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return nil, fmt.Errorf("system_configs: creating %s: %w", filepath.Dir(destPath), err)
		}
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			return nil, fmt.Errorf("system_configs: writing %s: %w", destPath, err)
		}

		out = append(out, core.ProjectResource{
			ID:         r.ID,
			Attributes: map[string]any{"content_hash": hashContent(content)},
		})
	}

	return out, nil
}

// Plan diffs desired resources against current ones by content hash only.
// There's no "create" case in practice -- a path only becomes a resource
// once pacman reports it as an existing package's modified backup file --
// but it's handled the same as dotfiles for consistency and to cover a
// hand-authored project entry for a file that's since been reset to its
// package default.
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
				Description: fmt.Sprintf("write %s", id),
			})
		case c.Attributes["content_hash"] != d.Attributes["content_hash"]:
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: id, Kind: core.ActionUpdate,
				Description: fmt.Sprintf("update %s (content differs)", id),
			})
		}
	}
	for id := range currentByID {
		if _, exists := desiredByID[id]; exists {
			continue
		}
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: id, Kind: core.ActionDelete,
			Description: fmt.Sprintf("reset %s to package default", id),
		})
	}

	return actions, nil
}

// Apply writes the desired content to the system path via `sudo cp` --
// copy-only, no symlink strategy: less common practice and more
// surprising for a root-owned path than for a user's own dotfiles. The
// parent directory is created first: a drop-in path (e.g.
// /etc/sddm.conf.d/theme.conf) may not exist yet on a freshly-reproduced
// machine, since discovering it in the first place required the directory
// to already exist on the machine it was exported from. Delete is
// deliberately not implemented: there's no general way to "reset a file
// to its package default" other than reinstalling the package, which is
// out of scope for a simple file write.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	switch action.Kind {
	case core.ActionCreate, core.ActionUpdate:
		srcPath := filepath.Join(project.FilesDir(projectDir), action.ResourceID)

		mkdirName, mkdirArgs := sudo.Wrap("mkdir", "-p", filepath.Dir(action.ResourceID))
		if _, err := p.run(ctx, mkdirName, mkdirArgs...); err != nil {
			return err
		}

		name, args := sudo.Wrap("cp", srcPath, action.ResourceID)
		_, err := p.run(ctx, name, args...)
		return err

	default:
		return nil
	}
}

// Validate re-reads each desired config's live content and compares its
// hash against the one recorded at export time.
func (p *Provider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	results := make([]core.ValidationResult, 0, len(desired))

	for _, d := range desired {
		content, err := os.ReadFile(d.ID)
		if err != nil {
			if os.IsNotExist(err) {
				results = append(results, core.ValidationResult{
					ResourceType: p.Type(), ResourceID: d.ID, Drifted: true, Detail: "missing",
				})
				continue
			}
			return nil, fmt.Errorf("system_configs: reading %s: %w", d.ID, err)
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

// backupFile is one parsed entry from a package's "Backup Files" section.
type backupFile struct {
	pkg   string
	path  string
	state string
}

// parseBackupFiles parses `pacman -Qii` output (across one or every
// package) into its Backup Files entries. Real pacman output shape:
//
//	Name            : nginx
//	...
//	Backup Files    : /etc/nginx/nginx.conf [unmodified]
//	                  /etc/nginx/conf.d/example.conf [modified]
//	Extended Data   : pkgtype=pkg
//
// -- the first entry rides on the "Backup Files" label line itself,
// further entries are continuation lines indented to the same column with
// no label, and "Backup Files    : None" means the package declares no
// backup files at all.
func parseBackupFiles(output string) []backupFile {
	var entries []backupFile
	var currentPkg string
	inBackup := false

	for _, raw := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(raw)

		if trimmed == "" {
			inBackup = false
			continue
		}

		if rest, ok := strings.CutPrefix(trimmed, "Name"); ok {
			rest = strings.TrimSpace(rest)
			if value, ok := strings.CutPrefix(rest, ":"); ok {
				currentPkg = strings.TrimSpace(value)
				inBackup = false
				continue
			}
		}

		if rest, ok := strings.CutPrefix(trimmed, "Backup Files"); ok {
			rest = strings.TrimSpace(rest)
			rest = strings.TrimPrefix(rest, ":")
			rest = strings.TrimSpace(rest)
			inBackup = true
			if rest != "" && rest != "None" {
				if bf, ok := parseBackupEntry(rest, currentPkg); ok {
					entries = append(entries, bf)
				}
			}
			continue
		}

		if inBackup && strings.HasPrefix(trimmed, "/") {
			if bf, ok := parseBackupEntry(trimmed, currentPkg); ok {
				entries = append(entries, bf)
			}
			continue
		}

		inBackup = false
	}

	return entries
}

// parseBackupEntry parses a single "/path [state]" fragment.
func parseBackupEntry(s, pkg string) (backupFile, bool) {
	open := strings.LastIndex(s, "[")
	closeIdx := strings.LastIndex(s, "]")
	if open < 0 || closeIdx < 0 || closeIdx < open {
		return backupFile{}, false
	}

	path := strings.TrimSpace(s[:open])
	state := strings.TrimSpace(s[open+1 : closeIdx])
	if path == "" {
		return backupFile{}, false
	}

	return backupFile{pkg: pkg, path: path, state: state}, true
}

func hashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

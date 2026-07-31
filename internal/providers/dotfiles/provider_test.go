package dotfiles

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/project"
)

func writeHomeFile(t *testing.T, home, rel, content string) {
	t.Helper()
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestDiscoverBlanketScansTopLevelHomeDotfiles(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".zshrc", "export PATH=$PATH:/foo\n")
	writeHomeFile(t, home, ".config-of-my-own.conf", "not actually in .config\n")
	writeHomeFile(t, home, "not-a-dotfile.txt", "should never be picked up\n")

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	byID := make(map[string]core.Resource, len(resources))
	for _, r := range resources {
		byID[r.ID] = r
	}

	for _, id := range []string{".zshrc", ".config-of-my-own.conf"} {
		r, ok := byID[id]
		if !ok {
			t.Errorf("missing %s resource", id)
			continue
		}
		if r.Confidence != core.ConfidenceHigh {
			t.Errorf("%s confidence = %v, want high", id, r.Confidence)
		}
		if r.Attributes["kind"] != "file" {
			t.Errorf("%s kind = %v, want file", id, r.Attributes["kind"])
		}
	}
	if _, ok := byID["not-a-dotfile.txt"]; ok {
		t.Error("non-dotfile should never be discovered")
	}
}

func TestDiscoverExcludesSensitiveTopLevelHomeFiles(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".netrc", "machine example.com login me password secret\n")
	writeHomeFile(t, home, ".bash_history", "curl -H 'Authorization: Bearer secret'\n")
	writeHomeFile(t, home, ".zshrc", "safe content\n")

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	for _, r := range resources {
		if r.ID == ".netrc" || r.ID == ".bash_history" {
			t.Fatalf("sensitive file %s was discovered: %+v", r.ID, r)
		}
	}

	found := false
	for _, r := range resources {
		if r.ID == ".zshrc" {
			found = true
		}
	}
	if !found {
		t.Error(".zshrc should still be discovered")
	}
}

// TestDiscoverExcludesPrivateModeFiles locks in the permission-based
// heuristic found during real-world verification: a file only readable by
// its owner (e.g. mode 0600) is skipped regardless of name, since that
// pattern strongly signals a credential or session file (as with the real
// $HOME/.claude.json and .config/pulse/cookie found on a live desktop).
func TestDiscoverExcludesPrivateModeFiles(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".some-tool-session.json", `{"oauthToken":"secret"}`)
	if err := os.Chmod(filepath.Join(home, ".some-tool-session.json"), 0o600); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	writeHomeFile(t, home, ".zshrc", "safe content\n") // default 0644

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	byID := make(map[string]core.Resource, len(resources))
	for _, r := range resources {
		byID[r.ID] = r
	}
	if _, ok := byID[".some-tool-session.json"]; ok {
		t.Error("private-mode (0600) file should never be discovered")
	}
	if _, ok := byID[".zshrc"]; !ok {
		t.Error("world-readable .zshrc should still be discovered")
	}
}

// TestDiscoverExcludesPrivateModeDirectoriesInAppTree covers the same
// heuristic one level in: a subdirectory within an otherwise-included app
// config (e.g. a browser profile directory) that's locked down to the
// owner should be skipped, along with everything inside it, even though
// the app directory itself is included.
func TestDiscoverExcludesPrivateModeDirectoriesInAppTree(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".config/someapp/settings.json", `{"real":"config"}`)
	writeHomeFile(t, home, ".config/someapp/profile/cookies.sqlite-not-really", "session data")
	if err := os.Chmod(filepath.Join(home, ".config/someapp/profile"), 0o700); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1: %+v", len(resources), resources)
	}
	if fc := resources[0].Attributes["file_count"]; fc != 1 {
		t.Errorf("file_count = %v, want 1 (the private profile/ subdirectory should be excluded entirely)", fc)
	}
}

func TestDiscoverNeverBlanketIncludesTopLevelDirectories(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".ssh/id_rsa", "-----BEGIN OPENSSH PRIVATE KEY-----\n")
	writeHomeFile(t, home, ".gnupg/secring.gpg", "fake key material\n")

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("top-level dot-directories should never be scanned, got: %+v", resources)
	}
}

func TestDiscoverConfigDirectFiles(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".config/mimeapps.list", "[Default Applications]\n")

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 1 || resources[0].ID != ".config/mimeapps.list" {
		t.Fatalf("got %+v, want a single .config/mimeapps.list resource", resources)
	}
	if resources[0].Confidence != core.ConfidenceHigh {
		t.Errorf("confidence = %v, want high", resources[0].Confidence)
	}
}

func TestDiscoverGroupsConfigAppDirectoryAsSingleResource(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".config/waybar/config", "{}\n")
	writeHomeFile(t, home, ".config/waybar/style.css", "* {}\n")

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1 grouped resource: %+v", len(resources), resources)
	}

	r := resources[0]
	if r.ID != ".config/waybar" {
		t.Errorf("ID = %q, want .config/waybar", r.ID)
	}
	if r.Attributes["kind"] != "dir" {
		t.Errorf("kind = %v, want dir", r.Attributes["kind"])
	}
	if r.Confidence != core.ConfidenceMedium {
		t.Errorf("confidence = %v, want medium", r.Confidence)
	}
	if r.Attributes["file_count"] != 2 {
		t.Errorf("file_count = %v, want 2", r.Attributes["file_count"])
	}
}

func TestDiscoverFiltersJunkWithinAppDirectory(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".config/someapp/settings.json", `{"real":"config"}`)
	writeHomeFile(t, home, ".config/someapp/Cache/blob-data", "junk")
	writeHomeFile(t, home, ".config/someapp/state.sqlite", "junk")
	writeHomeFile(t, home, ".config/someapp/SingletonLock", "junk")
	writeHomeFile(t, home, ".config/someapp/telemetry/upload.token", "junk")

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1: %+v", len(resources), resources)
	}
	if fc := resources[0].Attributes["file_count"]; fc != 1 {
		t.Errorf("file_count = %v, want 1 (only settings.json should survive filtering)", fc)
	}
}

func TestDiscoverExcludesKnownConfigApps(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".config/discord/settings.json", "{}\n")
	writeHomeFile(t, home, ".config/google-chrome/Preferences", "{}\n")

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("excluded apps should never be discovered, got: %+v", resources)
	}
}

func TestDiscoverRespectsMaxWalkDepth(t *testing.T) {
	home := t.TempDir()
	// depth 1..4 relative to the app root should be included; deeper than
	// that should not.
	writeHomeFile(t, home, ".config/deepapp/a/b/c/shallow-enough.conf", "included\n")
	writeHomeFile(t, home, ".config/deepapp/a/b/c/d/e/too-deep.conf", "excluded\n")

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1: %+v", len(resources), resources)
	}
	if fc := resources[0].Attributes["file_count"]; fc != 1 {
		t.Errorf("file_count = %v, want 1 (the too-deep file should be excluded)", fc)
	}
}

func TestExportAndApplyRoundTripDirResource(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	writeHomeFile(t, home, ".config/waybar/config", "{}\n")
	writeHomeFile(t, home, ".config/waybar/style.css", "* {}\n")

	p := newWithHome(home)
	ctx := context.Background()

	discovered, err := p.Discover(ctx, core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	exported, err := p.Export(ctx, projectDir, discovered)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(exported) != 1 {
		t.Fatalf("got %d exported, want 1", len(exported))
	}

	for _, rel := range []string{"config", "style.css"} {
		got, err := os.ReadFile(filepath.Join(project.FilesDir(projectDir), ".config/waybar", rel))
		if err != nil {
			t.Fatalf("reading exported %s: %v", rel, err)
		}
		if len(got) == 0 {
			t.Errorf("%s exported empty", rel)
		}
	}

	// Simulate a clean reinstall: remove the live directory, then apply the
	// single create action for the whole group.
	if err := os.RemoveAll(filepath.Join(home, ".config/waybar")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	actions, err := p.Plan(ctx, exported, nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != core.ActionCreate || actions[0].Attributes["kind"] != "dir" {
		t.Fatalf("got %+v, want a single dir create action", actions)
	}

	if err := p.Apply(ctx, projectDir, actions[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(home, ".config/waybar/config"))
	if err != nil {
		t.Fatalf("reading reinstalled file: %v", err)
	}
	if string(got) != "{}\n" {
		t.Errorf("got %q", got)
	}
}

func TestApplyDirDeleteRemovesWholeTree(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".config/waybar/config", "{}\n")
	writeHomeFile(t, home, ".config/waybar/style.css", "* {}\n")

	p := newWithHome(home)
	action := core.Action{
		ResourceID: ".config/waybar", Kind: core.ActionDelete,
		Attributes: map[string]any{"kind": "dir"},
	}
	if err := p.Apply(context.Background(), t.TempDir(), action); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config/waybar")); !os.IsNotExist(err) {
		t.Error(".config/waybar should have been removed entirely")
	}
}

func TestValidateDetectsDirDrift(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".config/waybar/config", "changed\n")

	p := newWithHome(home)
	files := map[string][]byte{"config": []byte("original\n")}
	desired := []core.ProjectResource{
		{ID: ".config/waybar", Attributes: map[string]any{"kind": "dir", "content_hash": hashTree(files)}},
	}

	results, err := p.Validate(context.Background(), desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(results) != 1 || !results[0].Drifted {
		t.Fatalf("got %+v, want drifted", results)
	}
}

func TestExportCopiesContentIntoProjectFilesTree(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".zshrc", "export PATH=$PATH:/foo\n")

	p := newWithHome(home)
	ctx := context.Background()

	resources, err := p.Discover(ctx, core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	projectDir := t.TempDir()
	exported, err := p.Export(ctx, projectDir, resources)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(exported) != 1 {
		t.Fatalf("got %d exported resources, want 1", len(exported))
	}

	gotContent, err := os.ReadFile(filepath.Join(project.FilesDir(projectDir), ".zshrc"))
	if err != nil {
		t.Fatalf("reading exported file: %v", err)
	}
	if string(gotContent) != "export PATH=$PATH:/foo\n" {
		t.Errorf("exported content = %q", gotContent)
	}
	if exported[0].Attributes["strategy"] != "copy" {
		t.Errorf("strategy = %v, want copy", exported[0].Attributes["strategy"])
	}
	if exported[0].Attributes["content_hash"] != hashContent(gotContent) {
		t.Errorf("content_hash mismatch")
	}
}

func TestPlanCreateUpdateDelete(t *testing.T) {
	p := newWithHome(t.TempDir())

	desired := []core.ProjectResource{
		{ID: ".zshrc", Attributes: map[string]any{"content_hash": "same", "strategy": "copy"}},
		{ID: ".vimrc", Attributes: map[string]any{"content_hash": "new-hash", "strategy": "copy"}},
		{ID: ".gitconfig", Attributes: map[string]any{"content_hash": "want-this", "strategy": "copy"}},
	}
	current := []core.Resource{
		{ID: ".zshrc", Attributes: map[string]any{"content_hash": "same"}},
		{ID: ".gitconfig", Attributes: map[string]any{"content_hash": "have-this"}}, // drifted content
		{ID: ".tmux.conf", Attributes: map[string]any{"content_hash": "unwanted"}},  // not desired
	}

	actions, err := p.Plan(context.Background(), desired, current)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	byID := make(map[string]core.Action, len(actions))
	for _, a := range actions {
		byID[a.ResourceID] = a
	}

	if len(actions) != 3 {
		t.Fatalf("got %d actions, want 3: %+v", len(actions), actions)
	}
	if byID[".vimrc"].Kind != core.ActionCreate {
		t.Errorf(".vimrc kind = %v, want create", byID[".vimrc"].Kind)
	}
	if byID[".gitconfig"].Kind != core.ActionUpdate {
		t.Errorf(".gitconfig kind = %v, want update", byID[".gitconfig"].Kind)
	}
	if byID[".tmux.conf"].Kind != core.ActionDelete {
		t.Errorf(".tmux.conf kind = %v, want delete", byID[".tmux.conf"].Kind)
	}
	if _, exists := byID[".zshrc"]; exists {
		t.Error(".zshrc should be a noop (matching hash), got an action")
	}
}

func TestApplyCopyStrategyWritesTargetFile(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()

	destPath := filepath.Join(project.FilesDir(projectDir), ".zshrc")
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(destPath, []byte("desired content\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := newWithHome(home)
	err := p.Apply(context.Background(), projectDir, core.Action{
		ResourceID: ".zshrc", Kind: core.ActionCreate,
		Attributes: map[string]any{"strategy": "copy"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatalf("reading applied file: %v", err)
	}
	if string(got) != "desired content\n" {
		t.Errorf("got %q", got)
	}

	info, err := os.Lstat(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("expected a regular file for copy strategy, got a symlink")
	}
}

func TestApplySymlinkStrategyCreatesSymlink(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()

	destPath := filepath.Join(project.FilesDir(projectDir), ".zshrc")
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(destPath, []byte("desired content\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := newWithHome(home)
	err := p.Apply(context.Background(), projectDir, core.Action{
		ResourceID: ".zshrc", Kind: core.ActionCreate,
		Attributes: map[string]any{"strategy": "symlink"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	targetPath := filepath.Join(home, ".zshrc")
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected a symlink for symlink strategy")
	}

	link, err := os.Readlink(targetPath)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if link != destPath {
		t.Errorf("symlink points to %q, want %q", link, destPath)
	}
}

func TestApplyDeleteIsIdempotent(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".vimrc", "content\n")

	p := newWithHome(home)
	ctx := context.Background()
	action := core.Action{ResourceID: ".vimrc", Kind: core.ActionDelete}

	if err := p.Apply(ctx, t.TempDir(), action); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".vimrc")); !os.IsNotExist(err) {
		t.Fatalf(".vimrc still exists after delete")
	}

	// Deleting an already-missing target must not error.
	if err := p.Apply(ctx, t.TempDir(), action); err != nil {
		t.Fatalf("second delete (already missing): %v", err)
	}
}

func TestValidateDetectsMissingAndDrifted(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".zshrc", "unchanged\n")
	writeHomeFile(t, home, ".gitconfig", "edited on disk\n")
	// .vimrc intentionally absent.

	p := newWithHome(home)
	desired := []core.ProjectResource{
		{ID: ".zshrc", Attributes: map[string]any{"content_hash": hashContent([]byte("unchanged\n"))}},
		{ID: ".gitconfig", Attributes: map[string]any{"content_hash": hashContent([]byte("original\n"))}},
		{ID: ".vimrc", Attributes: map[string]any{"content_hash": hashContent([]byte("whatever\n"))}},
	}

	results, err := p.Validate(context.Background(), desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	byID := make(map[string]core.ValidationResult, len(results))
	for _, r := range results {
		byID[r.ResourceID] = r
	}

	if byID[".zshrc"].Drifted {
		t.Error(".zshrc should not be drifted")
	}
	if !byID[".gitconfig"].Drifted || byID[".gitconfig"].Detail != "content differs" {
		t.Errorf(".gitconfig result = %+v", byID[".gitconfig"])
	}
	if !byID[".vimrc"].Drifted || byID[".vimrc"].Detail != "missing" {
		t.Errorf(".vimrc result = %+v", byID[".vimrc"])
	}
}

// TestFullLifecycle exercises exactly the Phase 4 exit criteria: a real
// dotfile is discovered, exported, edited on disk, and the resulting drift
// is correctly detected by plan/validate and reconciled by apply.
func TestFullLifecycle(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	writeHomeFile(t, home, ".zshrc", "original content\n")

	p := newWithHome(home)
	ctx := context.Background()

	discovered, err := p.Discover(ctx, core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	exported, err := p.Export(ctx, projectDir, discovered)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Immediately after export, desired matches current: no actions.
	current, err := p.Discover(ctx, core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	actions, err := p.Plan(ctx, exported, current)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("Plan after export: got %d actions, want 0: %+v", len(actions), actions)
	}

	// Edit the file on disk, simulating drift.
	writeHomeFile(t, home, ".zshrc", "edited content\n")

	current, err = p.Discover(ctx, core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	actions, err = p.Plan(ctx, exported, current)
	if err != nil {
		t.Fatalf("Plan after edit: %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != core.ActionUpdate {
		t.Fatalf("Plan after edit: got %+v, want a single update action", actions)
	}

	results, err := p.Validate(ctx, exported)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(results) != 1 || !results[0].Drifted {
		t.Fatalf("Validate after edit: got %+v, want drifted", results)
	}

	// Apply reconciles the target back to the desired (originally exported)
	// content.
	if err := p.Apply(ctx, projectDir, actions[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	results, err = p.Validate(ctx, exported)
	if err != nil {
		t.Fatalf("Validate after apply: %v", err)
	}
	if len(results) != 1 || results[0].Drifted {
		t.Fatalf("Validate after apply: got %+v, want no drift", results)
	}

	got, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatalf("reading reconciled file: %v", err)
	}
	if string(got) != "original content\n" {
		t.Errorf("reconciled content = %q, want original content", got)
	}
}

func TestDoctorReportsBrokenSymlinkOnly(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	p := newWithHome(home)
	ctx := context.Background()

	// A healthy symlink pointing at a real files/ entry.
	okDest := filepath.Join(project.FilesDir(projectDir), "ok")
	if err := os.MkdirAll(filepath.Dir(okDest), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(okDest, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink(okDest, filepath.Join(home, "ok")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	// A broken symlink pointing at a files/ entry that doesn't exist.
	brokenDest := filepath.Join(project.FilesDir(projectDir), "broken")
	if err := os.Symlink(brokenDest, filepath.Join(home, "broken")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	// A copy-strategy resource, which can never have a broken symlink.
	writeHomeFile(t, home, "copied", "content")

	desired := []core.ProjectResource{
		{ID: "ok", Attributes: map[string]any{"strategy": "symlink"}},
		{ID: "broken", Attributes: map[string]any{"strategy": "symlink"}},
		{ID: "copied", Attributes: map[string]any{"strategy": "copy"}},
	}

	diagnoses, err := p.Doctor(ctx, projectDir, desired)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(diagnoses) != 1 || diagnoses[0].ResourceID != "broken" {
		t.Fatalf("got %+v, want a single diagnosis for \"broken\"", diagnoses)
	}
}

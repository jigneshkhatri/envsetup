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

func TestDiscoverFindsOnlyExistingKnownFiles(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".zshrc", "export PATH=$PATH:/foo\n")
	writeHomeFile(t, home, ".config/nvim/init.lua", "-- nvim config\n")
	// Everything else in KnownPaths is deliberately left absent.

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("got %d resources, want 2: %+v", len(resources), resources)
	}

	byID := make(map[string]core.Resource, len(resources))
	for _, r := range resources {
		byID[r.ID] = r
	}
	if _, ok := byID[".zshrc"]; !ok {
		t.Error("missing .zshrc resource")
	}
	if r := byID[".config/nvim/init.lua"]; r.Confidence != core.ConfidenceHigh {
		t.Errorf("confidence = %v, want high", r.Confidence)
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

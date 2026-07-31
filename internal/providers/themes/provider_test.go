package themes

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

func TestDiscoverGroupsThemeAsSingleResource(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".local/share/themes/Nordic/gtk-3.0/gtk.css", "* {}\n")
	writeHomeFile(t, home, ".local/share/themes/Nordic/gtk-4.0/gtk.css", "* {}\n")
	writeHomeFile(t, home, ".local/share/themes/Nordic/index.theme", "[Desktop Entry]\n")

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1: %+v", len(resources), resources)
	}

	r := resources[0]
	if r.ID != filepath.Join(".local/share/themes", "Nordic") {
		t.Errorf("ID = %q", r.ID)
	}
	if r.Confidence != core.ConfidenceMedium {
		t.Errorf("confidence = %v, want medium", r.Confidence)
	}
	if r.Attributes["file_count"] != 3 {
		t.Errorf("file_count = %v, want 3", r.Attributes["file_count"])
	}
}

func TestDiscoverScansAllKnownContainers(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".local/share/icons/Papirus/index.theme", "[Icon Theme]\n")
	writeHomeFile(t, home, ".icons/Breeze/cursor.theme", "[cursor]\n")
	writeHomeFile(t, home, ".themes/Arc/gtk-3.0/gtk.css", "* {}\n")

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 3 {
		t.Fatalf("got %d resources, want 3: %+v", len(resources), resources)
	}
}

func TestDiscoverSkipsEmptyAndPrivateThemeDirs(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local/share/themes/Empty"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeHomeFile(t, home, ".local/share/themes/Locked/index.theme", "[Desktop Entry]\n")
	if err := os.Chmod(filepath.Join(home, ".local/share/themes/Locked"), 0o700); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	p := newWithHome(home)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("got %d resources, want 0 (empty and private dirs should both be skipped): %+v", len(resources), resources)
	}
}

func TestExportAndApplyRoundTrip(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	writeHomeFile(t, home, ".local/share/themes/Nordic/index.theme", "[Desktop Entry]\n")
	writeHomeFile(t, home, ".local/share/themes/Nordic/gtk-3.0/gtk.css", "* { color: red; }\n")

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

	got, err := os.ReadFile(filepath.Join(project.FilesDir(projectDir), ".local/share/themes/Nordic/gtk-3.0/gtk.css"))
	if err != nil {
		t.Fatalf("reading exported file: %v", err)
	}
	if string(got) != "* { color: red; }\n" {
		t.Errorf("got %q", got)
	}

	// Simulate a clean reinstall.
	if err := os.RemoveAll(filepath.Join(home, ".local/share/themes/Nordic")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	actions, err := p.Plan(ctx, exported, nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != core.ActionCreate {
		t.Fatalf("got %+v, want a single create action", actions)
	}

	if err := p.Apply(ctx, projectDir, actions[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err = os.ReadFile(filepath.Join(home, ".local/share/themes/Nordic/gtk-3.0/gtk.css"))
	if err != nil {
		t.Fatalf("reading reinstalled file: %v", err)
	}
	if string(got) != "* { color: red; }\n" {
		t.Errorf("got %q", got)
	}
}

func TestPlanDetectsUpdateAndDelete(t *testing.T) {
	p := newWithHome(t.TempDir())

	desired := []core.ProjectResource{
		{ID: ".themes/Arc", Attributes: map[string]any{"content_hash": "want-this"}},
	}
	current := []core.Resource{
		{ID: ".themes/Arc", Attributes: map[string]any{"content_hash": "have-this"}},
		{ID: ".themes/Unwanted", Attributes: map[string]any{"content_hash": "x"}},
	}

	actions, err := p.Plan(context.Background(), desired, current)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 2 {
		t.Fatalf("got %d actions, want 2: %+v", len(actions), actions)
	}

	byID := make(map[string]core.Action, len(actions))
	for _, a := range actions {
		byID[a.ResourceID] = a
	}
	if byID[".themes/Arc"].Kind != core.ActionUpdate {
		t.Errorf("Arc kind = %v, want update", byID[".themes/Arc"].Kind)
	}
	if byID[".themes/Unwanted"].Kind != core.ActionDelete {
		t.Errorf("Unwanted kind = %v, want delete", byID[".themes/Unwanted"].Kind)
	}
}

func TestApplyDeleteRemovesWholeTree(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".themes/Arc/gtk-3.0/gtk.css", "* {}\n")
	writeHomeFile(t, home, ".themes/Arc/index.theme", "[Desktop Entry]\n")

	p := newWithHome(home)
	action := core.Action{ResourceID: ".themes/Arc", Kind: core.ActionDelete}
	if err := p.Apply(context.Background(), t.TempDir(), action); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".themes/Arc")); !os.IsNotExist(err) {
		t.Error(".themes/Arc should have been removed entirely")
	}
}

func TestValidateDetectsMissingAndDrifted(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".themes/Arc/gtk.css", "changed\n")

	p := newWithHome(home)
	files := map[string][]byte{"gtk.css": []byte("original\n")}
	desired := []core.ProjectResource{
		{ID: ".themes/Arc", Attributes: map[string]any{"content_hash": hashTree(files)}},
		{ID: ".themes/Missing", Attributes: map[string]any{"content_hash": "whatever"}},
	}

	results, err := p.Validate(context.Background(), desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	byID := make(map[string]core.ValidationResult, len(results))
	for _, r := range results {
		byID[r.ResourceID] = r
	}
	if !byID[".themes/Arc"].Drifted || byID[".themes/Arc"].Detail != "content differs" {
		t.Errorf("Arc result = %+v", byID[".themes/Arc"])
	}
	if !byID[".themes/Missing"].Drifted || byID[".themes/Missing"].Detail != "missing" {
		t.Errorf("Missing result = %+v", byID[".themes/Missing"])
	}
}

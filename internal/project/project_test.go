package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	p := New(dir, "test-project")
	p.SetResourcesFor("package", []core.ProjectResource{
		{ID: "neovim", Attributes: map[string]any{"provenance": "pacman"}},
		{ID: "yay", Attributes: map[string]any{"provenance": "aur"}},
	})

	if err := p.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if reloaded.Manifest.Name != "test-project" {
		t.Errorf("Manifest.Name = %q, want %q", reloaded.Manifest.Name, "test-project")
	}
	if reloaded.Manifest.Version != schemaVersion {
		t.Errorf("Manifest.Version = %d, want %d", reloaded.Manifest.Version, schemaVersion)
	}

	packages := reloaded.ResourcesFor("package")
	if len(packages) != 2 {
		t.Fatalf("got %d packages, want 2", len(packages))
	}

	byID := make(map[string]core.ProjectResource, len(packages))
	for _, r := range packages {
		byID[r.ID] = r
	}

	if got := byID["neovim"].Attributes["provenance"]; got != "pacman" {
		t.Errorf("neovim provenance = %v, want pacman", got)
	}
	if got := byID["yay"].Attributes["provenance"]; got != "aur" {
		t.Errorf("yay provenance = %v, want aur", got)
	}
}

func TestLoadProjectWithNoResourcesYet(t *testing.T) {
	// Simulates a project right after `init`: a manifest exists, but no
	// resources/ dir has been written yet (no export has run).
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, manifestFileName)
	if err := os.WriteFile(manifestPath, []byte("name: fresh\nversion: 1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := p.Types(); len(got) != 0 {
		t.Errorf("Types() = %v, want empty", got)
	}
}

func TestLoadMissingManifest(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err == nil {
		t.Fatal("Load: expected error for missing manifest, got nil")
	}
}

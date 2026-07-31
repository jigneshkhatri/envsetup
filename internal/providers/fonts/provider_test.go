package fonts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/project"
)

type fakeCall struct {
	name string
	args []string
}

type fakeRunner struct {
	calls []fakeCall
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, fakeCall{name: name, args: args})
	return "", nil
}

func writeHomeFile(t *testing.T, home, rel string, content string) {
	t.Helper()
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestDiscoverFindsFontFilesRecursivelySkippingOthers(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".local/share/fonts/JetBrainsMono/Regular.ttf", "ttf-bytes")
	writeHomeFile(t, home, ".local/share/fonts/JetBrainsMono/README.md", "not a font")
	writeHomeFile(t, home, ".fonts/Custom.otf", "otf-bytes")

	p := newWithRunner(home, (&fakeRunner{}).run)
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

	ttfID := filepath.Join(".local/share/fonts/JetBrainsMono", "Regular.ttf")
	if r, ok := byID[ttfID]; !ok {
		t.Errorf("missing %s", ttfID)
	} else if r.Confidence != core.ConfidenceHigh {
		t.Errorf("confidence = %v, want high", r.Confidence)
	}

	otfID := filepath.Join(".fonts", "Custom.otf")
	if _, ok := byID[otfID]; !ok {
		t.Errorf("missing %s", otfID)
	}
}

func TestExportCopiesContentIntoProjectFilesTree(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".fonts/Custom.otf", "otf-bytes")

	p := newWithRunner(home, (&fakeRunner{}).run)
	ctx := context.Background()

	discovered, err := p.Discover(ctx, core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	projectDir := t.TempDir()
	exported, err := p.Export(ctx, projectDir, discovered)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(exported) != 1 {
		t.Fatalf("got %d exported, want 1", len(exported))
	}

	got, err := os.ReadFile(filepath.Join(project.FilesDir(projectDir), ".fonts", "Custom.otf"))
	if err != nil {
		t.Fatalf("reading exported file: %v", err)
	}
	if string(got) != "otf-bytes" {
		t.Errorf("got %q", got)
	}
	if exported[0].Attributes["content_hash"] != hashContent(got) {
		t.Error("content_hash mismatch")
	}
}

func TestPlanCreateUpdateDelete(t *testing.T) {
	p := newWithRunner(t.TempDir(), (&fakeRunner{}).run)

	desired := []core.ProjectResource{
		{ID: "a.ttf", Attributes: map[string]any{"content_hash": "same"}},
		{ID: "b.ttf", Attributes: map[string]any{"content_hash": "new"}},
		{ID: "c.ttf", Attributes: map[string]any{"content_hash": "want"}},
	}
	current := []core.Resource{
		{ID: "a.ttf", Attributes: map[string]any{"content_hash": "same"}},
		{ID: "c.ttf", Attributes: map[string]any{"content_hash": "have"}},
		{ID: "d.ttf", Attributes: map[string]any{"content_hash": "unwanted"}},
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
	if byID["b.ttf"].Kind != core.ActionCreate {
		t.Errorf("b.ttf kind = %v, want create", byID["b.ttf"].Kind)
	}
	if byID["c.ttf"].Kind != core.ActionUpdate {
		t.Errorf("c.ttf kind = %v, want update", byID["c.ttf"].Kind)
	}
	if byID["d.ttf"].Kind != core.ActionDelete {
		t.Errorf("d.ttf kind = %v, want delete", byID["d.ttf"].Kind)
	}
	if _, exists := byID["a.ttf"]; exists {
		t.Error("a.ttf should be a noop")
	}
}

func TestApplyCreateWritesFileAndRefreshesCache(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()

	destPath := filepath.Join(project.FilesDir(projectDir), "Custom.ttf")
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(destPath, []byte("font-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fr := &fakeRunner{}
	p := newWithRunner(home, fr.run)

	err := p.Apply(context.Background(), projectDir, core.Action{ResourceID: "Custom.ttf", Kind: core.ActionCreate})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(home, "Custom.ttf"))
	if err != nil {
		t.Fatalf("reading applied file: %v", err)
	}
	if string(got) != "font-bytes" {
		t.Errorf("got %q", got)
	}

	if len(fr.calls) != 1 || fr.calls[0].name != "fc-cache" {
		t.Fatalf("expected one fc-cache call, got %+v", fr.calls)
	}
	wantArgs := []string{"-f", filepath.Join(home, ".local/share/fonts"), filepath.Join(home, ".fonts")}
	if strings.Join(fr.calls[0].args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("fc-cache args = %v, want %v", fr.calls[0].args, wantArgs)
	}
}

func TestApplyDeleteRemovesFileAndRefreshesCache(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "Custom.ttf", "font-bytes")

	fr := &fakeRunner{}
	p := newWithRunner(home, fr.run)

	err := p.Apply(context.Background(), t.TempDir(), core.Action{ResourceID: "Custom.ttf", Kind: core.ActionDelete})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Custom.ttf")); !os.IsNotExist(err) {
		t.Error("font file should have been removed")
	}
	if len(fr.calls) != 1 || fr.calls[0].name != "fc-cache" {
		t.Fatalf("expected one fc-cache call, got %+v", fr.calls)
	}
}

func TestApplyDeleteIsIdempotent(t *testing.T) {
	p := newWithRunner(t.TempDir(), (&fakeRunner{}).run)
	action := core.Action{ResourceID: "missing.ttf", Kind: core.ActionDelete}
	if err := p.Apply(context.Background(), t.TempDir(), action); err != nil {
		t.Fatalf("Apply on already-missing font: %v", err)
	}
}

func TestValidateDetectsMissingAndDrifted(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, "unchanged.ttf", "unchanged")
	writeHomeFile(t, home, "edited.ttf", "edited on disk")

	p := newWithRunner(home, (&fakeRunner{}).run)
	desired := []core.ProjectResource{
		{ID: "unchanged.ttf", Attributes: map[string]any{"content_hash": hashContent([]byte("unchanged"))}},
		{ID: "edited.ttf", Attributes: map[string]any{"content_hash": hashContent([]byte("original"))}},
		{ID: "missing.ttf", Attributes: map[string]any{"content_hash": hashContent([]byte("whatever"))}},
	}

	results, err := p.Validate(context.Background(), desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	byID := make(map[string]core.ValidationResult, len(results))
	for _, r := range results {
		byID[r.ResourceID] = r
	}
	if byID["unchanged.ttf"].Drifted {
		t.Error("unchanged.ttf should not be drifted")
	}
	if !byID["edited.ttf"].Drifted || byID["edited.ttf"].Detail != "content differs" {
		t.Errorf("edited.ttf result = %+v", byID["edited.ttf"])
	}
	if !byID["missing.ttf"].Drifted || byID["missing.ttf"].Detail != "missing" {
		t.Errorf("missing.ttf result = %+v", byID["missing.ttf"])
	}
}

// TestFullLifecycle mirrors the Phase 6 exit criteria: a manually-installed
// font is discovered, exported, and correctly reinstalled (including cache
// rebuild) after drift.
func TestFullLifecycle(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	writeHomeFile(t, home, ".local/share/fonts/Custom.ttf", "original bytes")

	fr := &fakeRunner{}
	p := newWithRunner(home, fr.run)
	ctx := context.Background()

	discovered, err := p.Discover(ctx, core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	exported, err := p.Export(ctx, projectDir, discovered)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	current, err := p.Discover(ctx, core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if actions, err := p.Plan(ctx, exported, current); err != nil || len(actions) != 0 {
		t.Fatalf("Plan after export: actions=%+v err=%v", actions, err)
	}

	// Simulate the font file being deleted (e.g. accidentally removed).
	if err := os.Remove(filepath.Join(home, ".local/share/fonts/Custom.ttf")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	current, err = p.Discover(ctx, core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	actions, err := p.Plan(ctx, exported, current)
	if err != nil {
		t.Fatalf("Plan after delete: %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != core.ActionCreate {
		t.Fatalf("Plan after delete: got %+v, want a single create action", actions)
	}

	if err := p.Apply(ctx, projectDir, actions[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(fr.calls) != 1 || fr.calls[0].name != "fc-cache" {
		t.Fatalf("expected fc-cache to run after reinstalling, got %+v", fr.calls)
	}

	results, err := p.Validate(ctx, exported)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(results) != 1 || results[0].Drifted {
		t.Fatalf("Validate after apply: got %+v, want no drift", results)
	}

	got, err := os.ReadFile(filepath.Join(home, ".local/share/fonts/Custom.ttf"))
	if err != nil {
		t.Fatalf("reading reinstalled font: %v", err)
	}
	if string(got) != "original bytes" {
		t.Errorf("reinstalled content = %q, want original bytes", got)
	}
}

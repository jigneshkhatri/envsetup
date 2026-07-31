package themes

import (
	"context"
	"errors"
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

// fakeRunner is a commandRunner backed by fixture behavior, so tests never
// invoke the real pacman/sudo binaries. Paths in owned/unowned are matched
// exactly against a `pacman -Qo <path>` call's argument.
type fakeRunner struct {
	calls  []fakeCall
	owned  map[string]bool
	onCopy func(src, dest string) // invoked for "sudo cp <src> <dest>" calls, so tests can read staged temp-file content before it's removed
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, fakeCall{name: name, args: args})

	if name == "pacman" && len(args) == 2 && args[0] == "-Qo" {
		if f.owned[args[1]] {
			return "owned", nil
		}
		return "", errors.New("No package owns " + args[1])
	}

	if name == "sudo" && len(args) == 3 && args[0] == "cp" && f.onCopy != nil {
		f.onCopy(args[1], args[2])
	}

	return "", nil
}

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

// TestPlanTriggersUpdateForActivationOnlyChange covers a gap found during
// real-container testing: switching which already-installed theme is
// active involves no file content change at all, so content-hash diffing
// alone would silently no-op. Wanting activation must be its own trigger.
func TestPlanTriggersUpdateForActivationOnlyChange(t *testing.T) {
	p := newWithHome(t.TempDir())

	t.Run("desired active but not currently active triggers update", func(t *testing.T) {
		desired := []core.ProjectResource{
			{ID: "/usr/share/sddm/themes/elegant", Attributes: map[string]any{"content_hash": "same", "active": true}},
		}
		current := []core.Resource{
			{ID: "/usr/share/sddm/themes/elegant", Attributes: map[string]any{"content_hash": "same"}}, // not active
		}

		actions, err := p.Plan(context.Background(), desired, current)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if len(actions) != 1 || actions[0].Kind != core.ActionUpdate {
			t.Fatalf("got %+v, want a single update action", actions)
		}
	})

	t.Run("already active is a noop", func(t *testing.T) {
		desired := []core.ProjectResource{
			{ID: "/usr/share/sddm/themes/elegant", Attributes: map[string]any{"content_hash": "same", "active": true}},
		}
		current := []core.Resource{
			{ID: "/usr/share/sddm/themes/elegant", Attributes: map[string]any{"content_hash": "same", "active": true}},
		}

		actions, err := p.Plan(context.Background(), desired, current)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if len(actions) != 0 {
			t.Fatalf("got %+v, want no actions", actions)
		}
	})

	t.Run("not wanting active is never itself a reason to act", func(t *testing.T) {
		desired := []core.ProjectResource{
			{ID: "/usr/share/sddm/themes/other", Attributes: map[string]any{"content_hash": "same"}}, // no opinion on activation
		}
		current := []core.Resource{
			{ID: "/usr/share/sddm/themes/other", Attributes: map[string]any{"content_hash": "same", "active": true}}, // happens to be active live
		}

		actions, err := p.Plan(context.Background(), desired, current)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if len(actions) != 0 {
			t.Fatalf("got %+v, want no actions (absence of \"active\" must not trigger deactivation)", actions)
		}
	})
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

func writeSystemFile(t *testing.T, systemRoot, abs, content string) {
	t.Helper()
	path := filepath.Join(systemRoot, strings.TrimPrefix(abs, "/"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestDiscoverSystemThemesFiltersPackageOwned(t *testing.T) {
	systemRoot := t.TempDir()
	writeSystemFile(t, systemRoot, "/usr/share/themes/Manual/index.theme", "[Desktop Entry]\n")
	writeSystemFile(t, systemRoot, "/usr/share/themes/Packaged/index.theme", "[Desktop Entry]\n")

	fr := &fakeRunner{owned: map[string]bool{"/usr/share/themes/Packaged": true}}
	p := newWithRoots(t.TempDir(), systemRoot, fr.run)

	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1 (Packaged should be filtered out): %+v", len(resources), resources)
	}
	if resources[0].ID != "/usr/share/themes/Manual" {
		t.Errorf("ID = %q", resources[0].ID)
	}
	if resources[0].Attributes["scope"] != "system" {
		t.Errorf("scope = %v, want system", resources[0].Attributes["scope"])
	}
}

func TestDiscoverDetectsActiveSDDMTheme(t *testing.T) {
	systemRoot := t.TempDir()
	writeSystemFile(t, systemRoot, "/usr/share/sddm/themes/elegant/theme.conf", "x")
	writeSystemFile(t, systemRoot, "/usr/share/sddm/themes/other/theme.conf", "y")
	writeSystemFile(t, systemRoot, "/etc/sddm.conf.d/theme.conf", "[Theme]\nCurrent=elegant\n")

	fr := &fakeRunner{}
	p := newWithRoots(t.TempDir(), systemRoot, fr.run)

	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	byID := make(map[string]core.Resource, len(resources))
	for _, r := range resources {
		byID[r.ID] = r
	}
	if active, _ := byID["/usr/share/sddm/themes/elegant"].Attributes["active"].(bool); !active {
		t.Error("elegant should be flagged active")
	}
	if _, ok := byID["/usr/share/sddm/themes/other"].Attributes["active"]; ok {
		t.Error("other should not have an active attribute at all")
	}
}

func TestParseThemeCurrent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantOK  bool
	}{
		{"basic", "[Theme]\nCurrent=my-theme\n", "my-theme", true},
		{"with other sections", "[General]\nFoo=bar\n\n[Theme]\n# a comment\nCurrent=my-theme\n", "my-theme", true},
		{"case-insensitive section", "[theme]\nCurrent=my-theme\n", "my-theme", true},
		{"no theme section", "[General]\nFoo=bar\n", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseThemeCurrent([]byte(tt.content))
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestApplySystemScopeCopiesViaSudo(t *testing.T) {
	fr := &fakeRunner{}
	systemRoot := t.TempDir()
	p := newWithRoots(t.TempDir(), systemRoot, fr.run)
	projectDir := t.TempDir()

	action := core.Action{
		ResourceID: "/usr/share/themes/Manual", Kind: core.ActionCreate,
		Attributes: map[string]any{"scope": "system"},
	}
	if err := p.Apply(context.Background(), projectDir, action); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	wantTarget := filepath.Join(systemRoot, "usr/share/themes/Manual")
	wantSrc := filepath.Join(project.FilesDir(projectDir), "usr/share/themes/Manual")
	want := []fakeCall{
		{name: "sudo", args: []string{"rm", "-rf", wantTarget}},
		{name: "sudo", args: []string{"cp", "-r", wantSrc, wantTarget}},
	}
	if len(fr.calls) != len(want) {
		t.Fatalf("got %d calls, want %d: %+v", len(fr.calls), len(want), fr.calls)
	}
	for i, c := range fr.calls {
		if c.name != want[i].name || strings.Join(c.args, " ") != strings.Join(want[i].args, " ") {
			t.Errorf("call %d = %+v, want %+v", i, c, want[i])
		}
	}
}

func TestApplyActivatesSDDMTheme(t *testing.T) {
	var capturedContent, capturedDest string
	fr := &fakeRunner{
		onCopy: func(src, dest string) {
			if content, err := os.ReadFile(src); err == nil {
				capturedContent = string(content)
			}
			capturedDest = dest
		},
	}
	systemRoot := t.TempDir()
	p := newWithRoots(t.TempDir(), systemRoot, fr.run)
	projectDir := t.TempDir()

	destInProject := filepath.Join(project.FilesDir(projectDir), "usr/share/sddm/themes/elegant")
	if err := os.MkdirAll(destInProject, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destInProject, "theme.conf"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	action := core.Action{
		ResourceID: "/usr/share/sddm/themes/elegant", Kind: core.ActionCreate,
		Attributes: map[string]any{"scope": "system", "active": true},
	}
	if err := p.Apply(context.Background(), projectDir, action); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if capturedContent != "[Theme]\nCurrent=elegant\n" {
		t.Errorf("activation file content = %q", capturedContent)
	}
	wantDest := filepath.Join(systemRoot, "etc/sddm.conf.d/envsetup-theme.conf")
	if capturedDest != wantDest {
		t.Errorf("activation file dest = %q, want %q", capturedDest, wantDest)
	}
}

func TestApplyNonActiveThemeDoesNotTouchSDDMConfig(t *testing.T) {
	fr := &fakeRunner{onCopy: func(src, dest string) { t.Errorf("unexpected activation write: %s -> %s", src, dest) }}
	systemRoot := t.TempDir()
	p := newWithRoots(t.TempDir(), systemRoot, fr.run)
	projectDir := t.TempDir()

	action := core.Action{
		ResourceID: "/usr/share/themes/Manual", Kind: core.ActionCreate,
		Attributes: map[string]any{"scope": "system"}, // no "active" key
	}
	if err := p.Apply(context.Background(), projectDir, action); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestValidateChecksActiveTheme(t *testing.T) {
	systemRoot := t.TempDir()
	writeSystemFile(t, systemRoot, "/usr/share/sddm/themes/elegant/theme.conf", "x")

	files := map[string][]byte{"theme.conf": []byte("x")}
	desired := []core.ProjectResource{
		{ID: "/usr/share/sddm/themes/elegant", Attributes: map[string]any{
			"scope": "system", "active": true, "content_hash": hashTree(files),
		}},
	}

	t.Run("mismatched active theme drifts", func(t *testing.T) {
		writeSystemFile(t, systemRoot, "/etc/sddm.conf.d/theme.conf", "[Theme]\nCurrent=other\n")
		p := newWithRoots(t.TempDir(), systemRoot, (&fakeRunner{}).run)

		results, err := p.Validate(context.Background(), desired)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if len(results) != 1 || !results[0].Drifted || results[0].Detail != "not the active SDDM theme" {
			t.Fatalf("got %+v", results)
		}
	})

	t.Run("matching active theme is clean", func(t *testing.T) {
		writeSystemFile(t, systemRoot, "/etc/sddm.conf.d/theme.conf", "[Theme]\nCurrent=elegant\n")
		p := newWithRoots(t.TempDir(), systemRoot, (&fakeRunner{}).run)

		results, err := p.Validate(context.Background(), desired)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if len(results) != 1 || results[0].Drifted {
			t.Fatalf("got %+v", results)
		}
	})
}

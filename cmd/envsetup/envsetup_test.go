package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/registry"
)

// testProvider is a minimal in-memory core.Provider used only to exercise
// the CLI end to end. It is never registered outside tests.
type testProvider struct {
	system map[string]string // id -> value, simulates live system state
}

func (p *testProvider) Type() string { return "widget" }

func (p *testProvider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	var out []core.Resource
	for id, v := range p.system {
		out = append(out, core.Resource{
			Type:       p.Type(),
			ID:         id,
			Attributes: map[string]any{"value": v},
			Confidence: core.ConfidenceHigh,
			Provenance: core.Provenance{Source: "test"},
		})
	}
	return out, nil
}

func (p *testProvider) Export(ctx context.Context, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, len(resources))
	for i, r := range resources {
		out[i] = core.ProjectResource{ID: r.ID, Attributes: r.Attributes}
	}
	return out, nil
}

func (p *testProvider) Plan(ctx context.Context, desired []core.ProjectResource, current []core.Resource) ([]core.Action, error) {
	currentIDs := make(map[string]bool, len(current))
	for _, c := range current {
		currentIDs[c.ID] = true
	}

	var actions []core.Action
	for _, d := range desired {
		if !currentIDs[d.ID] {
			actions = append(actions, core.Action{
				ResourceType: p.Type(), ResourceID: d.ID, Kind: core.ActionCreate,
				Description: "create " + d.ID, Attributes: d.Attributes,
			})
		}
	}
	return actions, nil
}

func (p *testProvider) Apply(ctx context.Context, action core.Action) error {
	if action.Kind == core.ActionCreate {
		value, _ := action.Attributes["value"].(string)
		p.system[action.ResourceID] = value
	}
	return nil
}

func (p *testProvider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	var results []core.ValidationResult
	for _, d := range desired {
		_, exists := p.system[d.ID]
		results = append(results, core.ValidationResult{
			ResourceType: p.Type(), ResourceID: d.ID, Drifted: !exists,
		})
	}
	return results, nil
}

func newTestApp(t *testing.T, system map[string]string) (*App, *bytes.Buffer) {
	t.Helper()

	reg := registry.New()
	if err := reg.Register(&testProvider{system: system}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	out := &bytes.Buffer{}
	return &App{
		Registry: reg,
		Out:      out,
		Err:      &bytes.Buffer{},
		In:       strings.NewReader(""),
	}, out
}

func execute(t *testing.T, app *App, args ...string) error {
	t.Helper()

	root := newRootCmd(app)
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	root.SetArgs(args)
	return root.Execute()
}

// TestCLILifecycle drives every command (init, scan, export, plan, apply,
// validate, doctor) against a fake provider, proving the CLI wiring end to
// end -- including plan's 0/2 exit-code convention.
func TestCLILifecycle(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "proj")

	system := map[string]string{"widget-a": "v1"}
	app, out := newTestApp(t, system)

	if err := execute(t, app, "init", projectDir); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	out.Reset()
	if err := execute(t, app, "scan"); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !strings.Contains(out.String(), "widget-a") {
		t.Errorf("scan output missing widget-a: %s", out)
	}

	out.Reset()
	if err := execute(t, app, "export", "--project", projectDir, "--yes"); err != nil {
		t.Fatalf("export: %v\n%s", err, out)
	}

	// Right after export, the project matches the live system.
	out.Reset()
	app.ExitCode = 0
	if err := execute(t, app, "plan", "--project", projectDir); err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if app.ExitCode != 0 {
		t.Errorf("plan exit code = %d, want 0 right after export", app.ExitCode)
	}

	// Introduce drift by removing the resource from the live system.
	delete(system, "widget-a")

	out.Reset()
	app.ExitCode = 0
	if err := execute(t, app, "plan", "--project", projectDir); err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if app.ExitCode != 2 {
		t.Errorf("plan exit code = %d, want 2 with pending changes", app.ExitCode)
	}

	out.Reset()
	if err := execute(t, app, "apply", "--project", projectDir, "--yes"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if _, ok := system["widget-a"]; !ok {
		t.Errorf("apply did not recreate widget-a: %+v", system)
	}

	out.Reset()
	app.ExitCode = 0
	if err := execute(t, app, "validate", "--project", projectDir); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	if app.ExitCode != 0 {
		t.Errorf("validate exit code = %d, want 0 after apply", app.ExitCode)
	}

	out.Reset()
	if err := execute(t, app, "doctor", "--project", projectDir); err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
}

func TestApplyDeclinedLeavesSystemUnchanged(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "proj")

	system := map[string]string{}
	app, out := newTestApp(t, system)
	app.In = strings.NewReader("n\n")

	if err := execute(t, app, "init", projectDir); err != nil {
		t.Fatalf("init: %v", err)
	}

	system["widget-a"] = "v1"
	out.Reset()
	if err := execute(t, app, "export", "--project", projectDir, "--yes"); err != nil {
		t.Fatalf("export: %v", err)
	}
	delete(system, "widget-a")

	out.Reset()
	if err := execute(t, app, "apply", "--project", projectDir); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("expected cancellation message, got %q", out.String())
	}
	if _, ok := system["widget-a"]; ok {
		t.Errorf("apply applied changes despite decline: %+v", system)
	}
}

func TestInitRefusesExistingProject(t *testing.T) {
	dir := t.TempDir()
	app, _ := newTestApp(t, map[string]string{})

	if err := execute(t, app, "init", dir); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if err := execute(t, app, "init", dir); err == nil {
		t.Fatal("second init: expected error, got nil")
	}
}

func TestPlanWithoutProjectHintsInit(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{})
	err := execute(t, app, "plan", "--project", t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing project, got nil")
	}
	if !strings.Contains(err.Error(), "envsetup init") {
		t.Errorf("error should hint at `envsetup init`, got: %v", err)
	}
}

func TestResolveProjectDir(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("project", "", "")

	t.Run("flag wins over env", func(t *testing.T) {
		cmd.Flags().Set("project", "/from-flag")
		t.Setenv("ENVSETUP_PROJECT", "/from-env")
		if got := resolveProjectDir(cmd); got != "/from-flag" {
			t.Errorf("got %q, want /from-flag", got)
		}
	})

	t.Run("env var fallback", func(t *testing.T) {
		cmd.Flags().Set("project", "")
		t.Setenv("ENVSETUP_PROJECT", "/from-env")
		if got := resolveProjectDir(cmd); got != "/from-env" {
			t.Errorf("got %q, want /from-env", got)
		}
	})

	t.Run("default is current directory", func(t *testing.T) {
		cmd.Flags().Set("project", "")
		t.Setenv("ENVSETUP_PROJECT", "")
		if got := resolveProjectDir(cmd); got != "." {
			t.Errorf("got %q, want .", got)
		}
	})
}

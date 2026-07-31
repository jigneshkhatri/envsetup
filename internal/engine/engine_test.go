package engine

import (
	"context"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/project"
	"github.com/jigneshkhatri/envsetup/internal/registry"
)

// fakeProvider is a minimal in-memory core.Provider used only to exercise
// the engine's control flow end to end. It is never registered outside
// tests and ships with no real discovery/apply logic of its own.
type fakeProvider struct {
	system map[string]string // id -> value, simulates live system state
}

func newFakeProvider(system map[string]string) *fakeProvider {
	return &fakeProvider{system: system}
}

func (f *fakeProvider) Type() string { return "widget" }

func (f *fakeProvider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	var resources []core.Resource
	for id, value := range f.system {
		resources = append(resources, core.Resource{
			Type:       f.Type(),
			ID:         id,
			Attributes: map[string]any{"value": value},
			Provenance: core.Provenance{Source: "fake"},
			Confidence: core.ConfidenceHigh,
		})
	}
	return resources, nil
}

func (f *fakeProvider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	out := make([]core.ProjectResource, len(resources))
	for i, r := range resources {
		out[i] = core.ProjectResource{ID: r.ID, Attributes: r.Attributes}
	}
	return out, nil
}

func (f *fakeProvider) Plan(ctx context.Context, desired []core.ProjectResource, current []core.Resource) ([]core.Action, error) {
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
				ResourceType: f.Type(), ResourceID: id, Kind: core.ActionCreate,
				Description: "create " + id, Attributes: d.Attributes,
			})
		case c.Attributes["value"] != d.Attributes["value"]:
			actions = append(actions, core.Action{
				ResourceType: f.Type(), ResourceID: id, Kind: core.ActionUpdate,
				Description: "update " + id, Attributes: d.Attributes,
			})
		}
	}
	for id := range currentByID {
		if _, exists := desiredByID[id]; !exists {
			actions = append(actions, core.Action{
				ResourceType: f.Type(), ResourceID: id, Kind: core.ActionDelete,
				Description: "delete " + id,
			})
		}
	}
	return actions, nil
}

func (f *fakeProvider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	switch action.Kind {
	case core.ActionCreate, core.ActionUpdate:
		value, _ := action.Attributes["value"].(string)
		f.system[action.ResourceID] = value
	case core.ActionDelete:
		delete(f.system, action.ResourceID)
	}
	return nil
}

func (f *fakeProvider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	var results []core.ValidationResult
	for _, d := range desired {
		value, exists := f.system[d.ID]
		switch {
		case !exists:
			results = append(results, core.ValidationResult{ResourceType: f.Type(), ResourceID: d.ID, Drifted: true, Detail: "missing"})
		case value != d.Attributes["value"]:
			results = append(results, core.ValidationResult{ResourceType: f.Type(), ResourceID: d.ID, Drifted: true, Detail: "value differs"})
		default:
			results = append(results, core.ValidationResult{ResourceType: f.Type(), ResourceID: d.ID, Drifted: false})
		}
	}
	return results, nil
}

// TestEngineLifecycle drives a fake provider through the full
// Scan -> Export -> Plan -> Apply -> Validate lifecycle, proving the
// engine's control flow end to end without any real provider.
func TestEngineLifecycle(t *testing.T) {
	ctx := context.Background()

	system := map[string]string{
		"widget-a": "v1",
		"widget-b": "v1",
	}
	fake := newFakeProvider(system)

	reg := registry.New()
	if err := reg.Register(fake); err != nil {
		t.Fatalf("Register: %v", err)
	}

	dir := t.TempDir()
	proj := project.New(dir, "test-project")
	e := New(reg, proj, core.SystemContext{})

	// Scan: read-only discovery.
	scanned, err := e.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(scanned["widget"]) != 2 {
		t.Fatalf("Scan: got %d widgets, want 2", len(scanned["widget"]))
	}

	// Export: discover + convert to project resources, then persist.
	exportResults, err := e.Export(ctx)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(exportResults) != 1 || exportResults[0].Type != "widget" {
		t.Fatalf("Export: unexpected results %+v", exportResults)
	}
	if len(exportResults[0].NeedsReview) != 0 {
		t.Fatalf("Export: unexpected NeedsReview %+v", exportResults[0].NeedsReview)
	}
	proj.SetResourcesFor("widget", exportResults[0].Exported)
	if err := proj.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Plan immediately after export: system already matches desired state.
	actions, err := e.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("Plan after export: got %d actions, want 0: %+v", len(actions), actions)
	}

	// Introduce drift: change one resource's value, remove another from the
	// live system, and declare a brand new desired resource.
	system["widget-a"] = "v2"
	delete(system, "widget-b")
	proj.SetResourcesFor("widget", append(proj.ResourcesFor("widget"), core.ProjectResource{
		ID:         "widget-c",
		Attributes: map[string]any{"value": "v1"},
	}))

	actions, err = e.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan after drift: %v", err)
	}
	if len(actions) != 3 {
		t.Fatalf("Plan after drift: got %d actions, want 3: %+v", len(actions), actions)
	}

	// Apply reconciles everything in one pass.
	applied, err := e.Apply(ctx, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 3 {
		t.Fatalf("Apply: executed %d actions, want 3", len(applied))
	}

	// Validate: no drift remains.
	results, err := e.Validate(ctx)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, r := range results {
		if r.Drifted {
			t.Errorf("Validate: unexpected drift for %s: %s", r.ResourceID, r.Detail)
		}
	}

	// The saved project should round-trip through YAML with all 3 desired
	// resources intact.
	if err := proj.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := project.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(reloaded.ResourcesFor("widget")); got != 3 {
		t.Fatalf("Load: got %d widgets, want 3", got)
	}
}

func TestApplyDryRunExecutesNothing(t *testing.T) {
	ctx := context.Background()

	system := map[string]string{}
	fake := newFakeProvider(system)

	reg := registry.New()
	if err := reg.Register(fake); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proj := project.New(t.TempDir(), "test-project")
	proj.SetResourcesFor("widget", []core.ProjectResource{
		{ID: "widget-a", Attributes: map[string]any{"value": "v1"}},
	})

	e := New(reg, proj, core.SystemContext{})

	actions, err := e.Apply(ctx, ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("Apply: got %d planned actions, want 1", len(actions))
	}
	if len(system) != 0 {
		t.Fatalf("Apply: dry-run mutated system state: %+v", system)
	}
}

func TestApplyOnlyFiltersByType(t *testing.T) {
	ctx := context.Background()

	system := map[string]string{}
	fake := newFakeProvider(system)

	reg := registry.New()
	if err := reg.Register(fake); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proj := project.New(t.TempDir(), "test-project")
	proj.SetResourcesFor("widget", []core.ProjectResource{
		{ID: "widget-a", Attributes: map[string]any{"value": "v1"}},
	})

	e := New(reg, proj, core.SystemContext{})

	actions, err := e.Apply(ctx, ApplyOptions{Only: []string{"nonexistent-type"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("Apply: got %d actions, want 0 after filtering out all types", len(actions))
	}
	if len(system) != 0 {
		t.Fatalf("Apply: filtered-out action still executed: %+v", system)
	}
}

// userDeclaredFakeProvider simulates a provider like recipe: Discover
// always returns nothing, and it opts out of export via
// core.UserDeclaredProvider so hand-authored project entries survive
// `envsetup export`.
type userDeclaredFakeProvider struct {
	discoverCalled bool
}

func (f *userDeclaredFakeProvider) Type() string       { return "recipes" }
func (f *userDeclaredFakeProvider) UserDeclared() bool { return true }

func (f *userDeclaredFakeProvider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	f.discoverCalled = true
	return nil, nil
}

func (f *userDeclaredFakeProvider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	return nil, nil
}

func (f *userDeclaredFakeProvider) Plan(ctx context.Context, desired []core.ProjectResource, current []core.Resource) ([]core.Action, error) {
	return nil, nil
}

func (f *userDeclaredFakeProvider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	return nil
}

func (f *userDeclaredFakeProvider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	return nil, nil
}

func TestExportSkipsUserDeclaredProviders(t *testing.T) {
	ctx := context.Background()

	fake := &userDeclaredFakeProvider{}
	reg := registry.New()
	if err := reg.Register(fake); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proj := project.New(t.TempDir(), "test-project")
	// Simulate a hand-authored recipes.yaml entry already present in the
	// project -- export must leave it alone.
	proj.SetResourcesFor("recipes", []core.ProjectResource{
		{ID: "hand-authored", Attributes: map[string]any{"apply": "true", "check": "true"}},
	})

	e := New(reg, proj, core.SystemContext{})

	results, err := e.Export(ctx)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Export: got %d results, want 0 (user-declared provider should be skipped): %+v", len(results), results)
	}
	if fake.discoverCalled {
		t.Error("Export should not call Discover on a user-declared provider")
	}
	if got := proj.ResourcesFor("recipes"); len(got) != 1 {
		t.Fatalf("hand-authored recipes entry was touched: %+v", got)
	}
}

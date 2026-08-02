package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/project"
	"github.com/jigneshkhatri/envsetup/internal/registry"
)

// fakeProvider is a minimal in-memory core.Provider used only to exercise
// the engine's control flow end to end. It is never registered outside
// tests and ships with no real discovery/apply logic of its own.
type fakeProvider struct {
	system  map[string]string // id -> value, simulates live system state
	failIDs map[string]bool   // resource IDs whose Apply should return an error
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
	if f.failIDs[action.ResourceID] {
		return errors.New("simulated failure")
	}
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

	// Apply reconciles everything in one pass -- AllowUpdate/AllowRemove
	// are both required here since the drift includes an update
	// (widget-a's value changed) and a delete (widget-b was removed).
	result, err := e.Apply(ctx, ApplyOptions{AllowUpdate: true, AllowRemove: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Applied) != 3 {
		t.Fatalf("Apply: executed %d actions, want 3", len(result.Applied))
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("Apply: unexpectedly skipped %+v", result.Skipped)
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

	result, err := e.Apply(ctx, ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("Apply: got %d planned actions, want 1", len(result.Applied))
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

	result, err := e.Apply(ctx, ApplyOptions{Only: []string{"nonexistent-type"}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("Apply: got %d actions, want 0 after filtering out all types", len(result.Applied))
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

// TestApplyDefaultOnlyRunsCreateActions is the core safety guarantee: by
// default, apply must never override or remove configuration already on
// the host -- only fill in what's missing. Update and Delete actions
// should be reported as skipped, not executed, unless explicitly allowed.
func TestApplyDefaultOnlyRunsCreateActions(t *testing.T) {
	ctx := context.Background()

	system := map[string]string{
		"widget-a": "v1", // desired wants a different value -> update
		"widget-b": "v1", // not desired -> delete
	}
	fake := newFakeProvider(system)

	reg := registry.New()
	if err := reg.Register(fake); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proj := project.New(t.TempDir(), "test-project")
	proj.SetResourcesFor("widget", []core.ProjectResource{
		{ID: "widget-a", Attributes: map[string]any{"value": "v2"}}, // drifted value
		{ID: "widget-c", Attributes: map[string]any{"value": "v1"}}, // missing -> create
	})

	e := New(reg, proj, core.SystemContext{})

	result, err := e.Apply(ctx, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Applied) != 1 || result.Applied[0].ResourceID != "widget-c" || result.Applied[0].Kind != core.ActionCreate {
		t.Fatalf("Applied = %+v, want only the widget-c create", result.Applied)
	}
	if len(result.Skipped) != 2 {
		t.Fatalf("Skipped = %+v, want 2 (the update and the delete)", result.Skipped)
	}

	// The host's existing state must be untouched except for the new
	// widget-c.
	if system["widget-a"] != "v1" {
		t.Errorf("widget-a was modified despite AllowUpdate not being set: %q", system["widget-a"])
	}
	if _, exists := system["widget-b"]; !exists {
		t.Error("widget-b was removed despite AllowRemove not being set")
	}
	if system["widget-c"] != "v1" {
		t.Errorf("widget-c create did not run: %+v", system)
	}
}

func TestApplyProgressHooksFireForEveryAttemptAndSurviveAFailure(t *testing.T) {
	ctx := context.Background()

	system := map[string]string{}
	fake := newFakeProvider(system)
	fake.failIDs = map[string]bool{"widget-b": true}

	reg := registry.New()
	if err := reg.Register(fake); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proj := project.New(t.TempDir(), "test-project")
	proj.SetResourcesFor("widget", []core.ProjectResource{
		{ID: "widget-a", Attributes: map[string]any{"value": "v1"}},
		{ID: "widget-b", Attributes: map[string]any{"value": "v1"}}, // Apply fails for this one
		{ID: "widget-c", Attributes: map[string]any{"value": "v1"}},
	})

	e := New(reg, proj, core.SystemContext{})

	var started, done []string
	var doneErrs []error
	opts := ApplyOptions{
		OnActionStart: func(a core.Action) { started = append(started, a.ResourceID) },
		OnActionDone: func(a core.Action, err error) {
			done = append(done, a.ResourceID)
			doneErrs = append(doneErrs, err)
		},
	}

	result, err := e.Apply(ctx, opts)
	if err == nil {
		t.Fatal("Apply: want an aggregate error since widget-b fails, got nil")
	}
	if len(result.Applied) != 3 {
		t.Fatalf("Applied = %+v, want all 3 create actions attempted", result.Applied)
	}

	// Every attempted action gets both hooks, in order, regardless of
	// whether it failed -- Apply keeps going past a single failure.
	wantIDs := []string{"widget-a", "widget-b", "widget-c"}
	if len(started) != 3 || len(done) != 3 {
		t.Fatalf("started = %v, done = %v, want 3 of each", started, done)
	}
	for i, id := range wantIDs {
		if started[i] != id {
			t.Errorf("started[%d] = %q, want %q (progress must fire before Apply, not after)", i, started[i], id)
		}
		if done[i] != id {
			t.Errorf("done[%d] = %q, want %q", i, done[i], id)
		}
	}
	for i, id := range wantIDs {
		wantErr := id == "widget-b"
		if (doneErrs[i] != nil) != wantErr {
			t.Errorf("doneErrs[%d] (%s) = %v, want non-nil only for widget-b", i, id, doneErrs[i])
		}
	}

	// DryRun must never invoke either hook -- it's a preview, nothing was
	// actually attempted.
	var dryStarted int
	dryOpts := ApplyOptions{DryRun: true, OnActionStart: func(core.Action) { dryStarted++ }}
	if _, err := e.Apply(ctx, dryOpts); err != nil {
		t.Fatalf("Apply (dry run): %v", err)
	}
	if dryStarted != 0 {
		t.Errorf("OnActionStart fired %d times under DryRun, want 0", dryStarted)
	}
}

func TestApplyAllowUpdateOnlyRunsUpdatesNotDeletes(t *testing.T) {
	ctx := context.Background()

	system := map[string]string{
		"widget-a": "v1", // drifted -> update
		"widget-b": "v1", // not desired -> delete
	}
	fake := newFakeProvider(system)

	reg := registry.New()
	if err := reg.Register(fake); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proj := project.New(t.TempDir(), "test-project")
	proj.SetResourcesFor("widget", []core.ProjectResource{
		{ID: "widget-a", Attributes: map[string]any{"value": "v2"}},
	})

	e := New(reg, proj, core.SystemContext{})

	result, err := e.Apply(ctx, ApplyOptions{AllowUpdate: true})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(result.Applied) != 1 || result.Applied[0].Kind != core.ActionUpdate {
		t.Fatalf("Applied = %+v, want only the widget-a update", result.Applied)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Kind != core.ActionDelete {
		t.Fatalf("Skipped = %+v, want only the widget-b delete", result.Skipped)
	}
	if system["widget-a"] != "v2" {
		t.Errorf("widget-a was not updated: %q", system["widget-a"])
	}
	if _, exists := system["widget-b"]; !exists {
		t.Error("widget-b was removed despite AllowRemove not being set")
	}
}

func TestDoctorFindsBlankAndDuplicateIDs(t *testing.T) {
	ctx := context.Background()

	reg := registry.New()
	proj := project.New(t.TempDir(), "test-project")
	proj.SetResourcesFor("widget", []core.ProjectResource{
		{ID: "widget-a"},
		{ID: "widget-a"}, // duplicate
		{ID: ""},         // blank
	})

	e := New(reg, proj, core.SystemContext{})

	diagnoses, err := e.Doctor(ctx)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(diagnoses) != 2 {
		t.Fatalf("got %d diagnoses, want 2 (blank + duplicate): %+v", len(diagnoses), diagnoses)
	}
}

// doctorFakeProvider implements core.DoctorProvider to prove the engine
// dispatches to provider-specific checks via the type assertion.
type doctorFakeProvider struct {
	fakeProvider
}

func (f *doctorFakeProvider) Doctor(ctx context.Context, projectDir string, desired []core.ProjectResource) ([]core.Diagnosis, error) {
	var diagnoses []core.Diagnosis
	for _, d := range desired {
		diagnoses = append(diagnoses, core.Diagnosis{ResourceType: f.Type(), ResourceID: d.ID, Message: "fake problem"})
	}
	return diagnoses, nil
}

func TestDoctorDispatchesToProviderSpecificChecks(t *testing.T) {
	ctx := context.Background()

	fake := &doctorFakeProvider{fakeProvider{system: map[string]string{}}}
	reg := registry.New()
	if err := reg.Register(fake); err != nil {
		t.Fatalf("Register: %v", err)
	}

	proj := project.New(t.TempDir(), "test-project")
	proj.SetResourcesFor("widget", []core.ProjectResource{{ID: "widget-a"}})

	e := New(reg, proj, core.SystemContext{})

	diagnoses, err := e.Doctor(ctx)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(diagnoses) != 1 || diagnoses[0].Message != "fake problem" {
		t.Fatalf("got %+v, want one fake problem", diagnoses)
	}
}

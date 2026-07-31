package recipe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

func TestDiscoverAlwaysEmpty(t *testing.T) {
	p := newWithRunners(&bytes.Buffer{}, nil, nil)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("got %d resources, want 0", len(resources))
	}
}

func TestUserDeclaredIsTrue(t *testing.T) {
	p := newWithRunners(&bytes.Buffer{}, nil, nil)
	if !p.UserDeclared() {
		t.Error("UserDeclared() = false, want true")
	}
}

func TestPlanSkipsSatisfiedRecipe(t *testing.T) {
	run := func(ctx context.Context, script string) bool { return true }
	p := newWithRunners(&bytes.Buffer{}, run, nil)

	desired := []core.ProjectResource{
		{ID: "already-done", Attributes: map[string]any{"check": "true", "apply": "true"}},
	}
	actions, err := p.Plan(context.Background(), desired, nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("got %d actions, want 0 for a satisfied recipe", len(actions))
	}
}

func TestPlanIncludesUnsatisfiedRecipe(t *testing.T) {
	run := func(ctx context.Context, script string) bool { return false }
	p := newWithRunners(&bytes.Buffer{}, run, nil)

	desired := []core.ProjectResource{
		{ID: "pending", Attributes: map[string]any{"check": "false", "apply": "do-the-thing"}},
	}
	actions, err := p.Plan(context.Background(), desired, nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	if actions[0].Kind != core.ActionCreate {
		t.Errorf("kind = %v, want create", actions[0].Kind)
	}
	if actions[0].Attributes["apply"] != "do-the-thing" {
		t.Errorf("apply attribute = %v", actions[0].Attributes["apply"])
	}
}

func TestPlanErrorsOnMissingCheck(t *testing.T) {
	p := newWithRunners(&bytes.Buffer{}, nil, nil)
	desired := []core.ProjectResource{
		{ID: "no-check", Attributes: map[string]any{"apply": "do-the-thing"}},
	}
	if _, err := p.Plan(context.Background(), desired, nil); err == nil {
		t.Fatal("expected error for missing check command, got nil")
	}
}

func TestApplyStreamsOutputAndRunsApplyCommand(t *testing.T) {
	var out bytes.Buffer
	var gotScript string
	stream := func(ctx context.Context, w io.Writer, script string) error {
		gotScript = script
		fmt.Fprintln(w, "live output line")
		return nil
	}
	p := newWithRunners(&out, nil, stream)

	err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "my-recipe", Kind: core.ActionCreate,
		Attributes: map[string]any{"apply": "echo hi"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if gotScript != "echo hi" {
		t.Errorf("script = %q, want %q", gotScript, "echo hi")
	}
	if !bytes.Contains(out.Bytes(), []byte("live output line")) {
		t.Errorf("output not streamed to writer: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`recipe "my-recipe"`)) {
		t.Errorf("missing recipe header: %q", out.String())
	}
}

func TestApplyErrorsOnMissingApplyCommand(t *testing.T) {
	p := newWithRunners(&bytes.Buffer{}, nil, nil)
	err := p.Apply(context.Background(), "", core.Action{ResourceID: "no-apply", Kind: core.ActionCreate})
	if err == nil {
		t.Fatal("expected error for missing apply command, got nil")
	}
}

func TestApplyPropagatesStreamError(t *testing.T) {
	stream := func(ctx context.Context, w io.Writer, script string) error {
		return errors.New("boom")
	}
	p := newWithRunners(&bytes.Buffer{}, nil, stream)

	err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "failing", Kind: core.ActionCreate,
		Attributes: map[string]any{"apply": "false"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestValidateSatisfiedAndUnsatisfied(t *testing.T) {
	run := func(ctx context.Context, script string) bool { return script == "true" }
	p := newWithRunners(&bytes.Buffer{}, run, nil)

	desired := []core.ProjectResource{
		{ID: "ok", Attributes: map[string]any{"check": "true"}},
		{ID: "pending", Attributes: map[string]any{"check": "false"}},
	}
	results, err := p.Validate(context.Background(), desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	byID := make(map[string]core.ValidationResult, len(results))
	for _, r := range results {
		byID[r.ResourceID] = r
	}
	if byID["ok"].Drifted {
		t.Error("ok should not be drifted")
	}
	if !byID["pending"].Drifted {
		t.Error("pending should be drifted")
	}
}

func TestValidateErrorsOnMissingCheck(t *testing.T) {
	p := newWithRunners(&bytes.Buffer{}, nil, nil)
	desired := []core.ProjectResource{{ID: "no-check"}}
	if _, err := p.Validate(context.Background(), desired); err == nil {
		t.Fatal("expected error for missing check command, got nil")
	}
}

// TestFullLifecycleWithRealShell exercises the Phase 7 exit criteria using
// the real sh-based runners (execCheck/execApply), not fixtures: a
// hand-authored recipe is declared, planned, applied, and re-validated
// idempotently, entirely against a throwaway temp file.
func TestFullLifecycleWithRealShell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	desired := []core.ProjectResource{
		{
			ID: "create-marker",
			Attributes: map[string]any{
				"check": fmt.Sprintf("test -f %s", marker),
				"apply": fmt.Sprintf("touch %s", marker),
			},
		},
	}

	var out bytes.Buffer
	p := New(&out) // real execCheck/execApply
	ctx := context.Background()

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker should not exist yet: %v", err)
	}

	actions, err := p.Plan(ctx, desired, nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("Plan: got %d actions, want 1 (not yet satisfied)", len(actions))
	}

	if err := p.Apply(ctx, "", actions[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker should exist after apply: %v", err)
	}

	// Re-planning must now be a no-op: idempotency.
	actions, err = p.Plan(ctx, desired, nil)
	if err != nil {
		t.Fatalf("Plan after apply: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("Plan after apply: got %d actions, want 0 (idempotent)", len(actions))
	}

	results, err := p.Validate(ctx, desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(results) != 1 || results[0].Drifted {
		t.Fatalf("Validate: got %+v, want satisfied", results)
	}
}

package gitrepos

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

type fakeCall struct {
	name string
	args []string
}

// fakeRunner is a commandRunner backed by fixture output, so tests never
// invoke the real git binary.
type fakeRunner struct {
	calls   []fakeCall
	outputs map[string]string
	errs    map[string]error
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{outputs: map[string]string{}, errs: map[string]error{}}
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, fakeCall{name: name, args: args})
	key := name + " " + strings.Join(args, " ")
	if err, ok := f.errs[key]; ok {
		return "", err
	}
	return f.outputs[key], nil
}

func (f *fakeRunner) set(output, name string, args ...string) {
	key := name + " " + strings.Join(args, " ")
	f.outputs[key] = output
}

func (f *fakeRunner) fail(name string, args ...string) {
	key := name + " " + strings.Join(args, " ")
	f.errs[key] = errors.New("fake command failure")
}

func mkGitDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
}

func TestDiscoverFindsReposInKnownContainers(t *testing.T) {
	home := t.TempDir()

	tpmPath := filepath.Join(home, ".local/share/tpm")
	mkGitDir(t, tpmPath)

	tpm2Path := filepath.Join(home, ".tmux/plugins/tpm2")
	mkGitDir(t, tpm2Path)

	noOriginPath := filepath.Join(home, ".local/share/no-origin")
	mkGitDir(t, noOriginPath)

	notARepoPath := filepath.Join(home, ".local/share/not-a-repo")
	if err := os.MkdirAll(notARepoPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	fr := newFakeRunner()
	fr.set("https://github.com/example/tpm.git\n", "git", "-C", tpmPath, "remote", "get-url", "origin")
	fr.set("main\n", "git", "-C", tpmPath, "rev-parse", "--abbrev-ref", "HEAD")
	fr.set("https://github.com/example/tpm2.git\n", "git", "-C", tpm2Path, "remote", "get-url", "origin")
	fr.set("main\n", "git", "-C", tpm2Path, "rev-parse", "--abbrev-ref", "HEAD")
	fr.fail("git", "-C", noOriginPath, "remote", "get-url", "origin")

	p := newWithRunner(home, fr.run)
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

	tpmID := filepath.Join(".local/share", "tpm")
	if r, ok := byID[tpmID]; !ok {
		t.Errorf("missing %s resource", tpmID)
	} else {
		if r.Attributes["remote"] != "https://github.com/example/tpm.git" {
			t.Errorf("remote = %v", r.Attributes["remote"])
		}
		if r.Confidence != core.ConfidenceHigh {
			t.Errorf("confidence = %v, want high", r.Confidence)
		}
	}

	tpm2ID := filepath.Join(".tmux/plugins", "tpm2")
	if _, ok := byID[tpm2ID]; !ok {
		t.Errorf("missing %s resource", tpm2ID)
	}
}

func TestExportRecordsRemoteOnly(t *testing.T) {
	p := newWithRunner(t.TempDir(), nil)
	resources := []core.Resource{
		{ID: ".local/share/tpm", Attributes: map[string]any{"remote": "https://example.com/tpm.git", "ref": "main"}},
	}

	exported, err := p.Export(context.Background(), "", resources)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(exported) != 1 {
		t.Fatalf("got %d exported, want 1", len(exported))
	}
	if exported[0].Attributes["remote"] != "https://example.com/tpm.git" {
		t.Errorf("remote = %v", exported[0].Attributes["remote"])
	}
	if _, hasRef := exported[0].Attributes["ref"]; hasRef {
		t.Error("ref should not be set by Export -- pinning is opt-in")
	}
}

func TestPlanCreateConflictRefDriftAndDelete(t *testing.T) {
	p := newWithRunner(t.TempDir(), nil)

	desired := []core.ProjectResource{
		{ID: "missing", Attributes: map[string]any{"remote": "https://example.com/missing.git"}},
		{ID: "conflict", Attributes: map[string]any{"remote": "https://example.com/wanted.git"}},
		{ID: "ref-drift", Attributes: map[string]any{"remote": "https://example.com/ok.git", "ref": "v2"}},
		{ID: "in-sync", Attributes: map[string]any{"remote": "https://example.com/sync.git"}},
	}
	current := []core.Resource{
		{ID: "conflict", Attributes: map[string]any{"remote": "https://example.com/different.git", "ref": "main"}},
		{ID: "ref-drift", Attributes: map[string]any{"remote": "https://example.com/ok.git", "ref": "v1"}},
		{ID: "in-sync", Attributes: map[string]any{"remote": "https://example.com/sync.git", "ref": "main"}},
		{ID: "unwanted", Attributes: map[string]any{"remote": "https://example.com/unwanted.git", "ref": "main"}},
	}

	actions, err := p.Plan(context.Background(), desired, current)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	byID := make(map[string]core.Action, len(actions))
	for _, a := range actions {
		byID[a.ResourceID] = a
	}
	if len(actions) != 4 {
		t.Fatalf("got %d actions, want 4: %+v", len(actions), actions)
	}

	if byID["missing"].Kind != core.ActionCreate {
		t.Errorf("missing kind = %v, want create", byID["missing"].Kind)
	}

	conflict := byID["conflict"]
	if conflict.Kind != core.ActionUpdate {
		t.Errorf("conflict kind = %v, want update", conflict.Kind)
	}
	if v, _ := conflict.Attributes["conflict"].(bool); !v {
		t.Error("conflict action should carry Attributes[\"conflict\"]=true")
	}

	refDrift := byID["ref-drift"]
	if refDrift.Kind != core.ActionUpdate {
		t.Errorf("ref-drift kind = %v, want update", refDrift.Kind)
	}
	if refDrift.Attributes["ref"] != "v2" {
		t.Errorf("ref-drift target ref = %v, want v2", refDrift.Attributes["ref"])
	}

	if byID["unwanted"].Kind != core.ActionDelete {
		t.Errorf("unwanted kind = %v, want delete", byID["unwanted"].Kind)
	}

	if _, exists := byID["in-sync"]; exists {
		t.Error("in-sync should be a noop, got an action")
	}
}

func TestApplyClonesMissingRepo(t *testing.T) {
	home := t.TempDir()
	fr := newFakeRunner()
	p := newWithRunner(home, fr.run)

	target := filepath.Join(home, ".local/share/tpm")
	err := p.Apply(context.Background(), "", core.Action{
		ResourceID: ".local/share/tpm", Kind: core.ActionCreate,
		Attributes: map[string]any{"remote": "https://example.com/tpm.git"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	want := fakeCall{name: "git", args: []string{"clone", "https://example.com/tpm.git", target}}
	if len(fr.calls) != 1 || fr.calls[0].name != want.name || strings.Join(fr.calls[0].args, " ") != strings.Join(want.args, " ") {
		t.Errorf("calls = %+v, want %+v", fr.calls, want)
	}
}

func TestApplyRefusesConflict(t *testing.T) {
	fr := newFakeRunner()
	p := newWithRunner(t.TempDir(), fr.run)

	err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "conflict", Kind: core.ActionUpdate,
		Attributes: map[string]any{"conflict": true},
	})
	if err == nil {
		t.Fatal("expected error for conflicting repo, got nil")
	}
	if len(fr.calls) != 0 {
		t.Errorf("expected no commands to run for a conflict, got %+v", fr.calls)
	}
}

func TestApplyChecksOutPinnedRef(t *testing.T) {
	home := t.TempDir()
	fr := newFakeRunner()
	p := newWithRunner(home, fr.run)

	target := filepath.Join(home, "repo")
	err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "repo", Kind: core.ActionUpdate,
		Attributes: map[string]any{"ref": "v2"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	want := fakeCall{name: "git", args: []string{"-C", target, "checkout", "v2"}}
	if len(fr.calls) != 1 || strings.Join(fr.calls[0].args, " ") != strings.Join(want.args, " ") {
		t.Errorf("calls = %+v, want %+v", fr.calls, want)
	}
}

func TestApplyRefusesDeleteWithUncommittedChanges(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "repo")
	mkGitDir(t, target)

	fr := newFakeRunner()
	fr.set(" M some-file.txt\n", "git", "-C", target, "status", "--porcelain")
	p := newWithRunner(home, fr.run)

	err := p.Apply(context.Background(), "", core.Action{ResourceID: "repo", Kind: core.ActionDelete})
	if err == nil {
		t.Fatal("expected error for dirty repo, got nil")
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("dirty repo should not have been removed: %v", statErr)
	}
}

func TestApplyDeletesCleanRepo(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "repo")
	mkGitDir(t, target)

	fr := newFakeRunner()
	fr.set("", "git", "-C", target, "status", "--porcelain")
	p := newWithRunner(home, fr.run)

	if err := p.Apply(context.Background(), "", core.Action{ResourceID: "repo", Kind: core.ActionDelete}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("clean repo should have been removed")
	}
}

func TestValidateReportsMissingConflictAndClean(t *testing.T) {
	home := t.TempDir()

	cleanPath := filepath.Join(home, "clean")
	mkGitDir(t, cleanPath)

	conflictPath := filepath.Join(home, "conflict")
	mkGitDir(t, conflictPath)

	fr := newFakeRunner()
	fr.set("https://example.com/clean.git\n", "git", "-C", cleanPath, "remote", "get-url", "origin")
	fr.set("https://example.com/other.git\n", "git", "-C", conflictPath, "remote", "get-url", "origin")

	p := newWithRunner(home, fr.run)
	desired := []core.ProjectResource{
		{ID: "clean", Attributes: map[string]any{"remote": "https://example.com/clean.git"}},
		{ID: "conflict", Attributes: map[string]any{"remote": "https://example.com/clean.git"}},
		{ID: "missing", Attributes: map[string]any{"remote": "https://example.com/missing.git"}},
	}

	results, err := p.Validate(context.Background(), desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	byID := make(map[string]core.ValidationResult, len(results))
	for _, r := range results {
		byID[r.ResourceID] = r
	}
	if byID["clean"].Drifted {
		t.Error("clean should not be drifted")
	}
	if !byID["conflict"].Drifted || byID["conflict"].Detail != "remote differs" {
		t.Errorf("conflict result = %+v", byID["conflict"])
	}
	if !byID["missing"].Drifted || byID["missing"].Detail != "missing" {
		t.Errorf("missing result = %+v", byID["missing"])
	}
}

func TestDoctorReportsUnreachableRemoteOnly(t *testing.T) {
	fr := newFakeRunner()
	fr.set("abc123\tHEAD\n", "git", "ls-remote", "--exit-code", "https://example.com/reachable.git")
	fr.fail("git", "ls-remote", "--exit-code", "https://example.com/unreachable.git")

	p := newWithRunner(t.TempDir(), fr.run)
	desired := []core.ProjectResource{
		{ID: "reachable", Attributes: map[string]any{"remote": "https://example.com/reachable.git"}},
		{ID: "unreachable", Attributes: map[string]any{"remote": "https://example.com/unreachable.git"}},
	}

	diagnoses, err := p.Doctor(context.Background(), "", desired)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(diagnoses) != 1 || diagnoses[0].ResourceID != "unreachable" {
		t.Fatalf("got %+v, want a single diagnosis for \"unreachable\"", diagnoses)
	}
}

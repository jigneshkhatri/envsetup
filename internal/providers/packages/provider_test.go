package packages

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

type fakeCall struct {
	name string
	args []string
}

// fakeRunner is a commandRunner backed by fixture output, so tests never
// invoke real pacman/AUR-helper binaries.
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

func TestDiscoverTagsProvenanceAndConfidence(t *testing.T) {
	fr := newFakeRunner()
	fr.set("neovim\nripgrep\nyay-bin\n", "pacman", "-Qqe")
	fr.set("yay-bin\n", "pacman", "-Qqm")

	p := newWithRunner(fr.run, "yay")
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 3 {
		t.Fatalf("got %d resources, want 3", len(resources))
	}

	byID := make(map[string]core.Resource, len(resources))
	for _, r := range resources {
		byID[r.ID] = r
	}

	if got := byID["neovim"].Confidence; got != core.ConfidenceHigh {
		t.Errorf("neovim confidence = %v, want high", got)
	}
	if got := byID["neovim"].Provenance.Source; got != "pacman" {
		t.Errorf("neovim provenance = %v, want pacman", got)
	}
	if got := byID["yay-bin"].Confidence; got != core.ConfidenceMedium {
		t.Errorf("yay-bin confidence = %v, want medium", got)
	}
	if got := byID["yay-bin"].Provenance.Source; got != "aur" {
		t.Errorf("yay-bin provenance = %v, want aur", got)
	}
}

func TestExportCarriesProvenance(t *testing.T) {
	p := newWithRunner(nil, "")
	resources := []core.Resource{
		{ID: "neovim", Attributes: map[string]any{"provenance": "pacman"}},
	}

	exported, err := p.Export(context.Background(), "", resources)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(exported) != 1 || exported[0].Attributes["provenance"] != "pacman" {
		t.Errorf("got %+v", exported)
	}
}

func TestPlanCreatesInstallAndRemoveActions(t *testing.T) {
	p := newWithRunner(nil, "yay")

	desired := []core.ProjectResource{
		{ID: "neovim", Attributes: map[string]any{"provenance": "pacman"}},
		{ID: "yay-bin", Attributes: map[string]any{"provenance": "aur"}},
	}
	current := []core.Resource{
		{ID: "ripgrep", Attributes: map[string]any{"provenance": "pacman"}},
	}

	actions, err := p.Plan(context.Background(), desired, current)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 3 {
		t.Fatalf("got %d actions, want 3: %+v", len(actions), actions)
	}

	var creates, deletes int
	for _, a := range actions {
		switch a.Kind {
		case core.ActionCreate:
			creates++
		case core.ActionDelete:
			deletes++
			if a.ResourceID != "ripgrep" {
				t.Errorf("unexpected delete target %q", a.ResourceID)
			}
		}
	}
	if creates != 2 || deletes != 1 {
		t.Errorf("got %d creates, %d deletes, want 2 and 1", creates, deletes)
	}
}

func TestApplyInstallsViaPacmanOrAURHelper(t *testing.T) {
	fr := newFakeRunner()
	p := newWithRunner(fr.run, "yay")

	if err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "neovim", Kind: core.ActionCreate,
		Attributes: map[string]any{"provenance": "pacman"},
	}); err != nil {
		t.Fatalf("Apply (pacman): %v", err)
	}

	if err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "yay-bin", Kind: core.ActionCreate,
		Attributes: map[string]any{"provenance": "aur"},
	}); err != nil {
		t.Fatalf("Apply (aur): %v", err)
	}

	if err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "ripgrep", Kind: core.ActionDelete,
	}); err != nil {
		t.Fatalf("Apply (delete): %v", err)
	}

	want := []fakeCall{
		{name: "sudo", args: []string{"pacman", "-S", "--noconfirm", "neovim"}},
		{name: "yay", args: []string{"-S", "--noconfirm", "yay-bin"}},
		{name: "sudo", args: []string{"pacman", "-R", "--noconfirm", "ripgrep"}},
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

func TestApplyAURWithoutHelperFails(t *testing.T) {
	fr := newFakeRunner()
	p := newWithRunner(fr.run, "") // no AUR helper detected

	err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "yay-bin", Kind: core.ActionCreate,
		Attributes: map[string]any{"provenance": "aur"},
	})
	if err == nil {
		t.Fatal("expected error when no AUR helper is available, got nil")
	}
	if len(fr.calls) != 0 {
		t.Errorf("expected no commands to run, got %+v", fr.calls)
	}
}

func TestValidateReportsMissingPackages(t *testing.T) {
	fr := newFakeRunner()
	fr.set("neovim\n", "pacman", "-Qqe")
	fr.set("", "pacman", "-Qqm")

	p := newWithRunner(fr.run, "")
	desired := []core.ProjectResource{
		{ID: "neovim", Attributes: map[string]any{"provenance": "pacman"}},
		{ID: "ripgrep", Attributes: map[string]any{"provenance": "pacman"}},
	}

	results, err := p.Validate(context.Background(), desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	byID := make(map[string]core.ValidationResult, len(results))
	for _, r := range results {
		byID[r.ResourceID] = r
	}
	if byID["neovim"].Drifted {
		t.Error("neovim should not be drifted")
	}
	if !byID["ripgrep"].Drifted {
		t.Error("ripgrep should be drifted (not installed)")
	}
}

func TestDoctorReportsUnavailablePacmanPackagesOnly(t *testing.T) {
	fr := newFakeRunner()
	fr.set("Name : neovim\n", "pacman", "-Si", "neovim")
	fr.errs["pacman -Si ancient-pkg"] = fmt.Errorf("package not found")

	p := newWithRunner(fr.run, "")
	desired := []core.ProjectResource{
		{ID: "neovim", Attributes: map[string]any{"provenance": "pacman"}},
		{ID: "ancient-pkg", Attributes: map[string]any{"provenance": "pacman"}},
		{ID: "some-aur-pkg", Attributes: map[string]any{"provenance": "aur"}},
	}

	diagnoses, err := p.Doctor(context.Background(), "", desired)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(diagnoses) != 1 || diagnoses[0].ResourceID != "ancient-pkg" {
		t.Fatalf("got %+v, want a single diagnosis for \"ancient-pkg\" (AUR packages aren't checked)", diagnoses)
	}
}

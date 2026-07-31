package flatpak

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

type fakeCall struct {
	name string
	args []string
}

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

func TestDiscoverListsAppsWithOrigin(t *testing.T) {
	fr := newFakeRunner()
	fr.set("org.mozilla.firefox\tflathub\ncom.spotify.Client\tflathub\n", "flatpak", "list", "--app", "--columns=application,origin")

	p := newWithRunner(fr.run)
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
	if r, ok := byID["org.mozilla.firefox"]; !ok {
		t.Error("missing org.mozilla.firefox")
	} else {
		if r.Attributes["origin"] != "flathub" {
			t.Errorf("origin = %v, want flathub", r.Attributes["origin"])
		}
		if r.Confidence != core.ConfidenceHigh {
			t.Errorf("confidence = %v, want high", r.Confidence)
		}
	}
}

func TestDiscoverReturnsEmptyWhenFlatpakUnavailable(t *testing.T) {
	fr := newFakeRunner()
	fr.errs["flatpak list --app --columns=application,origin"] = errors.New("exec: \"flatpak\": executable file not found in $PATH")

	p := newWithRunner(fr.run)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v (should report nothing found, not error)", err)
	}
	if len(resources) != 0 {
		t.Fatalf("got %d resources, want 0", len(resources))
	}
}

func TestExportCarriesOrigin(t *testing.T) {
	p := newWithRunner(nil)
	resources := []core.Resource{
		{ID: "org.mozilla.firefox", Attributes: map[string]any{"origin": "flathub"}},
	}
	exported, err := p.Export(context.Background(), "", resources)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(exported) != 1 || exported[0].Attributes["origin"] != "flathub" {
		t.Errorf("got %+v", exported)
	}
}

func TestPlanCreateAndDelete(t *testing.T) {
	p := newWithRunner(nil)

	desired := []core.ProjectResource{
		{ID: "org.mozilla.firefox", Attributes: map[string]any{"origin": "flathub"}},
		{ID: "com.spotify.Client", Attributes: map[string]any{}}, // no origin -- defaults to flathub
	}
	current := []core.Resource{
		{ID: "org.gimp.GIMP", Attributes: map[string]any{"origin": "flathub"}},
	}

	actions, err := p.Plan(context.Background(), desired, current)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(actions) != 3 {
		t.Fatalf("got %d actions, want 3: %+v", len(actions), actions)
	}

	byID := make(map[string]core.Action, len(actions))
	for _, a := range actions {
		byID[a.ResourceID] = a
	}
	if byID["org.mozilla.firefox"].Kind != core.ActionCreate {
		t.Errorf("firefox kind = %v, want create", byID["org.mozilla.firefox"].Kind)
	}
	if got := byID["com.spotify.Client"].Attributes["origin"]; got != "flathub" {
		t.Errorf("spotify origin default = %v, want flathub", got)
	}
	if byID["org.gimp.GIMP"].Kind != core.ActionDelete {
		t.Errorf("gimp kind = %v, want delete", byID["org.gimp.GIMP"].Kind)
	}
}

func TestApplyInstallAndUninstallUseUserScope(t *testing.T) {
	fr := newFakeRunner()
	p := newWithRunner(fr.run)

	if err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "org.mozilla.firefox", Kind: core.ActionCreate,
		Attributes: map[string]any{"origin": "flathub"},
	}); err != nil {
		t.Fatalf("Apply (install): %v", err)
	}

	if err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "org.gimp.GIMP", Kind: core.ActionDelete,
	}); err != nil {
		t.Fatalf("Apply (uninstall): %v", err)
	}

	want := []fakeCall{
		{name: "flatpak", args: []string{"install", "--user", "--noninteractive", "-y", "flathub", "org.mozilla.firefox"}},
		{name: "flatpak", args: []string{"uninstall", "--user", "--noninteractive", "-y", "org.gimp.GIMP"}},
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

func TestValidateReportsMissingApps(t *testing.T) {
	fr := newFakeRunner()
	fr.set("org.mozilla.firefox\tflathub\n", "flatpak", "list", "--app", "--columns=application,origin")

	p := newWithRunner(fr.run)
	desired := []core.ProjectResource{
		{ID: "org.mozilla.firefox", Attributes: map[string]any{"origin": "flathub"}},
		{ID: "org.gimp.GIMP", Attributes: map[string]any{"origin": "flathub"}},
	}

	results, err := p.Validate(context.Background(), desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	byID := make(map[string]core.ValidationResult, len(results))
	for _, r := range results {
		byID[r.ResourceID] = r
	}
	if byID["org.mozilla.firefox"].Drifted {
		t.Error("firefox should not be drifted")
	}
	if !byID["org.gimp.GIMP"].Drifted {
		t.Error("gimp should be drifted (not installed)")
	}
}

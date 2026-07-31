package services

import (
	"context"
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

func (f *fakeRunner) fail(name string, args ...string) {
	key := name + " " + strings.Join(args, " ")
	f.errs[key] = context.DeadlineExceeded
}

func TestDiscoverListsBothScopes(t *testing.T) {
	fr := newFakeRunner()
	fr.set("sshd.service enabled\n", "systemctl", listArgs("system")...)
	fr.set("podman.socket enabled\n", "systemctl", listArgs("user")...)

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

	if r, ok := byID["system/sshd.service"]; !ok {
		t.Error("missing system/sshd.service")
	} else if r.Attributes["scope"] != "system" || r.Confidence != core.ConfidenceHigh {
		t.Errorf("sshd.service = %+v", r)
	}
	if r, ok := byID["user/podman.socket"]; !ok {
		t.Error("missing user/podman.socket")
	} else if r.Attributes["scope"] != "user" {
		t.Errorf("podman.socket = %+v", r)
	}
}

func TestDiscoverSkipsUnavailableScope(t *testing.T) {
	fr := newFakeRunner()
	fr.set("sshd.service enabled\n", "systemctl", listArgs("system")...)
	fr.fail("systemctl", listArgs("user")...)

	p := newWithRunner(fr.run)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1 (user scope should be skipped, not fatal): %+v", len(resources), resources)
	}
}

func TestExportCarriesScope(t *testing.T) {
	p := newWithRunner(nil)
	resources := []core.Resource{
		{ID: "user/podman.socket", Attributes: map[string]any{"scope": "user"}},
	}
	exported, err := p.Export(context.Background(), "", resources)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(exported) != 1 || exported[0].Attributes["scope"] != "user" {
		t.Errorf("got %+v", exported)
	}
}

func TestPlanEnableAndDisable(t *testing.T) {
	p := newWithRunner(nil)

	desired := []core.ProjectResource{
		{ID: "user/podman.socket", Attributes: map[string]any{"scope": "user"}},
		{ID: "system/sshd.service", Attributes: map[string]any{"scope": "system"}},
	}
	current := []core.Resource{
		{ID: "system/sshd.service", Attributes: map[string]any{"scope": "system"}},
		{ID: "system/bluetooth.service", Attributes: map[string]any{"scope": "system"}},
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
	if byID["user/podman.socket"].Kind != core.ActionCreate {
		t.Errorf("podman.socket kind = %v, want create", byID["user/podman.socket"].Kind)
	}
	if byID["system/bluetooth.service"].Kind != core.ActionDelete {
		t.Errorf("bluetooth.service kind = %v, want delete", byID["system/bluetooth.service"].Kind)
	}
	if _, exists := byID["system/sshd.service"]; exists {
		t.Error("sshd.service should be a noop (already enabled)")
	}
}

func TestApplyUserScopeRunsWithoutSudo(t *testing.T) {
	fr := newFakeRunner()
	p := newWithRunner(fr.run)

	err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "user/podman.socket", Kind: core.ActionCreate,
		Attributes: map[string]any{"scope": "user"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	want := fakeCall{name: "systemctl", args: []string{"--user", "enable", "podman.socket"}}
	if len(fr.calls) != 1 || fr.calls[0].name != want.name || strings.Join(fr.calls[0].args, " ") != strings.Join(want.args, " ") {
		t.Errorf("calls = %+v, want %+v", fr.calls, want)
	}
}

func TestApplySystemScopeRunsWithSudo(t *testing.T) {
	fr := newFakeRunner()
	p := newWithRunner(fr.run)

	err := p.Apply(context.Background(), "", core.Action{
		ResourceID: "system/bluetooth.service", Kind: core.ActionDelete,
		Attributes: map[string]any{"scope": "system"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	want := fakeCall{name: "sudo", args: []string{"systemctl", "disable", "bluetooth.service"}}
	if len(fr.calls) != 1 || fr.calls[0].name != want.name || strings.Join(fr.calls[0].args, " ") != strings.Join(want.args, " ") {
		t.Errorf("calls = %+v, want %+v", fr.calls, want)
	}
}

func TestValidateReportsNotEnabled(t *testing.T) {
	fr := newFakeRunner()
	fr.set("sshd.service enabled\n", "systemctl", listArgs("system")...)
	fr.set("", "systemctl", listArgs("user")...)

	p := newWithRunner(fr.run)
	desired := []core.ProjectResource{
		{ID: "system/sshd.service", Attributes: map[string]any{"scope": "system"}},
		{ID: "user/podman.socket", Attributes: map[string]any{"scope": "user"}},
	}

	results, err := p.Validate(context.Background(), desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	byID := make(map[string]core.ValidationResult, len(results))
	for _, r := range results {
		byID[r.ResourceID] = r
	}
	if byID["system/sshd.service"].Drifted {
		t.Error("sshd.service should not be drifted")
	}
	if !byID["user/podman.socket"].Drifted {
		t.Error("podman.socket should be drifted (not enabled)")
	}
}

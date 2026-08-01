package systemconfigs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

// realQiiFixture mirrors the exact shape of real `pacman -Qii` output
// (verified against a live Arch machine during development): the first
// backup entry rides on the "Backup Files" label line itself, further
// entries are indented continuation lines with no label, packages are
// separated by a blank line, and a package with none declares "None".
const realQiiFixture = `Name            : base
Version         : 3-2
Backup Files    : None
Extended Data   : pkgtype=pkg

Name            : filesystem
Version         : 2025.01.28-1
Backup Files    : /etc/passwd [modified]
                  /etc/group [modified]
                  /etc/fstab [modified]
                  /etc/shells [unmodified]
Extended Data   : pkgtype=pkg

Name            : nginx
Version         : 1.27.0-1
Backup Files    : /etc/nginx/nginx.conf [modified]
                  /etc/nginx/fastcgi.conf [unmodified]
Extended Data   : pkgtype=pkg

Name            : sudo
Version         : 1.9.15-1
Backup Files    : /etc/sudoers [unreadable]
                  /etc/pam.d/sudo [unmodified]
Extended Data   : pkgtype=pkg
`

func TestParseBackupFiles(t *testing.T) {
	entries := parseBackupFiles(realQiiFixture)

	byPath := make(map[string]backupFile, len(entries))
	for _, e := range entries {
		byPath[e.path] = e
	}

	// "None" produces no entries.
	if _, ok := byPath["/etc/passwd"]; !ok {
		t.Error("missing /etc/passwd entry")
	}

	want := map[string]struct{ state, pkg string }{
		"/etc/passwd":             {"modified", "filesystem"},
		"/etc/group":              {"modified", "filesystem"},
		"/etc/fstab":              {"modified", "filesystem"},
		"/etc/shells":             {"unmodified", "filesystem"},
		"/etc/nginx/nginx.conf":   {"modified", "nginx"},
		"/etc/nginx/fastcgi.conf": {"unmodified", "nginx"},
		"/etc/sudoers":            {"unreadable", "sudo"},
		"/etc/pam.d/sudo":         {"unmodified", "sudo"},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for path, w := range want {
		got, ok := byPath[path]
		if !ok {
			t.Errorf("missing entry for %s", path)
			continue
		}
		if got.state != w.state || got.pkg != w.pkg {
			t.Errorf("%s = %+v, want state=%s pkg=%s", path, got, w.state, w.pkg)
		}
	}
}

func TestDiscoverReportsOnlyModifiedAndNotExcluded(t *testing.T) {
	dir := t.TempDir()
	nginxConf := filepath.Join(dir, "nginx.conf")
	if err := os.WriteFile(nginxConf, []byte("server {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fixture := `Name            : filesystem
Backup Files    : /etc/passwd [modified]
                  ` + nginxConf + ` [modified]
                  /nonexistent/path/for/this/test.conf [modified]
Extended Data   : pkgtype=pkg
`
	// /etc/passwd is excluded by path; the nonexistent path can't be read,
	// so it's naturally skipped by the read-error fallback; only the
	// fixture-backed real path should survive.
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		return fixture, nil
	}

	p := newWithRunner(run)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 1 || resources[0].ID != nginxConf {
		t.Fatalf("got %+v, want a single resource for %s", resources, nginxConf)
	}
	if resources[0].Confidence != core.ConfidenceHigh {
		t.Errorf("confidence = %v, want high", resources[0].Confidence)
	}
}

// emptyQiiFixture is `pacman -Qii` output for a system with nothing
// modified -- used by the drop-in-dir tests below, which only care about
// the second Discover pass.
const emptyQiiFixture = `Name            : base
Backup Files    : None
Extended Data   : pkgtype=pkg
`

func TestDiscoverIncludesUnownedDropInFiles(t *testing.T) {
	root := t.TempDir()
	dropDir := filepath.Join(root, "etc", "sddm.conf.d")
	if err := os.MkdirAll(dropDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dropDir, "custom.conf"), []byte("[Theme]\nCurrent=manual-theme\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// A directory entry sitting alongside the file must be skipped -- only
	// regular files are candidates (see the comment in Discover).
	if err := os.MkdirAll(filepath.Join(dropDir, "not-a-file.d"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	run := func(ctx context.Context, name string, args ...string) (string, error) {
		if args[0] == "-Qii" {
			return emptyQiiFixture, nil
		}
		// -Qo: nothing is package-owned in this fixture.
		return "", errors.New("No package owns " + args[1])
	}

	p := newWithRoot(run, root)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1: %+v", len(resources), resources)
	}

	want := "/etc/sddm.conf.d/custom.conf"
	if resources[0].ID != want {
		t.Errorf("ID = %q, want %q", resources[0].ID, want)
	}
	if resources[0].Confidence != core.ConfidenceHigh {
		t.Errorf("confidence = %v, want high", resources[0].Confidence)
	}
	if resources[0].Provenance.Source != "local-file" {
		t.Errorf("provenance source = %q, want local-file", resources[0].Provenance.Source)
	}
}

func TestDiscoverExcludesPackageOwnedDropInFiles(t *testing.T) {
	root := t.TempDir()
	dropDir := filepath.Join(root, "etc", "sddm.conf.d")
	if err := os.MkdirAll(dropDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dropDir, "shipped-by-package.conf"), []byte("[Theme]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	run := func(ctx context.Context, name string, args ...string) (string, error) {
		if args[0] == "-Qii" {
			return emptyQiiFixture, nil
		}
		return "sddm 0.20.0-1", nil // -Qo succeeds: owned
	}

	p := newWithRoot(run, root)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("got %+v, want no resources (file is package-owned)", resources)
	}
}

func TestDiscoverDropInDirMissingIsNotAnError(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		return emptyQiiFixture, nil
	}

	// newWithRunner points systemRoot at a path that doesn't exist, so
	// every KnownDropInDirs lookup fails with "not exist" -- Discover must
	// treat that as "nothing here", not propagate an error.
	p := newWithRunner(run)
	resources, err := p.Discover(context.Background(), core.SystemContext{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("got %+v, want no resources", resources)
	}
}

func TestExportAndApplyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	systemPath := filepath.Join(dir, "etc-nginx.conf") // stand-in for an /etc path
	if err := os.WriteFile(systemPath, []byte("server {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var calls []string
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}
	p := newWithRunner(run)
	ctx := context.Background()

	resources := []core.Resource{
		{ID: systemPath, Attributes: map[string]any{"content_hash": hashContent([]byte("server {}\n"))}},
	}
	projectDir := t.TempDir()
	exported, err := p.Export(ctx, projectDir, resources)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(exported) != 1 {
		t.Fatalf("got %d exported, want 1", len(exported))
	}

	got, err := os.ReadFile(filepath.Join(projectDir, "files", systemPath))
	if err != nil {
		t.Fatalf("reading exported file: %v", err)
	}
	if string(got) != "server {}\n" {
		t.Errorf("got %q", got)
	}

	action := core.Action{ResourceID: systemPath, Kind: core.ActionUpdate}
	if err := p.Apply(ctx, projectDir, action); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	wantCall := "sudo cp " + filepath.Join(projectDir, "files", systemPath) + " " + systemPath
	if len(calls) != 1 || calls[0] != wantCall {
		t.Errorf("calls = %+v, want [%q]", calls, wantCall)
	}
}

func TestPlanDetectsUpdateAndDelete(t *testing.T) {
	p := newWithRunner(nil)

	desired := []core.ProjectResource{
		{ID: "/etc/nginx/nginx.conf", Attributes: map[string]any{"content_hash": "want-this"}},
	}
	current := []core.Resource{
		{ID: "/etc/nginx/nginx.conf", Attributes: map[string]any{"content_hash": "have-this"}},
		{ID: "/etc/ssh/sshd_config", Attributes: map[string]any{"content_hash": "x"}},
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
	if byID["/etc/nginx/nginx.conf"].Kind != core.ActionUpdate {
		t.Errorf("nginx.conf kind = %v, want update", byID["/etc/nginx/nginx.conf"].Kind)
	}
	if byID["/etc/ssh/sshd_config"].Kind != core.ActionDelete {
		t.Errorf("sshd_config kind = %v, want delete", byID["/etc/ssh/sshd_config"].Kind)
	}
}

func TestApplyDeleteIsNoop(t *testing.T) {
	var calls int
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		calls++
		return "", nil
	}
	p := newWithRunner(run)

	err := p.Apply(context.Background(), "", core.Action{ResourceID: "/etc/nginx/nginx.conf", Kind: core.ActionDelete})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if calls != 0 {
		t.Errorf("delete should be a no-op (no way to reset to package default), got %d command calls", calls)
	}
}

func TestValidateDetectsMissingAndDrifted(t *testing.T) {
	dir := t.TempDir()
	unchanged := filepath.Join(dir, "unchanged.conf")
	if err := os.WriteFile(unchanged, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	drifted := filepath.Join(dir, "drifted.conf")
	if err := os.WriteFile(drifted, []byte("edited\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	missing := filepath.Join(dir, "missing.conf")

	p := newWithRunner(nil)
	desired := []core.ProjectResource{
		{ID: unchanged, Attributes: map[string]any{"content_hash": hashContent([]byte("original\n"))}},
		{ID: drifted, Attributes: map[string]any{"content_hash": hashContent([]byte("original\n"))}},
		{ID: missing, Attributes: map[string]any{"content_hash": "whatever"}},
	}

	results, err := p.Validate(context.Background(), desired)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	byID := make(map[string]core.ValidationResult, len(results))
	for _, r := range results {
		byID[r.ResourceID] = r
	}
	if byID[unchanged].Drifted {
		t.Error("unchanged should not be drifted")
	}
	if !byID[drifted].Drifted || byID[drifted].Detail != "content differs" {
		t.Errorf("drifted result = %+v", byID[drifted])
	}
	if !byID[missing].Drifted || byID[missing].Detail != "missing" {
		t.Errorf("missing result = %+v", byID[missing])
	}
}

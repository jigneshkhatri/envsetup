package registry

import (
	"context"
	"testing"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

// stubProvider is a minimal core.Provider that does nothing -- only Type()
// matters for these tests, which exercise All()'s ordering, not any
// provider's actual behavior.
type stubProvider struct{ typ string }

func (s *stubProvider) Type() string { return s.typ }
func (s *stubProvider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	return nil, nil
}
func (s *stubProvider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	return nil, nil
}
func (s *stubProvider) Plan(ctx context.Context, desired []core.ProjectResource, current []core.Resource) ([]core.Action, error) {
	return nil, nil
}
func (s *stubProvider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	return nil
}
func (s *stubProvider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	return nil, nil
}

// TestAllOrdersDotfilesLast guards the fix for a real bug: All() used to
// sort providers alphabetically by type name, which put "dotfiles" before
// "packages" -- so a plain `apply` wrote tracked dotfiles first and then let
// package installation (which can drop its own default config files under
// $HOME) silently overwrite them. dotfiles must always be the last provider
// Plan/Apply walks, regardless of registration order.
func TestAllOrdersDotfilesLast(t *testing.T) {
	// Registered in a deliberately adversarial order (alphabetical, the old
	// buggy behavior) to prove All() no longer just echoes registration or
	// alphabetical order back out.
	typesInRegistrationOrder := []string{
		"dotfiles", "flatpaks", "fonts", "git_repos",
		"packages", "recipes", "services", "system_configs", "themes",
	}

	r := New()
	for _, typ := range typesInRegistrationOrder {
		if err := r.Register(&stubProvider{typ: typ}); err != nil {
			t.Fatalf("registering %s: %v", typ, err)
		}
	}

	all := r.All()
	if len(all) != len(typesInRegistrationOrder) {
		t.Fatalf("All() returned %d providers, want %d", len(all), len(typesInRegistrationOrder))
	}

	last := all[len(all)-1].Type()
	if last != "dotfiles" {
		t.Fatalf("All()'s last provider = %q, want %q -- dotfiles must apply last so package/theme/etc. defaults never override tracked dotfiles", last, "dotfiles")
	}

	// packages must precede dotfiles: package installs are the most common
	// source of a default config file landing under $HOME after dotfiles
	// would otherwise have already been applied.
	packagesIdx, dotfilesIdx := -1, -1
	for i, p := range all {
		switch p.Type() {
		case "packages":
			packagesIdx = i
		case "dotfiles":
			dotfilesIdx = i
		}
	}
	if packagesIdx == -1 || dotfilesIdx == -1 {
		t.Fatalf("expected both packages and dotfiles in All(), got %v", typeNames(all))
	}
	if packagesIdx > dotfilesIdx {
		t.Fatalf("packages (index %d) must come before dotfiles (index %d), got order %v", packagesIdx, dotfilesIdx, typeNames(all))
	}
}

// TestAllOrdersUnknownTypesAfterKnownAlphabetically ensures a provider type
// with no explicit entry in applyOrder (e.g. one added by a future
// provider before this list is updated) still sorts deterministically,
// after every known type, rather than panicking or landing in an
// unpredictable position.
func TestAllOrdersUnknownTypesAfterKnownAlphabetically(t *testing.T) {
	r := New()
	for _, typ := range []string{"zzz_unknown", "packages", "aaa_unknown", "dotfiles"} {
		if err := r.Register(&stubProvider{typ: typ}); err != nil {
			t.Fatalf("registering %s: %v", typ, err)
		}
	}

	got := typeNames(r.All())
	want := []string{"packages", "dotfiles", "aaa_unknown", "zzz_unknown"}
	if len(got) != len(want) {
		t.Fatalf("All() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("All() = %v, want %v", got, want)
		}
	}
}

func typeNames(providers []core.Provider) []string {
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = p.Type()
	}
	return names
}

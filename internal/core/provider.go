package core

import "context"

// SystemContext carries information about the live machine that providers
// need for discovery, without hardcoding it into the Provider interface
// itself.
type SystemContext struct {
	HomeDir string
}

// ActionKind describes what Apply must do to reconcile a single resource.
type ActionKind int

const (
	ActionNoop ActionKind = iota
	ActionCreate
	ActionUpdate
	ActionDelete
)

func (k ActionKind) String() string {
	switch k {
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	case ActionDelete:
		return "delete"
	default:
		return "noop"
	}
}

// Action is one reconciling step produced by a provider's Plan.
type Action struct {
	ResourceType string
	ResourceID   string
	Kind         ActionKind
	// Description is a human-readable summary shown in plan/apply output.
	Description string
	// Attributes carries the desired end-state attributes for Create and
	// Update actions, so Apply is self-contained and doesn't need to
	// re-query the project. Nil for Delete and Noop.
	Attributes map[string]any
}

// ValidationResult reports whether a single desired resource still matches
// live system state.
type ValidationResult struct {
	ResourceType string
	ResourceID   string
	Drifted      bool
	Detail       string
}

// Provider teaches the core engine how to Discover, Export, Plan, Apply, and
// Validate one resource type. It is the project's single extensibility
// seam: the core knows nothing about pacman, dotfiles, or systemd -- only
// about this interface.
type Provider interface {
	// Type returns the resource type name this provider owns, e.g.
	// "package".
	Type() string

	// Discover scans the live system and returns every resource of this
	// type it can find, each tagged with a Confidence level. Never
	// modifies the system.
	Discover(ctx context.Context, sys SystemContext) ([]Resource, error)

	// Export converts discovered resources into their project (desired
	// state) representation. projectDir is the exported project's root
	// directory, for providers that must read or write raw file content
	// under its files/ tree (e.g. dotfiles) rather than just small
	// attributes; providers that don't need this may ignore it.
	Export(ctx context.Context, projectDir string, resources []Resource) ([]ProjectResource, error)

	// Plan diffs desired resources (from the project) against current
	// resources (from a fresh Discover) and returns the actions needed to
	// reconcile them. Most providers diff purely via attributes (e.g. a
	// content hash) computed by Discover and Export, without touching the
	// filesystem themselves -- but this isn't an interface guarantee: a
	// provider whose resource type has no other way to represent "current
	// state" (e.g. recipes, where an idempotency check command *is* the
	// state) may do real work here instead.
	Plan(ctx context.Context, desired []ProjectResource, current []Resource) ([]Action, error)

	// Apply executes a single action produced by Plan. projectDir is the
	// exported project's root directory, mirroring Export.
	Apply(ctx context.Context, projectDir string, action Action) error

	// Validate reports drift between desired resources and live system
	// state, without modifying anything.
	Validate(ctx context.Context, desired []ProjectResource) ([]ValidationResult, error)
}

// UserDeclaredProvider is implemented by a Provider whose resources are
// entirely hand-authored in the project rather than found by Discover --
// e.g. recipes, the escape hatch for anything automatic discovery can't
// model. The engine type-asserts for this and skips such providers during
// export, so hand-authored project entries are never overwritten by an
// (always empty) discovered list.
type UserDeclaredProvider interface {
	UserDeclared() bool
}

// Diagnosis is one health-check finding reported by `envsetup doctor`.
// Unlike ValidationResult, a Diagnosis isn't about drift from desired state
// -- it's a problem that would make drift detection or apply unreliable in
// the first place (a broken symlink, an unreachable remote, a package no
// longer available to install).
type Diagnosis struct {
	ResourceType string
	// ResourceID is empty for a project-wide finding not tied to one
	// resource (e.g. a schema problem).
	ResourceID string
	Message    string
}

// DoctorProvider is implemented by a Provider offering additional health
// diagnostics beyond drift detection. Optional: not every provider needs
// bespoke checks, so the engine type-asserts for this rather than making it
// part of the core Provider interface.
type DoctorProvider interface {
	// Doctor inspects desired resources of this type and reports any
	// problems found. Never modifies anything.
	Doctor(ctx context.Context, projectDir string, desired []ProjectResource) ([]Diagnosis, error)
}

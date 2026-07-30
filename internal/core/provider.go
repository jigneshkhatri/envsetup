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
	// reconcile them. Deliberately has no filesystem access of its own --
	// providers diff via attributes (e.g. a content hash) computed by
	// Discover and Export, not by re-reading files.
	Plan(ctx context.Context, desired []ProjectResource, current []Resource) ([]Action, error)

	// Apply executes a single action produced by Plan. projectDir is the
	// exported project's root directory, mirroring Export.
	Apply(ctx context.Context, projectDir string, action Action) error

	// Validate reports drift between desired resources and live system
	// state, without modifying anything.
	Validate(ctx context.Context, desired []ProjectResource) ([]ValidationResult, error)
}

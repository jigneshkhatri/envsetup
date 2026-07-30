// Package core defines the resource-agnostic types and the Provider
// interface that every EnvSetup resource type (packages, dotfiles, ...)
// plugs into. It has no knowledge of pacman, dotfiles, or systemd -- that
// knowledge lives in internal/providers/*.
package core

// Confidence expresses how certain discovery is that a detected resource is
// deliberate, user-intended system state. Anything below ConfidenceHigh must
// be flagged for review rather than silently exported -- EnvSetup never
// guesses.
type Confidence int

const (
	ConfidenceUnknown Confidence = iota
	ConfidenceLow
	ConfidenceMedium
	ConfidenceHigh
)

func (c Confidence) String() string {
	switch c {
	case ConfidenceHigh:
		return "high"
	case ConfidenceMedium:
		return "medium"
	case ConfidenceLow:
		return "low"
	default:
		return "unknown"
	}
}

// Provenance records where a resource originated, so exported state stays
// traceable back to its source.
type Provenance struct {
	// Source identifies the kind of origin, e.g. "pacman", "aur", "git",
	// "manual", "local-file".
	Source string
	// Origin holds source-specific detail: a repo URL, a package repo name,
	// etc. Optional.
	Origin string
}

// Resource is a single piece of system state as found on the live machine
// by a provider's Discover.
type Resource struct {
	Type       string
	ID         string
	Attributes map[string]any
	Provenance Provenance
	Confidence Confidence
}

// ProjectResource is a resource as declared in an exported project's
// desired state. It carries no discovery metadata (confidence/provenance)
// because it represents intent, not a live observation.
type ProjectResource struct {
	ID         string
	Attributes map[string]any
}

// Package registry holds the static, compiled-in table mapping resource-type
// names to the Provider implementation that owns them. Providers register
// themselves here; the core engine only ever talks to the registry, never
// to a specific provider package.
package registry

import (
	"fmt"
	"sort"
	"sync"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

// Registry maps resource-type name to the Provider that owns it.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]core.Provider
}

// applyOrder fixes the sequence All() returns providers in -- most
// importantly, the order Plan builds actions in and Apply executes them in.
// Every other provider runs before dotfiles: installing packages, enabling
// services, installing flatpaks/fonts, cloning git repos, running recipes,
// writing system-wide configs, and installing themes can all drop their own
// default configuration files under $HOME (e.g. a package's postinstall
// step, or a first-run default written the moment a tool is launched).
// Dotfiles must therefore be applied last, so a user's own tracked
// dotfiles always win over -- and are never silently overwritten by -- one
// of those defaults. Types not listed here (e.g. a future provider) sort
// alphabetically after every listed type.
var applyOrder = map[string]int{
	"packages":       0,
	"services":       1,
	"flatpaks":       2,
	"fonts":          3,
	"git_repos":      4,
	"recipes":        5,
	"system_configs": 6,
	"themes":         7,
	"dotfiles":       8,
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{providers: make(map[string]core.Provider)}
}

// Register adds a provider to the registry. It is an error to register two
// providers for the same resource type.
func (r *Registry) Register(p core.Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	typ := p.Type()
	if _, exists := r.providers[typ]; exists {
		return fmt.Errorf("registry: provider for type %q already registered", typ)
	}
	r.providers[typ] = p
	return nil
}

// Get returns the provider registered for typ, if any.
func (r *Registry) Get(typ string) (core.Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[typ]
	return p, ok
}

// All returns every registered provider in applyOrder -- deterministic
// across runs, and safe for Plan/Apply to execute directly: providers that
// can drop default configuration files always precede dotfiles.
func (r *Registry) All() []core.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.providers))
	for typ := range r.providers {
		types = append(types, typ)
	}
	sort.Slice(types, func(i, j int) bool {
		oi, iKnown := applyOrder[types[i]]
		oj, jKnown := applyOrder[types[j]]
		switch {
		case iKnown && jKnown:
			return oi < oj
		case iKnown:
			return true
		case jKnown:
			return false
		default:
			return types[i] < types[j]
		}
	})

	providers := make([]core.Provider, len(types))
	for i, typ := range types {
		providers[i] = r.providers[typ]
	}
	return providers
}

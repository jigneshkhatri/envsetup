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

// All returns every registered provider, ordered deterministically by type
// name so command output (scan, plan, ...) is stable across runs.
func (r *Registry) All() []core.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.providers))
	for typ := range r.providers {
		types = append(types, typ)
	}
	sort.Strings(types)

	providers := make([]core.Provider, len(types))
	for i, typ := range types {
		providers[i] = r.providers[typ]
	}
	return providers
}

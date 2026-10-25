package deployment

import (
	"fmt"
	"sort"
	"sync"

	core "github.com/multica-ai/multica/server/internal/deployment"
)

// Registry manages the set of available deployment provider adapters.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]core.Provider
}

// NewRegistry initializes an empty, thread-safe provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]core.Provider),
	}
}

// Register adds a provider adapter to the registry. Returns an error if a provider with the same kind is already registered.
func (r *Registry) Register(p core.Provider) error {
	if p == nil {
		return fmt.Errorf("cannot register nil provider")
	}

	kind := p.Kind()
	if kind == "" {
		return fmt.Errorf("provider kind cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.providers[kind]; exists {
		return fmt.Errorf("provider %q is already registered", kind)
	}

	r.providers[kind] = p
	return nil
}

// Get returns the registered provider adapter for the given kind.
func (r *Registry) Get(kind string) (core.Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", core.ErrProviderNotFound, kind)
	}

	return p, nil
}

// List returns the sorted list of registered provider kinds.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	kinds := make([]string, 0, len(r.providers))
	for k := range r.providers {
		kinds = append(kinds, k)
	}

	sort.Strings(kinds)
	return kinds
}

// EnsureCapability verifies that a provider supports the requested capability.
func (r *Registry) EnsureCapability(kind string, check func(core.CapabilitySet) bool) error {
	p, err := r.Get(kind)
	if err != nil {
		return err
	}

	caps := p.Capabilities()
	if !check(caps) {
		return fmt.Errorf("%w: provider %q does not support requested capability", core.ErrCapabilityUnsupported, kind)
	}

	return nil
}

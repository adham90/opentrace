package connector

import (
	"sync"

	"github.com/opentrace/opentrace/internal/agent"
)

// Registry manages active data source connectors.
type Registry struct {
	mu          sync.RWMutex
	connectors  map[ConnectorType]DataSource
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		connectors: make(map[ConnectorType]DataSource),
	}
}

// Register adds or replaces a connector. If a connector of the same type
// already exists, it is closed before being replaced.
func (r *Registry) Register(ds DataSource) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if old, exists := r.connectors[ds.Type()]; exists {
		old.Close()
	}
	r.connectors[ds.Type()] = ds
}

// Get returns the connector for the given type, or nil if not registered.
func (r *Registry) Get(ct ConnectorType) DataSource {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.connectors[ct]
}

// Unregister removes and closes the connector for the given type.
// Returns true if a connector was removed.
func (r *Registry) Unregister(ct ConnectorType) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	ds, exists := r.connectors[ct]
	if !exists {
		return false
	}
	ds.Close()
	delete(r.connectors, ct)
	return true
}

// CloseAll closes and removes all registered connectors.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for ct, ds := range r.connectors {
		ds.Close()
		delete(r.connectors, ct)
	}
}

// HasDataConnectors returns true if at least one data connector is registered.
func (r *Registry) HasDataConnectors() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.connectors) > 0
}

// AllTools aggregates tools from all registered connectors.
// Returns an empty slice (not nil) if no connectors are registered.
func (r *Registry) AllTools() []agent.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]agent.Tool, 0)
	for _, ds := range r.connectors {
		tools = append(tools, ds.Tools()...)
	}
	return tools
}

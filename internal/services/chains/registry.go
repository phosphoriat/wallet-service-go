package chains

import (
	"fmt"
	"sort"

	"wallet-service/internal/config"
	"wallet-service/internal/utils"
)

// Factory creates a chain for a gate from config.json.
type Factory func(gate config.Gate) (Chain, error)

// factories maps a chain type to its implementation. The key is gate.chain from config.json, which defaults to the gate name (e.g. "ethereum").
// To add a network, implement Chain and register its constructor here.
var factories = map[string]Factory{
	"ethereum": NewEthereum,
}

// Registry maps gate names to their chains.
// It is filled once at startup and only read afterwards, so concurrent Get calls from HTTP handlers are safe without locking.
type Registry struct {
	chains map[string]Chain
}

// NewRegistry creates a chain for every gate in the config.
//
// Initialization is all-or-nothing: if any gate has an unsupported chain type or fails to initialize (e.g. an invalid mnemonic),
// the chains created so far are closed and an error is returned, so the service does not start.
func NewRegistry(gates []config.Gate) (*Registry, error) {
	r := &Registry{chains: make(map[string]Chain, len(gates))}

	for _, g := range gates {
		factory, ok := factories[g.Chain]
		if !ok {
			r.Close()
			return nil, fmt.Errorf("gate %q: unsupported chain %q", g.Name, g.Chain)
		}
		chain, err := factory(g)
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("gate %q: %w", g.Name, err)
		}
		r.chains[g.Name] = chain
	}

	return r, nil
}

// Get returns the chain of the gate, or a NotFound error (HTTP 404)
// if the gate is not in config.json.
func (r *Registry) Get(gate string) (Chain, error) {
	c, ok := r.chains[gate]
	if !ok {
		return nil, utils.NotFound("unknown gate %q", gate)
	}

	return c, nil
}

// Names returns the sorted list of gate names (used for startup logging)
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.chains))

	for n := range r.chains {
		names = append(names, n)
	}
	sort.Strings(names)

	return names
}

// Close releases all chains (wallet-core resources) and empties the registry.
// Must be called once on shutdown, when no requests are being served.
func (r *Registry) Close() {
	for name, c := range r.chains {
		c.Close()
		delete(r.chains, name)
	}
}

package llm

import "fmt"

// Registry holds registered LLM drivers keyed by model name.
type Registry struct {
	drivers map[string]Driver
}

func NewRegistry() *Registry {
	return &Registry{drivers: make(map[string]Driver)}
}

func (r *Registry) Register(d Driver) {
	r.drivers[d.Name()] = d
}

func (r *Registry) Lookup(name string) (Driver, error) {
	d, ok := r.drivers[name]
	if !ok {
		return nil, fmt.Errorf("no driver registered for model %q", name)
	}
	return d, nil
}

// Names returns all registered model names.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.drivers))
	for k := range r.drivers {
		out = append(out, k)
	}
	return out
}

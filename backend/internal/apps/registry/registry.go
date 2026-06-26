package registry

import (
	"sort"
	"sync"
)

var (
	factoriesMu sync.RWMutex
	factories   = map[string]Factory{}
)

type Registry struct {
	modules map[string]Module
}

func New(modules ...Module) *Registry {
	r := &Registry{modules: map[string]Module{}}
	for _, module := range modules {
		r.Register(module)
	}
	return r
}

func RegisterFactory(name string, factory Factory) {
	if name == "" || factory == nil {
		return
	}
	factoriesMu.Lock()
	defer factoriesMu.Unlock()
	factories[name] = factory
}

func NewFromRegistered(deps Dependencies) *Registry {
	factoriesMu.RLock()
	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	sort.Strings(names)
	local := make([]Factory, 0, len(names))
	for _, name := range names {
		local = append(local, factories[name])
	}
	factoriesMu.RUnlock()

	r := New()
	for _, factory := range local {
		r.Register(factory(deps))
	}
	return r
}

func RegisteredFactoryNames() []string {
	factoriesMu.RLock()
	defer factoriesMu.RUnlock()
	out := make([]string, 0, len(factories))
	for name := range factories {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) Register(module Module) {
	if module == nil || module.Name() == "" {
		return
	}
	r.modules[module.Name()] = module
}

func (r *Registry) Get(name string) (Module, bool) {
	module, ok := r.modules[name]
	return module, ok
}

func (r *Registry) Modules() []Module {
	out := make([]Module, 0, len(r.modules))
	for _, module := range r.modules {
		out = append(out, module)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

func (r *Registry) Manifests(lang string) []Manifest {
	modules := r.Modules()
	out := make([]Manifest, 0, len(modules))
	for _, module := range modules {
		out = append(out, module.Manifest(lang))
	}
	return out
}

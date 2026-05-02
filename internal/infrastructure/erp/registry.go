package erp

import (
	"fmt"
	"sort"
	"sync"
)

// Factory constrói um Provider a partir de um mapa de configuração.
// O caller (worker/server) passa o sub-mapa relevante de config.yaml.
type Factory func(config map[string]any) (Provider, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register inscreve um plugin no registry. Chamado em init() do pacote do plugin.
//
// Panica em duplicata para falhar barulhento durante desenvolvimento (nunca
// dois plugins com mesmo slug no mesmo binário).
func Register(slug string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := factories[slug]; exists {
		panic(fmt.Sprintf("erp: plugin %q já registrado", slug))
	}
	factories[slug] = factory
}

// New instancia um plugin pelo slug. Caller decide se erro = config ruim
// ou plugin não compilado neste binário (por blank import ausente).
func New(slug string, config map[string]any) (Provider, error) {
	mu.RLock()
	factory, ok := factories[slug]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("erp: plugin %q não encontrado (esqueceu blank import?)", slug)
	}
	return factory(config)
}

// List devolve os slugs registrados, ordenados — útil para UI/admin.
func List() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

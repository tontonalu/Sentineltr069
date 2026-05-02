package integration

import (
	"sync"
	"time"
)

// StatusTracker mantém em memória o último estado de sync de cada plugin —
// alimenta a UI /integrations sem precisar tocar no banco. Thread-safe.
//
// Quando precisarmos de histórico, migramos para uma tabela `integration_runs`.
type StatusTracker struct {
	mu       sync.RWMutex
	statuses map[string]*PluginStatus
}

func NewStatusTracker() *StatusTracker {
	return &StatusTracker{statuses: map[string]*PluginStatus{}}
}

// PluginStatus é o que a UI consome.
type PluginStatus struct {
	Slug          string
	LastRunAt     time.Time
	LastSuccessAt time.Time
	LastErrorAt   time.Time
	LastError     string
	LastResult    *SyncResult
	Healthy       bool
}

// Record é chamado pelo ERPSyncService após cada Tick.
// err nil = sucesso completo. err não-nil = falha (com result parcial).
func (t *StatusTracker) Record(slug string, result *SyncResult, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	st, ok := t.statuses[slug]
	if !ok {
		st = &PluginStatus{Slug: slug}
		t.statuses[slug] = st
	}

	st.LastRunAt = time.Now()
	st.LastResult = result

	if err != nil {
		st.LastErrorAt = st.LastRunAt
		st.LastError = err.Error()
		st.Healthy = false
		return
	}
	st.LastSuccessAt = st.LastRunAt
	st.LastError = ""
	st.Healthy = true
}

// Get retorna cópia do status (para evitar mutação externa).
func (t *StatusTracker) Get(slug string) *PluginStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if s, ok := t.statuses[slug]; ok {
		cpy := *s
		return &cpy
	}
	return nil
}

// All devolve todos os statuses, ordenados por slug.
func (t *StatusTracker) All() []PluginStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]PluginStatus, 0, len(t.statuses))
	for _, s := range t.statuses {
		out = append(out, *s)
	}
	return out
}

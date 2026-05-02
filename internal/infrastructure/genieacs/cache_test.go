package genieacs

import (
	"testing"
)

func TestWithCacheNilClient(t *testing.T) {
	c := New("http://x", "", "")
	c.WithCache(nil, 30)
	if c.cache != nil {
		t.Error("redis nil deve desligar cache")
	}
}

// Testes de cache hit/miss exigem Redis real ou mock — fica para
// integration tests com testcontainers.

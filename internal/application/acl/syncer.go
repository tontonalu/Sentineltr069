// Package acl coordena a publicação dos CIDRs autorizados para a porta
// TR-069/CWMP. O worker grava o arquivo /var/cwmp-acl/cidrs.txt; um
// systemd path-unit no host detecta a mudança e reconcilia iptables.
package acl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

// Syncer escreve a lista canônica de CIDRs num arquivo. Vê provisioning.ACLRepository
// como única fonte da verdade — o arquivo é só um espelho que o systemd consome.
type Syncer struct {
	repo     prov.ACLRepository
	filePath string
}

// NewSyncer cria um novo syncer. filePath deve ser um caminho absoluto onde
// o worker tem permissão de escrita (volume montado do host).
func NewSyncer(repo prov.ACLRepository, filePath string) *Syncer {
	return &Syncer{repo: repo, filePath: filePath}
}

// Tick lê do banco, gera o conteúdo do arquivo, e grava de forma atômica
// (tmpfile + rename — assim o systemd path-unit nunca lê um arquivo parcial).
func (s *Syncer) Tick(ctx context.Context) error {
	log := logger.FromContext(ctx)

	entries, err := s.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("acl: list cidrs: %w", err)
	}

	// Ordena para que escritas com a mesma lista produzam o mesmo arquivo,
	// evitando reconciliações ruidosas (path-unit dispara em PathChanged).
	cidrs := make([]string, 0, len(entries))
	for _, e := range entries {
		cidrs = append(cidrs, e.CIDR.String())
	}
	sort.Strings(cidrs)

	var buf []byte
	for _, c := range cidrs {
		buf = append(buf, c...)
		buf = append(buf, '\n')
	}

	if len(cidrs) == 0 {
		log.Warn("acl tick: lista vazia — enforcement vai bloquear todos os CPEs")
	}

	dir := filepath.Dir(s.filePath)
	tmp, err := os.CreateTemp(dir, ".cidrs-*.tmp")
	if err != nil {
		return fmt.Errorf("acl: create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // se o rename falhar, não deixa lixo

	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("acl: write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("acl: close tmp: %w", err)
	}
	// 0600: o reconciler systemd roda como root e ignora as permissões;
	// não há outro consumidor legítimo do arquivo no host.
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("acl: chmod: %w", err)
	}

	// Curto-circuito: se o conteúdo já bate, evita o rename (que dispara o
	// path-unit). Reduz reconciliações desnecessárias quando nada mudou.
	if cur, err := os.ReadFile(s.filePath); err == nil && bytesEqual(cur, buf) {
		return nil
	}

	if err := os.Rename(tmpName, s.filePath); err != nil {
		return fmt.Errorf("acl: rename: %w", err)
	}

	log.Info("acl tick", "cidrs", len(cidrs), "path", s.filePath)
	return nil
}

// Run executa Tick em loop até o contexto ser cancelado. Primeiro tick
// é imediato; subsequentes a cada interval.
func (s *Syncer) Run(ctx context.Context, interval time.Duration) {
	if err := s.Tick(ctx); err != nil {
		logger.FromContext(ctx).Error("acl initial tick", "err", err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Tick(ctx); err != nil {
				logger.FromContext(ctx).Error("acl tick", "err", err)
			}
		}
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package provisioning

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// ACLCIDR — uma faixa autorizada a alcançar a porta TR-069/CWMP. A enforcement
// real é no kernel (iptables) gerada a partir desta lista; aqui é só fonte da
// verdade.
type ACLCIDR struct {
	ID          uuid.UUID
	CIDR        netip.Prefix
	Description string
	CreatedBy   *uuid.UUID
	CreatedAt   time.Time
}

// ACLRepository — CRUD da lista de CIDRs autorizados. Não há Update porque o
// fluxo natural do operador é remover e re-adicionar (CIDR é a chave natural).
type ACLRepository interface {
	List(ctx context.Context) ([]ACLCIDR, error)
	Create(ctx context.Context, e *ACLCIDR) error
	Delete(ctx context.Context, id uuid.UUID) error
}

var (
	// ErrCIDRDuplicate retornado quando UNIQUE(cidr) é violada.
	ErrCIDRDuplicate = errors.New("acl: CIDR já cadastrado")
	// ErrCIDRNotFound retornado quando Delete não encontra.
	ErrCIDRNotFound = errors.New("acl: CIDR não encontrado")
	// ErrCIDRInvalid retornado quando o input não é um CIDR válido.
	ErrCIDRInvalid = errors.New("acl: CIDR inválido")
)

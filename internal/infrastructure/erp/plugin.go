// Package erp define a interface comum para integrações com ERPs externos
// (Voalle, IXC, MK-Auth, SGP, etc) e um registry de auto-registro.
//
// Princípios (do SentinelACS.md §9.1):
//
//  1. Isolamento total — cada plugin é um pacote Go separado em
//     internal/infrastructure/erp/<vendor>.
//  2. Interface única — todos os plugins implementam Provider.
//  3. Auto-registro — plugins se registram via init() em uma `registry`.
//  4. Configuração por plugin — credenciais e endpoints próprios.
//  5. Falha isolada — erro num plugin não derruba o sistema.
//  6. Versionamento — cada plugin tem seu próprio mapeamento ERP → canônico.
//
// Esta primeira versão (Fase 2.5) cobre apenas SyncCustomers + GetCustomerByID
// (read-only). Block/Unblock/Webhook entram na Fase 6 (Voalle Completo).
package erp

import (
	"context"
	"time"
)

// ProviderInfo descreve o plugin para a UI de /integrations.
type ProviderInfo struct {
	Slug         string       // 'voalle', 'ixc', 'mkauth', 'sgp'
	DisplayName  string       // 'Voalle', 'IXC Provedor', etc.
	Version      string       // SemVer do plugin (não do ERP)
	Author       string       // Quem mantém este plugin
	Capabilities []Capability // O que este plugin sabe fazer
}

// Capability — feature flag declarado pelo plugin. UI usa isso para decidir
// quais botões/abas mostrar (ex: só pluga "Bloquear cliente" se o plugin diz
// que suporta).
type Capability string

const (
	CapSyncCustomers   Capability = "sync_customers"
	CapSyncContracts   Capability = "sync_contracts"
	CapWebhookIncoming Capability = "webhook_incoming"
	CapBlockCustomer   Capability = "block_customer"
	CapUnblockCustomer Capability = "unblock_customer"
)

// Has checa se o plugin declara suportar uma capability.
func (i ProviderInfo) Has(c Capability) bool {
	for _, x := range i.Capabilities {
		if x == c {
			return true
		}
	}
	return false
}

// Customer é o modelo canônico que cada plugin retorna após mapear seu schema.
// Diferenças entre Voalle/IXC/etc ficam isoladas no plugin.
type Customer struct {
	ExternalID string
	FullName   string
	Document   string // CPF/CNPJ
	PPPoELogin string
	PlanName   string
	Address    string
	Status     string         // active | suspended | cancelled (canônico)
	Metadata   map[string]any // espaço livre — campos do ERP que não estão no canônico
}

// SyncOptions parametriza um sync incremental.
//
// Since pede só registros modificados após esse instante. Cursor é um token
// opaco que o plugin define — caller passa o NextCursor recebido na resposta
// anterior para paginar (cursor-based) ou nil (page-based via PageSize).
type SyncOptions struct {
	Since      *time.Time
	PageSize   int
	Pagination *Cursor
}

// Cursor é um token opaco — o conteúdo é definido pelo plugin (page number,
// continuation token, último ID processado, etc).
type Cursor struct {
	Token string
}

// SyncResult é o que SyncCustomers retorna.
type SyncResult struct {
	Customers  []Customer
	NextCursor *Cursor
	HasMore    bool
}

// WebhookEvent é entregue pelo plugin após parsear um webhook entrante.
// Type usa o vocabulário canônico: customer.created, customer.cancelled,
// customer.suspended, contract.plan_changed.
type WebhookEvent struct {
	Type       string
	ExternalID string
	Data       map[string]any
}

// Provider é a interface comum de todos os plugins ERP.
//
// Nem todo plugin implementa todos os métodos — verificar via
// Info().Capabilities. Métodos não suportados devem retornar
// ErrCapabilityUnsupported.
type Provider interface {
	Info() ProviderInfo
	HealthCheck(ctx context.Context) error

	// Read
	SyncCustomers(ctx context.Context, opts SyncOptions) (*SyncResult, error)
	GetCustomerByID(ctx context.Context, externalID string) (*Customer, error)

	// Write — opcional (CapBlockCustomer/CapUnblockCustomer)
	BlockCustomer(ctx context.Context, externalID string, reason string) error
	UnblockCustomer(ctx context.Context, externalID string) error

	// Webhook — opcional (CapWebhookIncoming)
	HandleWebhook(ctx context.Context, payload []byte, headers map[string]string) (*WebhookEvent, error)
}

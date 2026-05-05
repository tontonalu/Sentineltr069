// Package homologation contém as entidades do wizard de homologação de
// templates TR-069: Session (carrinho do operador), Mapping (canonical_key
// ↔ tr_path com resultado dos testes), CanonicalKey (catálogo padronizado)
// e ModelHomologation (registro auditável de profile homologado por modelo).
package homologation

import (
	"time"

	"github.com/google/uuid"

	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
)

// SessionStatus — máquina de estados do wizard.
type SessionStatus string

const (
	SessionDraft     SessionStatus = "draft"
	SessionProbing   SessionStatus = "probing"
	SessionTesting   SessionStatus = "testing"
	SessionCompleted SessionStatus = "completed"
	SessionAbandoned SessionStatus = "abandoned"
)

// Valid retorna true se o status é um dos cinco aceitos no CHECK do banco.
func (s SessionStatus) Valid() bool {
	switch s {
	case SessionDraft, SessionProbing, SessionTesting, SessionCompleted, SessionAbandoned:
		return true
	}
	return false
}

// IsActive marca os estados que bloqueiam outra sessão paralela no mesmo
// device — espelha o índice único parcial em homologation_sessions.
func (s SessionStatus) IsActive() bool {
	switch s {
	case SessionDraft, SessionProbing, SessionTesting:
		return true
	}
	return false
}

// TestStatus — resultado de read/write em um mapping.
type TestStatus string

const (
	TestPending TestStatus = "pending"
	TestOK      TestStatus = "ok"
	TestFail    TestStatus = "fail"
	TestSkipped TestStatus = "skipped"
)

// Valid retorna true se o status é um dos quatro aceitos no CHECK do banco.
func (t TestStatus) Valid() bool {
	switch t {
	case TestPending, TestOK, TestFail, TestSkipped:
		return true
	}
	return false
}

// Categories — enum para canonical_keys.category.
const (
	CategoryWiFi   = "wifi"
	CategoryWAN    = "wan"
	CategoryLAN    = "lan"
	CategoryMgmt   = "mgmt"
	CategoryDevice = "device"
	CategoryVoice  = "voice"
	CategoryOther  = "other"
)

// HomologationStatus — estado de um registro de homologação por modelo.
type HomologationStatus string

const (
	StatusHomologated HomologationStatus = "homologated"
	StatusDeprecated  HomologationStatus = "deprecated"
)

// Valid devolve true se está em um dos dois estados aceitos.
func (h HomologationStatus) Valid() bool {
	return h == StatusHomologated || h == StatusDeprecated
}

// CanonicalKey — entrada do catálogo. Hint paths alimentam o auto-mapeamento.
type CanonicalKey struct {
	ID                uuid.UUID
	Key               string
	LabelPT           string
	Description       string
	Category          string
	SuggestedDataType tmpl.DataType
	DefaultIsSecret   bool
	HintPathsTR098    []string
	HintPathsTR181    []string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Session — agregado de curta duração que carrega o estado do wizard.
//
// Vida do agregado:
//   draft   → operador acabou de criar, sem árvore ainda.
//   probing → Probe foi disparado, aguardando refresh do GenieACS.
//   testing → árvore disponível, operador está mapeando e testando.
//   completed → profile foi gerado e marcado homologado para o model_id.
//   abandoned → operador desistiu (manual ou por timeout futuro).
type Session struct {
	ID                 uuid.UUID
	LabDeviceID        uuid.UUID
	ModelID            uuid.UUID
	Status             SessionStatus
	CreatedBy          *uuid.UUID
	TreeSnapshot       []byte // JSONB serializado do Device.Raw após Probe
	Notes              string
	StartedAt          time.Time
	FinishedAt         *time.Time
	GeneratedProfileID *uuid.UUID

	Mappings []Mapping
}

// Mapping — uma linha (canonical_key escolhida) ↔ (tr_path no device de lab),
// carregando o resultado dos testes para auditoria e filtragem no Complete.
type Mapping struct {
	ID             uuid.UUID
	SessionID      uuid.UUID
	CanonicalKey   string
	TRPath         string
	ValueTemplate  string
	DataType       tmpl.DataType
	IsSecret       bool
	SortOrder      int
	ReadStatus     TestStatus
	WriteStatus    TestStatus
	ReadValue      *string
	WriteTestValue *string
	LastError      *string
	TestedAt       *time.Time
}

// EligibleForProfile devolve true se o mapping pode entrar no profile gerado:
// read precisa ter passado, e write precisa ser ok ou explicitamente skipped
// (skipped cobre is_secret=true, onde escrever apaga o valor original).
func (m Mapping) EligibleForProfile() bool {
	return m.ReadStatus == TestOK && (m.WriteStatus == TestOK || m.WriteStatus == TestSkipped)
}

// ModelHomologation — carimbo auditável: "este profile foi homologado para
// este modelo, via esta sessão, por este usuário, neste momento".
type ModelHomologation struct {
	ID                uuid.UUID
	ModelID           uuid.UUID
	ProfileID         uuid.UUID
	SessionID         *uuid.UUID
	HomologatedBy     *uuid.UUID
	HomologatedAt     time.Time
	Status            HomologationStatus
	DeprecatedAt      *time.Time
	DeprecatedReason  string
}

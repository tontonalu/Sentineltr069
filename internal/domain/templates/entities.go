// Package templates contém as entidades canônicas de profiles de configuração.
//
// Um Profile agrupa N Parameters que mapeiam canonical_key (chave de negócio)
// para tr_path (caminho CWMP específico do modelo) com value_template Pongo2.
package templates

import (
	"time"

	"github.com/google/uuid"
)

// DataType — tipos suportados pelo NBI no campo parameterValues.
// Mantemos essa constante separada do XSD literal para evitar typos
// nos handlers; a conversão para "xsd:string" é feita no engine.
type DataType string

const (
	DataTypeString      DataType = "string"
	DataTypeInt         DataType = "int"
	DataTypeBool        DataType = "bool"
	DataTypeUnsignedInt DataType = "unsignedInt"
	DataTypeDateTime    DataType = "dateTime"
)

// XSD devolve o literal "xsd:..." que o NBI espera no SetParameterValues.
func (d DataType) XSD() string {
	switch d {
	case DataTypeInt:
		return "xsd:int"
	case DataTypeBool:
		return "xsd:boolean"
	case DataTypeUnsignedInt:
		return "xsd:unsignedInt"
	case DataTypeDateTime:
		return "xsd:dateTime"
	default:
		return "xsd:string"
	}
}

// Valid retorna true se a DataType é uma das constantes conhecidas.
func (d DataType) Valid() bool {
	switch d {
	case DataTypeString, DataTypeInt, DataTypeBool, DataTypeUnsignedInt, DataTypeDateTime:
		return true
	}
	return false
}

// Profile é o agregado raiz — um conjunto versionado de parâmetros para um
// vendor/modelo (ou genérico). VendorID/ModelID NULL = aplicável a qualquer.
type Profile struct {
	ID          uuid.UUID
	Name        string
	Description string
	VendorID    *uuid.UUID
	ModelID     *uuid.UUID
	Version     int
	IsActive    bool
	CreatedBy   *uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time

	Parameters []Parameter
}

// Parameter — uma linha do profile. canonical_key é a chave de negócio
// estável; tr_path é específico do modelo. value_template usa sintaxe Pongo2.
type Parameter struct {
	ID            uuid.UUID
	ProfileID     uuid.UUID
	CanonicalKey  string
	TRPath        string
	ValueTemplate string
	DataType      DataType
	IsSecret      bool
	SortOrder     int
}

// HistoryEntry — snapshot append-only de uma versão do profile.
// snapshot é serializado JSON do profile + parameters no momento do save.
type HistoryEntry struct {
	ID         int64
	ProfileID  uuid.UUID
	Version    int
	Snapshot   []byte
	ChangedBy  *uuid.UUID
	ChangeNote string
	CreatedAt  time.Time
}

// ResolvedParameter — parâmetro com valor já renderizado pelo engine, pronto
// para virar SetParameterValues no NBI.
type ResolvedParameter struct {
	CanonicalKey string
	TRPath       string
	Value        any
	DataType     DataType
	IsSecret     bool
}

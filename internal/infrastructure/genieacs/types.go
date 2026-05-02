package genieacs

import "time"

// Device é a representação parsed de um device retornado pelo NBI.
// Raw guarda o JSON original — útil para extrair virtual params dinamicamente
// sem precisar definir struct para cada parâmetro.
type Device struct {
	ID         string         `json:"_id"`
	LastInform time.Time      `json:"_lastInform"`
	LastBoot   *time.Time     `json:"_lastBoot,omitempty"`
	Tags       []string       `json:"_tags,omitempty"`
	Raw        map[string]any `json:"-"`
}

// TaskID identifica uma task assíncrona criada via NBI.
type TaskID string

// Task representa uma operação CWMP enfileirada para um CPE.
//
// O NBI retorna apenas alguns campos quando consultado; ao criar, enviamos
// só name + parâmetros específicos. Usamos tags com omitempty para não
// poluir o JSON.
type Task struct {
	ID        string         `json:"_id,omitempty"`
	Device    string         `json:"device,omitempty"`
	Name      string         `json:"name"`
	Timestamp time.Time      `json:"timestamp,omitempty"`
	Fault     map[string]any `json:"fault,omitempty"`
	Retries   int            `json:"retries,omitempty"`

	// setParameterValues
	ParameterValues [][]any `json:"parameterValues,omitempty"`

	// getParameterValues / refreshObject
	ParameterNames []string `json:"parameterNames,omitempty"`
	ObjectName     string   `json:"objectName,omitempty"`

	// download (firmware/config push)
	FileType string `json:"fileType,omitempty"`
	FileName string `json:"fileName,omitempty"`
	URL      string `json:"url,omitempty"`
}

// Parameter — tupla path/value/type para SetParameterValues.
//
// Type segue o vocabulário XSD que o NBI espera:
//   - "xsd:string"
//   - "xsd:int"
//   - "xsd:boolean"
//   - "xsd:dateTime"
//   - "xsd:unsignedInt"
type Parameter struct {
	Path  string
	Value any
	Type  string
}

// Tuple devolve a representação que o NBI espera no campo parameterValues.
func (p Parameter) Tuple() []any {
	if p.Type == "" {
		return []any{p.Path, p.Value}
	}
	return []any{p.Path, p.Value, p.Type}
}

// Query — filtros para listar devices via NBI.
//
// Filter aceita o dialeto Mongo do GenieACS (ex: {"_lastInform": {"$gte": "..."}}).
// Projection é uma lista de paths a incluir/excluir.
type Query struct {
	Filter     map[string]any
	Projection []string
	Limit      int
	Skip       int
	Sort       map[string]int
}

// Fault — estrutura padrão de erro de provisioning retornada nas tasks.
type Fault struct {
	Code      string         `json:"faultCode"`
	String    string         `json:"faultString"`
	Path      string         `json:"path,omitempty"`
	Detail    map[string]any `json:"detail,omitempty"`
	Timestamp time.Time      `json:"timestamp,omitempty"`
}

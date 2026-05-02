package alerting

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Condition é a DSL declarativa. Mantemos minimalista — apenas o suficiente
// para os 3 casos de uso do MVP:
//
//  1. device_offline — N+ devices ficaram offline (count_pct > X em uma janela)
//  2. cpu_high — média de CPU > X em janela Y
//  3. wifi_clients_drop — clientes Wi-Fi caíram > X% em janela Y
//
// Usar uma DSL ad-hoc evita amarração com bibliotecas exóticas. Quando o
// requisito virar mais rico (joins, NOT, OR), trocamos por CEL ou similar.
//
// JSON canônico:
//
//	{
//	  "type": "aggregate",
//	  "metric": "device_status",
//	  "filter": {"pop_id": "...", "status": "offline"},
//	  "aggregation": "count_pct",
//	  "operator": ">",
//	  "threshold": 10,
//	  "window": "5m"
//	}
type Condition struct {
	Type        ConditionType   `json:"type"`
	Metric      Metric          `json:"metric"`
	Filter      ConditionFilter `json:"filter,omitempty"`
	Aggregation Aggregation     `json:"aggregation"`
	Operator    Operator        `json:"operator"`
	Threshold   float64         `json:"threshold"`
	Window      string          `json:"window"` // "5m" | "1h" | "30s"
}

type ConditionType string

const (
	TypeAggregate ConditionType = "aggregate"
	TypeSingle    ConditionType = "single" // 1 device, sem agregação ("este device CPU > 90 nos últimos 5min")
)

// Metric — fontes de dados. Cada uma tem seu próprio agregador no engine.
type Metric string

const (
	MetricDeviceStatus     Metric = "device_status"      // online/offline (do devices.status)
	MetricCPUPct           Metric = "cpu_pct"            // de telemetry_system
	MetricMemPct           Metric = "mem_pct"
	MetricWifiClients      Metric = "wifi_clients"       // de telemetry_wifi
	MetricOpticalRxDBM     Metric = "optical_rx_dbm"     // de telemetry_wan
	MetricUptimeSeconds    Metric = "uptime_seconds"
)

func (m Metric) Valid() bool {
	switch m {
	case MetricDeviceStatus, MetricCPUPct, MetricMemPct,
		MetricWifiClients, MetricOpticalRxDBM, MetricUptimeSeconds:
		return true
	}
	return false
}

// Aggregation — função aplicada na janela.
type Aggregation string

const (
	AggCount    Aggregation = "count"     // quantidade de matches
	AggCountPct Aggregation = "count_pct" // percentual sobre total
	AggAvg      Aggregation = "avg"
	AggMax      Aggregation = "max"
	AggMin      Aggregation = "min"
	AggLast     Aggregation = "last"      // última amostra (single device)
)

func (a Aggregation) Valid() bool {
	switch a {
	case AggCount, AggCountPct, AggAvg, AggMax, AggMin, AggLast:
		return true
	}
	return false
}

// Operator — comparador binário aplicado ao resultado da agregação vs threshold.
type Operator string

const (
	OpGT  Operator = ">"
	OpGTE Operator = ">="
	OpLT  Operator = "<"
	OpLTE Operator = "<="
	OpEQ  Operator = "=="
	OpNEQ Operator = "!="
)

func (o Operator) Valid() bool {
	switch o {
	case OpGT, OpGTE, OpLT, OpLTE, OpEQ, OpNEQ:
		return true
	}
	return false
}

// Eval aplica o operador a um resultado. Usado pelo engine após agregar.
func (o Operator) Eval(value, threshold float64) bool {
	switch o {
	case OpGT:
		return value > threshold
	case OpGTE:
		return value >= threshold
	case OpLT:
		return value < threshold
	case OpLTE:
		return value <= threshold
	case OpEQ:
		return value == threshold
	case OpNEQ:
		return value != threshold
	}
	return false
}

// ConditionFilter — restringe o universo de samples antes da agregação.
// Todos os campos são opcionais (filtro vazio = sem restrição).
type ConditionFilter struct {
	POPID      *uuid.UUID `json:"pop_id,omitempty"`
	VendorID   *uuid.UUID `json:"vendor_id,omitempty"`
	ModelID    *uuid.UUID `json:"model_id,omitempty"`
	DeviceID   *uuid.UUID `json:"device_id,omitempty"`
	Status     string     `json:"status,omitempty"`
	Tag        string     `json:"tag,omitempty"`
	Band       string     `json:"band,omitempty"` // 2.4G | 5G (para wifi_clients)
}

// Validate retorna erro se a regra é semântica/sintaticamente inválida.
// Não checa se a janela tem dados suficientes — isso é problema de runtime.
func (c Condition) Validate() error {
	if c.Type != TypeAggregate && c.Type != TypeSingle {
		return fmt.Errorf("condition.type %q inválido (use aggregate|single)", c.Type)
	}
	if !c.Metric.Valid() {
		return fmt.Errorf("condition.metric %q desconhecido", c.Metric)
	}
	if !c.Aggregation.Valid() {
		return fmt.Errorf("condition.aggregation %q desconhecido", c.Aggregation)
	}
	if !c.Operator.Valid() {
		return fmt.Errorf("condition.operator %q inválido", c.Operator)
	}
	if c.Window == "" {
		return errors.New("condition.window obrigatório (ex: '5m', '1h')")
	}
	if _, err := ParseWindow(c.Window); err != nil {
		return err
	}
	if c.Type == TypeSingle && c.Filter.DeviceID == nil {
		return errors.New("condition.type=single exige filter.device_id")
	}
	if c.Aggregation == AggCountPct && c.Metric != MetricDeviceStatus {
		return fmt.Errorf("aggregation count_pct só faz sentido para metric=device_status")
	}
	return nil
}

// ParseWindow — aceita sufixos s|m|h|d. Retorna time.Duration.
func ParseWindow(w string) (time.Duration, error) {
	w = strings.TrimSpace(w)
	if w == "" {
		return 0, errors.New("janela vazia")
	}
	// Aceita sufixo "d" (dias) que time.ParseDuration não suporta nativamente.
	if strings.HasSuffix(w, "d") {
		var n int
		if _, err := fmt.Sscanf(w, "%dd", &n); err != nil || n <= 0 {
			return 0, fmt.Errorf("janela %q inválida (formato: 1d, 7d)", w)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(w)
	if err != nil {
		return 0, fmt.Errorf("janela %q inválida: %w", w, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("janela %q deve ser positiva", w)
	}
	return d, nil
}

// MarshalCondition / UnmarshalCondition — wrappers para round-trip
// confiável entre PG JSONB e struct Go (preserva ordem/tipo).
func MarshalCondition(c Condition) ([]byte, error) { return json.Marshal(c) }

func UnmarshalCondition(raw []byte) (Condition, error) {
	var c Condition
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("alerting: condition JSON inválido: %w", err)
	}
	return c, nil
}

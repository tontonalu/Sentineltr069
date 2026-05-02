package alerting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/alerting"
	"github.com/celinet/sentinel-acs/internal/domain/inventory"
	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
)

// CompositeSource roteia cada Metric para a fonte de dados apropriada.
//   - device_status → inventory.DeviceRepository
//   - cpu_pct/mem_pct/uptime → telemetry.Repository (system)
//   - wifi_clients → telemetry.Repository (wifi)
//   - optical_rx_dbm → telemetry.Repository (wan)
type CompositeSource struct {
	devices   inventory.DeviceRepository
	telemetry tele.Repository
}

func NewCompositeSource(devices inventory.DeviceRepository, telemetry tele.Repository) *CompositeSource {
	return &CompositeSource{devices: devices, telemetry: telemetry}
}

// Evaluate dispatcha por metric.
func (s *CompositeSource) Evaluate(ctx context.Context, c domain.Condition, now time.Time) (*MetricResult, error) {
	switch c.Metric {
	case domain.MetricDeviceStatus:
		return s.evalDeviceStatus(ctx, c, now)
	case domain.MetricCPUPct, domain.MetricMemPct, domain.MetricUptimeSeconds:
		return s.evalSystemTelemetry(ctx, c, now)
	case domain.MetricWifiClients:
		return s.evalWifiClients(ctx, c, now)
	case domain.MetricOpticalRxDBM:
		return s.evalWanTelemetry(ctx, c, now)
	}
	return nil, fmt.Errorf("metric %q não suportada pelo CompositeSource", c.Metric)
}

// evalDeviceStatus — count|count_pct de devices online/offline filtrados.
// Janela aqui é ignorada (snapshot do estado atual basta para device_status).
func (s *CompositeSource) evalDeviceStatus(ctx context.Context, c domain.Condition, _ time.Time) (*MetricResult, error) {
	filter := inventory.DeviceFilter{
		POPID:    c.Filter.POPID,
		VendorID: c.Filter.VendorID,
		ModelID:  c.Filter.ModelID,
		Status:   c.Filter.Status, // ex: "offline"
		Tag:      c.Filter.Tag,
	}
	matching, _, err := s.devices.List(ctx, filter, inventory.Page{Limit: 50000})
	if err != nil {
		return nil, fmt.Errorf("list devices (matching): %w", err)
	}

	switch c.Aggregation {
	case domain.AggCount:
		return &MetricResult{Value: float64(len(matching))}, nil

	case domain.AggCountPct:
		// total = mesmos filtros mas SEM o status — denominador.
		denom := filter
		denom.Status = ""
		all, _, err := s.devices.List(ctx, denom, inventory.Page{Limit: 50000})
		if err != nil {
			return nil, fmt.Errorf("list devices (total): %w", err)
		}
		if len(all) == 0 {
			return &MetricResult{Value: 0}, nil
		}
		pct := float64(len(matching)) / float64(len(all)) * 100
		return &MetricResult{
			Value: pct,
			Sample: map[string]any{
				"matching_count": len(matching),
				"total_count":    len(all),
			},
		}, nil
	}
	return nil, fmt.Errorf("aggregation %q inválida para device_status", c.Aggregation)
}

func (s *CompositeSource) evalSystemTelemetry(ctx context.Context, c domain.Condition, now time.Time) (*MetricResult, error) {
	d, err := domain.ParseWindow(c.Window)
	if err != nil {
		return nil, err
	}
	rg := tele.Range{From: now.Add(-d), To: now}

	deviceID, err := s.requireDeviceFilter(c)
	if err != nil {
		return nil, err
	}
	samples, err := s.telemetry.QuerySystemRaw(ctx, deviceID, rg)
	if err != nil {
		return nil, fmt.Errorf("query system telemetry: %w", err)
	}
	if len(samples) == 0 {
		return nil, nil
	}

	// Extrai a série apropriada.
	values := make([]float64, 0, len(samples))
	for _, s := range samples {
		switch c.Metric {
		case domain.MetricCPUPct:
			if s.CPUPct != nil {
				values = append(values, *s.CPUPct)
			}
		case domain.MetricMemPct:
			if s.MemPct != nil {
				values = append(values, *s.MemPct)
			}
		case domain.MetricUptimeSeconds:
			if s.UptimeSeconds != nil {
				values = append(values, float64(*s.UptimeSeconds))
			}
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	v, err := aggregate(values, c.Aggregation)
	if err != nil {
		return nil, err
	}
	return &MetricResult{Value: v, DeviceID: &deviceID}, nil
}

func (s *CompositeSource) evalWifiClients(ctx context.Context, c domain.Condition, now time.Time) (*MetricResult, error) {
	d, err := domain.ParseWindow(c.Window)
	if err != nil {
		return nil, err
	}
	rg := tele.Range{From: now.Add(-d), To: now}

	deviceID, err := s.requireDeviceFilter(c)
	if err != nil {
		return nil, err
	}
	samples, err := s.telemetry.QueryWifiRaw(ctx, deviceID, rg)
	if err != nil {
		return nil, fmt.Errorf("query wifi telemetry: %w", err)
	}
	values := make([]float64, 0, len(samples))
	for _, sm := range samples {
		if c.Filter.Band != "" && sm.Band != c.Filter.Band {
			continue
		}
		if sm.ConnectedClients == nil {
			continue
		}
		values = append(values, float64(*sm.ConnectedClients))
	}
	if len(values) == 0 {
		return nil, nil
	}
	v, err := aggregate(values, c.Aggregation)
	if err != nil {
		return nil, err
	}
	return &MetricResult{Value: v, DeviceID: &deviceID}, nil
}

func (s *CompositeSource) evalWanTelemetry(ctx context.Context, c domain.Condition, now time.Time) (*MetricResult, error) {
	d, err := domain.ParseWindow(c.Window)
	if err != nil {
		return nil, err
	}
	rg := tele.Range{From: now.Add(-d), To: now}

	deviceID, err := s.requireDeviceFilter(c)
	if err != nil {
		return nil, err
	}
	samples, err := s.telemetry.QueryWanRaw(ctx, deviceID, rg)
	if err != nil {
		return nil, fmt.Errorf("query wan telemetry: %w", err)
	}
	values := make([]float64, 0, len(samples))
	for _, sm := range samples {
		if c.Metric == domain.MetricOpticalRxDBM && sm.OpticalRxDBM != nil {
			values = append(values, *sm.OpticalRxDBM)
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	v, err := aggregate(values, c.Aggregation)
	if err != nil {
		return nil, err
	}
	return &MetricResult{Value: v, DeviceID: &deviceID}, nil
}

// requireDeviceFilter — métricas de telemetria operam sobre 1 device.
// type=single deve trazer device_id no filter; sem ele a regra é incompleta.
func (s *CompositeSource) requireDeviceFilter(c domain.Condition) (uuid.UUID, error) {
	if c.Filter.DeviceID == nil {
		return uuid.Nil, fmt.Errorf("metric %q exige filter.device_id", c.Metric)
	}
	return *c.Filter.DeviceID, nil
}

// aggregate aplica a função de agregação a um slice de floats.
func aggregate(values []float64, agg domain.Aggregation) (float64, error) {
	if len(values) == 0 {
		return 0, fmt.Errorf("nenhum valor para agregar")
	}
	switch agg {
	case domain.AggLast:
		return values[len(values)-1], nil
	case domain.AggCount:
		return float64(len(values)), nil
	case domain.AggMax:
		m := values[0]
		for _, v := range values[1:] {
			if v > m {
				m = v
			}
		}
		return m, nil
	case domain.AggMin:
		m := values[0]
		for _, v := range values[1:] {
			if v < m {
				m = v
			}
		}
		return m, nil
	case domain.AggAvg:
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		return sum / float64(len(values)), nil
	}
	return 0, fmt.Errorf("aggregation %q não aplicável a séries numéricas", agg)
}

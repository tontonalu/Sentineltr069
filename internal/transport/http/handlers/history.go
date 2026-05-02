package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	devicepages "github.com/celinet/sentinel-acs/internal/views/pages/devices"
)

// HistoryHandler — gráficos históricos por device.
type HistoryHandler struct {
	Devices   domain.DeviceRepository
	Telemetry tele.Repository
}

// History GET /devices/{id}/history?range=24h|7d|30d
func (h *HistoryHandler) History(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	dev, err := h.Devices.GetByID(r.Context(), id)
	if errors.Is(err, domain.ErrDeviceNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("history get device", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rangeStr := r.URL.Query().Get("range")
	if rangeStr == "" {
		rangeStr = "24h"
	}
	rg, useHourly := rangeFromString(rangeStr)

	in := devicepages.HistoryInput{
		Device: *dev,
		Range:  rangeStr,
	}

	// Wi-Fi: separamos 2.4G e 5G em séries diferentes — UI plota cada banda.
	if useHourly {
		points, err := h.Telemetry.QueryWifiHourly(r.Context(), id, rg)
		if err == nil {
			in.WifiSeries, in.WifiSeries5G = bucketHourlyByBand(points)
		}
		wan, err := h.Telemetry.QueryWanHourly(r.Context(), id, rg)
		if err == nil {
			in.WanRxSeries = wanHourlyToSeries(wan, true)
			in.WanTxSeries = wanHourlyToSeries(wan, false)
		}
		sys, err := h.Telemetry.QuerySystemHourly(r.Context(), id, rg)
		if err == nil {
			in.CPUSeries, in.MemSeries = systemHourlyToSeries(sys)
		}
	} else {
		wifi, err := h.Telemetry.QueryWifiRaw(r.Context(), id, rg)
		if err == nil {
			in.WifiSeries, in.WifiSeries5G = bucketRawWifiByBand(wifi)
		}
		wan, err := h.Telemetry.QueryWanRaw(r.Context(), id, rg)
		if err == nil {
			in.WanRxSeries = wanRawToSeries(wan, true)
			in.WanTxSeries = wanRawToSeries(wan, false)
		}
		sys, err := h.Telemetry.QuerySystemRaw(r.Context(), id, rg)
		if err == nil {
			in.CPUSeries, in.MemSeries = systemRawToSeries(sys)
		}
	}

	// Sumário 24h sempre da raw (precisão maior). Independente do range exibido.
	in.Summary = h.buildSummary(r.Context(), id)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = devicepages.History(in).Render(r.Context(), w)
}

// buildSummary monta o card de "Resumo (24h)" a partir das raw samples
// do último dia. Falhas de query devolvem summary vazia (UI mostra "—").
func (h *HistoryHandler) buildSummary(ctx context.Context, id uuid.UUID) devicepages.HistorySummary {
	now := time.Now().UTC()
	rg := tele.Last24h(now)
	var s devicepages.HistorySummary

	wifi, _ := h.Telemetry.QueryWifiRaw(ctx, id, rg)
	if len(wifi) > 0 {
		// Last = última amostra de qualquer banda; média = todas as bandas.
		var sum, count int
		for _, w := range wifi {
			if w.ConnectedClients == nil {
				continue
			}
			sum += *w.ConnectedClients
			count++
		}
		if count > 0 {
			avg := sum / count
			s.AvgClients24h = &avg
			last := wifi[len(wifi)-1].ConnectedClients
			if last != nil {
				v := *last
				s.LastClients24h = &v
			}
		}
	}

	sys, _ := h.Telemetry.QuerySystemRaw(ctx, id, rg)
	if len(sys) > 0 {
		last := sys[len(sys)-1]
		if last.UptimeSeconds != nil {
			v := *last.UptimeSeconds
			s.LastUptime = &v
		}
		if last.CPUPct != nil {
			v := *last.CPUPct
			s.LastCPU = &v
		}
		if last.MemPct != nil {
			v := *last.MemPct
			s.LastMem = &v
		}
	}
	return s
}

// rangeFromString resolve "24h"|"7d"|"30d" → (Range, useHourly).
// 24h usa raw (granular minuto/5min). 7d/30d usam hourly (faster).
func rangeFromString(s string) (tele.Range, bool) {
	now := time.Now().UTC()
	switch s {
	case "7d":
		return tele.Last7d(now), true
	case "30d":
		return tele.Last30d(now), true
	default:
		return tele.Last24h(now), false
	}
}

// ──────────── adaptadores domain → SeriesPoint ────────────

func bucketRawWifiByBand(samples []tele.WifiSample) (g24, g5 []devicepages.SeriesPoint) {
	for _, s := range samples {
		if s.ConnectedClients == nil {
			continue
		}
		p := devicepages.SeriesPoint{X: s.Time.UnixMilli(), Y: float64(*s.ConnectedClients)}
		switch s.Band {
		case tele.Band24G:
			g24 = append(g24, p)
		case tele.Band5G:
			g5 = append(g5, p)
		}
	}
	return
}

func bucketHourlyByBand(points []tele.HourlyWifiPoint) (g24, g5 []devicepages.SeriesPoint) {
	for _, p := range points {
		sp := devicepages.SeriesPoint{X: p.Bucket.UnixMilli(), Y: float64(p.AvgClients)}
		switch p.Band {
		case tele.Band24G:
			g24 = append(g24, sp)
		case tele.Band5G:
			g5 = append(g5, sp)
		}
	}
	return
}

func wanRawToSeries(samples []tele.WanSample, rx bool) []devicepages.SeriesPoint {
	out := make([]devicepages.SeriesPoint, 0, len(samples))
	for _, s := range samples {
		var v *int64
		if rx {
			v = s.RxBytes
		} else {
			v = s.TxBytes
		}
		if v == nil {
			continue
		}
		out = append(out, devicepages.SeriesPoint{X: s.Time.UnixMilli(), Y: float64(*v)})
	}
	return out
}

func wanHourlyToSeries(points []tele.HourlyWanPoint, rx bool) []devicepages.SeriesPoint {
	out := make([]devicepages.SeriesPoint, 0, len(points))
	for _, p := range points {
		v := p.RxDelta
		if !rx {
			v = p.TxDelta
		}
		out = append(out, devicepages.SeriesPoint{X: p.Bucket.UnixMilli(), Y: float64(v)})
	}
	return out
}

func systemRawToSeries(samples []tele.SystemSample) (cpu, mem []devicepages.SeriesPoint) {
	for _, s := range samples {
		if s.CPUPct != nil {
			cpu = append(cpu, devicepages.SeriesPoint{X: s.Time.UnixMilli(), Y: *s.CPUPct})
		}
		if s.MemPct != nil {
			mem = append(mem, devicepages.SeriesPoint{X: s.Time.UnixMilli(), Y: *s.MemPct})
		}
	}
	return
}

func systemHourlyToSeries(points []tele.HourlySystemPoint) (cpu, mem []devicepages.SeriesPoint) {
	for _, p := range points {
		if p.AvgCPU != nil {
			cpu = append(cpu, devicepages.SeriesPoint{X: p.Bucket.UnixMilli(), Y: *p.AvgCPU})
		}
		if p.AvgMem != nil {
			mem = append(mem, devicepages.SeriesPoint{X: p.Bucket.UnixMilli(), Y: *p.AvgMem})
		}
	}
	return
}

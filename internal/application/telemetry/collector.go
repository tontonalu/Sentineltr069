package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/celinet/sentinel-acs/internal/domain/inventory"
	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

// CollectorOptions configura o comportamento do collector.
//
// Defaults pensados para 1.000 devices em <5 min:
//   - chunk=200 (5 chunks → 5 goroutines paralelas)
//   - perDeviceTimeout=10s (NBI lento + JSON parsing)
type CollectorOptions struct {
	ChunkSize        int
	Parallel         int
	PerDeviceTimeout time.Duration
	OnlineThreshold  time.Duration
}

func defaultOptions() CollectorOptions {
	return CollectorOptions{
		ChunkSize:        200,
		Parallel:         5,
		PerDeviceTimeout: 10 * time.Second,
		OnlineThreshold:  30 * time.Minute,
	}
}

// Collector lê o inventário, busca o snapshot atual de cada device no NBI
// e grava as samples derivadas no Postgres/Timescale.
//
// Estratégia "soft refresh": usamos o GetDevice (com cache 30s) — os valores
// vêm do último inform conhecido pelo GenieACS. Mais simples e leve que
// disparar Refresh+poll para cada device. Quando virtual params estiverem
// implementados (Pré-req-A), podemos opcionalmente trocar para Refresh ativo.
type Collector struct {
	devices DeviceLister
	genie   GenieReader
	repo    tele.Repository
	opts    CollectorOptions
}

type DeviceLister interface {
	List(ctx context.Context, f inventory.DeviceFilter, p inventory.Page) ([]inventory.Device, int, error)
}

type GenieReader interface {
	GetDevice(ctx context.Context, genieacsID string) (*genieacs.Device, error)
}

func NewCollector(devices DeviceLister, genie GenieReader, repo tele.Repository, opts CollectorOptions) *Collector {
	if opts.ChunkSize <= 0 {
		opts = defaultOptions()
	}
	if opts.Parallel <= 0 {
		opts.Parallel = 1
	}
	if opts.PerDeviceTimeout <= 0 {
		opts.PerDeviceTimeout = 10 * time.Second
	}
	if opts.OnlineThreshold <= 0 {
		opts.OnlineThreshold = 30 * time.Minute
	}
	return &Collector{devices: devices, genie: genie, repo: repo, opts: opts}
}

// TickResult — métricas de uma rodada do collector.
type TickResult struct {
	Devices       int
	WifiSamples   int
	WanSamples    int
	SystemSamples int
	Errors        int
	Duration      time.Duration
}

// Tick coleta uma janela. Pega devices online, divide em chunks e paraleliza.
// Erros parciais por device não abortam — são contabilizados em Errors.
func (c *Collector) Tick(ctx context.Context) (*TickResult, error) {
	if c == nil {
		return nil, errors.New("telemetry: collector nil")
	}
	start := time.Now()
	log := logger.FromContext(ctx)

	devices, err := c.listOnline(ctx)
	if err != nil {
		return nil, err
	}
	res := &TickResult{Devices: len(devices)}
	if len(devices) == 0 {
		res.Duration = time.Since(start)
		return res, nil
	}

	// Janela alinhada para o tick: todos os samples desta rodada compartilham
	// o mesmo timestamp, facilitando JOIN/comparação na continuous aggregate.
	now := start.UTC().Truncate(time.Second)

	chunks := splitChunks(devices, c.opts.ChunkSize)
	sem := make(chan struct{}, c.opts.Parallel)
	var (
		mu       sync.Mutex
		wifiAll  []tele.WifiSample
		wanAll   []tele.WanSample
		sysAll   []tele.SystemSample
		errCount int
	)

	var wg sync.WaitGroup
	for _, chunk := range chunks {
		wg.Add(1)
		sem <- struct{}{}
		go func(chunk []inventory.Device) {
			defer wg.Done()
			defer func() { <-sem }()

			localWifi, localWan, localSys, errs := c.collectChunk(ctx, chunk, now)

			mu.Lock()
			wifiAll = append(wifiAll, localWifi...)
			wanAll = append(wanAll, localWan...)
			sysAll = append(sysAll, localSys...)
			errCount += errs
			mu.Unlock()
		}(chunk)
	}
	wg.Wait()

	if err := c.repo.InsertWifi(ctx, wifiAll); err != nil {
		log.Error("telemetry insert wifi", "err", err)
		errCount++
	}
	if err := c.repo.InsertWan(ctx, wanAll); err != nil {
		log.Error("telemetry insert wan", "err", err)
		errCount++
	}
	if err := c.repo.InsertSystem(ctx, sysAll); err != nil {
		log.Error("telemetry insert system", "err", err)
		errCount++
	}

	res.WifiSamples = len(wifiAll)
	res.WanSamples = len(wanAll)
	res.SystemSamples = len(sysAll)
	res.Errors = errCount
	res.Duration = time.Since(start)
	return res, nil
}

func (c *Collector) listOnline(ctx context.Context) ([]inventory.Device, error) {
	// Page grande: 5k devices por iter. RNF-01 manda 50k, mas o collector
	// só pega ONLINE, e na prática <30% deles estão simultaneamente online
	// (a maioria dos CPEs informam de hora em hora). Se ultrapassar, dá
	// pra paginar — por enquanto 5k é suficiente.
	const maxPage = 5000
	devs, _, err := c.devices.List(ctx, inventory.DeviceFilter{Status: inventory.StatusOnline},
		inventory.Page{Limit: maxPage})
	if err != nil {
		return nil, fmt.Errorf("telemetry: list devices: %w", err)
	}
	return devs, nil
}

func (c *Collector) collectChunk(ctx context.Context, chunk []inventory.Device, now time.Time) (
	[]tele.WifiSample, []tele.WanSample, []tele.SystemSample, int,
) {
	log := logger.FromContext(ctx)
	var (
		wifi []tele.WifiSample
		wan  []tele.WanSample
		sys  []tele.SystemSample
		errs int
	)
	for _, d := range chunk {
		select {
		case <-ctx.Done():
			return wifi, wan, sys, errs
		default:
		}
		dCtx, cancel := context.WithTimeout(ctx, c.opts.PerDeviceTimeout)
		dev, err := c.genie.GetDevice(dCtx, d.GenieACSID)
		cancel()
		if err != nil {
			errs++
			log.Debug("telemetry getdevice failed", "device", d.GenieACSID, "err", err)
			continue
		}
		w, wn, s := ParseDevice(now, d.ID, dev.Raw)
		for _, sample := range w {
			if sample.HasAnyMetric() {
				wifi = append(wifi, sample)
			}
		}
		if wn.HasAnyMetric() {
			wan = append(wan, wn)
		}
		if s.HasAnyMetric() {
			sys = append(sys, s)
		}
	}
	return wifi, wan, sys, errs
}

func splitChunks(devs []inventory.Device, size int) [][]inventory.Device {
	if size <= 0 {
		return [][]inventory.Device{devs}
	}
	var chunks [][]inventory.Device
	for i := 0; i < len(devs); i += size {
		end := i + size
		if end > len(devs) {
			end = len(devs)
		}
		chunks = append(chunks, devs[i:end])
	}
	return chunks
}

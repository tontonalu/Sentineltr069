// Package telemetry implementa o collector que extrai séries temporais
// dos devices e grava nas hypertables.
//
// O parser é o coração do CP-4.4 — recebe um genieacs.Device.Raw e devolve
// samples canônicas, ignorando paths ausentes (CPE off, modelo sem o param).
package telemetry

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
)

// ParseDevice extrai todas as samples (wifi/wan/system) de um device.
// `now` é o timestamp da coleta (alinhado para o tick atual, não por device).
func ParseDevice(now time.Time, deviceID uuid.UUID, raw map[string]any) ([]tele.WifiSample, tele.WanSample, tele.SystemSample) {
	wifi := parseWifi(now, deviceID, raw)
	wan := parseWan(now, deviceID, raw)
	sys := parseSystem(now, deviceID, raw)
	return wifi, wan, sys
}

// ──────────── Wi-Fi ────────────

// Tentamos os caminhos canônicos primeiro (virtual params §7.4) e caímos
// para TR-098/TR-181 se não houver.
//
// Limite: extraímos no máximo 4 SSIDs por device (2 por banda).
// CPEs típicos têm 2-4 redes ativas; mais que isso é exceção.
const maxWifiPerDevice = 4

func parseWifi(now time.Time, deviceID uuid.UUID, raw map[string]any) []tele.WifiSample {
	var out []tele.WifiSample

	// Virtual param canônico (preferido):
	//   VirtualParameters.WiFi24G_SSID, WiFi5G_SSID, WiFi24G_Clients, etc.
	if s := tryVirtualWifi(now, deviceID, raw, tele.Band24G); s.HasAnyMetric() {
		out = append(out, s)
	}
	if s := tryVirtualWifi(now, deviceID, raw, tele.Band5G); s.HasAnyMetric() {
		out = append(out, s)
	}
	if len(out) > 0 {
		return out
	}

	// Fallback TR-098: InternetGatewayDevice.LANDevice.1.WLANConfiguration.{i}
	for i := 1; i <= maxWifiPerDevice; i++ {
		base := "InternetGatewayDevice.LANDevice.1.WLANConfiguration." + strconv.Itoa(i)
		s := tele.WifiSample{Time: now, DeviceID: deviceID}
		s.SSID = genieacs.ParamString(raw, base+".SSID")
		s.Band = inferBandTR098(genieacs.ParamString(raw, base+".X_HW_Mode"),
			genieacs.ParamString(raw, base+".Standard"),
			genieacs.ParamString(raw, base+".Channel"))
		s.Channel = parseIntPtr(genieacs.ParamString(raw, base+".Channel"))
		s.TxPower = parseIntPtr(genieacs.ParamString(raw, base+".TransmitPower"))
		s.ConnectedClients = countAssociatedTR098(raw, base)
		if s.HasAnyMetric() {
			out = append(out, s)
		}
	}
	if len(out) > 0 {
		return out
	}

	// Fallback TR-181: Device.WiFi.SSID.{i} + Device.WiFi.AccessPoint.{i}
	for i := 1; i <= maxWifiPerDevice; i++ {
		base := "Device.WiFi.SSID." + strconv.Itoa(i)
		s := tele.WifiSample{Time: now, DeviceID: deviceID}
		s.SSID = genieacs.ParamString(raw, base+".SSID")
		// Radio: SSID.{i}.LowerLayers aponta pra Radio.{j}; simplificamos
		// inferindo banda pelo channel da AccessPoint correspondente.
		ap := "Device.WiFi.AccessPoint." + strconv.Itoa(i)
		radio := "Device.WiFi.Radio." + strconv.Itoa(i)
		ch := parseIntPtr(genieacs.ParamString(raw, radio+".Channel"))
		s.Channel = ch
		s.Band = inferBandFromChannel(ch)
		s.TxPower = parseIntPtr(genieacs.ParamString(raw, radio+".TransmitPower"))
		s.ConnectedClients = countAssociatedTR181(raw, ap)
		if s.HasAnyMetric() {
			out = append(out, s)
		}
	}
	return out
}

// tryVirtualWifi lê virtual params canônicos. Convenção:
//
//	VirtualParameters.WiFi24G_SSID
//	VirtualParameters.WiFi24G_Channel
//	VirtualParameters.WiFi24G_Clients
//	VirtualParameters.WiFi24G_TxPower
//	(análogo para WiFi5G_*)
func tryVirtualWifi(now time.Time, deviceID uuid.UUID, raw map[string]any, band string) tele.WifiSample {
	prefix := "VirtualParameters.WiFi" + bandKey(band) + "_"
	s := tele.WifiSample{Time: now, DeviceID: deviceID, Band: band}
	s.SSID = genieacs.ParamString(raw, prefix+"SSID")
	s.Channel = parseIntPtr(genieacs.ParamString(raw, prefix+"Channel"))
	s.ConnectedClients = parseIntPtr(genieacs.ParamString(raw, prefix+"Clients"))
	s.TxPower = parseIntPtr(genieacs.ParamString(raw, prefix+"TxPower"))
	return s
}

func bandKey(band string) string {
	if band == tele.Band24G {
		return "24G"
	}
	return "5G"
}

// countAssociatedTR098 conta sub-objetos AssociatedDevice.{N}.MACAddress.
func countAssociatedTR098(raw map[string]any, base string) *int {
	v := genieacs.ParamValue(raw, base+".AssociatedDevice")
	return countSubObjects(v)
}

func countAssociatedTR181(raw map[string]any, ap string) *int {
	v := genieacs.ParamValue(raw, ap+".AssociatedDevice")
	return countSubObjects(v)
}

// countSubObjects conta filhos de um nó intermediário do NBI.
// O NBI marca objetos com "_object": true; os filhos numerados (1, 2, 3...)
// representam instâncias.
func countSubObjects(v any) *int {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	c := 0
	for k, val := range m {
		if strings.HasPrefix(k, "_") {
			continue
		}
		// Numeric keys são instâncias.
		if _, err := strconv.Atoi(k); err != nil {
			continue
		}
		// Confirma que é objeto (não escalar acidental).
		if _, ok := val.(map[string]any); ok {
			c++
		}
	}
	if c == 0 {
		return nil
	}
	return &c
}

// ──────────── WAN ────────────

func parseWan(now time.Time, deviceID uuid.UUID, raw map[string]any) tele.WanSample {
	s := tele.WanSample{Time: now, DeviceID: deviceID}

	// Virtual params preferidos.
	s.RxBytes = parseInt64Ptr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.WAN_RxBytes",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.Stats.EthernetBytesReceived",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Stats.EthernetBytesReceived",
		"Device.IP.Interface.1.Stats.BytesReceived",
	))
	s.TxBytes = parseInt64Ptr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.WAN_TxBytes",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.Stats.EthernetBytesSent",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Stats.EthernetBytesSent",
		"Device.IP.Interface.1.Stats.BytesSent",
	))
	s.OpticalRxDBM = parseFloatPtr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.OpticalRxDBM",
		"InternetGatewayDevice.X_HW_GponInfo.RxPower",
	))
	s.OpticalTxDBM = parseFloatPtr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.OpticalTxDBM",
		"InternetGatewayDevice.X_HW_GponInfo.TxPower",
	))
	return s
}

// ──────────── System ────────────

func parseSystem(now time.Time, deviceID uuid.UUID, raw map[string]any) tele.SystemSample {
	s := tele.SystemSample{Time: now, DeviceID: deviceID}
	s.CPUPct = parseFloatPtr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.CPUPct",
		"InternetGatewayDevice.DeviceInfo.X_HW_CPUUsage",
		"Device.DeviceInfo.ProcessStatus.CPUUsage",
	))
	s.MemPct = parseFloatPtr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.MemPct",
		"InternetGatewayDevice.DeviceInfo.X_HW_MemUsage",
		"Device.DeviceInfo.MemoryStatus.Free",
	))
	s.UptimeSeconds = parseInt64Ptr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.UptimeSeconds",
		"InternetGatewayDevice.DeviceInfo.UpTime",
		"Device.DeviceInfo.UpTime",
	))
	return s
}

// ──────────── inference helpers ────────────

func inferBandTR098(mode, standard, channel string) string {
	mode = strings.ToLower(mode)
	standard = strings.ToLower(standard)
	if strings.Contains(mode, "5g") || strings.Contains(standard, "ac") || strings.Contains(standard, "ax") {
		return tele.Band5G
	}
	if strings.Contains(mode, "2.4") || strings.Contains(standard, "g") || strings.Contains(standard, "n") {
		// "n" pode ser tanto 2.4 quanto 5 — usa channel para desempate.
		if ch, err := strconv.Atoi(channel); err == nil {
			return inferBandStr(ch)
		}
		return tele.Band24G
	}
	if ch, err := strconv.Atoi(channel); err == nil {
		return inferBandStr(ch)
	}
	return ""
}

func inferBandFromChannel(ch *int) string {
	if ch == nil {
		return ""
	}
	return inferBandStr(*ch)
}

// inferBandStr — canais 1-14 = 2.4G; 32+ = 5G. Limite simplificado mas
// preciso para os planos brasileiros (sem suporte a 6GHz por enquanto).
func inferBandStr(ch int) string {
	if ch >= 1 && ch <= 14 {
		return tele.Band24G
	}
	if ch >= 32 {
		return tele.Band5G
	}
	return ""
}

func parseIntPtr(s string) *int {
	if s == "" {
		return nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return &v
}

func parseInt64Ptr(s string) *int64 {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return nil
	}
	return &v
}

func parseFloatPtr(s string) *float64 {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return nil
	}
	return &v
}

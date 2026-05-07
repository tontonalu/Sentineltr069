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

// ParseDeviceFull estende ParseDevice com hosts (LAN connected) e ports
// (status físico das portas). Mantemos ParseDevice como API estável para
// quem só quer as séries clássicas.
func ParseDeviceFull(now time.Time, deviceID uuid.UUID, raw map[string]any) (
	[]tele.WifiSample, tele.WanSample, tele.SystemSample,
	[]tele.HostSample, []tele.PortSample,
) {
	wifi, wan, sys := ParseDevice(now, deviceID, raw)
	hosts := parseHosts(now, deviceID, raw)
	ports := parsePorts(now, deviceID, raw)
	return wifi, wan, sys, hosts, ports
}

// ──────────── Wi-Fi ────────────

// Tentamos os caminhos canônicos primeiro (virtual params §7.4) e caímos
// para TR-098/TR-181 se não houver.
//
// Limite: extraímos no máximo 8 SSIDs por device. CPEs como V-SOL V2804AX
// expõem 8 WLANConfigurations (4 por banda — main, guest, IPTV, etc), e
// vimos clientes associados em índices 5+ no campo. Samples sem métrica
// real continuam sendo descartadas via HasAnyMetric.
const maxWifiPerDevice = 8

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
// Cai pra `TotalAssociations` (campo escalar) quando o NBI não expandiu a
// lista — vendor V-SOL/Realtek expõe só o agregado, não os sub-objetos
// individuais.
func countAssociatedTR098(raw map[string]any, base string) *int {
	if c := countSubObjects(genieacs.ParamObject(raw, base+".AssociatedDevice")); c != nil {
		return c
	}
	return parseIntPtr(genieacs.ParamString(raw, base+".TotalAssociations"))
}

// countAssociatedTR181 — TR-181 sempre expõe a lista AssociatedDevice;
// AssociatedDeviceNumberOfEntries é o fallback quando o NBI não trouxe
// os filhos (projection limitada).
func countAssociatedTR181(raw map[string]any, ap string) *int {
	if c := countSubObjects(genieacs.ParamObject(raw, ap+".AssociatedDevice")); c != nil {
		return c
	}
	return parseIntPtr(genieacs.ParamString(raw, ap+".AssociatedDeviceNumberOfEntries"))
}

// countSubObjects conta filhos de um nó intermediário do NBI.
// O NBI marca objetos com "_object": true; os filhos numerados (1, 2, 3...)
// representam instâncias.
func countSubObjects(m map[string]any) *int {
	if m == nil {
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

// wanScanIndices — vendors brasileiros (V-SOL/Realtek, ZTE, FiberHome) costumam
// pôr a conexão WAN ativa em índices > 1 (.2 ou .3). Idem WANIPConnection /
// WANPPPConnection — o índice da conexão sob o WANConnectionDevice escolhido
// também varia. Scaneamos um conjunto pequeno e pegamos o primeiro valor
// não-vazio (já que ConnectionDevices ociosos retornam 0 ou string vazia).
const (
	wanScanMaxConnDevice = 4
	wanScanMaxConnIndex  = 4
)

func parseWan(now time.Time, deviceID uuid.UUID, raw map[string]any) tele.WanSample {
	s := tele.WanSample{Time: now, DeviceID: deviceID}

	rxPaths := append([]string{"VirtualParameters.WAN_RxBytes"}, wanStatsPaths(raw, "EthernetBytesReceived")...)
	rxPaths = append(rxPaths, "Device.IP.Interface.1.Stats.BytesReceived")
	txPaths := append([]string{"VirtualParameters.WAN_TxBytes"}, wanStatsPaths(raw, "EthernetBytesSent")...)
	txPaths = append(txPaths, "Device.IP.Interface.1.Stats.BytesSent")

	s.RxBytes = parseInt64Ptr(genieacs.FirstNonEmpty(raw, rxPaths...))
	s.TxBytes = parseInt64Ptr(genieacs.FirstNonEmpty(raw, txPaths...))
	s.OpticalRxDBM = parseFloatPtr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.OpticalRxDBM",
		"InternetGatewayDevice.X_HW_GponInfo.RxPower",
		"InternetGatewayDevice.WANDevice.1.X_GponInterafceConfig.RXPower",
	))
	s.OpticalTxDBM = parseFloatPtr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.OpticalTxDBM",
		"InternetGatewayDevice.X_HW_GponInfo.TxPower",
		"InternetGatewayDevice.WANDevice.1.X_GponInterafceConfig.TXPower",
	))
	return s
}

// wanStatsPaths gera a lista de paths candidatos pra um campo de Stats em
// WANIPConnection e WANPPPConnection, varrendo os índices comuns (1..4). A
// ordem importa: tentamos IP primeiro (mais comum em DHCP), depois PPP.
func wanStatsPaths(_ map[string]any, statField string) []string {
	out := make([]string, 0, wanScanMaxConnDevice*wanScanMaxConnIndex*2)
	for cd := 1; cd <= wanScanMaxConnDevice; cd++ {
		for ci := 1; ci <= wanScanMaxConnIndex; ci++ {
			out = append(out,
				"InternetGatewayDevice.WANDevice.1.WANConnectionDevice."+strconv.Itoa(cd)+
					".WANIPConnection."+strconv.Itoa(ci)+".Stats."+statField,
				"InternetGatewayDevice.WANDevice.1.WANConnectionDevice."+strconv.Itoa(cd)+
					".WANPPPConnection."+strconv.Itoa(ci)+".Stats."+statField,
			)
		}
	}
	return out
}

// ──────────── System ────────────

func parseSystem(now time.Time, deviceID uuid.UUID, raw map[string]any) tele.SystemSample {
	s := tele.SystemSample{Time: now, DeviceID: deviceID}
	s.CPUPct = parseFloatPtr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.CPUPct",
		"InternetGatewayDevice.DeviceInfo.X_HW_CPUUsage",
		// Realtek/V-SOL TR-098 padrão.
		"InternetGatewayDevice.DeviceInfo.ProcessStatus.CPUUsage",
		"Device.DeviceInfo.ProcessStatus.CPUUsage",
	))
	// Memória: virtual/vendor params trazem direto o %; quando não temos,
	// caímos pra Free+Total e calculamos. É a única forma do TR-098 padrão.
	if v := genieacs.FirstNonEmpty(raw,
		"VirtualParameters.MemPct",
		"InternetGatewayDevice.DeviceInfo.X_HW_MemUsage",
	); v != "" {
		s.MemPct = parseFloatPtr(v)
	} else {
		s.MemPct = computeMemPct(raw)
	}
	s.UptimeSeconds = parseInt64Ptr(genieacs.FirstNonEmpty(raw,
		"VirtualParameters.UptimeSeconds",
		"InternetGatewayDevice.DeviceInfo.UpTime",
		"Device.DeviceInfo.UpTime",
	))
	s.TemperatureC = parseTemperature(raw)
	return s
}

// computeMemPct — TR-098 padrão expõe `MemoryStatus.Free` e `.Total` em KB
// (sem campo de uso direto). `% usado = (1 - free/total) * 100`. Retorna
// nil se faltar qualquer um dos dois ou se total=0 (evita div/zero).
func computeMemPct(raw map[string]any) *float64 {
	free := parseFloatPtr(genieacs.FirstNonEmpty(raw,
		"InternetGatewayDevice.DeviceInfo.MemoryStatus.Free",
		"Device.DeviceInfo.MemoryStatus.Free",
	))
	total := parseFloatPtr(genieacs.FirstNonEmpty(raw,
		"InternetGatewayDevice.DeviceInfo.MemoryStatus.Total",
		"Device.DeviceInfo.MemoryStatus.Total",
	))
	if free == nil || total == nil || *total <= 0 {
		return nil
	}
	pct := (1 - *free/(*total)) * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct
}

// parseTemperature percorre TemperatureSensor.{1..maxTempSensors} e devolve a
// maior leitura encontrada — quando o CPE expõe múltiplos sensores (módulo
// óptico, board, CPU), o maior é o que a operação se importa.
const maxTempSensors = 4

func parseTemperature(raw map[string]any) *float64 {
	var max *float64
	for i := 1; i <= maxTempSensors; i++ {
		v := parseFloatPtr(genieacs.FirstNonEmpty(raw,
			"InternetGatewayDevice.DeviceInfo.TemperatureStatus.TemperatureSensor."+strconv.Itoa(i)+".Value",
			"Device.DeviceInfo.TemperatureStatus.TemperatureSensor."+strconv.Itoa(i)+".Value",
		))
		if v == nil {
			continue
		}
		if max == nil || *v > *max {
			max = v
		}
	}
	if max == nil {
		// VirtualParameters como último recurso.
		if v := parseFloatPtr(genieacs.ParamString(raw, "VirtualParameters.TemperatureC")); v != nil {
			max = v
		}
	}
	return max
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

// ──────────── Hosts (LAN connected devices) ────────────

const maxHostsPerDevice = 64 // teto defensivo; CPEs típicos têm 5-30 hosts

// parseHosts percorre `Hosts.Host.{i}` em TR-098 e TR-181, dedup por MAC.
// Retorna no máximo maxHostsPerDevice samples — o resto é ignorado para
// proteger memória se algum CPE expor lista absurda.
func parseHosts(now time.Time, deviceID uuid.UUID, raw map[string]any) []tele.HostSample {
	seen := map[string]bool{}
	var out []tele.HostSample

	// TR-098: InternetGatewayDevice.LANDevice.{N}.Hosts.Host.{i}
	for ld := 1; ld <= 4 && len(out) < maxHostsPerDevice; ld++ {
		base := "InternetGatewayDevice.LANDevice." + strconv.Itoa(ld) + ".Hosts.Host"
		obj := genieacs.ParamObject(raw, base)
		if obj == nil {
			continue
		}
		for i := 1; i <= maxHostsPerDevice; i++ {
			if len(out) >= maxHostsPerDevice {
				break
			}
			path := base + "." + strconv.Itoa(i)
			if genieacs.ParamObject(raw, path) == nil {
				continue
			}
			h := hostFromTR098(now, deviceID, raw, path)
			if h.MACAddress == "" || seen[h.MACAddress] {
				continue
			}
			seen[h.MACAddress] = true
			out = append(out, h)
		}
	}

	// TR-181: Device.Hosts.Host.{i}
	if len(out) < maxHostsPerDevice {
		base := "Device.Hosts.Host"
		if genieacs.ParamObject(raw, base) != nil {
			for i := 1; i <= maxHostsPerDevice; i++ {
				if len(out) >= maxHostsPerDevice {
					break
				}
				path := base + "." + strconv.Itoa(i)
				if genieacs.ParamObject(raw, path) == nil {
					continue
				}
				h := hostFromTR181(now, deviceID, raw, path)
				if h.MACAddress == "" || seen[h.MACAddress] {
					continue
				}
				seen[h.MACAddress] = true
				out = append(out, h)
			}
		}
	}
	return out
}

func hostFromTR098(now time.Time, deviceID uuid.UUID, raw map[string]any, base string) tele.HostSample {
	h := tele.HostSample{Time: now, DeviceID: deviceID}
	h.MACAddress = strings.ToUpper(strings.TrimSpace(genieacs.ParamString(raw, base+".MACAddress")))
	h.Hostname = genieacs.ParamString(raw, base+".HostName")
	h.IPAddress = genieacs.FirstNonEmpty(raw,
		base+".IPAddress",
		base+".X_IPAddress",
	)
	h.AddressSource = normalizeAddressSource(genieacs.ParamString(raw, base+".AddressSource"))
	h.Layer1Interface = inferL1FromTR098(genieacs.ParamString(raw, base+".Layer1Interface"),
		genieacs.ParamString(raw, base+".InterfaceType"),
		genieacs.ParamString(raw, base+".X_AssociatedDevice"))
	h.ActiveSeconds = parseInt64Ptr(genieacs.FirstNonEmpty(raw,
		base+".LeaseTimeRemaining",
		base+".X_HW_LeaseTime",
		base+".AssociationTime",
	))
	h.SignalDBM = parseIntPtr(genieacs.FirstNonEmpty(raw,
		base+".X_HW_RSSI",
		base+".SignalStrength",
	))
	return h
}

func hostFromTR181(now time.Time, deviceID uuid.UUID, raw map[string]any, base string) tele.HostSample {
	h := tele.HostSample{Time: now, DeviceID: deviceID}
	h.MACAddress = strings.ToUpper(strings.TrimSpace(genieacs.ParamString(raw, base+".PhysAddress")))
	h.Hostname = genieacs.ParamString(raw, base+".HostName")
	h.IPAddress = genieacs.FirstNonEmpty(raw,
		base+".IPAddress",
		base+".IPv4Address.1.IPAddress",
	)
	h.AddressSource = normalizeAddressSource(genieacs.ParamString(raw, base+".AddressSource"))
	h.Layer1Interface = inferL1FromTR181(genieacs.ParamString(raw, base+".Layer1Interface"),
		genieacs.ParamString(raw, base+".AssociatedDevice"))
	h.ActiveSeconds = parseInt64Ptr(genieacs.FirstNonEmpty(raw,
		base+".LeaseTimeRemaining",
		base+".AssociationTime",
	))
	h.SignalDBM = parseIntPtr(genieacs.ParamString(raw, base+".SignalStrength"))
	return h
}

// normalizeAddressSource encurta variantes ("DHCP","Dynamic","Reserved","Static")
// para o conjunto canônico que a UI exibe.
func normalizeAddressSource(s string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(t, "static"):
		return "Static"
	case strings.Contains(t, "dhcp"), strings.Contains(t, "dynamic"), strings.Contains(t, "reserved"):
		return "DHCP"
	}
	return ""
}

func inferL1FromTR098(layer1, ifType, assoc string) string {
	hint := strings.ToLower(layer1 + " " + ifType + " " + assoc)
	switch {
	case strings.Contains(hint, "wlan"), strings.Contains(hint, "wifi"), strings.Contains(hint, "associateddevice"):
		// 5GHz frequente: WLANConfiguration.5 ou Radio.2
		if strings.Contains(hint, ".5.") || strings.Contains(hint, "radio.2") || strings.Contains(hint, "5g") {
			return "WiFi-5G"
		}
		return "WiFi-2.4G"
	case strings.Contains(hint, "ethernet"), strings.Contains(hint, "lan"):
		return "Ethernet"
	}
	return ""
}

func inferL1FromTR181(layer1, assoc string) string {
	hint := strings.ToLower(layer1 + " " + assoc)
	switch {
	case strings.Contains(hint, "wifi"), strings.Contains(hint, "accesspoint"):
		if strings.Contains(hint, "radio.2") || strings.Contains(hint, "accesspoint.5") || strings.Contains(hint, "accesspoint.2") {
			return "WiFi-5G"
		}
		return "WiFi-2.4G"
	case strings.Contains(hint, "ethernet"):
		return "Ethernet"
	}
	return ""
}

// ──────────── Ports (status físico Ethernet/WAN) ────────────

// portCandidate descreve onde procurar uma porta no NBI.
// Tentamos TR-098 primeiro (mais comum em CPEs brasileiros), depois TR-181.
type portCandidate struct {
	Name        string
	TR098Status []string
	TR098Speed  []string
	TR098Duplex []string
	TR181Status []string
	TR181Speed  []string
	TR181Duplex []string
}

func portCandidates() []portCandidate {
	return []portCandidate{
		{
			Name: "WAN",
			TR098Status: []string{
				"InternetGatewayDevice.WANDevice.1.WANEthernetInterfaceConfig.Status",
				"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANEthernetLinkConfig.EthernetLinkStatus",
			},
			TR098Speed:  []string{"InternetGatewayDevice.WANDevice.1.WANEthernetInterfaceConfig.MaxBitRate"},
			TR098Duplex: []string{"InternetGatewayDevice.WANDevice.1.WANEthernetInterfaceConfig.DuplexMode"},
			TR181Status: []string{"Device.Ethernet.Interface.1.Status"},
			TR181Speed:  []string{"Device.Ethernet.Interface.1.CurrentBitRate", "Device.Ethernet.Interface.1.MaxBitRate"},
			TR181Duplex: []string{"Device.Ethernet.Interface.1.DuplexMode"},
		},
		portLAN(1),
		portLAN(2),
		portLAN(3),
		portLAN(4),
	}
}

func portLAN(n int) portCandidate {
	idx := strconv.Itoa(n)
	return portCandidate{
		Name:        "LAN" + idx,
		TR098Status: []string{"InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig." + idx + ".Status"},
		TR098Speed: []string{
			"InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig." + idx + ".MaxBitRate",
			"InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig." + idx + ".X_HW_NegotiatedSpeed",
		},
		TR098Duplex: []string{"InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig." + idx + ".DuplexMode"},
		// Em TR-181, Interface.1 é WAN; LAN começa em 2 por convenção.
		TR181Status: []string{"Device.Ethernet.Interface." + strconv.Itoa(n+1) + ".Status"},
		TR181Speed: []string{
			"Device.Ethernet.Interface." + strconv.Itoa(n+1) + ".CurrentBitRate",
			"Device.Ethernet.Interface." + strconv.Itoa(n+1) + ".MaxBitRate",
		},
		TR181Duplex: []string{"Device.Ethernet.Interface." + strconv.Itoa(n+1) + ".DuplexMode"},
	}
}

func parsePorts(now time.Time, deviceID uuid.UUID, raw map[string]any) []tele.PortSample {
	var out []tele.PortSample
	for _, c := range portCandidates() {
		statusRaw := genieacs.FirstNonEmpty(raw, append(c.TR098Status, c.TR181Status...)...)
		if statusRaw == "" {
			continue
		}
		st := normalizePortStatus(statusRaw)
		if st == "" {
			continue
		}
		p := tele.PortSample{
			Time:     now,
			DeviceID: deviceID,
			PortName: c.Name,
			Status:   st,
		}
		p.SpeedMbps = parseSpeedMbps(genieacs.FirstNonEmpty(raw, append(c.TR098Speed, c.TR181Speed...)...))
		p.Duplex = normalizeDuplex(genieacs.FirstNonEmpty(raw, append(c.TR098Duplex, c.TR181Duplex...)...))
		out = append(out, p)
	}
	return out
}

func normalizePortStatus(s string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	switch {
	case t == "up", t == "1", t == "true", t == "enabled", t == "connected":
		return "Up"
	case t == "down", t == "0", t == "false", t == "disabled", t == "noLink", t == "nolink", t == "disconnected":
		return "Down"
	}
	// Algumas implementações reportam "NoLink"/"Error"/"Dormant" — tratamos
	// como Down para o operador ter visão pessimista.
	if strings.Contains(t, "down") || strings.Contains(t, "error") || strings.Contains(t, "dormant") || strings.Contains(t, "nolink") {
		return "Down"
	}
	if strings.Contains(t, "up") {
		return "Up"
	}
	return ""
}

// parseSpeedMbps aceita valores em bps (TR-181 CurrentBitRate vem em Mbps já)
// ou strings "100","1000","Auto" — devolve nil se não conseguir interpretar.
func parseSpeedMbps(s string) *int {
	if s == "" {
		return nil
	}
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "auto" || t == "-1" || t == "0" {
		return nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return &v
}

func normalizeDuplex(s string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(t, "full"):
		return "Full"
	case strings.Contains(t, "half"):
		return "Half"
	}
	return ""
}

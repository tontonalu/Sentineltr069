package telemetry

import (
	"testing"
	"time"

	"github.com/google/uuid"

	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
)

// TestParseHostsTR098 — extrai hosts da subárvore InternetGatewayDevice.LANDevice.1.Hosts.Host.
func TestParseHostsTR098(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	dev := uuid.New()
	raw := map[string]any{
		"InternetGatewayDevice": map[string]any{
			"LANDevice": map[string]any{
				"1": map[string]any{
					"Hosts": map[string]any{
						"Host": map[string]any{
							"_object": true,
							"1": map[string]any{
								"_object":            true,
								"MACAddress":         paramScalar("AA:BB:CC:DD:EE:01", "xsd:string"),
								"HostName":           paramScalar("notebook-jose", "xsd:string"),
								"IPAddress":          paramScalar("192.168.1.10", "xsd:string"),
								"AddressSource":      paramScalar("DHCP", "xsd:string"),
								"Layer1Interface":    paramScalar("WLANConfiguration.1", "xsd:string"),
								"LeaseTimeRemaining": paramScalar("3600", "xsd:int"),
								"X_HW_RSSI":          paramScalar("-58", "xsd:int"),
							},
							"2": map[string]any{
								"_object":         true,
								"MACAddress":      paramScalar("AA:BB:CC:DD:EE:02", "xsd:string"),
								"HostName":        paramScalar("smart-tv", "xsd:string"),
								"IPAddress":       paramScalar("192.168.1.11", "xsd:string"),
								"AddressSource":   paramScalar("Static", "xsd:string"),
								"Layer1Interface": paramScalar("LANEthernetInterfaceConfig.1", "xsd:string"),
							},
						},
					},
				},
			},
		},
	}
	hosts := parseHosts(now, dev, raw)
	if len(hosts) != 2 {
		t.Fatalf("esperava 2 hosts, recebi %d", len(hosts))
	}

	byMAC := map[string]tele.HostSample{}
	for _, h := range hosts {
		byMAC[h.MACAddress] = h
	}

	jose := byMAC["AA:BB:CC:DD:EE:01"]
	if jose.Hostname != "notebook-jose" {
		t.Errorf("hostname=%q", jose.Hostname)
	}
	if jose.AddressSource != "DHCP" {
		t.Errorf("addr source=%q", jose.AddressSource)
	}
	if jose.Layer1Interface != "WiFi-2.4G" {
		t.Errorf("L1=%q (esperava WiFi-2.4G)", jose.Layer1Interface)
	}
	if jose.SignalDBM == nil || *jose.SignalDBM != -58 {
		t.Errorf("signal=%v", jose.SignalDBM)
	}

	tv := byMAC["AA:BB:CC:DD:EE:02"]
	if tv.AddressSource != "Static" {
		t.Errorf("tv addr source=%q", tv.AddressSource)
	}
	if tv.Layer1Interface != "Ethernet" {
		t.Errorf("tv L1=%q", tv.Layer1Interface)
	}
}

// TestParseHostsTR181 — fallback para Device.Hosts.Host.
func TestParseHostsTR181(t *testing.T) {
	now := time.Now()
	dev := uuid.New()
	raw := map[string]any{
		"Device": map[string]any{
			"Hosts": map[string]any{
				"Host": map[string]any{
					"_object": true,
					"1": map[string]any{
						"_object":     true,
						"PhysAddress": paramScalar("aa:bb:cc:dd:ee:ff", "xsd:string"),
						"HostName":    paramScalar("celular-maria", "xsd:string"),
						"IPAddress":   paramScalar("10.0.0.42", "xsd:string"),
						"AddressSource": paramScalar("Dynamic", "xsd:string"),
					},
				},
			},
		},
	}
	hosts := parseHosts(now, dev, raw)
	if len(hosts) != 1 {
		t.Fatalf("esperava 1, recebi %d", len(hosts))
	}
	h := hosts[0]
	if h.MACAddress != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("MAC normalizado=%q", h.MACAddress)
	}
	if h.AddressSource != "DHCP" {
		t.Errorf("addr source=%q", h.AddressSource)
	}
}

// TestParseHostsDedupByMAC — MAC duplicado em sub-objetos diferentes deve
// aparecer apenas uma vez (vendor bagunçado).
func TestParseHostsDedupByMAC(t *testing.T) {
	now := time.Now()
	dev := uuid.New()
	raw := map[string]any{
		"InternetGatewayDevice": map[string]any{
			"LANDevice": map[string]any{
				"1": map[string]any{
					"Hosts": map[string]any{
						"Host": map[string]any{
							"_object": true,
							"1": map[string]any{
								"_object":    true,
								"MACAddress": paramScalar("11:22:33:44:55:66", "xsd:string"),
							},
							"2": map[string]any{
								"_object":    true,
								"MACAddress": paramScalar("11:22:33:44:55:66", "xsd:string"),
							},
						},
					},
				},
			},
		},
	}
	hosts := parseHosts(now, dev, raw)
	if len(hosts) != 1 {
		t.Errorf("dedup falhou: recebi %d hosts", len(hosts))
	}
}

// TestParsePortsWAN — extrai status da WAN via TR-098.
func TestParsePortsWAN(t *testing.T) {
	now := time.Now()
	dev := uuid.New()
	raw := map[string]any{
		"InternetGatewayDevice": map[string]any{
			"WANDevice": map[string]any{
				"1": map[string]any{
					"WANEthernetInterfaceConfig": map[string]any{
						"Status":     paramScalar("Up", "xsd:string"),
						"MaxBitRate": paramScalar("1000", "xsd:int"),
						"DuplexMode": paramScalar("Full", "xsd:string"),
					},
				},
			},
		},
	}
	ports := parsePorts(now, dev, raw)
	if len(ports) == 0 {
		t.Fatal("esperava ao menos a porta WAN")
	}
	var wan *tele.PortSample
	for i := range ports {
		if ports[i].PortName == "WAN" {
			wan = &ports[i]
		}
	}
	if wan == nil {
		t.Fatal("WAN não encontrada nas portas extraídas")
	}
	if wan.Status != "Up" {
		t.Errorf("status=%q", wan.Status)
	}
	if wan.SpeedMbps == nil || *wan.SpeedMbps != 1000 {
		t.Errorf("speed=%v", wan.SpeedMbps)
	}
	if wan.Duplex != "Full" {
		t.Errorf("duplex=%q", wan.Duplex)
	}
}

// TestParsePortsLAN_TR181 — usa Device.Ethernet.Interface (TR-181).
func TestParsePortsLAN_TR181(t *testing.T) {
	now := time.Now()
	dev := uuid.New()
	raw := map[string]any{
		"Device": map[string]any{
			"Ethernet": map[string]any{
				"Interface": map[string]any{
					"_object": true,
					"2": map[string]any{
						"_object":         true,
						"Status":          paramScalar("Up", "xsd:string"),
						"CurrentBitRate":  paramScalar("100", "xsd:int"),
						"DuplexMode":      paramScalar("Half", "xsd:string"),
					},
					"3": map[string]any{
						"_object": true,
						"Status":  paramScalar("Down", "xsd:string"),
					},
				},
			},
		},
	}
	ports := parsePorts(now, dev, raw)
	byName := map[string]tele.PortSample{}
	for _, p := range ports {
		byName[p.PortName] = p
	}
	lan1, ok := byName["LAN1"]
	if !ok {
		t.Fatal("LAN1 não extraída")
	}
	if lan1.Status != "Up" {
		t.Errorf("LAN1 status=%q", lan1.Status)
	}
	if lan1.SpeedMbps == nil || *lan1.SpeedMbps != 100 {
		t.Errorf("LAN1 speed=%v", lan1.SpeedMbps)
	}
	if lan1.Duplex != "Half" {
		t.Errorf("LAN1 duplex=%q", lan1.Duplex)
	}
	lan2, ok := byName["LAN2"]
	if !ok {
		t.Fatal("LAN2 não extraída")
	}
	if lan2.Status != "Down" {
		t.Errorf("LAN2 status=%q", lan2.Status)
	}
}

// TestNormalizePortStatus — mapeamento de variantes TR-069 para Up/Down canônico.
func TestNormalizePortStatus(t *testing.T) {
	cases := map[string]string{
		"Up":           "Up",
		"up":           "Up",
		"1":            "Up",
		"true":         "Up",
		"Connected":    "Up",
		"Down":         "Down",
		"NoLink":       "Down",
		"Disabled":     "Down",
		"Error":        "Down",
		"":             "",
		"Unknown":      "",
	}
	for in, want := range cases {
		if got := normalizePortStatus(in); got != want {
			t.Errorf("normalizePortStatus(%q)=%q want %q", in, got, want)
		}
	}
}

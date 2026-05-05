package genieacs

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFlattenTree_HuaweiTR098(t *testing.T) {
	const raw = `{
		"_id": "00:1A:2B-XYZ",
		"_lastInform": "2026-01-01T12:00:00Z",
		"InternetGatewayDevice": {
			"_object": true,
			"DeviceInfo": {
				"_object": true,
				"Manufacturer": {"_value": "Huawei", "_type": "xsd:string", "_writable": false},
				"SoftwareVersion": {"_value": "V5R20", "_type": "xsd:string", "_writable": false}
			},
			"LANDevice": {
				"_object": true,
				"1": {
					"_object": true,
					"WLANConfiguration": {
						"_object": true,
						"1": {
							"_object": true,
							"SSID": {"_value": "celinet-2g", "_type": "xsd:string", "_writable": true},
							"Channel": {"_value": 6, "_type": "xsd:unsignedInt", "_writable": true}
						}
					}
				}
			}
		}
	}`
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	entries := FlattenTree(doc)
	wantCount := 4 // 2 DeviceInfo + 2 WLAN — chaves _* devem ser ignoradas
	if len(entries) != wantCount {
		t.Fatalf("FlattenTree count = %d, want %d (got %+v)", len(entries), wantCount, entries)
	}
	// Ordenado por path: Channel < Manufacturer < SSID < SoftwareVersion (alfa)
	wantPaths := []string{
		"InternetGatewayDevice.DeviceInfo.Manufacturer",
		"InternetGatewayDevice.DeviceInfo.SoftwareVersion",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Channel",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID",
	}
	for i, want := range wantPaths {
		if entries[i].Path != want {
			t.Errorf("entries[%d].Path = %q, want %q", i, entries[i].Path, want)
		}
	}
	// Valida flags do SSID (writable, type)
	ssid := findByPath(entries, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID")
	if ssid == nil {
		t.Fatal("SSID não encontrado")
	}
	if !ssid.Writable {
		t.Error("SSID writable=false, esperado true")
	}
	if ssid.Type != "xsd:string" {
		t.Errorf("SSID type=%q, want xsd:string", ssid.Type)
	}
	if v, ok := ssid.Value.(string); !ok || v != "celinet-2g" {
		t.Errorf("SSID value=%v (%T), want celinet-2g", ssid.Value, ssid.Value)
	}
}

func TestFlattenTree_NilSafe(t *testing.T) {
	if got := FlattenTree(nil); got != nil {
		t.Errorf("FlattenTree(nil) = %v, want nil", got)
	}
	if got := FlattenTree(map[string]any{}); len(got) != 0 {
		t.Errorf("FlattenTree({}) len=%d, want 0", len(got))
	}
}

func TestFilterTree_PrefixAndSearch(t *testing.T) {
	entries := []TreeEntry{
		{Path: "Device.WiFi.SSID.1.SSID", HasValue: true},
		{Path: "Device.WiFi.SSID.2.SSID", HasValue: true},
		{Path: "Device.WiFi.Radio.1.Channel", HasValue: true},
		{Path: "Device.DHCPv4.Server.Pool.1.MinAddress", HasValue: true},
	}
	got := FilterTree(entries, "Device.WiFi", "")
	if len(got) != 3 {
		t.Errorf("prefix Device.WiFi: %d, want 3", len(got))
	}
	got = FilterTree(entries, "", "ssid")
	if len(got) != 2 {
		t.Errorf("search ssid: %d, want 2", len(got))
	}
	got = FilterTree(entries, "Device.WiFi", "channel")
	if len(got) != 1 || got[0].Path != "Device.WiFi.Radio.1.Channel" {
		t.Errorf("combined filter mismatch: %+v", got)
	}
	// Sem filtro = passa tudo (mesma referência aceitável).
	if got := FilterTree(entries, "", ""); len(got) != len(entries) {
		t.Errorf("empty filter changed length")
	}
}

func TestSanitizeTree_RedactsSecretLeaves(t *testing.T) {
	const raw = `{
		"InternetGatewayDevice": {
			"_object": true,
			"LANDevice": {
				"_object": true,
				"1": {
					"_object": true,
					"WLANConfiguration": {
						"_object": true,
						"1": {
							"_object": true,
							"SSID": {"_value": "celinet-2g", "_type": "xsd:string"},
							"KeyPassphrase": {"_value": "supersecret123", "_type": "xsd:string"},
							"PreSharedKey": {
								"_object": true,
								"1": {
									"_object": true,
									"KeyPassphrase": {"_value": "alsosecret", "_type": "xsd:string"}
								}
							}
						}
					}
				}
			},
			"WANDevice": {
				"_object": true,
				"1": {
					"_object": true,
					"WANConnectionDevice": {
						"_object": true,
						"1": {
							"_object": true,
							"WANPPPConnection": {
								"_object": true,
								"1": {
									"_object": true,
									"Username": {"_value": "user@isp", "_type": "xsd:string"},
									"Password": {"_value": "pppoe-pwd", "_type": "xsd:string"}
								}
							}
						}
					}
				}
			}
		}
	}`
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	SanitizeTree(doc)

	entries := FlattenTree(doc)
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Path, "KeyPassphrase"),
			strings.HasSuffix(e.Path, "Password"),
			strings.HasSuffix(e.Path, "PreSharedKey.1.KeyPassphrase"):
			if v, _ := e.Value.(string); v != "(redacted)" {
				t.Errorf("path %q value=%v, esperava (redacted)", e.Path, e.Value)
			}
		default:
			if v, ok := e.Value.(string); ok && v == "(redacted)" {
				t.Errorf("path %q sanitizado indevidamente", e.Path)
			}
		}
	}
	// SSID e Username devem manter valor original.
	for _, e := range entries {
		if strings.HasSuffix(e.Path, ".SSID") {
			if v, _ := e.Value.(string); v != "celinet-2g" {
				t.Errorf("SSID alterado: %v", e.Value)
			}
		}
		if strings.HasSuffix(e.Path, ".Username") {
			if v, _ := e.Value.(string); v != "user@isp" {
				t.Errorf("Username alterado: %v", e.Value)
			}
		}
	}
}

func findByPath(entries []TreeEntry, path string) *TreeEntry {
	for i := range entries {
		if entries[i].Path == path {
			return &entries[i]
		}
	}
	return nil
}

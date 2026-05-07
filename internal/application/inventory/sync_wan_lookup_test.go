package inventory

import "testing"

// helper TR-069 shape: cada parâmetro é um {"_value": ..., "_type": ...} aninhado.
func paramScalar(value any) map[string]any {
	return map[string]any{"_value": value, "_type": "xsd:string"}
}

// TestFindFirstWANField_TR098_DefaultIndex — caso clássico .1.1.1.
func TestFindFirstWANField_TR098_DefaultIndex(t *testing.T) {
	raw := map[string]any{
		"InternetGatewayDevice": map[string]any{
			"WANDevice": map[string]any{
				"1": map[string]any{
					"WANConnectionDevice": map[string]any{
						"1": map[string]any{
							"WANPPPConnection": map[string]any{
								"1": map[string]any{
									"Username":          paramScalar("operador-1"),
									"ExternalIPAddress": paramScalar("100.64.10.5"),
								},
							},
						},
					},
				},
			},
		},
	}
	if got := findFirstWANField(raw, "Username"); got != "operador-1" {
		t.Errorf("Username=%q want operador-1", got)
	}
	if got := findFirstWANField(raw, "ExternalIPAddress"); got != "100.64.10.5" {
		t.Errorf("IP=%q want 100.64.10.5", got)
	}
}

// TestFindFirstWANField_TR098_AlternativeIndex — V-SOL/Realtek/ZTE expõem
// frequentemente o uplink em WANConnectionDevice.2 ou .3 (uma conexão por
// VLAN). O extractor precisa varrer todas.
func TestFindFirstWANField_TR098_AlternativeIndex(t *testing.T) {
	raw := map[string]any{
		"InternetGatewayDevice": map[string]any{
			"WANDevice": map[string]any{
				"1": map[string]any{
					"WANConnectionDevice": map[string]any{
						// .1 é bridge mode (sem auth); .3 é o PPPoE de verdade.
						"1": map[string]any{
							"WANIPConnection": map[string]any{
								"1": map[string]any{
									"ConnectionType": paramScalar("Bridge"),
								},
							},
						},
						"3": map[string]any{
							"WANPPPConnection": map[string]any{
								"1": map[string]any{
									"Username":          paramScalar("luciano17361"),
									"ExternalIPAddress": paramScalar("177.10.20.30"),
								},
							},
						},
					},
				},
			},
		},
	}
	if got := findFirstWANField(raw, "Username"); got != "luciano17361" {
		t.Errorf("Username=%q want luciano17361 (encontrar em .3)", got)
	}
	if got := findFirstWANField(raw, "ExternalIPAddress"); got != "177.10.20.30" {
		t.Errorf("IP=%q want 177.10.20.30", got)
	}
}

// TestFindFirstWANField_IPoEFallback — devices em IPoE puro têm o IP em
// WANIPConnection.1.ExternalIPAddress (sem PPPoE).
func TestFindFirstWANField_IPoEFallback(t *testing.T) {
	raw := map[string]any{
		"InternetGatewayDevice": map[string]any{
			"WANDevice": map[string]any{
				"1": map[string]any{
					"WANConnectionDevice": map[string]any{
						"1": map[string]any{
							"WANIPConnection": map[string]any{
								"1": map[string]any{
									"ExternalIPAddress": paramScalar("10.20.30.40"),
								},
							},
						},
					},
				},
			},
		},
	}
	if got := findFirstWANField(raw, "ExternalIPAddress"); got != "10.20.30.40" {
		t.Errorf("IP=%q want 10.20.30.40 (IPoE fallback)", got)
	}
	// Username não existe em IPoE — deve retornar vazio.
	if got := findFirstWANField(raw, "Username"); got != "" {
		t.Errorf("Username=%q want empty (sem PPPoE)", got)
	}
}

// TestFindFirstTR181Param — Device.PPP.Interface.X.Username.
func TestFindFirstTR181Param(t *testing.T) {
	raw := map[string]any{
		"Device": map[string]any{
			"PPP": map[string]any{
				"Interface": map[string]any{
					"_object": true,
					"2": map[string]any{
						"Username": paramScalar("user-tr181"),
					},
				},
			},
		},
	}
	if got := findFirstTR181Param(raw, "PPP.Interface", "Username"); got != "user-tr181" {
		t.Errorf("got %q want user-tr181", got)
	}
}

// TestFindFirstTR181IPv4 — pula loopback e link-local, devolve o primeiro
// IP usável.
func TestFindFirstTR181IPv4(t *testing.T) {
	raw := map[string]any{
		"Device": map[string]any{
			"IP": map[string]any{
				"Interface": map[string]any{
					"_object": true,
					"1": map[string]any{
						"IPv4Address": map[string]any{
							"1": map[string]any{
								"IPAddress": paramScalar("127.0.0.1"), // loopback — pula
							},
						},
					},
					"2": map[string]any{
						"IPv4Address": map[string]any{
							"1": map[string]any{
								"IPAddress": paramScalar("169.254.1.1"), // link-local — pula
							},
							"2": map[string]any{
								"IPAddress": paramScalar("100.64.5.5"), // CGNAT — válido
							},
						},
					},
				},
			},
		},
	}
	if got := findFirstTR181IPv4(raw); got != "100.64.5.5" {
		t.Errorf("got %q want 100.64.5.5", got)
	}
}

// TestIsUsableWANIP — gates do filtro.
func TestIsUsableWANIP(t *testing.T) {
	cases := map[string]bool{
		"100.64.5.5":   true,  // CGNAT
		"177.10.20.30": true,  // pública
		"10.0.0.1":     true,  // RFC1918 (válida — operador pode estar atrás de NAT)
		"127.0.0.1":    false, // loopback
		"0.0.0.0":      false, // unspecified
		"169.254.1.1":  false, // link-local
		"":             false,
		"not-an-ip":    false,
	}
	for ip, want := range cases {
		if got := isUsableWANIP(ip); got != want {
			t.Errorf("isUsableWANIP(%q)=%v want %v", ip, got, want)
		}
	}
}

package telemetry

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/celinet/sentinel-acs/internal/domain/inventory"
	tele "github.com/celinet/sentinel-acs/internal/domain/telemetry"
)

// helper — monta raw como o NBI retornaria.
func paramScalar(value any, xsdType string) map[string]any {
	return map[string]any{"_value": value, "_type": xsdType}
}

func TestParseWifiVirtualParams(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	dev := uuid.New()
	raw := map[string]any{
		"VirtualParameters": map[string]any{
			"WiFi24G_SSID":    paramScalar("MinhaRede2G", "xsd:string"),
			"WiFi24G_Channel": paramScalar("6", "xsd:int"),
			"WiFi24G_Clients": paramScalar("12", "xsd:int"),
			"WiFi24G_TxPower": paramScalar("100", "xsd:int"),
			"WiFi5G_SSID":     paramScalar("MinhaRede5G", "xsd:string"),
			"WiFi5G_Channel":  paramScalar("36", "xsd:int"),
			"WiFi5G_Clients":  paramScalar("4", "xsd:int"),
		},
	}
	wifi, _, _ := ParseDevice(now, dev, raw)
	if len(wifi) != 2 {
		t.Fatalf("expected 2 SSIDs, got %d", len(wifi))
	}
	for _, s := range wifi {
		if s.SSID == "" || s.Band == "" {
			t.Errorf("sample sem SSID/Band: %+v", s)
		}
	}
}

func TestParseWifiTR098Fallback(t *testing.T) {
	now := time.Now()
	dev := uuid.New()
	raw := map[string]any{
		"InternetGatewayDevice": map[string]any{
			"LANDevice": map[string]any{
				"1": map[string]any{
					"WLANConfiguration": map[string]any{
						"1": map[string]any{
							"SSID":     paramScalar("Casa-2G", "xsd:string"),
							"Channel":  paramScalar("11", "xsd:int"),
							"Standard": paramScalar("g", "xsd:string"),
							"AssociatedDevice": map[string]any{
								"1": map[string]any{"MACAddress": paramScalar("aa:bb:cc:dd:ee:ff", "xsd:string")},
								"2": map[string]any{"MACAddress": paramScalar("11:22:33:44:55:66", "xsd:string")},
							},
						},
					},
				},
			},
		},
	}
	wifi, _, _ := ParseDevice(now, dev, raw)
	if len(wifi) == 0 {
		t.Fatal("esperava ao menos 1 sample TR-098")
	}
	s := wifi[0]
	if s.SSID != "Casa-2G" {
		t.Errorf("SSID=%q", s.SSID)
	}
	if s.Band != tele.Band24G {
		t.Errorf("Band=%q", s.Band)
	}
	if s.ConnectedClients == nil || *s.ConnectedClients != 2 {
		t.Errorf("connected=%v", s.ConnectedClients)
	}
}

func TestParseWanVirtualParams(t *testing.T) {
	now := time.Now()
	dev := uuid.New()
	raw := map[string]any{
		"VirtualParameters": map[string]any{
			"WAN_RxBytes":    paramScalar("1000000", "xsd:unsignedInt"),
			"WAN_TxBytes":    paramScalar("250000", "xsd:unsignedInt"),
			"OpticalRxDBM":   paramScalar("-19.42", "xsd:string"),
			"OpticalTxDBM":   paramScalar("2.10", "xsd:string"),
		},
	}
	_, wan, _ := ParseDevice(now, dev, raw)
	if wan.RxBytes == nil || *wan.RxBytes != 1000000 {
		t.Errorf("rx=%v", wan.RxBytes)
	}
	if wan.OpticalRxDBM == nil || *wan.OpticalRxDBM != -19.42 {
		t.Errorf("rxdbm=%v", wan.OpticalRxDBM)
	}
}

func TestParseSystemFallbackTR098(t *testing.T) {
	now := time.Now()
	dev := uuid.New()
	raw := map[string]any{
		"InternetGatewayDevice": map[string]any{
			"DeviceInfo": map[string]any{
				"UpTime":          paramScalar("123456", "xsd:unsignedInt"),
				"X_HW_CPUUsage":   paramScalar("23.5", "xsd:string"),
				"X_HW_MemUsage":   paramScalar("48", "xsd:string"),
			},
		},
	}
	_, _, sys := ParseDevice(now, dev, raw)
	if sys.UptimeSeconds == nil || *sys.UptimeSeconds != 123456 {
		t.Errorf("uptime=%v", sys.UptimeSeconds)
	}
	if sys.CPUPct == nil || *sys.CPUPct != 23.5 {
		t.Errorf("cpu=%v", sys.CPUPct)
	}
}

func TestParseEmptyRawProducesNoSamples(t *testing.T) {
	now := time.Now()
	dev := uuid.New()
	wifi, wan, sys := ParseDevice(now, dev, map[string]any{})
	if len(wifi) != 0 {
		t.Errorf("wifi=%d", len(wifi))
	}
	if wan.HasAnyMetric() {
		t.Errorf("wan should be empty: %+v", wan)
	}
	if sys.HasAnyMetric() {
		t.Errorf("sys should be empty: %+v", sys)
	}
}

func TestSplitChunks(t *testing.T) {
	cases := []struct {
		name string
		n    int
		size int
		want int
	}{
		{"exato", 200, 100, 2},
		{"resto", 250, 100, 3},
		{"menor", 50, 100, 1},
		{"zero", 0, 100, 0},
		{"size_zero_returns_one_chunk", 10, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			devs := make([]inventory.Device, c.n)
			got := splitChunks(devs, c.size)
			if c.n == 0 {
				if got != nil {
					t.Errorf("want nil, got %d chunks", len(got))
				}
				return
			}
			if len(got) != c.want {
				t.Errorf("want %d chunks, got %d", c.want, len(got))
			}
		})
	}
}

func TestInferBandFromChannel(t *testing.T) {
	cases := []struct {
		ch   int
		want string
	}{
		{1, tele.Band24G},
		{6, tele.Band24G},
		{11, tele.Band24G},
		{36, tele.Band5G},
		{149, tele.Band5G},
		{0, ""},
		{20, ""},
	}
	for _, c := range cases {
		got := inferBandStr(c.ch)
		if got != c.want {
			t.Errorf("ch=%d → got=%q want=%q", c.ch, got, c.want)
		}
	}
}

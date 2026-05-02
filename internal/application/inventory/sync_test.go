package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"

	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
)

// makeServer monta um httptest server que devolve as devices fornecidas.
func makeServer(t *testing.T, devices []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devices/" {
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(devices)
	}))
}

// makeDeviceRaw simula a estrutura de resposta do NBI para testes.
func makeDeviceRaw(id, mfg, model, serial, fw, pppoe string) map[string]any {
	return map[string]any{
		"_id":         id,
		"_lastInform": time.Now().Format(time.RFC3339),
		"DeviceID": map[string]any{
			"_object":      true,
			"Manufacturer": map[string]any{"_value": mfg},
			"OUI":          map[string]any{"_value": "00E0FC"},
			"ProductClass": map[string]any{"_value": model},
			"SerialNumber": map[string]any{"_value": serial},
		},
		"InternetGatewayDevice": map[string]any{
			"DeviceInfo": map[string]any{
				"SoftwareVersion": map[string]any{"_value": fw},
			},
			"WANDevice": map[string]any{
				"1": map[string]any{
					"WANConnectionDevice": map[string]any{
						"1": map[string]any{
							"WANPPPConnection": map[string]any{
								"1": map[string]any{
									"Username": map[string]any{"_value": pppoe},
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestSyncCreatesNewDevice(t *testing.T) {
	devices := newFakeDeviceRepo()
	customers := newFakeCustomerRepo()
	vendors := newFakeVendorRepo()
	models := newFakeModelRepo()

	srv := makeServer(t, []map[string]any{
		makeDeviceRaw("ABC-123", "Huawei", "HG8245H", "SN001", "V5R019", ""),
	})
	defer srv.Close()

	svc := NewSyncService(devices, customers, vendors, models,
		genieacs.New(srv.URL, "", ""), 30*time.Minute)

	res, err := svc.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.Total != 1 || res.Created != 1 {
		t.Errorf("esperava 1 created, got %+v", res)
	}

	d, err := devices.GetByGenieACSID(context.Background(), "ABC-123")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if d.SerialNumber != "SN001" {
		t.Errorf("serial: %q", d.SerialNumber)
	}
	if d.FirmwareVersion != "V5R019" {
		t.Errorf("firmware: %q", d.FirmwareVersion)
	}
	if d.OUI != "00E0FC" {
		t.Errorf("OUI: %q", d.OUI)
	}
	if d.Status != domain.StatusOnline {
		t.Errorf("status: %q", d.Status)
	}
	if d.ModelID == nil {
		t.Error("modelID deveria estar populado")
	}

	// Vendor + model criados automaticamente.
	v, err := vendors.GetBySlug(context.Background(), "huawei")
	if err != nil {
		t.Fatalf("vendor: %v", err)
	}
	if v.Name != "Huawei" {
		t.Errorf("vendor name: %q", v.Name)
	}
	m, err := models.GetByVendorAndModel(context.Background(), v.ID, "HG8245H")
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if m.TRDataModel != domain.TR098 {
		t.Errorf("trModel: %q", m.TRDataModel)
	}
}

func TestSyncIdempotent(t *testing.T) {
	devices := newFakeDeviceRepo()
	customers := newFakeCustomerRepo()
	vendors := newFakeVendorRepo()
	models := newFakeModelRepo()

	raw := makeDeviceRaw("ABC-999", "ZTE", "F670L", "SN999", "V1.0", "")

	srv := makeServer(t, []map[string]any{raw})
	defer srv.Close()

	svc := NewSyncService(devices, customers, vendors, models,
		genieacs.New(srv.URL, "", ""), 30*time.Minute)

	if _, err := svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	res2, err := svc.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res2.Created != 0 || res2.Updated != 1 {
		t.Errorf("segundo tick deveria ser update, got %+v", res2)
	}

	// Apenas 1 vendor "zte" no fake (não duplicou).
	all, _ := vendors.List(context.Background())
	if len(all) != 1 {
		t.Errorf("vendor duplicado: %d", len(all))
	}
}

func TestSyncLinksCustomerByPPPoE(t *testing.T) {
	customers := newFakeCustomerRepo(domain.Customer{
		FullName:   "João Silva",
		PPPoELogin: "joao123",
		Status:     domain.CustomerActive,
	})
	devices := newFakeDeviceRepo()
	vendors := newFakeVendorRepo()
	models := newFakeModelRepo()

	srv := makeServer(t, []map[string]any{
		makeDeviceRaw("CPE-X", "Intelbras", "WiFiber 121AC", "SNX", "V2.0", "joao123"),
	})
	defer srv.Close()

	svc := NewSyncService(devices, customers, vendors, models,
		genieacs.New(srv.URL, "", ""), 30*time.Minute)

	res, err := svc.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.LinkedCustomer != 1 {
		t.Errorf("esperava 1 customer linkado, got %d", res.LinkedCustomer)
	}

	d, _ := devices.GetByGenieACSID(context.Background(), "CPE-X")
	if d.CustomerID == nil {
		t.Fatal("customer não vinculado")
	}
}

func TestSyncOfflineWhenLastInformOld(t *testing.T) {
	devices := newFakeDeviceRepo()
	customers := newFakeCustomerRepo()
	vendors := newFakeVendorRepo()
	models := newFakeModelRepo()

	old := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	raw := makeDeviceRaw("OLD-1", "Nokia", "G-140W-C", "SOLD", "1.0", "")
	raw["_lastInform"] = old

	srv := makeServer(t, []map[string]any{raw})
	defer srv.Close()

	svc := NewSyncService(devices, customers, vendors, models,
		genieacs.New(srv.URL, "", ""), 30*time.Minute)

	if _, err := svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, _ := devices.GetByGenieACSID(context.Background(), "OLD-1")
	if d.Status != domain.StatusOffline {
		t.Errorf("status esperado offline, got %q", d.Status)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Huawei":           "huawei",
		"  TP-Link  ":      "tp-link",
		"Nokia G140":       "nokia-g140",
		"WiFiber 121.AC":   "wifiber-121-ac",
		"":                 "unknown",
		"---":              "unknown",
		"X__Y":             "x-y",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q): got %q, want %q", in, got, want)
		}
	}
}

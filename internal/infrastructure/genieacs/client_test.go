package genieacs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devices/" {
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "1" {
			t.Errorf("limit não enviado: %v", r.URL.Query())
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", "")
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestQueryDevicesParsesLastInform(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{
			"_id": "ABC-123",
			"_lastInform": "2026-05-01T10:00:00Z",
			"_lastBoot": "2026-04-30T08:00:00Z",
			"_tags": ["voalle", "homologacao"]
		}]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", "")
	devs, err := c.QueryDevices(context.Background(), Query{Limit: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("esperava 1 device, got %d", len(devs))
	}
	d := devs[0]
	if d.ID != "ABC-123" {
		t.Errorf("id inesperado: %q", d.ID)
	}
	expected, _ := time.Parse(time.RFC3339, "2026-05-01T10:00:00Z")
	if !d.LastInform.Equal(expected) {
		t.Errorf("lastInform: got %v, want %v", d.LastInform, expected)
	}
	if d.LastBoot == nil {
		t.Errorf("lastBoot deveria ser preenchido")
	}
	if len(d.Tags) != 2 {
		t.Errorf("esperava 2 tags, got %v", d.Tags)
	}
}

func TestQueryDevicesEncodesFilter(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("query")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", "")
	_, err := c.QueryDevices(context.Background(), Query{
		Filter: map[string]any{"_id": "X-Y-Z"},
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(capturedQuery), &got); err != nil {
		t.Fatalf("unmarshal query: %v", err)
	}
	if got["_id"] != "X-Y-Z" {
		t.Errorf("filter não bateu: %v", got)
	}
}

func TestGetDeviceNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", "")
	_, err := c.GetDevice(context.Background(), "missing-id")
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("esperava ErrDeviceNotFound, got %v", err)
	}
}

func TestSetParameterValuesPostsBody(t *testing.T) {
	var captured struct {
		Path  string
		Body  map[string]any
		Query string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Path = r.URL.Path
		captured.Query = r.URL.RawQuery
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"_id":"task-1","name":"setParameterValues"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", "")
	id, err := c.SetParameterValues(context.Background(), "DEV-1", []Parameter{
		{Path: "InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID", Value: "MinhaRede", Type: "xsd:string"},
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if string(id) != "task-1" {
		t.Errorf("task id inesperado: %q", id)
	}
	if !strings.Contains(captured.Path, "/devices/DEV-1/tasks") {
		t.Errorf("path inesperado: %s", captured.Path)
	}
	if !strings.Contains(captured.Query, "connection_request") {
		t.Errorf("connection_request não enviado: %s", captured.Query)
	}
	if captured.Body["name"] != "setParameterValues" {
		t.Errorf("name body inesperado: %v", captured.Body)
	}
}

func TestRebootAndCR(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"_id":"t-` + http.StatusText(calls) + `"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", "")
	if _, err := c.Reboot(context.Background(), "DEV-2"); err != nil {
		t.Fatalf("reboot: %v", err)
	}
	if err := c.ConnectionRequest(context.Background(), "DEV-2"); err != nil {
		t.Fatalf("CR: %v", err)
	}
	if calls != 2 {
		t.Errorf("esperava 2 chamadas, got %d", calls)
	}
}

func TestAPIErrorCarriesStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "device offline", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(srv.URL, "", "")
	_, err := c.SetParameterValues(context.Background(), "DEV-X", []Parameter{
		{Path: "X", Value: "Y", Type: "xsd:string"},
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("esperava APIError, got %v", err)
	}
	if apiErr.Status != http.StatusServiceUnavailable {
		t.Errorf("status: %d", apiErr.Status)
	}
	if !strings.Contains(apiErr.Body, "device offline") {
		t.Errorf("body: %q", apiErr.Body)
	}
}

func TestBasicAuthEnviado(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "admin" || p != "secret" {
			t.Errorf("auth incorreta: ok=%v u=%q p=%q", ok, u, p)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "secret")
	_ = c.Ping(context.Background())
}

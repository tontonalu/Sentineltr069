package templates

import (
	"strings"
	"testing"
	"time"

	"github.com/celinet/sentinel-acs/internal/domain/inventory"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
	"github.com/google/uuid"
)

func ctxFixture() Context {
	return Context{
		Customer: &inventory.Customer{
			ID:         uuid.New(),
			FullName:   "João da Silva",
			Document:   "123.456.789-01",
			PPPoELogin: "joaosilva",
			PlanName:   "Plano 500MB",
			Status:     "active",
		},
		Device: &inventory.Device{
			ID:           uuid.New(),
			GenieACSID:   "abc-123",
			SerialNumber: "SN12345",
		},
		POP: &inventory.POP{
			ID:   uuid.New(),
			Name: "Centro",
			City: "Niterói",
		},
		Now: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
	}
}

func TestRenderLiteral(t *testing.T) {
	e := NewEngine()
	got, err := e.Render("hello world", ctxFixture())
	if err != nil || got != "hello world" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestRenderSimpleVar(t *testing.T) {
	e := NewEngine()
	got, err := e.Render("{{customer.full_name}}", ctxFixture())
	if err != nil || got != "João da Silva" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestRenderUpperFirstWord(t *testing.T) {
	e := NewEngine()
	got, err := e.Render("{{ customer.full_name | upper | first_word }}_2G", ctxFixture())
	if err != nil || got != "JOÃO_2G" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestRenderLastNDigitsFromCPF(t *testing.T) {
	e := NewEngine()
	got, err := e.Render("{{ customer.document | last_n_digits:8 }}", ctxFixture())
	if err != nil || got != "45678901" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestRenderSlugify(t *testing.T) {
	e := NewEngine()
	got, err := e.Render("{{ customer.full_name | slugify }}", ctxFixture())
	if err != nil || got != "joao-da-silva" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestRenderUnknownVar(t *testing.T) {
	e := NewEngine()
	_, err := e.Render("{{ contract.total }}", ctxFixture())
	if err == nil || !strings.Contains(err.Error(), "variável desconhecida") {
		t.Fatalf("esperava erro de variável desconhecida, got %v", err)
	}
}

func TestRenderMissingFieldEmpty(t *testing.T) {
	e := NewEngine()
	got, err := e.Render("[{{ customer.address }}]", ctxFixture())
	if err != nil || got != "[]" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestRenderUnknownFilter(t *testing.T) {
	e := NewEngine()
	_, err := e.Render("{{ customer.full_name | rocket }}", ctxFixture())
	if err == nil || !strings.Contains(err.Error(), "filtro desconhecido") {
		t.Fatalf("esperava erro filtro, got %v", err)
	}
}

func TestRenderDateFilter(t *testing.T) {
	e := NewEngine()
	got, err := e.Render("{{ now | date:\"2006-01-02\" }}", ctxFixture())
	if err != nil || got != "2026-05-02" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestRenderMaskPhone(t *testing.T) {
	e := NewEngine()
	got, err := e.Render("{{ '11987654321' | mask_phone }}", Context{Now: time.Now()})
	// nota: literal entre aspas não é parte da sintaxe atual; isso confirma erro.
	if err == nil {
		t.Fatalf("aspas em var não são suportadas, got %q", got)
	}
}

func TestRenderParameterCoerceInt(t *testing.T) {
	e := NewEngine()
	p := tmpl.Parameter{
		CanonicalKey:  "wifi.channel",
		ValueTemplate: "11",
		DataType:      tmpl.DataTypeInt,
		TRPath:        "Device.WiFi.Radio.1.Channel",
	}
	res, err := e.RenderParameter(p, ctxFixture())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v, ok := res.Value.(int64); !ok || v != 11 {
		t.Fatalf("got %T %v", res.Value, res.Value)
	}
}

func TestRenderProfileStopsOnError(t *testing.T) {
	e := NewEngine()
	params := []tmpl.Parameter{
		{CanonicalKey: "ok", ValueTemplate: "literal", DataType: tmpl.DataTypeString, TRPath: "x"},
		{CanonicalKey: "bad", ValueTemplate: "{{ contract.invalid }}", DataType: tmpl.DataTypeString, TRPath: "y"},
	}
	_, err := e.RenderProfile(params, ctxFixture())
	if err == nil {
		t.Fatal("esperava erro propagado de RenderProfile")
	}
}

func TestNoCustomerFallsToEmpty(t *testing.T) {
	e := NewEngine()
	ctx := Context{Now: time.Now()}
	got, err := e.Render("[{{ customer.full_name }}]", ctx)
	if err != nil || got != "[]" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

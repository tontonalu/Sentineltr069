// Package templates implementa o engine de renderização de value_template.
//
// Sintaxe: {{ var.path | filter1 | filter2:arg | filter3 }}
//
// Variáveis raiz disponíveis: customer, device, pop, now.
// Acesso a campos via dot notation: customer.full_name, device.serial.
//
// Sandbox por construção:
//   - Zero acesso a filesystem, network, exec.
//   - Apenas leitura de Context (struct read-only).
//   - Filtros são funções puras Go registradas no construtor.
//
// Pongo2 foi descartado: peso (~3 MB), sandbox depende de configuração e
// abre superfície de ataque desnecessária. O subset acima cobre todos os
// casos do RF-03 sem ônus de manutenção.
package templates

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/celinet/sentinel-acs/internal/domain/inventory"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
)

// Context é o snapshot de variáveis disponíveis a um value_template.
// Ponteiros nil são permitidos (campos ausentes resultam em string vazia).
type Context struct {
	Customer *inventory.Customer
	Device   *inventory.Device
	POP      *inventory.POP
	Now      time.Time
}

// Engine renderiza value_templates. Filtros são registrados no construtor e
// imutáveis durante a vida do engine — thread-safe sem lock.
type Engine struct {
	filters map[string]FilterFunc
}

// FilterFunc — assinatura unificada para filtros. arg é o argumento opcional
// declarado depois do `:` (ex: last_n_digits:8 → arg = "8").
type FilterFunc func(value any, arg string) (any, error)

// NewEngine devolve engine com os filtros canônicos prontos.
func NewEngine() *Engine {
	e := &Engine{filters: map[string]FilterFunc{}}
	e.registerDefaults()
	return e
}

// RegisterFilter adiciona um filtro custom. Substitui se já existir.
// Não thread-safe — chamar apenas durante bootstrap.
func (e *Engine) RegisterFilter(name string, f FilterFunc) {
	e.filters[name] = f
}

// Render aplica o template a um Context. Erro é retornado se a expressão
// referencia variável inexistente, filtro desconhecido, ou falha de filtro.
//
// Strings que não contêm `{{` são retornadas literalmente (rota rápida).
func (e *Engine) Render(template string, ctx Context) (string, error) {
	if !strings.Contains(template, "{{") {
		return template, nil
	}
	var b strings.Builder
	i := 0
	for i < len(template) {
		open := strings.Index(template[i:], "{{")
		if open < 0 {
			b.WriteString(template[i:])
			break
		}
		open += i
		b.WriteString(template[i:open])
		close := strings.Index(template[open:], "}}")
		if close < 0 {
			return "", fmt.Errorf("template: '{{' não fechado em pos %d", open)
		}
		expr := template[open+2 : open+close]
		val, err := e.evalExpr(expr, ctx)
		if err != nil {
			return "", err
		}
		b.WriteString(coerceString(val))
		i = open + close + 2
	}
	return b.String(), nil
}

// RenderParameter aplica Render + valida o resultado contra DataType,
// devolvendo ResolvedParameter pronto para virar Parameter no NBI.
func (e *Engine) RenderParameter(p tmpl.Parameter, ctx Context) (tmpl.ResolvedParameter, error) {
	raw, err := e.Render(p.ValueTemplate, ctx)
	if err != nil {
		return tmpl.ResolvedParameter{}, fmt.Errorf("render %q: %w", p.CanonicalKey, err)
	}
	val, err := coerceTo(raw, p.DataType)
	if err != nil {
		return tmpl.ResolvedParameter{}, fmt.Errorf("coerce %q: %w", p.CanonicalKey, err)
	}
	return tmpl.ResolvedParameter{
		CanonicalKey: p.CanonicalKey,
		TRPath:       p.TRPath,
		Value:        val,
		DataType:     p.DataType,
		IsSecret:     p.IsSecret,
	}, nil
}

// RenderProfile renderiza todos os parâmetros de um profile. Para de propagar
// no primeiro erro — provisioning não aceita estado parcial.
func (e *Engine) RenderProfile(params []tmpl.Parameter, ctx Context) ([]tmpl.ResolvedParameter, error) {
	out := make([]tmpl.ResolvedParameter, 0, len(params))
	for _, p := range params {
		r, err := e.RenderParameter(p, ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ──────────────── Avaliação ────────────────

// pipeRe separa expressões do tipo `var.path | filter:arg | filter2`.
// Aceita whitespace entre tokens; argumento de filtro pode conter espaços.
var pipeRe = regexp.MustCompile(`\s*\|\s*`)

func (e *Engine) evalExpr(expr string, ctx Context) (any, error) {
	parts := pipeRe.Split(strings.TrimSpace(expr), -1)
	if len(parts) == 0 {
		return "", nil
	}
	val, err := lookup(parts[0], ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range parts[1:] {
		name, arg := splitFilter(f)
		fn, ok := e.filters[name]
		if !ok {
			return nil, fmt.Errorf("template: filtro desconhecido %q", name)
		}
		val, err = fn(val, arg)
		if err != nil {
			return nil, fmt.Errorf("filtro %s: %w", name, err)
		}
	}
	return val, nil
}

// splitFilter separa "name:arg" em (name, arg). arg pode ser quoted ou plain.
func splitFilter(s string) (string, string) {
	colon := strings.Index(s, ":")
	if colon < 0 {
		return strings.TrimSpace(s), ""
	}
	name := strings.TrimSpace(s[:colon])
	arg := strings.TrimSpace(s[colon+1:])
	if len(arg) >= 2 && (arg[0] == '"' || arg[0] == '\'') && arg[0] == arg[len(arg)-1] {
		arg = arg[1 : len(arg)-1]
	}
	return name, arg
}

// lookup percorre dot-path no Context. Strings vazias para campo ausente,
// erro só se a raiz for desconhecida (ajuda a pegar typos).
func lookup(path string, ctx Context) (any, error) {
	parts := strings.Split(path, ".")
	root := parts[0]

	var current any
	switch root {
	case "customer":
		if ctx.Customer == nil {
			return "", nil
		}
		current = customerMap(ctx.Customer)
	case "device":
		if ctx.Device == nil {
			return "", nil
		}
		current = deviceMap(ctx.Device)
	case "pop":
		if ctx.POP == nil {
			return "", nil
		}
		current = popMap(ctx.POP)
	case "now":
		if len(parts) > 1 {
			return "", fmt.Errorf("template: 'now' não tem subcampos")
		}
		return ctx.Now, nil
	default:
		return nil, fmt.Errorf("template: variável desconhecida %q (use customer|device|pop|now)", root)
	}

	for _, p := range parts[1:] {
		m, ok := current.(map[string]any)
		if !ok {
			return "", nil
		}
		v, ok := m[p]
		if !ok {
			return "", nil
		}
		current = v
	}
	return current, nil
}

// customerMap converte Customer em map para acesso por dot-path.
// Mantemos chaves snake_case porque é a convenção do template (consistente
// com nomenclatura do NBI/Voalle).
func customerMap(c *inventory.Customer) map[string]any {
	return map[string]any{
		"id":              c.ID.String(),
		"external_id":     c.ExternalID,
		"external_source": c.ExternalSource,
		"full_name":       c.FullName,
		"document":        c.Document,
		"pppoe_login":     c.PPPoELogin,
		"plan_name":       c.PlanName,
		"address":         c.Address,
		"status":          c.Status,
	}
}

func deviceMap(d *inventory.Device) map[string]any {
	model := ""
	if d.ModelID != nil {
		model = d.ModelID.String()
	}
	ipWan := ""
	if d.IPWAN != nil {
		ipWan = d.IPWAN.String()
	}
	return map[string]any{
		"id":               d.ID.String(),
		"genieacs_id":      d.GenieACSID,
		"serial":           d.SerialNumber,
		"serial_number":    d.SerialNumber,
		"mac":              d.MAC,
		"oui":              d.OUI,
		"model_id":         model,
		"firmware_version": d.FirmwareVersion,
		"ip_wan":           ipWan,
		"status":           d.Status,
	}
}

func popMap(p *inventory.POP) map[string]any {
	return map[string]any{
		"id":    p.ID.String(),
		"name":  p.Name,
		"city":  p.City,
		"state": p.State,
	}
}

// ──────────────── Coerção ────────────────

func coerceString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%v", v)
}

// coerceTo converte string renderizada para o tipo do parâmetro. Usado
// apenas no resultado final (RenderParameter).
func coerceTo(s string, dt tmpl.DataType) (any, error) {
	switch dt {
	case tmpl.DataTypeString, "":
		return s, nil
	case tmpl.DataTypeInt:
		return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	case tmpl.DataTypeUnsignedInt:
		v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return nil, err
		}
		return v, nil
	case tmpl.DataTypeBool:
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off", "":
			return false, nil
		}
		return nil, fmt.Errorf("bool inválido: %q", s)
	case tmpl.DataTypeDateTime:
		return time.Parse(time.RFC3339, strings.TrimSpace(s))
	}
	return nil, errors.New("data_type desconhecido")
}


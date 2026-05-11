package devices

import (
	"strconv"
	"strings"

	"github.com/google/uuid"

	devapp "github.com/celinet/sentinel-acs/internal/application/devices"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
)

// inputTypeFor mapeia DataType → atributo type do input.
func inputTypeFor(dt tmpl.DataType) string {
	switch dt {
	case tmpl.DataTypeInt, tmpl.DataTypeUnsignedInt:
		return "number"
	case tmpl.DataTypeDateTime:
		return "datetime-local"
	}
	return "text"
}

// isTruthyValue — interpretação leniente para popular <select> de bool.
func isTruthyValue(v string) bool {
	switch v {
	case "true", "1", "TRUE", "True", "yes", "on":
		return true
	}
	return false
}

// shortUUID — primeiros 8 chars; suficiente para link clicável legível.
func shortUUID(id uuid.UUID) string {
	s := id.String()
	if len(s) >= 8 {
		return s[:8] + "…"
	}
	return s
}

// hasAnyEditable — true quando o usuário pode editar e ao menos 1 field do
// card é writable. Usado pelo wrapper de form do card para decidir se
// renderiza o <form> com botão Salvar ou só os campos read-only.
func hasAnyEditable(canEdit bool, fields []devapp.FieldView) bool {
	if !canEdit {
		return false
	}
	for _, f := range fields {
		if f.Writable {
			return true
		}
	}
	return false
}

// hasSkipped — true quando ao menos um campo foi preservado (secret em
// branco). Usado pelo banner para decidir se mostra a "nota informativa".
func hasSkipped(results []devapp.FieldUpdateResult) bool {
	for _, r := range results {
		if r.Skipped {
			return true
		}
	}
	return false
}

// batchSkippedSuffix — sufixo textual listando os canonical_keys preservados.
// Quando não há nenhum skip, devolve string vazia.
func batchSkippedSuffix(results []devapp.FieldUpdateResult) string {
	keys := make([]string, 0)
	for _, r := range results {
		if r.Skipped {
			keys = append(keys, r.CanonicalKey)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	return "senha em branco preservada: " + strings.Join(keys, ", ")
}

// redundantDeviceKeys — chaves canônicas da categoria 'device' que já
// aparecem na Identificação como campos do Device entity. Removemos da
// seção "Informações TR-069" para evitar duplicação visual (ex: serial
// aparece em "Serial" e antes aparecia também em "Serial Number").
var redundantDeviceKeys = map[string]struct{}{
	"device.firmware.version": {},
	"device.serial":           {},
	"device.manufacturer":     {},
	"device.model":            {},
	"device.product_class":    {}, // alias do model em vendors brasileiros
}

// filterRedundantDeviceFields remove os fields cuja informação já está
// renderizada na Identificação E os campos de portas físicas (que vão para
// a aba "Status das portas" via telemetry, sem repetir aqui).
//
// O profile_view.go classifica `port.*` em CategoryDevice por padrão, então
// se não filtrarmos eles entopem a aba Dispositivo. Mantém ordem estável.
func filterRedundantDeviceFields(in []devapp.FieldView) []devapp.FieldView {
	out := make([]devapp.FieldView, 0, len(in))
	for _, f := range in {
		if _, dup := redundantDeviceKeys[f.CanonicalKey]; dup {
			continue
		}
		if strings.HasPrefix(f.CanonicalKey, "port.") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// filterWifiBand devolve os fields cuja canonical_key tem o sufixo
// solicitado (ex: ".2g" ou ".5g"). Mantém ordem.
func filterWifiBand(fields []devapp.FieldView, suffix string) []devapp.FieldView {
	out := make([]devapp.FieldView, 0, len(fields))
	for _, f := range fields {
		if strings.HasSuffix(f.CanonicalKey, "."+suffix) {
			out = append(out, f)
		}
	}
	return out
}

// filterWifiOther devolve os fields que não são claramente 2.4G nem 5G —
// vão para o card "Outros parâmetros Wi-Fi" (ex: country_code).
func filterWifiOther(fields []devapp.FieldView) []devapp.FieldView {
	out := make([]devapp.FieldView, 0, len(fields))
	for _, f := range fields {
		if !strings.HasSuffix(f.CanonicalKey, ".2g") && !strings.HasSuffix(f.CanonicalKey, ".5g") {
			out = append(out, f)
		}
	}
	return out
}

// filterPPPoEFields — fields que mapeiam PPPoE.username/password.
func filterPPPoEFields(fields []devapp.FieldView) []devapp.FieldView {
	out := make([]devapp.FieldView, 0, len(fields))
	for _, f := range fields {
		if strings.HasPrefix(f.CanonicalKey, "pppoe.") {
			out = append(out, f)
		}
	}
	return out
}

// filterNonPPPoEFields — demais fields da categoria WAN (IP, MTU, DNS, MAC, etc).
func filterNonPPPoEFields(fields []devapp.FieldView) []devapp.FieldView {
	out := make([]devapp.FieldView, 0, len(fields))
	for _, f := range fields {
		if !strings.HasPrefix(f.CanonicalKey, "pppoe.") {
			out = append(out, f)
		}
	}
	return out
}

// intToStr — atalho usado dentro dos templs (templ não suporta strconv.Itoa
// direto na expressão sem alias).
func intToStr(n int) string { return strconv.Itoa(n) }

// uuidFromStr — usado nas templates para reconstruir um uuid.UUID a partir
// da forma string (passada via parametro do componente). Ignora erros — se
// o caller já tem um Device.ID.String(), o parse sempre funciona.
func uuidFromStr(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}

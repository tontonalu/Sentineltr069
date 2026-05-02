package genieacs

import (
	"fmt"
	"strings"
)

// ParamValue extrai o valor escalar de um parâmetro TR-069 do JSON do NBI.
//
// O NBI retorna parâmetros como sub-objetos:
//
//	{
//	  "DeviceID": {
//	    "_object": true,
//	    "Manufacturer": {"_value": "Huawei", "_type": "xsd:string", "_timestamp": "..."}
//	  }
//	}
//
// ParamValue(raw, "DeviceID.Manufacturer") devolve "Huawei".
// Caminhos inexistentes devolvem nil — caller decide o tipo via type assertion.
func ParamValue(raw map[string]any, path string) any {
	if raw == nil || path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	var current any = raw
	for _, p := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		next, ok := m[p]
		if !ok {
			return nil
		}
		current = next
	}
	if m, ok := current.(map[string]any); ok {
		if v, ok := m["_value"]; ok {
			return v
		}
		// Não tem _value — caminho intermediário, devolve nil pra evitar
		// retornar o objeto inteiro (que poderia ser interpretado como string).
		return nil
	}
	return current
}

// ParamString é o atalho para ParamValue convertendo qualquer tipo escalar
// para string. Devolve "" para nil ou objetos.
func ParamString(raw map[string]any, path string) string {
	v := ParamValue(raw, path)
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
	case float64:
		// JSON sempre decode números para float64; remove decimal trivial.
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// FirstNonEmpty devolve o primeiro path que produz string não-vazia.
// Útil para escolher entre TR-098 e TR-181 sem saber qual o device usa.
func FirstNonEmpty(raw map[string]any, paths ...string) string {
	for _, p := range paths {
		if v := ParamString(raw, p); v != "" {
			return v
		}
	}
	return ""
}

// Param é um helper de instância no Device.
func (d Device) Param(path string) any { return ParamValue(d.Raw, path) }

// ParamString é o helper string para Device.
func (d Device) ParamString(path string) string { return ParamString(d.Raw, path) }

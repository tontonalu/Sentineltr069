package genieacs

import (
	"sort"
	"strings"
)

// TreeEntry é um nó da árvore TR-069 achatado a partir do Device.Raw do NBI.
//
// O NBI guarda a árvore como JSON aninhado. Cada folha é um objeto com chaves
// `_value`, `_type`, `_writable`, `_timestamp`; cada nó intermediário tem
// `_object: true`. Indices numéricos (instâncias) viram chaves "1", "2", "N"
// sob o objeto pai. FlattenTree percorre essa estrutura e devolve uma lista
// linearizada.
type TreeEntry struct {
	Path     string // ex: "InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID"
	Value    any    // valor escalar (string, float64, bool, nil)
	Type     string // "xsd:string", "xsd:int", etc — vazio quando não é folha
	Writable bool   // true se o NBI marcou _writable
	HasValue bool   // false para nós intermediários (apenas estrutura)
}

// FlattenTree achata a árvore TR-069 do GenieACS NBI.
//
// Saída ordenada alfabeticamente por Path (estável → diff entre snapshots
// fica trivial). Chaves que começam com `_` são metadados internos do NBI
// (`_id`, `_lastInform`, `_tags`, `_writable`, `_value`, ...) e não viram
// entradas — exceto que valores de folha são extraídos via `_value`.
//
// Limitação consciente: nós intermediários puros (apenas `_object: true`)
// não viram entries. Se o caller precisa navegar a árvore, agrupa as folhas
// por prefix.
func FlattenTree(raw map[string]any) []TreeEntry {
	if raw == nil {
		return nil
	}
	out := make([]TreeEntry, 0, 256)
	walkTree(raw, "", &out)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// walkTree é recursão depth-first. Concatena `parent + "." + key` exceto
// no nível raiz para evitar prefixo vazio.
func walkTree(node map[string]any, prefix string, out *[]TreeEntry) {
	for k, v := range node {
		if strings.HasPrefix(k, "_") {
			continue
		}
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		child, ok := v.(map[string]any)
		if !ok {
			// Folha "exposta" — raro no GenieACS, mas resiliente.
			*out = append(*out, TreeEntry{
				Path:     path,
				Value:    v,
				HasValue: true,
			})
			continue
		}
		// Sub-objeto: ou tem _value (folha) ou é nó intermediário (recurse).
		if val, hasVal := child["_value"]; hasVal {
			entry := TreeEntry{
				Path:     path,
				Value:    val,
				HasValue: true,
			}
			if t, ok := child["_type"].(string); ok {
				entry.Type = t
			}
			if w, ok := child["_writable"].(bool); ok {
				entry.Writable = w
			}
			*out = append(*out, entry)
			continue
		}
		walkTree(child, path, out)
	}
}

// secretLeafSubstrings são fragmentos case-insensitive que, quando aparecem
// no último segmento de um path TR-069, identificam folhas que provavelmente
// carregam dados sensíveis (senhas de Wi-Fi, PPPoE, SIP, etc).
//
// Lista deliberadamente conservadora: prefere falsos positivos (redact a mais)
// a falsos negativos (vazar). Operador pode ver o path mas não o valor.
var secretLeafSubstrings = []string{
	"password",
	"passphrase",
	"presharedkey",
	"secret",
	"authpassword",
	"authpasscode",
	"keypassphrase",
}

// SanitizeTree percorre a árvore JSON do NBI in-place e substitui o `_value`
// de toda folha cujo último segmento bata com algum dos fragmentos sensíveis
// por uma string literal "(redacted)". Não muda estrutura (paths continuam
// navegáveis e auto-mapeamento ainda detecta presença).
//
// Idempotente: aplicar duas vezes não muda nada.
func SanitizeTree(raw map[string]any) {
	if raw == nil {
		return
	}
	sanitizeNode(raw, "")
}

func sanitizeNode(node map[string]any, parentLeafName string) {
	for k, v := range node {
		if strings.HasPrefix(k, "_") {
			continue
		}
		child, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if _, hasVal := child["_value"]; hasVal {
			if isSecretLeafName(k) {
				child["_value"] = "(redacted)"
			}
			continue
		}
		sanitizeNode(child, k)
	}
	_ = parentLeafName // reservado para uso futuro (ex: detectar sub-objetos secret pelo nome do pai)
}

// isSecretLeafName devolve true se `name` (último segmento do path) contém
// algum fragmento da lista de sensíveis (case-insensitive).
func isSecretLeafName(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range secretLeafSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// FilterTree devolve apenas entries cujo path bate com prefix (igual ou começa
// com `prefix.`) e/ou contém `search` (case-insensitive substring no path).
// Argumentos vazios = sem filtro daquele tipo.
func FilterTree(entries []TreeEntry, prefix, search string) []TreeEntry {
	if prefix == "" && search == "" {
		return entries
	}
	searchLower := strings.ToLower(search)
	out := make([]TreeEntry, 0, len(entries))
	for _, e := range entries {
		if prefix != "" {
			if e.Path != prefix && !strings.HasPrefix(e.Path, prefix+".") {
				continue
			}
		}
		if searchLower != "" && !strings.Contains(strings.ToLower(e.Path), searchLower) {
			continue
		}
		out = append(out, e)
	}
	return out
}

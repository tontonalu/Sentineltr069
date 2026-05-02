package templates

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// registerDefaults popula o engine com os filtros canônicos da §8.2 do doc.
//
// Filtros são puros (sem efeito colateral) e operam sempre sobre string —
// se o input não for string, é coercido via coerceString primeiro.
func (e *Engine) registerDefaults() {
	e.RegisterFilter("upper", filterUpper)
	e.RegisterFilter("lower", filterLower)
	e.RegisterFilter("title", filterTitle)
	e.RegisterFilter("trim", filterTrim)
	e.RegisterFilter("first_word", filterFirstWord)
	e.RegisterFilter("last_word", filterLastWord)
	e.RegisterFilter("last_n_digits", filterLastNDigits)
	e.RegisterFilter("first_n_digits", filterFirstNDigits)
	e.RegisterFilter("digits_only", filterDigitsOnly)
	e.RegisterFilter("slugify", filterSlugify)
	e.RegisterFilter("mask_phone", filterMaskPhone)
	e.RegisterFilter("default", filterDefault)
	e.RegisterFilter("replace", filterReplace)
	e.RegisterFilter("substring", filterSubstring)
	e.RegisterFilter("date", filterDate)
}

func asString(v any) string { return coerceString(v) }

func filterUpper(v any, _ string) (any, error) { return strings.ToUpper(asString(v)), nil }
func filterLower(v any, _ string) (any, error) { return strings.ToLower(asString(v)), nil }
func filterTitle(v any, _ string) (any, error) {
	s := asString(v)
	out := make([]rune, 0, len(s))
	startWord := true
	for _, r := range s {
		if unicode.IsSpace(r) {
			startWord = true
			out = append(out, r)
			continue
		}
		if startWord {
			out = append(out, unicode.ToUpper(r))
			startWord = false
		} else {
			out = append(out, unicode.ToLower(r))
		}
	}
	return string(out), nil
}

func filterTrim(v any, _ string) (any, error) { return strings.TrimSpace(asString(v)), nil }

func filterFirstWord(v any, _ string) (any, error) {
	parts := strings.Fields(asString(v))
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], nil
}

func filterLastWord(v any, _ string) (any, error) {
	parts := strings.Fields(asString(v))
	if len(parts) == 0 {
		return "", nil
	}
	return parts[len(parts)-1], nil
}

// filterLastNDigits — retorna os últimos N dígitos numéricos do input.
// Útil para gerar senha PPPoE a partir do CPF: "123.456.789-01" → "78901".
func filterLastNDigits(v any, arg string) (any, error) {
	n, err := strconv.Atoi(arg)
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("last_n_digits exige arg numérico > 0, recebi %q", arg)
	}
	digits := digitsOnly(asString(v))
	if len(digits) <= n {
		return digits, nil
	}
	return digits[len(digits)-n:], nil
}

func filterFirstNDigits(v any, arg string) (any, error) {
	n, err := strconv.Atoi(arg)
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("first_n_digits exige arg numérico > 0, recebi %q", arg)
	}
	digits := digitsOnly(asString(v))
	if len(digits) <= n {
		return digits, nil
	}
	return digits[:n], nil
}

func filterDigitsOnly(v any, _ string) (any, error) { return digitsOnly(asString(v)), nil }

// filterSlugify — converte para slug ASCII lowercase com hifens.
// "João da Silva" → "joao-da-silva"
func filterSlugify(v any, _ string) (any, error) {
	s := strings.ToLower(asString(v))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case isAccented(r):
			b.WriteRune(stripAccent(r))
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-"), nil
}

// filterMaskPhone — formata um telefone BR. "11987654321" → "(11) 98765-4321".
// Aceita inputs já mascarados (extrai dígitos primeiro).
func filterMaskPhone(v any, _ string) (any, error) {
	d := digitsOnly(asString(v))
	switch len(d) {
	case 10:
		return fmt.Sprintf("(%s) %s-%s", d[:2], d[2:6], d[6:]), nil
	case 11:
		return fmt.Sprintf("(%s) %s-%s", d[:2], d[2:7], d[7:]), nil
	case 13: // +55DDXXXXXXXXX
		return fmt.Sprintf("+%s (%s) %s-%s", d[:2], d[2:4], d[4:9], d[9:]), nil
	}
	return d, nil
}

// filterDefault — retorna arg se valor vazio. {{ customer.address | default:"sem endereço" }}
func filterDefault(v any, arg string) (any, error) {
	s := asString(v)
	if strings.TrimSpace(s) == "" {
		return arg, nil
	}
	return s, nil
}

// filterReplace — formato arg "old=>new". Retira limitações da sintaxe simples.
func filterReplace(v any, arg string) (any, error) {
	parts := strings.SplitN(arg, "=>", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf(`replace exige formato "old=>new", recebi %q`, arg)
	}
	return strings.ReplaceAll(asString(v), parts[0], parts[1]), nil
}

// filterSubstring — "start:end" ou "start" (até o fim). Indices em runes.
func filterSubstring(v any, arg string) (any, error) {
	runes := []rune(asString(v))
	start, end := 0, len(runes)
	parts := strings.SplitN(arg, ":", 2)
	if parts[0] != "" {
		n, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("substring start inválido: %q", parts[0])
		}
		start = clamp(n, 0, len(runes))
	}
	if len(parts) == 2 && parts[1] != "" {
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("substring end inválido: %q", parts[1])
		}
		end = clamp(n, start, len(runes))
	}
	return string(runes[start:end]), nil
}

// filterDate — formata time.Time (ou string RFC3339) num layout Go.
// {{ now | date:"2006-01-02" }}
func filterDate(v any, arg string) (any, error) {
	if arg == "" {
		arg = "2006-01-02"
	}
	switch x := v.(type) {
	case time.Time:
		return x.Format(arg), nil
	case string:
		t, err := time.Parse(time.RFC3339, x)
		if err != nil {
			return nil, fmt.Errorf("date: input %q não é RFC3339", x)
		}
		return t.Format(arg), nil
	}
	return nil, fmt.Errorf("date: tipo %T não suportado", v)
}

// ──────────────── helpers ────────────────

func digitsOnly(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Mapa minimalista de acentos PT-BR. Suficiente para nomes; expansão Unicode
// total pediria golang.org/x/text — peso desnecessário para o caso de uso.
var accentTable = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i',
	'ó': 'o', 'ò': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u',
	'ç': 'c', 'ñ': 'n',
}

func isAccented(r rune) bool { _, ok := accentTable[r]; return ok }
func stripAccent(r rune) rune { return accentTable[r] }

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

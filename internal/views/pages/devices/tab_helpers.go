package devices

import (
	"strconv"

	"github.com/google/uuid"
)

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

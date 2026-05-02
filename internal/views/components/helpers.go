package components

import "strconv"

// itoa — atalho usado pelos componentes para converter int → string em
// atributos HTML (rows, colspan, etc).
func itoa(n int) string { return strconv.Itoa(n) }

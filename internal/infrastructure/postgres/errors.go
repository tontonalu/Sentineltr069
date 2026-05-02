package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation devolve true se o erro é violação de constraint UNIQUE
// e (opcionalmente) o nome do constraint bate com `name`.
//
// Use para mapear erros de driver para erros de domínio (ex: ErrEmailTaken).
func isUniqueViolation(err error, name string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code != "23505" {
		return false
	}
	if name == "" {
		return true
	}
	return pgErr.ConstraintName == name
}

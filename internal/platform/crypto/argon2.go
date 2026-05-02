// Package crypto — primitivas criptográficas usadas pela aplicação.
//
// Argon2id (este arquivo): hash de senhas. Parâmetros calibrados conforme
// recomendação OWASP 2024 (mínimo 19 MiB, 2 iterações, paralelismo 1) com
// margem extra (64 MiB, 3 iterações) — adequado para login não-frequente
// num servidor administrativo. Gere benchmark com `go test -bench` se quiser
// recalibrar para o hardware-alvo.
package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2Params controla custo de hashing.
// Os defaults estão em DefaultArgon2Params.
type Argon2Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2Params: ~64 MiB, 3 iter, paralelismo 1.
// Custo ~75ms num CPU server moderno — aceitável para login.
var DefaultArgon2Params = Argon2Params{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

// Encoded format (PHC): $argon2id$v=19$m=65536,t=3,p=1$<salt b64>$<hash b64>
const argon2idPrefix = "$argon2id$v=19$"

// HashPassword devolve o hash em formato PHC (string única; salt embutido).
func HashPassword(password string) (string, error) {
	return HashPasswordWith(password, DefaultArgon2Params)
}

func HashPasswordWith(password string, p Argon2Params) (string, error) {
	if password == "" {
		return "", errors.New("crypto: senha vazia")
	}
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("crypto: rand: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// VerifyPassword retorna true sse o password gera o mesmo hash que o
// encoded fornecido. Comparação em tempo constante (subtle.ConstantTimeCompare).
func VerifyPassword(password, encoded string) (bool, error) {
	if !strings.HasPrefix(encoded, argon2idPrefix) {
		return false, errors.New("crypto: hash não é argon2id v19")
	}
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", "salt", "hash"]
	if len(parts) != 6 {
		return false, errors.New("crypto: formato PHC inválido")
	}

	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return false, fmt.Errorf("crypto: params: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("crypto: salt: %w", err)
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("crypto: hash: %w", err)
	}

	candidate := argon2.IDKey([]byte(password), salt,
		p.Iterations, p.Memory, p.Parallelism, uint32(len(expected)))

	return subtle.ConstantTimeCompare(expected, candidate) == 1, nil
}

// NeedsRehash devolve true se o hash foi gerado com parâmetros mais fracos
// que os atuais — útil para fazer upgrade transparente no login.
func NeedsRehash(encoded string, current Argon2Params) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return true
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return true
	}
	return p.Memory < current.Memory ||
		p.Iterations < current.Iterations ||
		p.Parallelism < current.Parallelism
}

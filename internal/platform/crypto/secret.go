package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// SecretBox cifra/decifra segredos curtos (TOTP secrets, credenciais de
// integração, etc.) usando AES-256-GCM. A chave de 32 bytes é mantida fora
// do banco — em arquivo lido por App.AgeKeyFile ou em variável de ambiente.
//
// Por que GCM e não age aqui? GCM é stdlib, suficiente para campos curtos,
// e evita dep externa para uma necessidade pontual. age teria valor para
// arquivos inteiros / cifragem de backup.
type SecretBox struct {
	aead cipher.AEAD
}

// NewSecretBox aceita uma chave de 32 bytes. Use LoadKeyFromFile/LoadKeyFromHex
// para construir a chave — chamar este método com slice arbitrário é só para
// testes.
func NewSecretBox(key []byte) (*SecretBox, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: chave deve ter 32 bytes (got %d)", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretBox{aead: aead}, nil
}

// Encrypt devolve nonce||ciphertext em base64-std.
// Saída segura para colunas TEXT no Postgres.
func (s *SecretBox) Encrypt(plaintext []byte) (string, error) {
	if s == nil {
		return "", errors.New("crypto: SecretBox nil")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := s.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (s *SecretBox) EncryptString(plaintext string) (string, error) {
	return s.Encrypt([]byte(plaintext))
}

// Decrypt aceita o formato gerado por Encrypt.
func (s *SecretBox) Decrypt(encoded string) ([]byte, error) {
	if s == nil {
		return nil, errors.New("crypto: SecretBox nil")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("crypto: base64: %w", err)
	}
	ns := s.aead.NonceSize()
	if len(data) < ns+s.aead.Overhead() {
		return nil, errors.New("crypto: ciphertext muito curto")
	}
	nonce, ct := data[:ns], data[ns:]
	pt, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: AEAD.Open: %w", err)
	}
	return pt, nil
}

func (s *SecretBox) DecryptString(encoded string) (string, error) {
	pt, err := s.Decrypt(encoded)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// LoadKeyFromFile lê 64 caracteres hexadecimais (32 bytes) do arquivo.
// Espaços/quebras de linha em volta são ignorados. Em prod, este arquivo
// deve estar em volume não montado em backup, com perms 0400 e dono diferente
// do usuário da app (idealmente — em Docker secrets isso é automático).
func LoadKeyFromFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path vem de config validada
	if err != nil {
		return nil, fmt.Errorf("crypto: ler keyfile: %w", err)
	}
	hexStr := ""
	for _, b := range raw {
		if b != '\n' && b != '\r' && b != ' ' && b != '\t' {
			hexStr += string(b)
		}
	}
	return hex.DecodeString(hexStr)
}

// GenerateKey produz uma nova chave hex de 32 bytes (64 chars).
// Utilitário — escreva o resultado num keyfile com perms 0400.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

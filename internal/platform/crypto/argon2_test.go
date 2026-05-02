package crypto

import "testing"

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("ab0bra-cadabra!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "" {
		t.Fatal("hash vazio")
	}

	ok, err := VerifyPassword("ab0bra-cadabra!", hash)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("verify deveria ter retornado true")
	}

	ok, err = VerifyPassword("senha-errada", hash)
	if err != nil {
		t.Fatalf("verify (wrong): %v", err)
	}
	if ok {
		t.Fatal("verify deveria ter retornado false para senha errada")
	}
}

func TestVerifyMalformed(t *testing.T) {
	_, err := VerifyPassword("x", "not-a-phc-hash")
	if err == nil {
		t.Fatal("esperava erro para formato inválido")
	}
}

func TestNeedsRehash(t *testing.T) {
	weak, _ := HashPasswordWith("x", Argon2Params{
		Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
	if !NeedsRehash(weak, DefaultArgon2Params) {
		t.Fatal("hash fraco deveria precisar de rehash")
	}

	strong, _ := HashPassword("x")
	if NeedsRehash(strong, DefaultArgon2Params) {
		t.Fatal("hash com defaults atuais não deveria precisar de rehash")
	}
}

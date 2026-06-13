package crypto_test

import (
	"testing"

	"github.com/byteBuilderX/stratum/pkg/crypto"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := crypto.DeriveAESKey("test-pem-key")
	plaintext := "sk-abc123secretkey"

	ciphertext, err := crypto.Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if ciphertext == plaintext {
		t.Fatal("ciphertext should differ from plaintext")
	}

	got, err := crypto.Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if got != plaintext {
		t.Fatalf("want %q, got %q", plaintext, got)
	}
}

func TestEncryptNonDeterministic(t *testing.T) {
	key := crypto.DeriveAESKey("test-pem-key")
	c1, _ := crypto.Encrypt(key, "same")
	c2, _ := crypto.Encrypt(key, "same")
	if c1 == c2 {
		t.Fatal("two encryptions of same plaintext should produce different ciphertext (random nonce)")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := crypto.DeriveAESKey("key-one")
	key2 := crypto.DeriveAESKey("key-two")
	ct, _ := crypto.Encrypt(key1, "secret")
	if _, err := crypto.Decrypt(key2, ct); err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key := crypto.DeriveAESKey("test-pem-key")
	ct, _ := crypto.Encrypt(key, "secret")
	b := []byte(ct)
	b[len(b)-1] ^= 0xFF
	if _, err := crypto.Decrypt(key, string(b)); err == nil {
		t.Fatal("expected error on tampered ciphertext")
	}
}

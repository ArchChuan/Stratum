package handler

import (
	"testing"

	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
)

func TestEncryptDecryptSettingsRoundtrip(t *testing.T) {
	key := pkgcrypto.DeriveAESKey("test-jwt-pem")
	original := "sk-realkey123"
	enc, err := pkgcrypto.Encrypt(key, original)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	dec, err := pkgcrypto.Decrypt(key, enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if dec != original {
		t.Fatalf("want %q got %q", original, dec)
	}
}

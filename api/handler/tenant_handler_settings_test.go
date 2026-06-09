package handler

import (
	"testing"

	pkgcrypto "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/crypto"
)

func TestMaskAPIKey(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"abc", "a••••••••"}, // len<=6: show half (1 char) + 8 bullets
		{"sk-abc1234567", "sk-abc••••••••"},                           // len>6: show first 6 + 8 bullets
		{"sk-" + string(make([]byte, 30)), "sk-\x00\x00\x00••••••••"}, // long key
	}
	for _, tc := range cases {
		got := maskAPIKey(tc.input)
		if got != tc.want {
			t.Errorf("maskAPIKey(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

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

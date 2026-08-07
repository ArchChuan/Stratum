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

func TestResolveDataKey(t *testing.T) {
	const configured = "data-key-material"
	const fallback = "jwt-pem-fallback"

	cases := []struct {
		name       string
		configured string
		fallback   string
		wantErr    bool
		want       [32]byte // 为空表示与 DeriveAESKey 派生结果一致
	}{
		{
			name:       "configured takes precedence",
			configured: configured,
			fallback:   fallback,
			want:       crypto.DeriveAESKey(configured),
		},
		{
			name:     "fallback used when configured empty",
			fallback: fallback,
			want:     crypto.DeriveAESKey(fallback),
		},
		{
			name:    "both empty fails closed",
			wantErr: true,
			want:    [32]byte{}, // 禁止落到 SHA-256("") 公开常量
		},
		{
			name:       "empty fallback with configured set",
			configured: configured,
			want:       crypto.DeriveAESKey(configured),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := crypto.ResolveDataKey(tc.configured, tc.fallback)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error when both keys empty")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveDataKey failed: %v", err)
			}
			if got != tc.want {
				t.Fatal("ResolveDataKey result must match DeriveAESKey derivation")
			}
			// 派生结果必须能解密 DeriveAESKey 加密的密文（存量兼容）。
			ct, err := crypto.Encrypt(tc.want, "legacy-secret")
			if err != nil {
				t.Fatalf("Encrypt failed: %v", err)
			}
			plain, err := crypto.Decrypt(got, ct)
			if err != nil {
				t.Fatalf("Decrypt with resolved key failed: %v", err)
			}
			if plain != "legacy-secret" {
				t.Fatalf("want %q, got %q", "legacy-secret", plain)
			}
		})
	}
}

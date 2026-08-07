package crypto

import (
	"errors"
	"strings"
	"testing"
)

func testKey() [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

func TestEncryptSecret_roundTrip(t *testing.T) {
	key := testKey()
	for _, tc := range []struct {
		name      string
		plaintext string
	}{
		{name: "normal key", plaintext: "sk-abcdef123456"},
		{name: "unix path style secret", plaintext: "/vault/secrets/token\n"},
		{name: "unicode", plaintext: "密钥-中文-🔑"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored, err := EncryptSecret(key, tc.plaintext)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			if !strings.HasPrefix(stored, secretPrefix) {
				t.Fatalf("stored value %q missing version prefix", stored)
			}
			if strings.Contains(stored, tc.plaintext) {
				t.Fatalf("ciphertext leaks plaintext: %q", stored)
			}
			got, err := DecryptSecret(key, stored)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if got != tc.plaintext {
				t.Fatalf("round-trip mismatch: got %q, want %q", got, tc.plaintext)
			}
		})
	}
}

func TestEncryptSecret_isRandomized(t *testing.T) {
	key := testKey()
	a, err := EncryptSecret(key, "sk-same")
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncryptSecret(key, "sk-same")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("expected non-deterministic ciphertext (unique nonce), got %q twice", a)
	}
}

func TestEncryptSecret_emptyStaysEmpty(t *testing.T) {
	key := testKey()
	stored, err := EncryptSecret(key, "")
	if err != nil {
		t.Fatal(err)
	}
	if stored != "" {
		t.Fatalf("empty plaintext should stay empty, got %q", stored)
	}
	got, err := DecryptSecret(key, "")
	if err != nil {
		t.Fatalf("empty stored value should decrypt to empty: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestDecryptSecret_legacyPlaintextFailsClosed 验证存量明文（无前缀）必须报
// ErrLegacyPlaintext，禁止降级为明文使用。
func TestDecryptSecret_legacyPlaintextFailsClosed(t *testing.T) {
	key := testKey()
	for _, tc := range []struct {
		name   string
		stored string
	}{
		{name: "plain api key", stored: "sk-legacy-plaintext"},
		{name: "garbage", stored: "not-a-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecryptSecret(key, tc.stored)
			if !errors.Is(err, ErrLegacyPlaintext) {
				t.Fatalf("got %v, want ErrLegacyPlaintext", err)
			}
		})
	}
}

// TestDecryptSecret_badPrefixPayloadIsLegacy 验证带前缀但 payload 不是合法
// base64 的值（业务值恰好以 "enc:v1:" 开头）按无法识别的值处理，返回
// ErrLegacyPlaintext 提示重新保存，而不是误报为损坏密文。
func TestDecryptSecret_badPrefixPayloadIsLegacy(t *testing.T) {
	key := testKey()
	_, err := DecryptSecret(key, secretPrefix+"!!!not-base64!!!")
	if !errors.Is(err, ErrLegacyPlaintext) {
		t.Fatalf("got %v, want ErrLegacyPlaintext", err)
	}
}

// TestDecryptSecret_corruptedCiphertextFailsClosed 验证带前缀且 base64 合法
// 但解密失败的值（密文损坏或 key 不匹配）必须报错，不得返回任何内容。
func TestDecryptSecret_corruptedCiphertextFailsClosed(t *testing.T) {
	key := testKey()
	var otherKey [32]byte
	otherKey[0] = 0xFF // 与 key 不同的确定性密钥
	for _, tc := range []struct {
		name   string
		stored string
	}{
		{name: "valid base64 but truncated", stored: secretPrefix + "aGVsbG8="},
		{name: "wrong key", stored: mustEncrypt(otherKey, "sk-secret")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecryptSecret(key, tc.stored)
			if err == nil {
				t.Fatal("expected error for corrupted ciphertext")
			}
			if errors.Is(err, ErrLegacyPlaintext) {
				t.Fatalf("corrupted ciphertext should not be reported as legacy plaintext: %v", err)
			}
		})
	}
}

func mustEncrypt(key [32]byte, plaintext string) string {
	stored, err := EncryptSecret(key, plaintext)
	if err != nil {
		panic(err)
	}
	return stored
}

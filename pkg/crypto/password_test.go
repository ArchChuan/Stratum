package crypto_test

import (
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/crypto"
)

func TestHashAndCheckPassword(t *testing.T) {
	plain := "MySecure1Pass"
	hash, err := crypto.HashPassword(plain)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") && !strings.HasPrefix(hash, "$2y$") {
		t.Fatalf("expected bcrypt hash prefix, got %q", hash[:10])
	}
	if !crypto.CheckPassword(plain, hash) {
		t.Fatal("CheckPassword should match")
	}
	if crypto.CheckPassword("wrongpass1A", hash) {
		t.Fatal("CheckPassword should not match wrong password")
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name    string
		plain   string
		wantErr error
	}{
		{"valid minimal", "Abcd1234", nil},
		{"valid long", strings.Repeat("Ab1", 40), nil},
		{"too short", "Ab1", crypto.ErrPasswordTooShort},
		{"too long", strings.Repeat("A", 129) + "1bcdefgh", crypto.ErrPasswordTooLong},
		{"no uppercase", "abcd1234", crypto.ErrPasswordNoUpper},
		{"no lowercase", "ABCD1234", crypto.ErrPasswordNoLower},
		{"no digit", "Abcdefgh", crypto.ErrPasswordNoDigit},
		{"chinese chars don't satisfy upper/lower/digit", "密码ABCD1234密码", crypto.ErrPasswordNoLower},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := crypto.ValidatePassword(tc.plain)
			if err != tc.wantErr {
				t.Fatalf("ValidatePassword(%q) = %v, want %v", tc.plain, err, tc.wantErr)
			}
		})
	}
}

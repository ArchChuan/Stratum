// Package crypto provides cryptographic utilities (AES, password hashing, etc.).
package crypto

import (
	"errors"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	bcryptCost     = 12  // ~0.3s per hash, OWASP-recommended for server-side
	passwordMinLen = 8   // NIST SP 800-63B minimum
	passwordMaxLen = 128 // bcrypt limit is 72 bytes, prevent DoS with 128 char cap
)

var (
	ErrPasswordTooShort = errors.New("password: too short, minimum 8 characters")
	ErrPasswordTooLong  = errors.New("password: too long, maximum 128 characters")
	ErrPasswordNoUpper  = errors.New("password: must contain at least one uppercase letter")
	ErrPasswordNoLower  = errors.New("password: must contain at least one lowercase letter")
	ErrPasswordNoDigit  = errors.New("password: must contain at least one digit")
)

// HashPassword returns a bcrypt hash of plain. Returns an error if plain
// fails validation or bcrypt hashing fails.
func HashPassword(plain string) (string, error) {
	if err := ValidatePassword(plain); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword compares a plain password against a bcrypt hash.
// Returns true if they match, false otherwise.
func CheckPassword(plain, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ValidatePassword checks password meets minimum strength requirements:
// ≥8 chars ≤128, ≥1 uppercase, ≥1 lowercase, ≥1 digit (NIST SP 800-63B + OWASP).
func ValidatePassword(plain string) error {
	if utf8.RuneCountInString(plain) < passwordMinLen {
		return ErrPasswordTooShort
	}
	if len(plain) > passwordMaxLen {
		return ErrPasswordTooLong
	}
	var hasUpper, hasLower, hasDigit bool
	for _, r := range plain {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasUpper {
		return ErrPasswordNoUpper
	}
	if !hasLower {
		return ErrPasswordNoLower
	}
	if !hasDigit {
		return ErrPasswordNoDigit
	}
	return nil
}

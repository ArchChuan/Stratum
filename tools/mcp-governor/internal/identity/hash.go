package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

const SaltSize = 32

type Hasher struct {
	key []byte
}

func NewHasher(key []byte) (*Hasher, error) {
	if len(key) != SaltSize {
		return nil, fmt.Errorf("create hasher: salt length must be %d bytes", SaltSize)
	}

	return &Hasher{key: append([]byte(nil), key...)}, nil
}

func (h *Hasher) Hash(domain, value string) string {
	digest := hmac.New(sha256.New, h.key)
	_, _ = digest.Write([]byte(domain))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(value))

	return hex.EncodeToString(digest.Sum(nil))[:32]
}

func LoadSalt(path string) (*Hasher, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("load salt %q: inspect file: %w", path, err)
	}
	if err := validateSaltFile(info); err != nil {
		return nil, fmt.Errorf("load salt %q: %w", path, err)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("load salt %q: open file: %w", path, err)
	}
	defer file.Close()

	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("load salt %q: inspect opened file: %w", path, err)
	}
	if err := validateSaltFile(openedInfo); err != nil {
		return nil, fmt.Errorf("load salt %q: validate opened file: %w", path, err)
	}

	salt, err := io.ReadAll(io.LimitReader(file, SaltSize+1))
	if err != nil {
		return nil, fmt.Errorf("load salt %q: read file: %w", path, err)
	}
	if len(salt) != SaltSize {
		return nil, fmt.Errorf("load salt %q: content length must be %d bytes", path, SaltSize)
	}

	hasher, err := NewHasher(salt)
	if err != nil {
		return nil, fmt.Errorf("load salt %q: initialize hasher: %w", path, err)
	}
	return hasher, nil
}

func validateSaltFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("salt file must be regular")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("salt file permissions must be 0600")
	}
	if info.Size() != SaltSize {
		return fmt.Errorf("salt file size must be %d bytes", SaltSize)
	}
	return nil
}

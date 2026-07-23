package identity

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestHasherHashIsDeterministicAndDomainSeparated(t *testing.T) {
	t.Parallel()

	hasher, err := NewHasher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewHasher() error = %v", err)
	}

	first := hasher.Hash("observation", "sensitive-value")
	second := hasher.Hash("observation", "sensitive-value")
	otherDomain := hasher.Hash("tenant", "sensitive-value")

	if first != second {
		t.Fatalf("Hash() is not deterministic: %q != %q", first, second)
	}
	if first == otherDomain {
		t.Fatalf("Hash() did not separate domains: %q", first)
	}
	if len(first) != 32 {
		t.Fatalf("len(Hash()) = %d, want 32", len(first))
	}
	if strings.Contains(first, "sensitive-value") {
		t.Fatalf("Hash() output contains the input: %q", first)
	}
}

func TestNewHasherCopiesKey(t *testing.T) {
	t.Parallel()

	key := []byte("0123456789abcdef0123456789abcdef")
	hasher, err := NewHasher(key)
	if err != nil {
		t.Fatalf("NewHasher() error = %v", err)
	}
	want := hasher.Hash("domain", "value")

	for i := range key {
		key[i] = 'x'
	}

	if got := hasher.Hash("domain", "value"); got != want {
		t.Fatalf("Hash() changed after caller mutated key: got %q, want %q", got, want)
	}
}

func TestNewHasherRejectsInvalidKeyLengths(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, SaltSize - 1, SaltSize + 1, 64} {
		if _, err := NewHasher(make([]byte, size)); err == nil {
			t.Errorf("NewHasher() with %d bytes returned nil error", size)
		}
	}
}

func TestLoadSaltAcceptsRegular0600File(t *testing.T) {
	t.Parallel()

	salt := []byte("0123456789abcdef0123456789abcdef")
	path := writeSaltFile(t, salt, 0o600)

	hasher, err := LoadSalt(path)
	if err != nil {
		t.Fatalf("LoadSalt() error = %v", err)
	}
	want, err := NewHasher(salt)
	if err != nil {
		t.Fatalf("NewHasher() error = %v", err)
	}
	if got := hasher.Hash("domain", "value"); got != want.Hash("domain", "value") {
		t.Fatalf("loaded hasher Hash() = %q, want %q", got, want.Hash("domain", "value"))
	}
}

func TestLoadSaltRejectsUnsafeFiles(t *testing.T) {
	t.Parallel()

	validSalt := []byte("0123456789abcdef0123456789abcdef")

	tests := []struct {
		name string
		path func(t *testing.T) string
	}{
		{
			name: "permissions 0644",
			path: func(t *testing.T) string { return writeSaltFile(t, validSalt, 0o644) },
		},
		{
			name: "symlink",
			path: func(t *testing.T) string {
				target := writeSaltFile(t, validSalt, 0o600)
				link := filepath.Join(t.TempDir(), "salt-link")
				if err := os.Symlink(target, link); err != nil {
					t.Fatalf("Symlink() error = %v", err)
				}
				return link
			},
		},
		{
			name: "directory",
			path: func(t *testing.T) string { return t.TempDir() },
		},
		{
			name: "named pipe",
			path: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "salt-pipe")
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatalf("Mkfifo() error = %v", err)
				}
				return path
			},
		},
		{
			name: "short content",
			path: func(t *testing.T) string { return writeSaltFile(t, validSalt[:SaltSize-1], 0o600) },
		},
		{
			name: "long content",
			path: func(t *testing.T) string { return writeSaltFile(t, append(validSalt, 'x'), 0o600) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path(t)
			if _, err := LoadSalt(path); err == nil {
				t.Fatal("LoadSalt() returned nil error")
			}
		})
	}
}

func TestLoadSaltErrorsContainSafeContext(t *testing.T) {
	t.Parallel()

	salt := []byte("SECRET-SALT-MUST-NOT-LEAK-12345")
	path := writeSaltFile(t, salt, 0o644)

	_, err := LoadSalt(path)
	if err == nil {
		t.Fatal("LoadSalt() returned nil error")
	}
	message := err.Error()
	if !strings.Contains(message, "load salt") || !strings.Contains(message, path) {
		t.Fatalf("LoadSalt() error lacks operation/path context: %q", message)
	}
	if strings.Contains(message, string(salt)) {
		t.Fatalf("LoadSalt() error contains salt bytes: %q", message)
	}
}

func writeSaltFile(t *testing.T, content []byte, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "salt")
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	return path
}

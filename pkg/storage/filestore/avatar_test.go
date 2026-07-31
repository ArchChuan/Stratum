package filestore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAvatarStore_SaveAndServe(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAvatarStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Valid PNG (1x1 transparent pixel)
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x00, 0x01, 0x00, 0x01, 0x9B, 0x6B, 0x59,
		0xC0, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
		0x44, 0xAE, 0x42, 0x60, 0x82,
	}

	filename, err := store.SaveAvatar(bytes.NewReader(png), "user-1")
	if err != nil {
		t.Fatalf("SaveAvatar: %v", err)
	}
	if !strings.HasSuffix(filename, ".png") {
		t.Errorf("expected .png extension, got %q", filename)
	}
	if !strings.HasPrefix(filename, "user-1_") {
		t.Errorf("expected user-1_ prefix, got %q", filename)
	}

	// ServePath returns the full path to the file.
	sp := store.ServePath(filename)
	if sp == "" {
		t.Error("ServePath returned empty")
	}
	if _, err := os.Stat(sp); err != nil {
		t.Errorf("saved file not found at %s: %v", sp, err)
	}

	// Delete should remove the file.
	if err := store.DeleteAvatar(filename); err != nil {
		t.Fatalf("DeleteAvatar: %v", err)
	}
	if _, err := os.Stat(sp); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestAvatarStore_RejectInvalid(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAvatarStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Not an image — plain text.
	_, err = store.SaveAvatar(strings.NewReader("hello world"), "user-1")
	if !errors.Is(err, ErrAvatarInvalidExt) {
		t.Errorf("expected ErrAvatarInvalidExt, got %v", err)
	}
}

func TestAvatarStore_RejectTooLarge(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAvatarStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// > 2 MiB of garbage.
	big := make([]byte, MaxAvatarBytes+1)
	_, err = store.SaveAvatar(bytes.NewReader(big), "user-1")
	if !errors.Is(err, ErrAvatarTooLarge) {
		t.Errorf("expected ErrAvatarTooLarge, got %v", err)
	}
}

func TestAvatarStore_DeleteEmptyAndTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAvatarStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Empty filename is a no-op.
	if err := store.DeleteAvatar(""); err != nil {
		t.Errorf("DeleteAvatar empty: %v", err)
	}

	// Path traversal is rejected.
	if err := store.DeleteAvatar("../etc/passwd"); err == nil {
		t.Error("expected error for path traversal")
	}
	if err := store.DeleteAvatar("a/b"); err == nil {
		t.Error("expected error for forward slash")
	}
	if err := store.DeleteAvatar(string(filepath.Separator) + "bad"); err == nil {
		t.Error("expected error for leading separator")
	}
}

func TestURL(t *testing.T) {
	if u := URL(""); u != "" {
		t.Errorf("empty URL: %q", u)
	}
	if u := URL("user_1.png"); u != "/avatars/user_1.png" {
		t.Errorf("unexpected URL: %q", u)
	}
}

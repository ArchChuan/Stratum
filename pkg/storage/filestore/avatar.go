// Package filestore provides local-filesystem storage for user-uploaded assets
// (avatars). It is intentionally simple — no encryption, no auth — because assets
// are served directly via <img src> and must be publicly accessible.
package filestore

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// MaxAvatarBytes caps uploaded avatar size at 2 MiB.
	MaxAvatarBytes = 2 << 20
)

var (
	ErrAvatarTooLarge   = errors.New("avatar exceeds 2MB limit")
	ErrAvatarInvalidExt = errors.New("avatar must be jpeg, png, or webp")
)

var allowedMIMEs = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// AvatarStore saves/reads user avatar images on the local filesystem.
type AvatarStore struct {
	dir string
}

// NewAvatarStore creates an AvatarStore rooted at dir. The directory is created
// if it does not exist.
func NewAvatarStore(dir string) (*AvatarStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("filestore: create avatar dir %s: %w", dir, err)
	}
	return &AvatarStore{dir: dir}, nil
}

// SaveAvatar reads up to MaxAvatarBytes from reader, validates the content type,
// and writes the file as <userID>_<timestamp>.<ext>. Returns the filename (not
// full path) suitable for URL construction.
func (s *AvatarStore) SaveAvatar(reader io.Reader, userID string) (string, error) {
	lr := io.LimitReader(reader, MaxAvatarBytes+1)
	buf, err := io.ReadAll(lr)
	if err != nil {
		return "", fmt.Errorf("filestore: read avatar: %w", err)
	}
	if len(buf) > MaxAvatarBytes {
		return "", ErrAvatarTooLarge
	}
	if len(buf) == 0 {
		return "", errors.New("filestore: empty avatar upload")
	}

	contentType := http.DetectContentType(buf)
	ext, ok := allowedMIMEs[contentType]
	if !ok {
		return "", ErrAvatarInvalidExt
	}

	filename := fmt.Sprintf("%s_%d%s", userID, time.Now().UnixMilli(), ext)
	dst := filepath.Join(s.dir, filename)

	if err := os.WriteFile(dst, buf, 0o644); err != nil {
		return "", fmt.Errorf("filestore: write avatar: %w", err)
	}
	return filename, nil
}

// SaveAvatarMultipart is a convenience wrapper that reads from a multipart file
// header after validating size.
func (s *AvatarStore) SaveAvatarMultipart(fh *multipart.FileHeader, userID string) (string, error) {
	if fh.Size > MaxAvatarBytes {
		return "", ErrAvatarTooLarge
	}
	f, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("filestore: open multipart: %w", err)
	}
	defer f.Close()
	return s.SaveAvatar(f, userID)
}

// DeleteAvatar removes the named file from disk. No-op if the file doesn't exist.
func (s *AvatarStore) DeleteAvatar(filename string) error {
	if filename == "" {
		return nil
	}
	// Path traversal guard: only allow plain filenames, no directory separators.
	if strings.Contains(filename, string(filepath.Separator)) ||
		strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		return errors.New("filestore: invalid avatar filename")
	}
	p := filepath.Join(s.dir, filename)
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filestore: delete avatar: %w", err)
	}
	return nil
}

// ServePath returns the absolute disk path for a filename so Gin can serve it
// via c.File(). Returns empty string if filename is empty.
func (s *AvatarStore) ServePath(filename string) string {
	if filename == "" {
		return ""
	}
	return filepath.Join(s.dir, filename)
}

// URL returns the public URL path for an avatar filename.
func URL(filename string) string {
	if filename == "" {
		return ""
	}
	return "/avatars/" + filename
}

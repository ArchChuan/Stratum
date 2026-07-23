package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCatalogRejectsInvalidManifestAndSources(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/catalog\n")
	writeFile(t, filepath.Join(root, "docs", "valid.md"), "# Guide\n\nUseful content.\n")
	writeFile(t, filepath.Join(root, "docs", "empty.md"), "# Guide\n\nUseful content.\n\n## Empty\n")

	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name: "duplicate document ID",
			manifest: manifestWithDocuments(
				"  - id: duplicate\n    title: One\n    source: docs/valid.md\n    url: /docs/one\n" +
					"  - id: duplicate\n    title: Two\n    source: docs/valid.md\n    url: /docs/two\n",
			),
			want: "duplicate document id",
		},
		{
			name: "duplicate URL",
			manifest: manifestWithDocuments(
				"  - id: one\n    title: One\n    source: docs/valid.md\n    url: /docs/same\n" +
					"  - id: two\n    title: Two\n    source: docs/valid.md\n    url: /docs/same\n",
			),
			want: "duplicate document url",
		},
		{
			name: "empty Markdown section",
			manifest: manifestWithDocuments(
				"  - id: empty\n    title: Empty\n    source: docs/empty.md\n    url: /docs/empty\n",
			),
			want: "empty section",
		},
		{
			name: "source path escapes repository",
			manifest: manifestWithDocuments(
				"  - id: escape\n    title: Escape\n    source: ../outside.md\n    url: /docs/escape\n",
			),
			want: "source path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifestPath := filepath.Join(root, "catalog.yaml")
			writeFile(t, manifestPath, tt.manifest)
			_, err := buildCatalog(root, manifestPath)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("buildCatalog() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestBuildCatalogRejectsSourceSymlinkOutsideRepository(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/catalog\n")
	writeFile(t, outside, "# Outside\n\nUntrusted content.\n")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "docs", "linked.md")); err != nil {
		t.Fatalf("Symlink(): %v", err)
	}
	manifestPath := filepath.Join(root, "catalog.yaml")
	writeFile(t, manifestPath, manifestWithDocuments(
		"  - id: linked\n    title: Linked\n    source: docs/linked.md\n    url: /docs/linked\n",
	))

	_, err := buildCatalog(root, manifestPath)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "source path") {
		t.Fatalf("buildCatalog() error = %v, want source path rejection", err)
	}
}

func manifestWithDocuments(documents string) string {
	return "product_version: \"2026.07\"\ndocuments:\n" + documents
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

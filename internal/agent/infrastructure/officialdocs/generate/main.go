package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/byteBuilderX/stratum/pkg/textchunk"
	"gopkg.in/yaml.v3"
)

const (
	maxChunkRunes = 1200
	overlapRunes  = 120
)

var markdownHeading = regexp.MustCompile(`(?m)^(#{1,6})[ \t]+(.+?)[ \t]*$`)

type manifest struct {
	ProductVersion string             `yaml:"product_version"`
	Documents      []manifestDocument `yaml:"documents"`
}

type manifestDocument struct {
	ID     string `yaml:"id"`
	Title  string `yaml:"title"`
	Source string `yaml:"source"`
	URL    string `yaml:"url"`
}

type catalogEntry struct {
	DocumentID     string `json:"documentId"`
	Title          string `json:"title"`
	ProductVersion string `json:"productVersion"`
	Section        string `json:"section"`
	URL            string `json:"url"`
	Ordinal        int    `json:"ordinal"`
	Body           string `json:"body"`
}

type markdownSection struct {
	title     string
	container bool
}

func main() {
	manifestPath := flag.String("manifest", "", "path to the official docs manifest")
	outputPath := flag.String("out", "", "path to write normalized catalog JSON")
	flag.Parse()
	if *manifestPath == "" || *outputPath == "" {
		fatal(errors.New("both -manifest and -out are required"))
	}

	root, err := findRepositoryRoot()
	if err != nil {
		fatal(err)
	}
	absManifest, err := filepath.Abs(*manifestPath)
	if err != nil {
		fatal(fmt.Errorf("resolve manifest path: %w", err))
	}
	entries, err := buildCatalog(root, absManifest)
	if err != nil {
		fatal(err)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		fatal(fmt.Errorf("encode catalog: %w", err))
	}
	data = append(data, '\n')
	if err := os.WriteFile(*outputPath, data, 0o600); err != nil {
		fatal(fmt.Errorf("write catalog: %w", err))
	}
}

func buildCatalog(root, manifestPath string) ([]catalogEntry, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var cfg manifest
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if strings.TrimSpace(cfg.ProductVersion) == "" {
		return nil, errors.New("manifest product_version is empty")
	}
	if len(cfg.Documents) == 0 {
		return nil, errors.New("manifest documents are empty")
	}

	ids := make(map[string]struct{}, len(cfg.Documents))
	urls := make(map[string]struct{}, len(cfg.Documents))
	for _, document := range cfg.Documents {
		if err := validateDocument(document, ids, urls); err != nil {
			return nil, err
		}
	}

	var entries []catalogEntry
	for _, document := range cfg.Documents {
		sourcePath, err := resolveSourcePath(root, document.Source)
		if err != nil {
			return nil, fmt.Errorf("document %q source path: %w", document.ID, err)
		}
		markdown, err := os.ReadFile(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("read document %q: %w", document.ID, err)
		}
		sections, err := splitMarkdownSections(string(markdown))
		if err != nil {
			return nil, fmt.Errorf("document %q: %w", document.ID, err)
		}
		strategy := textchunk.NewStructureRecursiveStrategy()
		chunks := strategy.Chunk(context.Background(), string(markdown), maxChunkRunes, overlapRunes, nil)
		if len(chunks.Leaves) == 0 {
			return nil, fmt.Errorf("document %q generated no chunks", document.ID)
		}
		sectionOrdinals := make([]int, len(sections))
		for _, chunk := range chunks.Leaves {
			parentOrdinal, err := parentOrdinal(chunk.ParentID)
			if err != nil || parentOrdinal >= len(sections) {
				return nil, fmt.Errorf("document %q invalid chunk parent %q", document.ID, chunk.ParentID)
			}
			body := strings.TrimSpace(chunk.Content)
			if body == "" {
				continue
			}
			entries = append(entries, catalogEntry{
				DocumentID:     document.ID,
				Title:          document.Title,
				ProductVersion: cfg.ProductVersion,
				Section:        sections[parentOrdinal].title,
				URL:            document.URL,
				Ordinal:        sectionOrdinals[parentOrdinal],
				Body:           body,
			})
			sectionOrdinals[parentOrdinal]++
		}
		for sectionIndex, count := range sectionOrdinals {
			if count == 0 && !sections[sectionIndex].container {
				return nil, fmt.Errorf("document %q section %q generated no content", document.ID, sections[sectionIndex].title)
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].DocumentID != entries[j].DocumentID {
			return entries[i].DocumentID < entries[j].DocumentID
		}
		if entries[i].Section != entries[j].Section {
			return entries[i].Section < entries[j].Section
		}
		return entries[i].Ordinal < entries[j].Ordinal
	})
	return entries, nil
}

func validateDocument(document manifestDocument, ids, urls map[string]struct{}) error {
	document.ID = strings.TrimSpace(document.ID)
	document.Title = strings.TrimSpace(document.Title)
	document.Source = strings.TrimSpace(document.Source)
	document.URL = strings.TrimSpace(document.URL)
	if document.ID == "" || document.Title == "" || document.Source == "" || document.URL == "" {
		return errors.New("manifest document contains an empty required field")
	}
	if _, exists := ids[document.ID]; exists {
		return fmt.Errorf("duplicate document id %q", document.ID)
	}
	if _, exists := urls[document.URL]; exists {
		return fmt.Errorf("duplicate document url %q", document.URL)
	}
	ids[document.ID] = struct{}{}
	urls[document.URL] = struct{}{}
	return nil
}

func resolveSourcePath(root, source string) (string, error) {
	if filepath.IsAbs(source) {
		return "", errors.New("absolute paths are not allowed")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	candidate := filepath.Join(root, filepath.Clean(source))
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve source symlinks: %w", err)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes repository root")
	}
	return candidate, nil
}

func splitMarkdownSections(markdown string) ([]markdownSection, error) {
	matches := markdownHeading.FindAllStringSubmatchIndex(markdown, -1)
	if len(matches) == 0 {
		return nil, errors.New("document contains no Markdown headings")
	}
	if strings.TrimSpace(markdown[:matches[0][0]]) != "" {
		return nil, errors.New("document contains content before the first heading")
	}
	sections := make([]markdownSection, 0, len(matches))
	for i, match := range matches {
		bodyStart := match[1]
		bodyEnd := len(markdown)
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0]
		}
		title := strings.TrimSpace(markdown[match[4]:match[5]])
		body := strings.TrimSpace(markdown[bodyStart:bodyEnd])
		if title == "" {
			return nil, fmt.Errorf("empty section %q", title)
		}
		if body == "" {
			currentLevel := match[3] - match[2]
			if i+1 < len(matches) {
				nextLevel := matches[i+1][3] - matches[i+1][2]
				if nextLevel > currentLevel {
					sections = append(sections, markdownSection{title: title, container: true})
					continue
				}
			}
			return nil, fmt.Errorf("empty section %q", title)
		}
		sections = append(sections, markdownSection{title: title})
	}
	return sections, nil
}

func parentOrdinal(parentID string) (int, error) {
	if parentID == "" {
		return 0, nil
	}
	var ordinal int
	if _, err := fmt.Sscanf(parentID, "parent_%d", &ordinal); err != nil {
		return 0, err
	}
	return ordinal, nil
}

func findRepositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect repository root: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root with go.mod not found")
		}
		dir = parent
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

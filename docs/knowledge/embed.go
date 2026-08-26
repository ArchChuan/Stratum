// Package knowledgedocs embeds the built-in platform knowledge documents
// (docs/knowledge/*.md). This directory is the single source of truth for the
// built-in platform knowledge base: documents are published with the binary
// and synced into every tenant's built-in RAG workspace by
// application.BuiltinDocsSync (changed content replaces the old document,
// removed files are cleaned up, unchanged files are skipped).
//
// Layout convention: one document per file, category = one directory level
// (e.g. guides/agent.md, reference/mcp-integration.md). A document may carry
// minimal frontmatter (`---\ntitle: ...\n---`) for a display title; without it
// the first `# ` heading is used. Each file is ingested as a whole document,
// `##` sections become its chunks.
package knowledgedocs

import "embed"

// FS holds the built-in knowledge documents. Embedded from this directory so
// the docs shipped with a binary always match the binary's version.
//
//go:embed *
var FS embed.FS

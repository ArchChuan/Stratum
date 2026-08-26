package port

import "context"

// BuiltinDoc is one built-in platform knowledge document loaded from the
// embedded docs/knowledge tree. DocID and Hash are precomputed by the
// infrastructure source (builtindocs) so application code never depends on a
// concrete source package — it only compares hashes and IDs.
type BuiltinDoc struct {
	// DocID is the deterministic document ID (UUIDv5 of "builtin:"+Path under
	// the built-in namespace). It is the primary key used for create/update/
	// delete reconciliation against knowledge_docs.
	DocID string
	// Path is the embedded relative path ("guides/agent.md") — the document
	// identity. Unique across the tree.
	Path string
	// Title is the display title (frontmatter title or first `# ` heading).
	Title string
	// Category is the parent directory ("guides").
	Category string
	// Content is the normalized markdown body (frontmatter stripped, LF
	// newlines, leading/trailing whitespace trimmed). It is what gets ingested.
	Content string
	// Hash is sha256 hex of Content. A change to the file changes the hash and
	// triggers a replace on the next sync.
	Hash string
}

// BuiltinDocSource loads the built-in platform knowledge documents. The
// infrastructure implementation reads the embedded docs/knowledge tree.
type BuiltinDocSource interface {
	AllDocs(ctx context.Context) ([]BuiltinDoc, error)
}

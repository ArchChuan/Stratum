package port

// OfficialDocEntry is one section within an official documentation article.
type OfficialDocEntry struct {
	DocumentID     string
	Title          string
	ProductVersion string
	Section        string
	URL            string
	Ordinal        int
	Body           string
}

// OfficialDocsCatalog lists the embedded official documentation catalog.
// The catalog is platform reference data owned by the agent context; the
// knowledge context consumes it through this port so infrastructure
// packages never import sibling contexts.
type OfficialDocsCatalog interface {
	AllCatalogEntries() ([]OfficialDocEntry, error)
}

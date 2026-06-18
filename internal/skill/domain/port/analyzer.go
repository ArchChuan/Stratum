package port

// AnalysisResult holds the safety-check outcome for a code snippet.
type AnalysisResult struct {
	Safe    bool
	Reasons []string
}

// CodeAnalyzer inspects code for forbidden constructs before a skill is persisted.
type CodeAnalyzer interface {
	Check(lang, code string) AnalysisResult
}

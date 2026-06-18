package infrastructure

import (
	"strings"

	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
)

// staticAnalyzer implements port.CodeAnalyzer via pure string checks.
type staticAnalyzer struct{}

// NewStaticAnalyzer returns the default CodeAnalyzer.
func NewStaticAnalyzer() port.CodeAnalyzer { return &staticAnalyzer{} }

func (a *staticAnalyzer) Check(lang, code string) port.AnalysisResult {
	switch lang {
	case "python":
		return checkPython(code)
	case "javascript":
		return checkJS(code)
	default:
		return port.AnalysisResult{Safe: false, Reasons: []string{"unsupported language: " + lang}}
	}
}

var pyForbiddenImports = []string{
	"os", "sys", "subprocess", "shutil", "socket",
	"urllib", "http", "requests", "ctypes",
	"threading", "multiprocessing", "signal",
	"resource", "pathlib", "importlib", "builtins",
}

var pyForbiddenBuiltins = []string{
	"exec", "eval", "compile", "open", "__import__",
	"globals", "locals", "vars", "getattr", "setattr",
	"delattr", "dir",
}

func checkPython(code string) port.AnalysisResult {
	var reasons []string

	for _, mod := range pyForbiddenImports {
		if containsPyImport(code, mod) {
			reasons = append(reasons, "forbidden import: "+mod)
		}
	}

	for _, b := range pyForbiddenBuiltins {
		if containsToken(code, b) {
			reasons = append(reasons, "forbidden builtin: "+b)
		}
	}

	return port.AnalysisResult{Safe: len(reasons) == 0, Reasons: reasons}
}

// containsPyImport matches `import mod`, `import mod.x`, `from mod import ...`
func containsPyImport(code, mod string) bool {
	for _, line := range strings.Split(code, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import "+mod) ||
			strings.HasPrefix(trimmed, "from "+mod+" ") ||
			strings.HasPrefix(trimmed, "from "+mod+".") {
			return true
		}
	}
	return false
}

// jsForbiddenAccessors: globals that are forbidden when accessed via property (e.g. process.exit).
// Allows defining `function process(input)` as required by our calling convention.
var jsForbiddenAccessors = []string{
	"process", "global", "Buffer",
}

// jsForbiddenCalls: names that are forbidden when called or referenced as a value.
var jsForbiddenCalls = []string{
	"require", "XMLHttpRequest", "fetch", "WebSocket",
	"Worker", "importScripts",
}

// jsForbiddenTokens: identifiers that are always forbidden.
var jsForbiddenTokens = []string{
	"__dirname", "__filename",
}

var jsForbiddenPatterns = []string{
	"new Function(", "__proto__", "prototype.constructor",
}

func checkJS(code string) port.AnalysisResult {
	var reasons []string

	for _, g := range jsForbiddenAccessors {
		if strings.Contains(code, g+".") || strings.Contains(code, g+"[") {
			reasons = append(reasons, "forbidden global: "+g)
		}
	}

	for _, g := range jsForbiddenCalls {
		if containsToken(code, g) {
			reasons = append(reasons, "forbidden global: "+g)
		}
	}

	for _, g := range jsForbiddenTokens {
		if containsToken(code, g) {
			reasons = append(reasons, "forbidden global: "+g)
		}
	}

	for _, p := range jsForbiddenPatterns {
		if strings.Contains(code, p) {
			reasons = append(reasons, "forbidden pattern: "+p)
		}
	}

	return port.AnalysisResult{Safe: len(reasons) == 0, Reasons: reasons}
}

// containsToken checks that `name` appears as a standalone identifier.
func containsToken(code, name string) bool {
	idx := 0
	for {
		pos := strings.Index(code[idx:], name)
		if pos < 0 {
			return false
		}
		abs := idx + pos
		before := abs == 0 || !isIdentChar(rune(code[abs-1]))
		after := abs+len(name) >= len(code) || !isIdentChar(rune(code[abs+len(name)]))
		if before && after {
			return true
		}
		idx = abs + 1
	}
}

func isIdentChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_'
}

package main

import (
	"fmt"
	"go/format"
	"go/token"
	"go/types"
	"strings"
)

// resolved message/struct names used for cross-message references.
var structNames = map[string]string{} // fully-qualified proto name -> Go struct name

// messageType resolves a fully-qualified proto TypeName to the generated
// Go type. google.protobuf WKTs map to plain Go types (no runtime); user
// messages resolve via structNames — same-file references live in the same
// gen package, so the resolved name is just the struct name. Unknown
// TypeName (cross-file reference) fails closed: the mapping table has no
// entry, the generator must not guess.
func messageType(typeName string) (string, error) {
	switch typeName {
	case ".google.protobuf.Timestamp":
		return "time.Time", nil
	case ".google.protobuf.Struct":
		return "map[string]any", nil
	case ".google.protobuf.Value":
		return "any", nil
	}
	if goName, ok := structNames[typeName]; ok {
		return goName, nil
	}
	return "", fmt.Errorf("message %q not in this file (cross-file references unsupported; add to structNames via import)", typeName)
}

// resolveGoType validates a @gotype value against the whitelist. The base
// type path must be a stratum/internal package, encoding/json, or time
// (RawMessage / Duration are proto-inexpressible); builtin expressions with
// no dots (e.g. map[string][]any) are allowed. Pointer/slice/map prefixes
// are stripped before the check (*domain.AuthConfig, []domain.ProposalEvent).
func resolveGoType(gt string) (string, error) {
	// Dotted identifiers first: types.Eval with a nil package can never
	// resolve a qualified name (time.Time, github.com/.../domain.X), so
	// validation for those is the whitelist prefix match below — running
	// types.Eval first would reject every one of them.
	if strings.Contains(gt, ".") {
		// strip pointer/slice prefixes to reach the base type path;
		// "[]" is 2 bytes — a 1-byte strip would leave a stray "]" and fail
		// the whitelist match below ([]github.com/.../domain.X)
		base := gt
		if n := leadingTypePrefixLen(base); n > 0 {
			base = base[n:]
		}
		if strings.HasPrefix(base, "map[") {
			if idx := strings.Index(base, "]"); idx >= 0 {
				base = base[idx+1:] // map[K]V -> V
			}
		}
		for _, allow := range []string{
			"github.com/byteBuilderX/stratum/internal/",
			"encoding/json.",
			"time.",
		} {
			if strings.HasPrefix(base, allow) {
				return gt, nil
			}
		}
		return "", fmt.Errorf("@gotype %q outside whitelist (only stratum/internal, encoding/json, time, or builtin expressions)", gt)
	}
	if _, err := types.Eval(token.NewFileSet(), nil, 0, gt); err != nil {
		return "", fmt.Errorf("@gotype %q is not a valid Go type expression", gt)
	}
	return gt, nil // builtin expression (map/slice of builtins)
}

// splitGoTypePrefix peels pointer/slice/map-key prefixes off a Go type
// expression so the remainder is the base type: *github.com/.../domain.X ->
// "*" + "github.com/.../domain.X", map[string][]any -> "map[string]" +
// "[]any", []byte -> "[]" + "byte".
func splitGoTypePrefix(gt string) (prefix, rest string) {
	for strings.HasPrefix(gt, "*") {
		prefix += "*"
		gt = gt[1:]
	}
	for strings.HasPrefix(gt, "[]") {
		prefix += "[]"
		gt = gt[2:]
	}
	if strings.HasPrefix(gt, "map[") {
		if idx := strings.Index(gt, "]"); idx >= 0 {
			prefix += gt[:idx+1]
			gt = gt[idx+1:]
		}
	}
	return prefix, gt
}

// leadingTypePrefixLen returns the byte length of a leading "*" or "[]"
// type prefix, 0 when neither. resolveGoType uses it to skip the prefix
// before the whitelist match — "[]" must strip 2 bytes, not 1.
func leadingTypePrefixLen(s string) int {
	if strings.HasPrefix(s, "[]") {
		return 2
	}
	if strings.HasPrefix(s, "*") {
		return 1
	}
	return 0
}

// goTypeSegPath returns the folded package segment and the full import path
// of a Go type's base, plus whether the path is slash-qualified (foldable).
// github.com/.../agent/domain.OpProposalStatus -> ("domain",
// "github.com/.../agent/domain", true); time.Time -> ("time", "time", false);
// map[string][]any -> ("", "", false).
func goTypeSegPath(gt string) (seg, path string, foldable bool) {
	_, rest := splitGoTypePrefix(gt)
	idx := strings.LastIndex(rest, ".")
	if idx < 0 {
		return "", "", false
	}
	path = rest[:idx]
	if slash := strings.LastIndex(path, "/"); slash >= 0 {
		return path[slash+1:], path, true
	}
	return path, path, false
}

// foldGoType collapses a fully-qualified @gotype into the short form the
// generated file can reference: github.com/.../agent/domain.OpProposalStatus
// -> domain.OpProposalStatus. Pointer/slice/map prefixes are preserved;
// paths without a slash (time.Duration) and builtin expressions
// (map[string][]any) are returned unchanged. Go binds imports by package
// name, so the import (collected from the full path) plus the last path
// segment suffices — no explicit alias is needed.
func foldGoType(gt string) string {
	prefix, rest := splitGoTypePrefix(gt)
	if seg, _, ok := goTypeSegPath(rest); ok {
		return prefix + seg + rest[strings.LastIndex(rest, "."):]
	}
	return prefix + rest
}

// collectImports returns the set of package paths referenced by field types
// (github.com/.../domain.OpProposalStatus -> github.com/.../domain,
// time.Time -> time, map[string][]any -> none).
func collectImports(msgs []*message) map[string]bool {
	imports := map[string]bool{}
	for _, m := range msgs {
		for _, f := range m.Fields {
			if _, path, _ := goTypeSegPath(f.GoType); path != "" {
				imports[path] = true
			}
		}
	}
	return imports
}

// foldGuard fails closed on folded-name collisions: two packages sharing
// the last path segment (agent/domain and collab/domain) would both fold to
// domain.X and render an ambiguous reference. time/json paths have no slash
// and can never collide, but the check handles them uniformly.
func foldGuard(msgs []*message, protoPath string) error {
	segPath := map[string]string{}
	for _, m := range msgs {
		for _, f := range m.Fields {
			seg, path, ok := goTypeSegPath(f.GoType)
			if !ok {
				continue
			}
			if prev, dup := segPath[seg]; dup && prev != path {
				return fmt.Errorf("%s: types %s.X and %s.X collide on folded package segment %q",
					protoPath, prev, path, seg)
			}
			segPath[seg] = path
		}
	}
	return nil
}

// goFile renders one .go file with a struct per message. The buffer is
// passed through go/format so import grouping and alignment always satisfy
// gofmt; a format error means a generator bug (tested, not silent).
func goFile(msgs []*message, protoPath string) ([]byte, error) {
	if err := foldGuard(msgs, protoPath); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("// Code generated by protoc-gen-ginstruct. DO NOT EDIT.\n")
	fmt.Fprintf(&b, "// source: %s\n\n", protoPath)
	b.WriteString("package gen\n\n")
	if imports := collectImports(msgs); len(imports) > 0 {
		b.WriteString("import (\n")
		for pkgPath := range imports {
			fmt.Fprintf(&b, "\t%q\n", pkgPath)
		}
		b.WriteString(")\n\n")
	}
	for _, m := range msgs {
		fmt.Fprintf(&b, "type %s struct {\n", m.GoName)
		for _, f := range m.Fields {
			// Binding is a second tag beside the json tag (both optional
			// content-wise, but json always present), space-separated inside
			// the same backtick pair. Field types are folded here (render
			// layer): the model keeps the fully-qualified GoType.
			tags := f.JSONTag
			if f.Binding != "" {
				tags += ` binding:"` + f.Binding + `"`
			}
			fmt.Fprintf(&b, "\t%-24s %s `%s`\n", f.GoName, foldGoType(f.GoType), tags)
		}
		b.WriteString("}\n\n")
	}
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		panic(fmt.Sprintf("generator produced invalid Go: %v", err)) // generator bug, tests catch it
	}
	return src, nil
}

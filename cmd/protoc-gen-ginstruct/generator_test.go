package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// buildRequest compiles protoPath with the real protocompile compiler (no
// mocks) and wraps the descriptors in a CodeGeneratorRequest. Compiling the
// base name against the testdata import path keeps the file's canonical
// Path() as "sample.proto", so the generated source comment is stable for
// the golden. SourceInfoStandard is required: without it the // @binding /
// @gotype / @omitempty leading comments are silently dropped.
func buildRequest(t *testing.T, protoPath string) *pluginpb.CodeGeneratorRequest {
	t.Helper()
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{filepath.Dir(protoPath)},
		}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	files, err := compiler.Compile(context.Background(), filepath.Base(protoPath))
	if err != nil {
		t.Fatalf("compile %s: %v", protoPath, err)
	}
	descriptors := make([]*descriptorpb.FileDescriptorProto, 0, len(files))
	names := make([]string, 0, len(files))
	for _, f := range files {
		// v0.14.1: linker.File embeds protoreflect.FileDescriptor (no Proto());
		// the descriptor accessor lives on linker.Result.
		fd, ok := f.(linker.Result)
		if !ok {
			t.Fatalf("compile result %T does not implement linker.Result", f)
		}
		descriptors = append(descriptors, fd.FileDescriptorProto())
		names = append(names, f.Path())
	}
	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: names,
		ProtoFile:      descriptors,
	}
}

// TestGenerateSnapshot guards the approved output shape: any generator
// behavior change that moves the emitted text fails CI. Files are located by
// the same name the plugin emits (goFileName/tsFileName), never by content,
// so a Go/TS swap cannot be mistaken for the other side.
func TestGenerateSnapshot(t *testing.T) {
	resp := generate(buildRequest(t, "testdata/sample.proto"))
	if resp.Error != nil {
		t.Fatalf("generate: %s", *resp.Error)
	}
	contents := map[string]string{}
	for _, f := range resp.File {
		contents[f.GetName()] = f.GetContent()
	}
	for name, golden := range map[string]string{
		goFileName("sample.proto"): "testdata/sample.golden.go",
		tsFileName("sample.proto"): "testdata/sample.golden.ts",
	} {
		got, ok := contents[name]
		if !ok {
			t.Errorf("missing generated file %q", name)
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("missing golden %s: %v", golden, err)
		}
		if got != string(want) {
			t.Errorf("%s differs from golden:\n%s", name, diff(got, string(want)))
		}
	}
}

// TestFileNames pins the output path derivation: fixed gen/ dirs,
// basename-derived (one file per proto), .proto -> .go / .ts.
func TestFileNames(t *testing.T) {
	cases := map[string]struct{ goName, tsName string }{
		"sample.proto":               {"api/http/dto/gen/sample.go", "web/src/services/gen/sample.ts"},
		"collab/collaboration.proto": {"api/http/dto/gen/collaboration.go", "web/src/services/gen/collaboration.ts"},
		"proto/agent/agent.proto":    {"api/http/dto/gen/agent.go", "web/src/services/gen/agent.ts"},
	}
	for in, want := range cases {
		if got := goFileName(in); got != want.goName {
			t.Errorf("goFileName(%q) = %q, want %q", in, got, want.goName)
		}
		if got := tsFileName(in); got != want.tsName {
			t.Errorf("tsFileName(%q) = %q, want %q", in, got, want.tsName)
		}
	}
}

// TestMappingTable fixes every row of the §5 mapping table on the
// intermediate model: scalar/optional/repeated/map/message/WKT mapping plus
// @binding/@gotype/@omitempty effects. The @gotype status row's TSType
// follows the proto type (proto string -> "string"), reusing scalarType's
// TS column — the mapping table has one home in the generator.
func TestMappingTable(t *testing.T) {
	req := buildRequest(t, "testdata/sample.proto")
	resp := generate(req)
	if resp.Error != nil {
		t.Fatalf("generate: %s", *resp.Error)
	}
	msgs, err := collectMessages(req.ProtoFile[0])
	if err != nil {
		t.Fatalf("collectMessages: %v", err)
	}
	byName := map[string]*message{}
	for _, m := range msgs {
		byName[m.GoName] = m
	}
	type row struct {
		goType, tsType, jsonTag, binding string
	}
	cases := map[string]map[string]row{
		"SampleScalars": {
			"id":        {"string", "string", `json:"id"`, ""},
			"revision":  {"int64", "number", `json:"revision"`, ""},
			"page":      {"int32", "number", `json:"page"`, ""},
			"enabled":   {"bool", "boolean", `json:"enabled"`, ""},
			"score":     {"float64", "number", `json:"score"`, ""},
			"threshold": {"float32", "number", `json:"threshold"`, ""},
			"blob":      {"[]byte", "string", `json:"blob"`, ""},
		},
		"SampleMappings": {
			"name":         {"string", "string", `json:"name"`, "required,max=255"},
			"created_at":   {"time.Time", "string", `json:"created_at"`, ""},
			"status":       {"github.com/byteBuilderX/stratum/internal/agent/domain.OpProposalStatus", "string", `json:"status"`, ""},
			"config":       {"map[string]any", "Record<string, unknown>", `json:"config"`, ""},
			"sample_input": {"any", "unknown", `json:"sample_input"`, ""},
			"maybe":        {"*bool", "boolean | null", `json:"maybe"`, ""},
			"detail":       {"string", "string", `json:"detail,omitempty"`, ""},
			"tags":         {"[]string", "string[]", `json:"tags"`, ""},
			"headers":      {"map[string]string", "Record<string, string>", `json:"headers"`, ""},
			"steps":        {"[]SampleScalars", "SampleScalars[]", `json:"steps"`, ""},
			"overrides":    {"map[string]map[string]any", "Record<string, Record<string, unknown>>", `json:"overrides"`, ""},
		},
	}
	for msgName, fields := range cases {
		m := byName[msgName]
		if m == nil {
			t.Errorf("missing message %s in generated model", msgName)
			continue
		}
		for jsonName, want := range fields {
			var f *field
			for _, ff := range m.Fields {
				if ff.TSName == jsonName {
					f = ff
				}
			}
			if f == nil {
				t.Errorf("%s: missing field %s", msgName, jsonName)
				continue
			}
			if f.GoType != want.goType || f.TSType != want.tsType ||
				f.JSONTag != want.jsonTag || f.Binding != want.binding {
				t.Errorf("%s.%s = {GoType:%s TSType:%s JSONTag:%s Binding:%q}, want {GoType:%s TSType:%s JSONTag:%s Binding:%q}",
					msgName, jsonName, f.GoType, f.TSType, f.JSONTag, f.Binding,
					want.goType, want.tsType, want.jsonTag, want.binding)
			}
		}
	}
}

// TestBindingDirectives covers applyDirective: binding value forms, @omitempty,
// @gotype whitelist hit/reject, and directive-vs-plain-comment discrimination.
func TestBindingDirectives(t *testing.T) {
	fullGT := "github.com/byteBuilderX/stratum/internal/agent/domain.OpProposalStatus"
	cases := []struct {
		name string
		line string
		want field
		err  bool
	}{
		{"binding plain", "@binding: required", field{Binding: "required"}, false},
		{"binding with comma", "@binding: required,max=255", field{Binding: "required,max=255"}, false},
		{"binding trims space", "@binding:   required", field{Binding: "required"}, false},
		{"binding empty", "@binding:", field{}, false},
		{"omitempty", "@omitempty", field{OmitZero: true}, false},
		{"gotype whitelisted", "@gotype: " + fullGT, field{GoType: fullGT}, false},
		{"gotype rejected", "@gotype: os.File", field{}, true},
		{"unknown directive ignored", "@whatever: hi", field{}, false},
		{"plain comment ignored", "regular comment", field{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var fl field
			err := applyDirective(&fl, tc.line, "sample.proto", "name")
			if tc.err {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fl != tc.want {
				t.Errorf("field = %+v, want %+v", fl, tc.want)
			}
		})
	}
}

// TestBindingRenderedIntoTag checks that a binding tag lands in the same
// backtick pair as the json tag, next to it, and that a plain field emits no
// binding tag at all.
func TestBindingRenderedIntoTag(t *testing.T) {
	msgs := []*message{{GoName: "Widget", TSName: "Widget", Fields: []*field{
		{GoName: "Name", TSName: "name", GoType: "string", JSONTag: `json:"name"`},
		{GoName: "Label", TSName: "label", GoType: "string", JSONTag: `json:"label"`, Binding: "required,max=255"},
	}}}
	src, err := goFile(msgs, "widget.proto")
	if err != nil {
		t.Fatalf("goFile: %v", err)
	}
	for _, want := range []string{`json:"name"`, `json:"label" binding:"required,max=255"`} {
		if !strings.Contains(string(src), want) {
			t.Errorf("output missing %q:\n%s", want, src)
		}
	}
}

// TestGoFileFoldSegmentConflict is the fail-closed guard: two packages with
// the same last path segment (agent/domain, collab/domain) would both fold
// to domain.X — the file must refuse to render rather than emit an
// ambiguous reference.
func TestGoFileFoldSegmentConflict(t *testing.T) {
	msgs := []*message{{GoName: "A", TSName: "A", Fields: []*field{
		{GoName: "Status", TSName: "status", GoType: "github.com/byteBuilderX/stratum/internal/agent/domain.OpProposalStatus", JSONTag: `json:"status"`},
		{GoName: "Doc", TSName: "doc", GoType: "github.com/byteBuilderX/stratum/internal/collab/domain.Spec", JSONTag: `json:"doc"`},
	}}}
	if _, err := goFile(msgs, "conflict.proto"); err == nil {
		t.Error("goFile must fail closed when two packages share the folded segment")
	}
}

// TestFoldGoType fixes the render-layer folding: fully-qualified @gotype
// paths collapse to last-segment short names, while slash-free and builtin
// expressions stay untouched.
func TestFoldGoType(t *testing.T) {
	cases := map[string]string{
		"github.com/byteBuilderX/stratum/internal/agent/domain.OpProposalStatus": "domain.OpProposalStatus",
		"*github.com/byteBuilderX/stratum/internal/agent/domain.AuthConfig":      "*domain.AuthConfig",
		"[]github.com/byteBuilderX/stratum/internal/agent/domain.ProposalEvent":  "[]domain.ProposalEvent",
		"map[string]github.com/byteBuilderX/stratum/internal/agent/domain.Task":  "map[string]domain.Task",
		"time.Duration":        "time.Duration",
		"map[string][]any":     "map[string][]any",
		"map[string]time.Time": "map[string]time.Time",
		"[]string":             "[]string",
	}
	for in, want := range cases {
		if got := foldGoType(in); got != want {
			t.Errorf("foldGoType(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestScalarTypeEnumFailsClosed pins the design decision: enum fields have
// no mapping-table row and must fail closed, not guess a type.
func TestScalarTypeEnumFailsClosed(t *testing.T) {
	_, _, err := scalarType(&descriptorpb.FieldDescriptorProto{
		Type: descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
	})
	if err == nil {
		t.Error("scalarType(TYPE_ENUM) must fail closed; enum has no mapping")
	}
}

func TestGoFieldNameAcronyms(t *testing.T) {
	cases := map[string]string{
		"task_description":       "TaskDescription",
		"taskDescription":        "TaskDescription",
		"llm_model":              "LLMModel",
		"mcpToolIds":             "MCPToolIDs",
		"agent_user_messages_7d": "AgentUserMessages7d",
		"oauth2_client_id":       "OAuth2ClientID",
		"maxDailyCostUsd":        "MaxDailyCostUSD",
		"topK":                   "TopK",
	}
	for in, want := range cases {
		if got := goFieldName(in); got != want {
			t.Errorf("goFieldName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoTypeWhitelist(t *testing.T) {
	ok := []string{
		"github.com/byteBuilderX/stratum/internal/agent/domain.OpProposalStatus",
		"[]github.com/byteBuilderX/stratum/internal/agent/domain.ProposalEvent",
		"*github.com/byteBuilderX/stratum/internal/mcp/domain.AuthConfig",
		"map[string][]any",
		"[]string",
	}
	for _, in := range ok {
		if _, err := resolveGoType(in); err != nil {
			t.Errorf("resolveGoType(%q) unexpected error: %v", in, err)
		}
	}
	bad := []string{"os.File", "github.com/evil/corp.Secret"}
	for _, in := range bad {
		if _, err := resolveGoType(in); err == nil {
			t.Errorf("resolveGoType(%q) expected whitelist error", in)
		}
	}
}

// diff is a minimal two-string diff for test output (kept deliberately
// small — unified-diff split at the first differing line; the golden files
// are the real contract, this only orients the failure).
func diff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var bld strings.Builder
	for i := 0; i < len(al) && i < len(bl); i++ {
		if al[i] == bl[i] {
			continue
		}
		from := i
		if from > 3 {
			from -= 3
		}
		for j := from; j <= i; j++ {
			if j < len(al) {
				fmt.Fprintf(&bld, "-%s\n", al[j])
			}
			if j < len(bl) {
				fmt.Fprintf(&bld, "+%s\n", bl[j])
			}
		}
		return bld.String()
	}
	if len(al) != len(bl) {
		return fmt.Sprintf("line count differs: got %d, want %d", len(al), len(bl))
	}
	return ""
}

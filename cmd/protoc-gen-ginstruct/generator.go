package main

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// message is the intermediate model shared by the Go and TS emitters.
type message struct {
	GoName string
	TSName string
	Fields []*field
}

type field struct {
	GoName   string // PascalCase Go field name
	TSName   string // json key, verbatim from proto field name
	GoType   string // generated Go type
	TSType   string // generated TS type
	JSONTag  string // `json:"name,omitempty"` or `json:"name"`
	Binding  string // gin binding tag content, "" if none
	Optional bool   // proto3 optional
	OmitZero bool   // @omitempty comment present
}

// acronyms keeps generated Go field names aligned with the hand-written
// DTOs (LLMModel, MCPToolIDs, OAuth2ClientID, ...).
var acronyms = map[string]string{
	"api": "API", "url": "URL", "id": "ID", "ids": "IDs", "llm": "LLM",
	"mcp": "MCP", "oauth": "OAuth", "oauth2": "OAuth2", "http": "HTTP",
	"db": "DB", "sse": "SSE", "rag": "RAG", "json": "JSON", "ui": "UI",
}

// goFieldName converts a json key (snake_case or camelCase) to a PascalCase
// Go field name: task_description -> TaskDescription, taskDescription ->
// TaskDescription, llm_model -> LLMModel, agent_user_messages_7d ->
// AgentUserMessages7d, mcpToolIds -> MCPToolIDs.
func goFieldName(jsonName string) string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for _, r := range jsonName {
		if r == '_' {
			flush()
			continue
		}
		if r >= 'A' && r <= 'Z' && cur.Len() > 0 {
			flush()
		}
		cur.WriteRune(r)
	}
	flush()
	var b strings.Builder
	for _, w := range words {
		if acr, ok := acronyms[strings.ToLower(w)]; ok {
			b.WriteString(acr)
			continue
		}
		w = strings.ToLower(w)
		if len(w) > 0 {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}

// generate walks every file in FileToGenerate and emits one .go and one
// .ts file per proto file (dual output, shared walk/naming/type mapping).
// Errors are returned as the plugin response error — never silently dropped.
func generate(req *pluginpb.CodeGeneratorRequest) *pluginpb.CodeGeneratorResponse {
	resp := &pluginpb.CodeGeneratorResponse{SupportedFeatures: protoPtr(uint64(1))}
	toGenerate := map[string]bool{}
	for _, name := range req.GetFileToGenerate() {
		toGenerate[name] = true
	}
	for _, fd := range req.GetProtoFile() {
		if !toGenerate[fd.GetName()] {
			continue
		}
		msgs, err := collectMessages(fd)
		if err != nil {
			resp.Error = protoPtr(err.Error())
			return resp
		}
		// Go output
		goOut, err := goFile(msgs, fd.GetName())
		if err != nil {
			resp.Error = protoPtr(err.Error())
			return resp
		}
		resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
			Name:    protoPtr(goFileName(fd.GetName())),
			Content: protoPtr(string(goOut)),
		})
		// TS output (same walk, shared naming logic)
		tsOut := tsFile(msgs, fd.GetName())
		resp.File = append(resp.File, &pluginpb.CodeGeneratorResponse_File{
			Name:    protoPtr(tsFileName(fd.GetName())),
			Content: protoPtr(string(tsOut)),
		})
	}
	return resp
}

// collectMessages flattens top-level messages into the intermediate model,
// reading @binding/@gotype/@omitempty from source comments. structNames is
// pre-filled so cross-message references resolve to generated struct names.
// (Nested messages are not used by any migrated contract — YAGNI.)
func collectMessages(fd *descriptorpb.FileDescriptorProto) ([]*message, error) {
	for _, md := range fd.GetMessageType() {
		structNames["."+fd.GetPackage()+"."+md.GetName()] = goFieldName(md.GetName())
	}
	var msgs []*message
	for i, md := range fd.GetMessageType() {
		m, err := collectMessage(fd, md, int32(i))
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

func collectMessage(fd *descriptorpb.FileDescriptorProto, md *descriptorpb.DescriptorProto, msgIdx int32) (*message, error) {
	m := &message{GoName: goFieldName(md.GetName()), TSName: md.GetName()}
	seenGoNames := map[string]bool{}
	for i, f := range md.GetField() {
		fl, err := collectField(fd, f, msgIdx, int32(i))
		if err != nil {
			return nil, err
		}
		if seenGoNames[fl.GoName] {
			return nil, fmt.Errorf("%s: field %q and another field collide as Go name %q",
				fd.GetName(), f.GetName(), fl.GoName)
		}
		seenGoNames[fl.GoName] = true
		m.Fields = append(m.Fields, fl)
	}
	return m, nil
}

func collectField(fd *descriptorpb.FileDescriptorProto, f *descriptorpb.FieldDescriptorProto, msgIdx, fieldIdx int32) (*field, error) {
	jsonName := f.GetName()
	fl := &field{
		GoName:   goFieldName(jsonName),
		TSName:   jsonName,
		Optional: f.GetProto3Optional(),
	}
	// Comments: last preceding // @xxx line wins; the field's own leading
	// comment block is scanned in order.
	if err := parseFieldComments(fd, f, fl, msgIdx, fieldIdx); err != nil {
		return nil, err
	}
	// Default type mapping when no @gotype override.
	if fl.GoType == "" {
		goType, tsType, err := mapType(f, fd)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", fd.GetName(), jsonName, err)
		}
		fl.GoType, fl.TSType = goType, tsType
	} else {
		fl.TSType = tsScalarType(f) // TS side follows the proto type
	}
	// json tag: key verbatim; ,omitempty only via @omitempty. The comma goes
	// inside the quotes — encoding/json silently ignores one outside.
	tag := `json:"` + jsonName
	if fl.OmitZero {
		tag += `,omitempty`
	}
	tag += `"`
	fl.JSONTag = tag
	return fl, nil
}

// parseFieldComments scans a field's leading comment locations for
// @binding/@gotype/@omitempty directives and applies them to fl.
func parseFieldComments(fd *descriptorpb.FileDescriptorProto, f *descriptorpb.FieldDescriptorProto, fl *field, msgIdx, fieldIdx int32) error {
	for _, loc := range fd.GetSourceCodeInfo().GetLocation() {
		if !isFieldLocation(loc.GetPath(), msgIdx, fieldIdx) {
			continue
		}
		for _, line := range strings.Split(loc.GetLeadingComments(), "\n") {
			if err := applyDirective(fl, strings.TrimSpace(line), fd.GetName(), f.GetName()); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyDirective applies one // @xxx comment line to fl; @gotype errors are
// wrapped with the proto file and field name for plugin response context.
func applyDirective(fl *field, line, file, fieldName string) error {
	switch {
	case strings.HasPrefix(line, "@binding:"):
		fl.Binding = strings.TrimSpace(strings.TrimPrefix(line, "@binding:"))
	case strings.HasPrefix(line, "@gotype:"):
		gt := strings.TrimSpace(strings.TrimPrefix(line, "@gotype:"))
		goType, err := resolveGoType(gt)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", file, fieldName, err)
		}
		fl.GoType = goType
	case strings.HasPrefix(line, "@omitempty"):
		fl.OmitZero = true
	}
	return nil
}

func isFieldLocation(path []int32, msgIdx, fieldIdx int32) bool {
	// message.field location path: 4 (message_type) <msg idx> 2 (field) <field
	// idx>; both indices must match exactly, otherwise every field would
	// adopt every other field's leading comments.
	return len(path) == 4 && path[0] == 4 && path[1] == msgIdx && path[2] == 2 && path[3] == fieldIdx
}

// mapEntry locates the proto3 MapEntry message for a field's TypeName, or
// nil when the field is not a map. MapEntry messages are nested inside the
// message that declares the map field (e.g. SampleMappings.HeadersEntry
// lives in SampleMappings.nested_type), never top-level — resolve the
// parent message by fully-qualified name first, then match the entry by
// short name + options.map_entry.
func mapEntry(fd *descriptorpb.FileDescriptorProto, typeName string) *descriptorpb.DescriptorProto {
	parent, short := "", typeName
	if idx := strings.LastIndex(short, "."); idx >= 0 {
		parent, short = short[:idx], short[idx+1:]
	}
	for _, md := range fd.GetMessageType() {
		if "."+fd.GetPackage()+"."+md.GetName() != parent {
			continue
		}
		for _, nested := range md.GetNestedType() {
			if nested.GetName() == short && nested.GetOptions().GetMapEntry() {
				return nested
			}
		}
	}
	return nil
}

// mapEntryTypes reads the key/value field types of a MapEntry message.
func mapEntryTypes(entry *descriptorpb.DescriptorProto) (keyGo, valGo, valTS string, err error) {
	for _, ef := range entry.GetField() {
		switch {
		case ef.GetName() == "key":
			keyGo, _, err = scalarType(ef)
			if err != nil {
				return "", "", "", err
			}
		case ef.GetName() == "value":
			valGo, valTS, err = scalarType(ef)
			if err != nil {
				return "", "", "", err
			}
		}
	}
	return keyGo, valGo, valTS, nil
}

// mapType implements the §5 mapping table. proto3 map fields arrive as a
// repeated MapEntry message — locate the entry, read key/value field types,
// and emit map[K]V. Repeated adds a slice; proto3 optional adds a pointer;
// google.protobuf WKTs map to plain Go types (no timestamppb/structpb).
func mapType(f *descriptorpb.FieldDescriptorProto, fd *descriptorpb.FileDescriptorProto) (goType, tsType string, err error) {
	// map entry: repeated message whose options.map_entry == true
	if entry := mapEntry(fd, f.GetTypeName()); entry != nil {
		keyGo, valGo, valTS, err := mapEntryTypes(entry)
		if err != nil {
			return "", "", err
		}
		return "map[" + keyGo + "]" + valGo, "Record<string, " + valTS + ">", nil
	}
	switch {
	case f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED:
		base, ts, err := scalarType(f)
		if err != nil {
			return "", "", err
		}
		return "[]" + base, ts + "[]", nil
	case f.GetProto3Optional():
		base, ts, err := scalarType(f)
		if err != nil {
			return "", "", err
		}
		return "*" + base, ts + " | null", nil
	default:
		return scalarType(f)
	}
}

// scalarType maps one non-map field; message types resolve via messageType.
func scalarType(f *descriptorpb.FieldDescriptorProto) (goType, tsType string, err error) {
	if f.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
		goType, err = messageType(f.GetTypeName())
		if err != nil {
			return "", "", err
		}
		return goType, tsScalarType(f), nil
	}
	var g, ts string
	switch f.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		g, ts = "string", "string"
	case descriptorpb.FieldDescriptorProto_TYPE_INT64:
		g, ts = "int64", "number"
	case descriptorpb.FieldDescriptorProto_TYPE_INT32:
		g, ts = "int32", "number"
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		g, ts = "bool", "boolean"
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		g, ts = "float64", "number"
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		g, ts = "float32", "number"
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		g, ts = "[]byte", "string"
	default:
		return "", "", fmt.Errorf("unsupported proto type %v (mapping table missing)", f.GetType())
	}
	return g, ts, nil
}

// tsScalarType maps a message type to TS: WKTs to plain types, user
// messages to their interface name (json key verbatim, §7).
func tsScalarType(f *descriptorpb.FieldDescriptorProto) string {
	switch f.GetTypeName() {
	case ".google.protobuf.Timestamp":
		return "string"
	case ".google.protobuf.Struct":
		return "Record<string, unknown>"
	case ".google.protobuf.Value":
		return "unknown"
	default:
		name := strings.TrimPrefix(f.GetTypeName(), ".stratum.")
		if idx := strings.Index(name, "."); idx >= 0 {
			name = name[idx+1:]
		}
		return name
	}
}

func protoPtr[T any](v T) *T { return &v }

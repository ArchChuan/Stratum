# Proto 契约生成(前后端参数约束)实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 引入 .proto 契约作为前后端 HTTP JSON 参数契约唯一事实源,自研 protoc-gen-ginstruct 双输出生成 Go DTO struct 与 TS interface,一次性迁移全部 13 个手写 DTO 文件,150 个 golden 文件语义等价零变化。

**Architecture:** proto 文件 → buf generate → 自研插件 `protoc-gen-ginstruct`(标准插件协议 stdin/stdout)同时输出 `api/http/dto/gen/*.go`(纯 struct + 现有 json tag,encoding/json 语义,零 protobuf runtime)与 `web/src/services/gen/*.ts`(纯 interface,字段名 = json key 逐字节)。生成物不入 git,Makefile/CI 前置 `make proto-gen`。零破坏由 DTO parity 测试(迁移期)+ golden 语义等价 + 生成器文本快照(终态)三层守卫。

**Tech Stack:** buf(go install 固定版本)、google.golang.org/protobuf(插件协议,仅工具依赖)、bufbuild/protocompile(生成器单测)、encoding/json(生成物语义)、gin binding(校验经 @binding 注释保留)、TypeScript(前端类型)。

## Global Constraints

- 生成物 `api/http/dto/gen/`、`web/src/services/gen/` 进 `.gitignore`,git 只审计 `proto/`。
- 所有 `build/test/lint/fe-*` 入口目标前置依赖 `proto-gen`;CI 每个 Go 编译/前端构建 job 在编译前跑 `make proto-gen`。
- json key = proto 字段名逐字节(含存量 snake_case);Go 字段名 PascalCase 化(缩写词典);TS 属性名 = json key 原样。
- 序列化语义:encoding/json;禁止 protojson;禁止 protobuf runtime 类型(timestamppb/structpb 等)。
- binding 校验:proto 字段注释 `// @binding: <gin-tag>` 原样生成 binding tag,内容级漂移由 parity 测试守护。
- `@gotype` 白名单:全限定类型前缀必须 `github.com/byteBuilderX/stratum/internal/`,或无点号内建表达式(如 `map[string][]any`);非白名单生成器非零退出。
- 敏感字段:凭证值字段禁止进 proto(`Auth`/`Retry` 等走 @gotype 黑盒);响应侧凭证值字段名由 golden 机械断言禁止。
- multipart/form 契约(`UploadDocumentRequest`、`EvaluationCenterQuery`)不入 proto,以手写文件落在 gen 包(`gen/rag_manual.go`、`gen/query_manual.go`);手写 dto 包最终整体消亡。
- 转换器/业务方法(`To*Response`、`ServerConfig()`、`RunInput()`、`NewMCPServerConfigResponse` 等)迁到 gen 包手写文件,逐函数保留,禁止删除。
- Go 代码质量门禁:圈复杂度 ≤10、行 ≤120、嵌套 ≤4;生成器工具代码同样受门禁。
- 每批迁移独立 commit,CI 全绿再进下一批;golden 语义等价零变化为硬性验收。

---

### Task 1: 生成器核心——插件协议、descriptor 遍历、Go struct 输出

**Files:**

- Create: `cmd/protoc-gen-ginstruct/main.go`
- Create: `cmd/protoc-gen-ginstruct/generator.go`
- Create: `cmd/protoc-gen-ginstruct/gogen.go`

**Interfaces:**

- Produces: `main.go` 暴露 `func main()`(stdin 读 CodeGeneratorRequest → stdout 写 CodeGeneratorResponse);`generator.go` 暴露 `func generate(req *pluginpb.CodeGeneratorRequest) *pluginpb.CodeGeneratorResponse`、`func collectMessages(fd *descriptorpb.FileDescriptorProto) ([]*message, error)`、`func mapType(f *descriptorpb.FieldDescriptorProto, fd *descriptorpb.FileDescriptorProto) (goType, tsType string, err error)`、`func tsScalarType(f *descriptorpb.FieldDescriptorProto) string`;`gogen.go` 暴露 `func goFile(msgs []*message, protoPath string) []byte` 与 `func messageType(typeName string) (string, error)`、`func resolveGoType(gt string) (string, error)`——Task 3(TS 输出)、Task 5(parity)依赖。`time.Time` 经 `time` import 进生成代码,`map[string]any`/`any` 无点号不进 import。

- [ ] **Step 1: go.mod 添加工具依赖**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go get google.golang.org/protobuf@latest
go mod tidy
```

Expected: go.mod 出现 `google.golang.org/protobuf` require。生成器是唯一引入 protobuf runtime 的工具代码,生成物永远不依赖它。

- [ ] **Step 2: 写 `cmd/protoc-gen-ginstruct/main.go`**

```go
// Command protoc-gen-ginstruct generates plain Go DTO structs and TS
// interfaces from .proto contracts (standard protoc plugin protocol).
// Generated Go code carries the exact json field names declared in proto
// and encoding/json semantics — no protobuf runtime in generated output.
package main

import (
 "io"
 "os"

 "google.golang.org/protobuf/proto"
 "google.golang.org/protobuf/types/pluginpb"
)

func main() {
 data, err := io.ReadAll(os.Stdin)
 if err != nil {
  os.Exit(1)
 }
 var req pluginpb.CodeGeneratorRequest
 if err := proto.Unmarshal(data, &req); err != nil {
  os.Exit(1)
 }
 out, err := proto.Marshal(generate(&req))
 if err != nil {
  os.Exit(1)
 }
 if _, err := os.Stdout.Write(out); err != nil {
  os.Exit(1)
 }
}
```

- [ ] **Step 3: 写 `cmd/protoc-gen-ginstruct/generator.go`**

```go
package main

import (
 "fmt"
 "strings"

 "google.golang.org/protobuf/reflect/protoreflect"
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
  goOut := goFile(msgs, fd.GetName())
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
 for _, md := range fd.GetMessageType() {
  m, err := collectMessage(fd, md)
  if err != nil {
   return nil, err
  }
  msgs = append(msgs, m)
 }
 return msgs, nil
}

func collectMessage(fd *descriptorpb.FileDescriptorProto, md *descriptorpb.DescriptorProto) (*message, error) {
 m := &message{GoName: goFieldName(md.GetName()), TSName: md.GetName()}
 seenGoNames := map[string]bool{}
 for _, f := range md.GetField() {
  fl, err := collectField(fd, f)
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

func collectField(fd *descriptorpb.FileDescriptorProto, f *descriptorpb.FieldDescriptorProto) (*field, error) {
 jsonName := f.GetName()
 fl := &field{
  GoName:   goFieldName(jsonName),
  TSName:   jsonName,
  Optional: f.GetProto3Optional(),
 }
 // Comments: last preceding // @xxx line wins; the field's own leading
 // comment block is scanned in order.
 for _, loc := range fd.GetSourceCodeInfo().GetLocation() {
  if !isFieldLocation(loc.GetPath(), f) {
   continue
  }
  comment := loc.GetLeadingComments()
  for _, line := range strings.Split(comment, "\n") {
   line = strings.TrimSpace(line)
   switch {
   case strings.HasPrefix(line, "@binding:"):
    fl.Binding = strings.TrimSpace(strings.TrimPrefix(line, "@binding:"))
   case strings.HasPrefix(line, "@gotype:"):
    gt := strings.TrimSpace(strings.TrimPrefix(line, "@gotype:"))
    goType, err := resolveGoType(gt)
    if err != nil {
     return nil, fmt.Errorf("%s: %s: %w", fd.GetName(), jsonName, err)
    }
    fl.GoType = goType
   case strings.HasPrefix(line, "@omitempty"):
    fl.OmitZero = true
   }
  }
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
 // json tag: key verbatim; ,omitempty only via @omitempty.
 tag := `json:"` + jsonName + `"`
 if fl.OmitZero {
  tag += `,omitempty`
 }
 fl.JSONTag = tag
 return fl, nil
}

func isFieldLocation(path []int32, f *descriptorpb.FieldDescriptorProto) bool {
 // message.field location path: 4 (message_type) <idx> 2 (field) <idx>
 return len(path) == 4 && path[0] == 4 && path[2] == 2
}

// mapType implements the §5 mapping table. proto3 map fields arrive as a
// repeated MapEntry message — locate the entry, read key/value field types,
// and emit map[K]V. Repeated adds a slice; proto3 optional adds a pointer;
// google.protobuf WKTs map to plain Go types (no timestamppb/structpb).
func mapType(f *descriptorpb.FieldDescriptorProto, fd *descriptorpb.FileDescriptorProto) (goType, tsType string, err error) {
 // map entry: repeated message whose options.map_entry == true
 for _, md := range fd.GetMessageType() {
  short := f.GetTypeName()
  if idx := strings.LastIndex(short, "."); idx >= 0 {
   short = short[idx+1:]
  }
  if md.GetName() != short || !md.GetOptions().GetMapEntry() {
   continue
  }
  var keyGo, valGo, valTS string
  for _, ef := range md.GetField() {
   switch {
   case ef.GetName() == "key":
    keyGo, _, err = scalarType(ef)
    if err != nil {
     return "", "", err
    }
   case ef.GetName() == "value":
    valGo, valTS, err = scalarType(ef)
    if err != nil {
     return "", "", err
    }
   }
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
```

注:Message 与 Map 类型的解析因 TypeName 处理较长,放在 `gogen.go` Step 4 补完。

- [ ] **Step 4: 写 `cmd/protoc-gen-ginstruct/gogen.go`(message/map 解析 + Go 输出 + @gotype 白名单)**

```go
package main

import (
 "fmt"
 "go/format"
 "go/token"
 "go/types"
 "strings"

 "google.golang.org/protobuf/types/descriptorpb"
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
 expr, err := types.Eval(token.NewFileSet(), nil, 0, gt)
 if err != nil {
  return "", fmt.Errorf("@gotype %q is not a valid Go type expression", gt)
 }
 _ = expr
 if !strings.Contains(gt, ".") {
  return gt, nil // builtin expression (map/slice of builtins)
 }
 // strip pointer/slice/map prefixes to reach the base type path
 base := gt
 for strings.HasPrefix(base, "*") || strings.HasPrefix(base, "[]") {
  base = base[1:]
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

// goFile renders one .go file with a struct per message. The buffer is
// passed through go/format so import grouping and alignment always satisfy
// gofmt; a format error means a generator bug (tested, not silent).
func goFile(msgs []*message, protoPath string) []byte {
 var b strings.Builder
 b.WriteString("// Code generated by protoc-gen-ginstruct. DO NOT EDIT.\n")
 fmt.Fprintf(&b, "// source: %s\n\n", protoPath)
 b.WriteString("package gen\n\n")
 // collect imports: strip pointer/slice/map prefixes before extracting
 // the package path (*github.com/.../domain.X -> github.com/.../domain,
 // map[string][]any -> no dot, no import; time.Time -> time).
 imports := map[string]bool{}
 for _, m := range msgs {
  for _, f := range m.Fields {
   base := strings.TrimLeft(f.GoType, "*[]")
   if strings.HasPrefix(base, "map[") {
    if idx := strings.Index(base, "]"); idx >= 0 {
     base = base[idx+1:]
    }
   }
   if idx := strings.LastIndex(base, "."); idx >= 0 {
    imports[base[:idx]] = true
   }
  }
 }
 if len(imports) > 0 {
  b.WriteString("import (\n")
  for pkgPath := range imports {
   fmt.Fprintf(&b, "\t%q\n", pkgPath)
  }
  b.WriteString(")\n\n")
 }
 for _, m := range msgs {
  fmt.Fprintf(&b, "type %s struct {\n", m.GoName)
  for _, f := range m.Fields {
   fmt.Fprintf(&b, "\t%-24s %s `%s`\n", f.GoName, f.GoType, f.JSONTag)
  }
  b.WriteString("}\n\n")
 }
 src, err := format.Source([]byte(b.String()))
 if err != nil {
  panic(fmt.Sprintf("generator produced invalid Go: %v", err)) // generator bug, tests catch it
 }
 return src
}
```

注:生成的 struct 不需要全部 TypeName 解析复杂化——**所有用户 message 字段引用**都用同包 struct 名(gen 包内互引,`structNames` 预填),`@gotype` 才引入跨包类型。`time.Time` 引用会让 `time` 进 import;`map[string]any`/`any` 无点号不进 import。Task 2 的快照测试固定该行为,若 TypeName 与 structNames 有出入会红。

- [ ] **Step 5: 编译验证**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go build ./cmd/protoc-gen-ginstruct
```

Expected: exit 0。若 `types.Eval` 需要非 nil types.Config,用 `types.Config{Importer: importer.Default()}`(见 Task 2 测试红后修正)。

- [ ] **Step 6: 提交**

```bash
git add cmd/protoc-gen-ginstruct go.mod go.sum
git commit -m "feat(proto): protoc-gen-ginstruct 生成器核心(插件协议+Go struct 输出)"
```

---

### Task 2: 生成器单测——protocompile 快照 + 映射表 + @binding/@gotype 用例

**Files:**

- Create: `cmd/protoc-gen-ginstruct/generator_test.go`
- Create: `cmd/protoc-gen-ginstruct/testdata/sample.proto`
- Create: `cmd/protoc-gen-ginstruct/testdata/sample.golden.go`
- Create: `cmd/protoc-gen-ginstruct/testdata/sample.golden.ts`

**Interfaces:**

- Consumes: Task 1 的 `generate`、`goFieldName`(包内直接调用)。
- Produces: 文本快照 `sample.golden.go`/`.ts` 成为终态"批准形态"守卫——CI 每次跑本测试即校验生成器不漂移。

- [ ] **Step 1: go get protocompile 并写 `testdata/sample.proto`(覆盖映射表全部 11 行 + binding/gotype/omitempty + map 值 message)**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go get github.com/bufbuild/protocompile@latest
go mod tidy
```

Expected: go.mod 出现 `github.com/bufbuild/protocompile` require(仅生成器单测用,生成物与运行时不挂钩)。

```proto
syntax = "proto3";

package stratum.sample;

import "google/protobuf/timestamp.proto";
import "google/protobuf/struct.proto";

message SampleScalars {
  string id = 1;
  int64 revision = 2;
  int32 page = 3;
  bool enabled = 4;
  double score = 5;
  float threshold = 6;
  bytes blob = 7;
}

message SampleMappings {
  // @binding: required,max=255
  string name = 1;
  google.protobuf.Timestamp created_at = 2;
  // @gotype: github.com/byteBuilderX/stratum/internal/agent/domain.OpProposalStatus
  string status = 3;
  google.protobuf.Struct config = 4;
  google.protobuf.Value sample_input = 5;
  optional bool maybe = 6;
  // @omitempty
  string detail = 7;
  repeated string tags = 8;
  map<string, string> headers = 9;
  repeated SampleScalars steps = 10;
  map<string, google.protobuf.Struct> overrides = 11;
}
```

`overrides` 覆盖"map 值为 message(Struct)"分支:`map[string]map[string]any` / `Record<string, Record<string, unknown>>`。

- [ ] **Step 2: 写 `generator_test.go`(protocompile 从 .proto 文本构建,断言生成文本含关键行)**

```go
package main

import (
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/bufbuild/protocompile"
 "google.golang.org/protobuf/proto"
 "google.golang.org/protobuf/types/descriptorpb"
 "google.golang.org/protobuf/types/pluginpb"
)

func buildRequest(t *testing.T, protoPath string) *pluginpb.CodeGeneratorRequest {
 t.Helper()
 // Compile the base name against the testdata import path so the file's
 // canonical Path() is "sample.proto" (stable output naming in golden).
 compiler := protocompile.Compiler{
  Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
   ImportPaths: []string{filepath.Dir(protoPath)},
  }),
 }
 files, err := compiler.Compile(filepath.Base(protoPath))
 if err != nil {
  t.Fatalf("compile %s: %v", protoPath, err)
 }
 var descriptors []*descriptorpb.FileDescriptorProto
 for _, f := range files {
  descriptors = append(descriptors, f.Proto())
 }
 names := make([]string, 0, len(files))
 for _, f := range files {
  names = append(names, f.Path())
 }
 return &pluginpb.CodeGeneratorRequest{
  FileToGenerate: names,
  ProtoFile:      descriptors,
 }
}

func TestGenerateSnapshot(t *testing.T) {
 req := buildRequest(t, "testdata/sample.proto")
 resp := generate(req)
 if resp.Error != nil {
  t.Fatalf("generate: %s", *resp.Error)
 }
 got := map[string]string{}
 for _, f := range resp.File {
  got[f.GetName()] = f.GetContent()
 }
 for _, name := range []string{"api/http/dto/gen/sample.go", "web/src/services/gen/sample.ts"} {
  want, err := os.ReadFile("testdata/" + filepath.Base(name) + ".golden")
  if err != nil {
   t.Fatalf("missing golden for %s: %v", name, err)
  }
  if got[name] != string(want) {
   t.Errorf("%s differs from golden:\n%s", name, diff(got[name], string(want)))
  }
 }
}

func TestGoFieldNameAcronyms(t *testing.T) {
 cases := map[string]string{
  "task_description":     "TaskDescription",
  "taskDescription":      "TaskDescription",
  "llm_model":            "LLMModel",
  "mcpToolIds":           "MCPToolIDs",
  "agent_user_messages_7d": "AgentUserMessages7d",
  "oauth2_client_id":     "OAuth2ClientID",
  "topK":                 "TopK",
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
```

- [ ] **Step 3: 运行缩写词典测试**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go test ./cmd/protoc-gen-ginstruct -run TestGoFieldNameAcronyms -v
```

Expected: PASS。`goFieldName` 完整实现已在 Task 1 Step 3 给出(缩写词典覆盖 `mcpToolIds→MCPToolIDs`、`agent_user_messages_7d→AgentUserMessages7d`、`oauth2_client_id→OAuth2ClientID` 等存量 DTO 真实字段名);若个别用例红,扩展 Task 1 的 `acronyms` 词典后重跑——禁止改测试迁就实现,测试里全是存量 DTO 的真实字段名。

- [ ] **Step 4: 运行白名单测试**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go test ./cmd/protoc-gen-ginstruct -run TestGoTypeWhitelist -v
```

Expected: PASS。`resolveGoType` 已在 Task 1 Step 4 实现(无点号 builtin 表达式走 `types.Eval` 语法校验;带点号 strip 指针/切片/map 前缀后按 `internal/`、`encoding/json.`、`time.` 白名单前缀校验);`os.File` 与 `github.com/evil/corp.Secret` 必须被拒。message/map 分支(`messageType` TypeName 解析 + `mapType` map-entry 展开)也在 Task 1 内,由 Step 5 快照测试固定。

- [ ] **Step 5: 运行快照测试,生成 golden**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go test ./cmd/protoc-gen-ginstruct -run TestGenerateSnapshot -v 2>&1 | head -40
```

Expected: FAIL(无 golden 文件)。**手工审核生成文本**,确认与映射表语义一致(json key 原样、binding tag 原样、Timestamp→time.Time、Struct→map[string]any、Value→any、optional→指针、@omitempty→tag 后缀、repeated→[]T、map<string,string>→map[string]string、@gotype 白名单命中),然后复制为 golden:

```bash
# 从测试输出复制生成文本到 testdata/sample.golden.go 与 sample.golden.ts
# golden 是"批准形态",任何后续生成器行为变化都会红
```

- [ ] **Step 6: 全绿**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go test ./cmd/protoc-gen-ginstruct -v
```

Expected: 全部 PASS(golden 快照 + 缩写词典 + 白名单)。

- [ ] **Step 7: 提交**

```bash
git add cmd/protoc-gen-ginstruct/
git commit -m "feat(proto): 生成器单测(快照+缩写词典+@gotype 白名单)"
```

---

### Task 3: TS 输出——共享命名逻辑的 interface 生成

**Files:**

- Create: `cmd/protoc-gen-ginstruct/tsgen.go`
- Modify: `cmd/protoc-gen-ginstruct/generator.go`(无——`generate` 的 TS 调用点在 Task 1 已留)

**Interfaces:**

- Consumes: Task 1 的 `message`/`field` 模型——`field.TSType` 已由 Task 1 的 `mapType`/`scalarType`/`tsScalarType` 填好(TS 类型计算**只在 T1 一份**,T3 不重复实现);Task 2 的 golden 样例。
- Produces: `tsFile(msgs []*message, protoPath string) []byte`、`goFileName(protoPath string) string`、`tsFileName(protoPath string) string`——Task 4 起 buf 生成链与 `generate` 直接消费。

- [ ] **Step 1: 写 `tsgen.go`(纯渲染器)**

```go
package main

import (
 "fmt"
 "path/filepath"
 "strings"
)

// goFileName maps a proto path to the generated Go output path. Both
// outputs live in fixed gen/ dirs, basename-derived (one file per proto).
func goFileName(protoPath string) string {
 return "api/http/dto/gen/" + strings.TrimSuffix(filepath.Base(protoPath), ".proto") + ".go"
}

func tsFileName(protoPath string) string {
 return "web/src/services/gen/" + strings.TrimSuffix(filepath.Base(protoPath), ".proto") + ".ts"
}

// tsFile renders one .ts file: `export interface <Name> { ... }` per message.
// Field names are the proto json keys verbatim (same bytes as Go's json
// tags), so axios consumes the response JSON directly — no protojson, no
// runtime. TSType was already computed by Task 1's shared type mapping.
// @omitempty / proto3 optional fields become optional properties (`?`),
// matching the hand-written zod `.optional()` semantics across the frontend.
func tsFile(msgs []*message, protoPath string) []byte {
 var b strings.Builder
 b.WriteString("// Code generated by protoc-gen-ginstruct. DO NOT EDIT.\n")
 fmt.Fprintf(&b, "// source: %s\n\n", protoPath)
 for _, m := range msgs {
  fmt.Fprintf(&b, "export interface %s {\n", m.GoName)
  for _, f := range m.Fields {
   opt := ""
   if f.OmitZero || f.Optional {
    opt = "?"
   }
   fmt.Fprintf(&b, "  %s%s: %s;\n", f.TSName, opt, f.TSType)
  }
  b.WriteString("}\n\n")
 }
 return []byte(b.String())
}
```

- [ ] **Step 3: 运行快照测试,补 TS golden**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go test ./cmd/protoc-gen-ginstruct -run TestGenerateSnapshot -v
```

Expected: 断言 TS golden;复制生成 TS 为 `testdata/sample.golden.ts` 后全绿。TS 侧要点审核:`created_at: string`(json key 原样,非 camelCase 化)、`status: string`(@gotype 字段的 TS 侧由 proto string 决定)、`config: Record<string, unknown>`、`maybe?: boolean | null`。

- [ ] **Step 4: 提交**

```bash
git add cmd/protoc-gen-ginstruct/
git commit -m "feat(proto): TS interface 双输出(共享命名逻辑,json key 逐字节)"
```

---

### Task 4: buf 工具链——proto-gen 一键生成

**Files:**

- Create: `proto/collaboration/collaboration.proto`(仅骨架,Task 8 补全字段)
- Create: `buf.yaml`
- Create: `buf.gen.yaml`
- Modify: `Makefile`(`proto-gen` 目标 + 全部入口前置依赖)
- Modify: `.gitignore`
- Modify: `go.mod`(加 `github.com/bufbuild/buf` 工具依赖)

**Interfaces:**

- Consumes: Task 1-3 的插件二进制。
- Produces: `make proto-gen`——后续所有任务的前置命令;CI 改造(Task 6)依赖。

- [ ] **Step 1: 写 `buf.yaml` 与 `buf.gen.yaml`**

`buf.yaml`:

```yaml
version: v2
modules:
  - path: proto
lint:
  use:
    - STANDARD
  except:
    - FIELD_LOWER_SNAKE_CASE   # 存量契约混合 camelCase(json key 逐字节)
    - PACKAGE_DIRECTORY_MATCH  # proto 目录按 domain,package 含 domain
```

`buf.gen.yaml`:

```yaml
version: v2
inputs:
  - directory: proto
plugins:
  - local: bin/protoc-gen-ginstruct
    out: .
```

注:插件响应里已写绝对输出路径(`api/http/dto/gen/xxx.go`、`web/src/services/gen/xxx.ts`,见 Task 3 `goFileName`/`tsFileName`),`out: .` 直接落位;**不要加 `paths=source_relative`**——它会按 proto 相对路径改写响应文件名,与插件声明的输出路径冲突,生成物会落到错误目录。

- [ ] **Step 2: Makefile 加 `proto-gen` 目标,挂到全部编译入口**

```makefile
# --- proto contract generation -----------------------------------------

BUF_VERSION := v1.50.0
BUF := $(GOMODCACHE)/buf-$(BUF_VERSION)

.PHONY: proto-gen proto-install-buf
proto-install-buf:
 @command -v buf >/dev/null 2>&1 || go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
 @command -v buf >/dev/null 2>&1 || { echo "error: install buf: go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)"; exit 1; }

proto-gen: proto-install-buf bin/protoc-gen-ginstruct
 buf generate
 @echo "proto: generated api/http/dto/gen + web/src/services/gen"

bin/protoc-gen-ginstruct: $(shell find cmd/protoc-gen-ginstruct -name '*.go')
 mkdir -p bin
 go build -o bin/protoc-gen-ginstruct ./cmd/protoc-gen-ginstruct
```

修改已有入口(be-test、be-build、be-lint、fe-lint、fe-typecheck、fe-build、check、code-quality、contract-enforce)为前置依赖 `proto-gen`:

```makefile
be-test: proto-gen
be-build: proto-gen
be-lint: proto-gen
fe-lint: proto-gen
fe-typecheck: proto-gen
fe-build: proto-gen
check: proto-gen
code-quality: proto-gen
contract-enforce: proto-gen
```

- [ ] **Step 3: `.gitignore` 加生成物**

追加:

```
# proto 生成物(不入 git,由 make proto-gen 生成)
api/http/dto/gen/
web/src/services/gen/
bin/
```

- [ ] **Step 4: 写试点契约 `proto/collaboration/collaboration.proto`(完整字段,Task 8 直接用)**

```proto
syntax = "proto3";

package stratum.collaboration;

import "google/protobuf/timestamp.proto";
import "google/protobuf/struct.proto";

// CreateCollabRequest creates a plan. Strategy validation lives in the
// service layer so unknown values surface as ErrCollabInvalidInput.
// (comment matches the hand-written DTO so the contract stays self-documenting)
message CreateCollabRequest {
  // @binding: required
  string task_description = 1;
  // @gotype: github.com/byteBuilderX/stratum/internal/collab/domain.CollabStrategy
  // @binding: required
  string strategy = 2;
  // @binding: required
  repeated string participants = 3;
}

// CollabResponse is the plan surface shown to members.
message CollabResponse {
  string id = 1;
  string taskDescription = 2;
  string strategy = 3;
  string status = 4;
  string createdBy = 5;
  repeated string participants = 6;
  google.protobuf.Timestamp createdAt = 7;
  // @omitempty — hand-written DTO: *time.Time `json:"startedAt,omitempty"`
  optional google.protobuf.Timestamp startedAt = 8;
  // @omitempty — hand-written DTO: *time.Time `json:"completedAt,omitempty"`
  optional google.protobuf.Timestamp completedAt = 9;
}

// TaskStepResponse is the detail-view step surface: dependency structure,
// status, and the step's own input/output payloads.
message TaskStepResponse {
  string id = 1;
  string planId = 2;
  string agentId = 3;
  repeated string dependencies = 4;
  string status = 5;
  google.protobuf.Struct input = 6;
  // @omitempty — hand-written DTO: `json:"output,omitempty"`
  google.protobuf.Struct output = 7;
  // @omitempty — hand-written DTO: `json:"error,omitempty"`
  string error = 8;
  google.protobuf.Timestamp createdAt = 9;
}
```

字段与 json tag 逐字节对齐 `api/http/dto/collaboration.go`:`task_description`/`strategy`/`participants`(binding required)、`startedAt`/`completedAt` 是 `*time.Time` + omitempty(optional + @omitempty 双标记,TS 侧 `string | null` + `?`)、`output`/`error` 仅 omitempty(无指针)。

- [ ] **Step 5: 验证生成链(试点契约端到端)**

```bash
cd /home/yang/go-projects/stratum-proto-contract
make proto-gen
```

Expected: buf 编译 proto/collaboration 成功,`api/http/dto/gen/collaboration.go` 与 `web/src/services/gen/collaboration.ts` 生成——`make proto-gen` 幂等,重跑零 diff(下次生成可 `git diff --no-index` 对照或直接肉眼核对)。若 buf 未装,第一步自动 `go install`。**人工核对生成 struct 与手写 DTO 逐字段一致**(parity 测试 Task 5 建立后自动守卫)。

- [ ] **Step 6: 提交**

```bash
git add buf.yaml buf.gen.yaml Makefile .gitignore proto/collaboration/collaboration.proto
git commit -m "feat(proto): buf 工具链 + make proto-gen(全部编译入口前置依赖)"
```

---

### Task 5: DTO parity 测试框架——迁移期手写 vs 生成的逐字段守卫

**Files:**

- Create: `api/http/dto/gen/parity_test.go`

**Interfaces:**

- Consumes: Task 1 生成的 `api/http/dto/gen/*.go` 包(包名 `gen`);手写 `api/http/dto` 包。
- Produces: `TestParityHandwrittenVsGenerated` 与 `TestRemovedStructsGone`——后续每批(Task 9-19)迁移的必跑门禁。

**设计(与 §9 验收口径对齐):** parity 是**迁移期**守卫——手写 struct 还在时断言"生成 == 手写"逐字段全等(发现映射表错误);手写删除后该 struct 从 `parityPairs` 转入 `removedStructs`(反射无法断言"类型不存在",用 grep 守卫 dto 包)。终态守卫由 Task 2 快照测试承担(生成器不漂移)。

- [ ] **Step 1: 写 `parity_test.go`(对偶登记,无占位)**

```go
package gen_test

import (
 "os/exec"
 "reflect"
 "testing"

 "github.com/byteBuilderX/stratum/api/http/dto"
 "github.com/byteBuilderX/stratum/api/http/dto/gen"
)

// parityPairs 登记 (gen struct, 手写 struct) 类型对偶。
// 每批迁移工作流:写 proto → make proto-gen → 在此登记对偶 → parity 全绿 →
// 删除手写 struct → 整条对偶移除(手写类型不存在后编译失败)、名字转入
// removedStructs。
var parityPairs = []struct {
 name string
 gen  reflect.Type
 hw   reflect.Type
}{
 // 试点启用时取消注释(Task 8 Step 3):
 // {"CreateCollabRequest", reflect.TypeOf(gen.CreateCollabRequest{}), reflect.TypeOf(dto.CreateCollabRequest{})},
 // {"CollabResponse", reflect.TypeOf(gen.CollabResponse{}), reflect.TypeOf(dto.CollabResponse{})},
 // {"TaskStepResponse", reflect.TypeOf(gen.TaskStepResponse{}), reflect.TypeOf(dto.TaskStepResponse{})},
}

// removedStructs 登记"已从 dto 包删除"的 struct 名。
var removedStructs = map[string]bool{}

func TestParityHandwrittenVsGenerated(t *testing.T) {
 for _, pair := range parityPairs {
  compareStructs(t, pair.name, pair.gen, pair.hw)
 }
}

// compareStructs 逐字段断言:字段集合相同、json tag 逐字节相同、binding tag
// 逐字节相同、Go 类型按映射表等价(允许 int32↔int)。
func compareStructs(t *testing.T, name string, genT, hwT reflect.Type) {
 t.Helper()
 for i := 0; i < genT.NumField(); i++ {
  gf := genT.Field(i)
  hwf, ok := hwT.FieldByName(gf.Name)
  if !ok {
   t.Errorf("%s: gen field %s missing in handwritten", name, gf.Name)
   continue
  }
  if gf.Tag.Get("json") != hwf.Tag.Get("json") {
   t.Errorf("%s.%s: json tag %q != handwritten %q", name, gf.Name,
    gf.Tag.Get("json"), hwf.Tag.Get("json"))
  }
  if gf.Tag.Get("binding") != hwf.Tag.Get("binding") {
   t.Errorf("%s.%s: binding tag %q != handwritten %q", name, gf.Name,
    gf.Tag.Get("binding"), hwf.Tag.Get("binding"))
  }
  if !compatibleType(gf.Type, hwf.Type) {
   t.Errorf("%s.%s: type %v != handwritten %v", name, gf.Name, gf.Type, hwf.Type)
  }
 }
 if genT.NumField() != hwT.NumField() {
  t.Errorf("%s: gen has %d fields, handwritten has %d", name, genT.NumField(), hwT.NumField())
 }
}

// compatibleType 允许 §5 映射表定义的等价对:int32↔int(递归进 slice/map 元素)。
func compatibleType(a, b reflect.Type) bool {
 if a == b {
  return true
 }
 if a.Kind() == reflect.Int32 && b.Kind() == reflect.Int {
  return true
 }
 if b.Kind() == reflect.Int32 && a.Kind() == reflect.Int {
  return true
 }
 if a.Kind() == reflect.Slice && b.Kind() == reflect.Slice {
  return compatibleType(a.Elem(), b.Elem())
 }
 if a.Kind() == reflect.Map && b.Kind() == reflect.Map {
  return compatibleType(a.Key(), b.Key()) && compatibleType(a.Elem(), b.Elem())
 }
 return false
}

// TestRemovedStructsGone 防半迁移态:dto 包不得再出现已删除的 struct 名。
// 反射无法断言"类型不存在",用 grep 守卫仓库源码。
func TestRemovedStructsGone(t *testing.T) {
 if len(removedStructs) == 0 {
  t.Skip("no structs migrated yet")
 }
 names := make([]string, 0, len(removedStructs))
 for name := range removedStructs {
  names = append(names, name)
 }
 pattern := "type \\(" + joinPattern(names)
 out, err := exec.Command("grep", "-rn", "-E", pattern, "../").CombinedOutput()
 if err == nil {
  t.Errorf("dto package still contains migrated structs:\n%s", out)
 }
 _ = dto.UploadDocumentRequest{} // 锚定 dto 包 import 存在;Task 19 手写 dto 包消亡时与 import 一并移除
}

func joinPattern(names []string) string {
 out := ""
 for i, n := range names {
  if i > 0 {
   out += "\\|"
  }
  out += n
 }
 return out
}
```

注:`parityPairs` 里试点三条已预注释留好(Task 8 Step 3 取消注释启用);`removedStructs` 初始为空 → `TestRemovedStructsGone` Skip,先绿灯落地。锚定行 `_ = dto.UploadDocumentRequest{}` 只用于防 gofmt 删除 `dto` import——Task 19 删除手写 dto 包时,该行与 `dto` import 一并移除(见 Task 19 收尾步骤)。

- [ ] **Step 2: 运行确认当前 Skip**

```bash
cd /home/yang/go-projects/stratum-proto-contract
make proto-gen
go test ./api/http/dto/gen/ -v
```

Expected: `TestParityHandwrittenVsGenerated`(空循环通过)与 `TestRemovedStructsGone`(SKIP)。若 grep 命令或 import 报错,修复后重跑。

- [ ] **Step 3: 提交**

```bash
git add api/http/dto/gen/parity_test.go
git commit -m "feat(proto): DTO parity 测试框架(迁移期手写 vs 生成逐字段守卫)"
```

---

### Task 6: CI 改造——7 个编译型 job 前置 make proto-gen

**Files:**

- Modify: `.github/workflows/ci.yml`

**Interfaces:**

- Consumes: Task 4 的 `make proto-gen`。
- Produces: 后续每批迁移(Task 9-19)的自动验证底座。

- [ ] **Step 1: 读 ci.yml 现状确认 job 清单**

```bash
cd /home/yang/go-projects/stratum-proto-contract
grep -n "^\s\+\(make\|go test\|go vet\|golangci\)\|^jobs:\|^  [a-z-]*:" .github/workflows/ci.yml
```

Expected: 列出全部 job 与编译命令。凡出现 `go test`/`go vet`/`go build`/`make <入口>`/golangci-lint 的 job 都要前置生成。

- [ ] **Step 2: 每个编译型 job 的 steps 开头插入生成步骤**

在 `static-checks`、`code-quality`、`test`、`contract`、`frontend`、`workflow-e2e`、`tool-permission` 各 job 的 steps 首部插入:

```yaml
      - name: Generate proto contracts
        run: make proto-gen
```

- [ ] **Step 3: 验证 buf 版本源单一**

确认 Makefile `proto-install-buf` 的 `go install github.com/bufbuild/buf/cmd/buf@v1.50.0` 是唯一 buf 安装路径(CI 不再需要 setup-buf action)。检查 ci.yml 中无 `setup-buf`。

```bash
grep -n "buf" .github/workflows/ci.yml Makefile
```

Expected: 只有 Makefile 的 go install 一处。

- [ ] **Step 4: 本地模拟 CI 行为验证**

```bash
cd /home/yang/go-projects/stratum-proto-contract
rm -rf api/http/dto/gen web/src/services/gen   # 模拟干净 checkout
make check
```

Expected: `make check` 自动触发 proto-gen 生成后再跑 check 全链路(fe-typecheck/fe-lint/contract-enforce/code-quality)。

- [ ] **Step 5: 提交**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: 编译型 job 前置 make proto-gen(生成物不入 git 后 7 个 job 改造)"
```

---

### Task 7: worktree 脚本自动生成——干净 checkout 首构建不失败

**Files:**

- Modify: `scripts/new-worktree.sh`

**Interfaces:**

- Consumes: Task 4 的 `make proto-gen`。
- Produces: 所有后续任务使用 `scripts/new-worktree.sh` 建 worktree 时自动生成契约。

- [ ] **Step 1: 在 `git worktree add` 后追加生成步骤**

```bash
git fetch --no-tags origin main:refs/remotes/origin/main
git worktree add "$path" -b "$branch" origin/main

# proto 生成物不入 git:新 worktree 是干净 checkout,首构建前先生成契约。
# 生成失败直接退出——后续任何构建都会失败,早暴露优于晚失败。
(cd "$path" && make proto-gen)
```

- [ ] **Step 2: 验证**

```bash
cd /home/yang/go-projects/stratum-proto-contract
bash scripts/new-worktree.sh ../stratum-verify-worktree-test feat/verify-worktree-test
```

Expected: worktree 创建后自动执行 `make proto-gen`,`api/http/dto/gen/` 与 `web/src/services/gen/` 在该 worktree 内生成。

```bash
git worktree remove ../stratum-verify-worktree-test
git branch -D feat/verify-worktree-test
```

- [ ] **Step 3: 提交**

```bash
git add scripts/new-worktree.sh
git commit -m "chore(worktree): new-worktree.sh 创建后自动 make proto-gen"
```

---

### Task 8: 试点 domain——collaboration 全量迁移

**Files:**

- Modify: `proto/collaboration/collaboration.proto`(Task 4 Step 4 已含完整契约,此处只核对/生成)
- Create: `api/http/dto/gen/convert.go`(转换器迁移)
- Modify: `api/http/dto/gen/parity_test.go`(取消 3 条预留对偶注释)
- Delete: `api/http/dto/collaboration.go` 的 3 个 struct(转换器移走,文件整体删除)
- Modify: `web/src/modules/collab/api/collaboration.api.ts`
- Modify: 引用 `dto.CreateCollabRequest`/`dto.CollabResponse`/`dto.TaskStepResponse` 的 handler/service(编译器引导)

**Interfaces:**

- Consumes: Task 1 生成器(collaboration message → `api/http/dto/gen/collaboration.go`);Task 5 parity 框架。
- Produces: 迁移完成后的引用范式(handler 引用 `gen.` 包、转换器归 `gen/convert.go`、前端 import `services/gen`)——Task 9-19 的样板。

- [ ] **Step 1: 核对契约并生成**

`proto/collaboration/collaboration.proto` 的完整契约已在 Task 4 Step 4 定义(试点三条 message + 全部字段/注释)——**此处不重复全文,直接使用**。核对要点(与手写 DTO 逐字段对齐):

- `strategy` 字段同时带 `@gotype`(CollabStrategy 黑盒)与 `@binding: required`——两条注释都要,生成物才有 binding tag;
- `startedAt`/`completedAt` 是 `optional` + `@omitempty` 双标记(指针 + omitempty tag);
- `output`/`error` 仅 `@omitempty`(无指针)。

```bash
cd /home/yang/go-projects/stratum-proto-contract
make proto-gen
```

Expected: `api/http/dto/gen/collaboration.go`、`web/src/services/gen/collaboration.ts` 生成,与手写 DTO 逐字段一致(parity 测试下一步守护)。

- [ ] **Step 2: 生成 + 人工核对生成物**

```bash
cd /home/yang/go-projects/stratum-proto-contract
make proto-gen
cat api/http/dto/gen/collaboration.go
```

Expected: 生成 struct 与手写逐字段一致。核对要点:`TaskDescription string \`json:"task_description" binding:"required"\``、`Strategy collabdomain.CollabStrategy \`json:"strategy" binding:"required"\``(import 白名单)、`StartedAt *time.Time \`json:"startedAt,omitempty"\``、`Input map[string]any \`json:"input"\``。

- [ ] **Step 3: 启用 parity 对照(手写仍在)**

编辑 `api/http/dto/gen/parity_test.go`,取消 3 条对偶的注释(Task 5 Step 1 已留好):

```go
var parityPairs = []struct {
 name string
 gen  reflect.Type
 hw   reflect.Type
}{
 {"CreateCollabRequest", reflect.TypeOf(gen.CreateCollabRequest{}), reflect.TypeOf(dto.CreateCollabRequest{})},
 {"CollabResponse", reflect.TypeOf(gen.CollabResponse{}), reflect.TypeOf(dto.CollabResponse{})},
 {"TaskStepResponse", reflect.TypeOf(gen.TaskStepResponse{}), reflect.TypeOf(dto.TaskStepResponse{})},
}
```

文件顶部 import 补充 `"reflect"`。`parity_helper_test.go` 不需要创建(对偶直接引用类型)。

- [ ] **Step 4: 跑 parity,修正生成器直到全绿**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go test ./api/http/dto/gen/ -v -run TestParity
```

Expected: 首跑红(映射差异,如 binding 缺失、类型不符)。逐条按报错修正生成器(Task 1/2 代码)直到全绿。**这是映射表正确性的最终校准点**——任何手写 DTO 有而生成物没有的 tag/类型,都在这里暴露。

- [ ] **Step 5: 写 `api/http/dto/gen/convert.go`(转换器迁移,逐函数原样保留)**

```go
package gen

import (
 collabdomain "github.com/byteBuilderX/stratum/internal/collab/domain"
)

// ToCollabResponse 与手写 dto.ToCollabResponse 逐行一致(迁移保留)。
func ToCollabResponse(c collabdomain.Collaboration) CollabResponse {
 return CollabResponse{
  ID:              c.ID,
  TaskDescription: c.TaskDescription,
  Strategy:        string(c.Strategy),
  Status:          string(c.Status),
  CreatedBy:       c.CreatedBy,
  Participants:    c.Participants,
  CreatedAt:       c.CreatedAt,
  StartedAt:       c.StartedAt,
  CompletedAt:     c.CompletedAt,
 }
}

// ToTaskStepResponse 与手写 dto.ToTaskStepResponse 逐行一致(迁移保留)。
func ToTaskStepResponse(s collabdomain.TaskStep) TaskStepResponse {
 return TaskStepResponse{
  ID:           s.ID,
  PlanID:       s.PlanID,
  AgentID:      s.AgentID,
  Dependencies: s.Dependencies,
  Status:       string(s.Status),
  Input:        s.Input,
  Output:       s.Output,
  Error:        s.Error,
  CreatedAt:    s.CreatedAt,
 }
}
```

- [ ] **Step 6: 删除手写 struct,编译器引导改引用**

```bash
cd /home/yang/go-projects/stratum-proto-contract
rm api/http/dto/collaboration.go
go build ./... 2>&1 | head -30
```

Expected: 编译错误全部指向 `dto.CreateCollabRequest`/`dto.CollabResponse`/`dto.TaskStepResponse`/`dto.ToCollabResponse`/`dto.ToTaskStepResponse` 引用点。逐个改为 `gen.` 前缀(handler、service、测试、contract stub 等)。

同步更新 parity_test.go:3 条对偶从 `parityPairs` 移除(手写类型已不存在,保留会编译失败),名字转入 `removedStructs`:

```go
var removedStructs = map[string]bool{
 "CreateCollabRequest": true,
 "CollabResponse":      true,
 "TaskStepResponse":    true,
}
```

- [ ] **Step 7: 前端替换 CreateCollabPayload**

`web/src/modules/collab/api/collaboration.api.ts`:

```diff
-export interface CreateCollabPayload {
-  task_description: string;
-  strategy: string;
-  participants: string[];
-}
+import { CreateCollabRequest as CreateCollabPayload } from '@/services/gen/collaboration';
```

注:TS 生成 interface 名为 message 名 `CreateCollabRequest`,前端别名保持 `CreateCollabPayload` 引用名不变,调用处零改动。

- [ ] **Step 8: 试点全量验收**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go vet ./...
go test -short ./...
make fe-typecheck
make contract-enforce
```

Expected: 四项全绿。`contract-enforce` 验证 150 golden 语义等价零 diff——collaboration 相关的 golden(创建/查询/步骤相关契约)是迁移是否破坏行为的直接证据。

- [ ] **Step 9: 提交**

```bash
git add proto/ api/http/dto/gen/ web/src/services/gen/ web/src/modules/collab/
git commit -m "feat(proto): 试点迁移 collaboration(proto 契约+转换器归 gen/convert.go+前端 payload)"
```

---

## 阶段 2 — 批量迁移(每批 = 1 个 proto 文件 + 手写删除)

**每批统一工作流**(批内步骤不再重复,只列本批特有内容):

1. 写 `proto/<domain>/<name>.proto`(全文见各批)→ `make proto-gen`。
2. 人工核对 `api/http/dto/gen/<name>.go` 与手写逐字段一致(parity 会兜底)。
3. `parity_test.go` 登记本批 struct 对偶(手写还在)→ `go test ./api/http/dto/gen/ -run TestParity` 全绿。**若红:映射表或生成器有误,逐条修正生成器,禁止改手写字段**。
4. 手写 struct 所在文件若只剩 struct,整文件删除;有转换器/方法的,迁入 `api/http/dto/gen/convert.go`(逐函数原样);有保留项(multipart/form)的文件只删 struct。
5. parity 对偶移除、名字转入 `removedStructs`;`go build ./...` 编译器引导改 `dto.` → `gen.` 引用。
6. 前端契约类型替换(alias import 模式,试点已立样板)。
7. 验收:`go vet ./...`、`go test -short ./...`、`make fe-typecheck`、`make contract-enforce`(150 golden 语义等价零 diff)、`make code-quality`。
8. `git commit -m "feat(proto): 迁移 <domain>(<struct 数> struct)"`。CI 全绿再进下一批。

### Task 9: 批 1 — agent(5 struct)

**Files:**

- Create: `proto/agent/agent.proto`
- Modify: `api/http/dto/gen/convert.go`(无转换器,仅 import 清理)
- Delete: `api/http/dto/agent.go`
- Modify: `web/src/modules/agent/model/agent.ts`

**字段表(proto/agent/agent.proto 全文):**

```proto
syntax = "proto3";

package stratum.agent;

import "google/protobuf/struct.proto";

message CreateAgentRequest {
  // @binding: required,max=100
  string name = 1;
  // @binding: omitempty,max=2000
  string description = 2;
  // @binding: omitempty,max=16384
  string system_prompt = 3;
  // @binding: required
  string llm_model = 4;
  // @binding: required,min=1,max=90
  int32 max_iterations = 5;
  repeated string allowed_skills = 6;
}

message AgentResponse {
  string id = 1;
  string name = 2;
  string description = 3;
  string system_prompt = 4;
  string llm_model = 5;
  int32 max_iterations = 6;
  repeated string allowed_skills = 7;
  string created_at = 8; // 注意:手写是 string(非 time.Time),proto 用 string
}

message ExecuteAgentRequest {
  // @binding: required
  string query = 1;
  google.protobuf.Struct context = 2;
  google.protobuf.Struct variables = 3;
}

message ExecuteAgentResponse {
  // @omitempty
  google.protobuf.Value result = 1;
  // @omitempty
  repeated AgentStep steps = 2;
  string status = 3;
  // @omitempty
  string error = 4;
}

message AgentStep {
  int32 iteration = 1;
  string action = 2;
  // @omitempty
  string tool = 3;
  // @omitempty
  google.protobuf.Value input = 4;
  // @omitempty
  google.protobuf.Value output = 5;
}
```

**要点**:`CreatedAt` 手写是 `string`(非 time.Time)——proto string 直配,不使用 Timestamp;`context`/`variables` 手写 `map[string]interface{}` → Struct;`Result`/`Input`/`Output` `interface{}` → Value(any);`Steps []AgentStep` 嵌套 message 同文件生成。

**前端替换**(`web/src/modules/agent/model/agent.ts`):`ExecuteAgentPayload` interface 删除,改 `import { ExecuteAgentRequest as ExecuteAgentPayload } from '@/services/gen/agent';`。若前端 payload 字段与后端 `ExecuteAgentRequest` 字段集不一致(前端多/少字段),以实际调用处编译报错为准逐一核对——**字段不一致即契约漂移,先对齐 proto 再改前端**,禁止用 `as unknown as` 绕过。

### Task 10: 批 2 — mcp_config(3 struct + 4 个业务函数)

**Files:**

- Create: `proto/mcp/mcp_config.proto`
- Modify: `api/http/dto/gen/convert.go`(迁 ServerConfig()、NewMCPServerConfigResponse、IsSensitiveMCPConfigKey、filterMCPConfigValues、authCredentialConfigured)
- Delete: `api/http/dto/mcp_config.go`
- Modify: `web/src/modules/mcp/model/mcp.ts`

**proto 全文:**

```proto
syntax = "proto3";

package stratum.mcp;

import "google/protobuf/struct.proto";

message MCPServerConfigRequest {
  string id = 1;
  // @binding: required
  string name = 2;
  string version = 3;
  // @binding: required
  string transport = 4;
  string command = 5;
  repeated string args = 6;
  string url = 7;
  map<string, string> env = 8;
  // @omitempty
  map<string, string> headers = 9;
  repeated string capabilities = 10;
  // @gotype: time.Duration   // JSON 是纳秒整数,proto Duration 的 "3.5s" 不兼容
  int64 timeout = 11;
  // @gotype: *github.com/byteBuilderX/stratum/internal/mcp/domain.AuthConfig
  // @omitempty
  google.protobuf.Struct auth = 12;
  // @gotype: *github.com/byteBuilderX/stratum/internal/mcp/domain.RetryConfig
  // @omitempty
  google.protobuf.Struct retry = 13;
  // @gotype: encoding/json.RawMessage
  google.protobuf.Value system_key = 14;
}

message MCPAuthConfigResponse {
  // @gotype: github.com/byteBuilderX/stratum/internal/mcp/domain.AuthType
  string type = 1;
  // @omitempty
  string api_key_header = 2;
  // @omitempty
  string oauth2_client_id = 3;
  // @omitempty
  string oauth2_token_url = 4;
  // @omitempty
  repeated string oauth2_scopes = 5;
  bool credential_configured = 6; // 凭证值剔除设计:只暴露布尔指示器
}

message MCPServerConfigResponse {
  string id = 1;
  string name = 2;
  string version = 3;
  string transport = 4;
  string command = 5;
  repeated string args = 6;
  string url = 7;
  map<string, string> env = 8;
  // @omitempty
  map<string, string> headers = 9;
  repeated string capabilities = 10;
  // @gotype: time.Duration
  int64 timeout = 11;
  // @omitempty
  optional MCPAuthConfigResponse auth = 12;
  // @gotype: *github.com/byteBuilderX/stratum/internal/mcp/domain.RetryConfig
  // @omitempty
  google.protobuf.Struct retry = 13;
  string management_mode = 14;
  repeated string editors = 15;
}
```

**convert.go 迁移清单**(逐函数原样,敏感过滤逻辑是既有 golden 的守护对象):

```go
func (r MCPServerConfigRequest) ServerConfig() (*domain.ServerConfig, error) { /* 原样 */ }
func NewMCPServerConfigResponse(cfg *domain.ServerConfig) MCPServerConfigResponse { /* 原样,含 filterMCPConfigValues/authCredentialConfigured */ }
func IsSensitiveMCPConfigKey(key string) bool { /* 原样 */ }
func filterMCPConfigValues(values map[string]string) map[string]string { /* 原样 */ }
func authCredentialConfigured(auth *domain.AuthConfig) bool { /* 原样 */ }
```

**要点**:`Auth *domain.AuthConfig`/`Retry *domain.RetryConfig` 走 @gotype 黑盒(凭证字段不进契约);`SystemKey json.RawMessage` → `@gotype: encoding/json.RawMessage`(白名单特例,Task 2 已改);`Timeout time.Duration` → `@gotype: time.Duration` + proto int64(TS 侧 number,纳秒);`MCPAuthConfigResponse.Type domain.AuthType` → @gotype;`Auth *MCPAuthConfigResponse`(响应侧)是 optional message → `*MCPAuthConfigResponse`。

**前端替换**(`web/src/modules/mcp/model/mcp.ts`):`MCPAuthConfigResponse` 与 `MCPServerConfigResponse` interface 删除,改 `import { MCPAuthConfigResponse, MCPServerConfigResponse } from '@/services/gen/mcp_config';`——TS 生成 interface 名与后端同名,直接替换。

### Task 11: 批 3 — workflow(6 struct + RunInput 业务方法)

**Files:**

- Create: `proto/workflow/workflow.proto`
- Modify: `api/http/dto/gen/convert.go`(迁 RunInput)
- Delete: `api/http/dto/workflow.go`
- Modify: 前端 `web/src/modules/workflow/`(zod 派生类型保留,只改引用点编译报错处)

**proto 全文:**

```proto
syntax = "proto3";

package stratum.workflow;

import "google/protobuf/struct.proto";

message CreateWorkflowRequest {
  // @binding: required
  string name = 1;
  string description = 2;
  // @gotype: github.com/byteBuilderX/stratum/internal/workflow/domain.Spec
  // @binding: required
  google.protobuf.Struct spec = 3;
  // @gotype: github.com/byteBuilderX/stratum/internal/workflow/domain.InputSchema
  // @binding: required
  google.protobuf.Struct input_schema = 4;
}

message UpdateWorkflowRequest {
  // @binding: required
  string name = 1;
  string description = 2;
  // @gotype: github.com/byteBuilderX/stratum/internal/workflow/domain.Spec
  // @binding: required
  google.protobuf.Struct spec = 3;
  // @gotype: github.com/byteBuilderX/stratum/internal/workflow/domain.InputSchema
  // @binding: required
  google.protobuf.Struct input_schema = 4;
  // @binding: required
  int64 expected_revision = 5;
}

message StartWorkflowRunRequest {
  // @binding: required
  string version_id = 1;
  // @binding: required
  string task = 2;
  google.protobuf.Struct fields = 3;
  // @binding: required
  string idempotency_key = 4;
}

message WorkflowControlRequest {
  // @binding: required
  int64 expected_generation = 1;
  string reason = 2;
}

message WorkflowApprovalDecisionRequest {
  // @binding: required
  string run_id = 1;
  // @binding: required
  string attempt_id = 2;
  // @binding: required
  int64 expected_generation = 3;
  // @gotype: github.com/byteBuilderX/stratum/internal/workflow/domain.ApprovalDecision
  // @binding: required,oneof=approve reject
  string decision = 4;
  string comment = 5;
}

message WorkflowManualResolveRequest {
  // @binding: required
  int64 expected_generation = 1;
  // @gotype: github.com/byteBuilderX/stratum/internal/workflow/domain.ManualAction
  // @binding: required
  string action = 2;
  string output_summary = 3;
}
```

**convert.go 迁移**(业务方法,含 fields.task 保留字校验,逐行保留):

```go
func (r StartWorkflowRunRequest) RunInput() (map[string]any, error) {
 if _, exists := r.Fields["task"]; exists {
  return nil, fmt.Errorf("fields.task is reserved")
 }
 input := make(map[string]any, len(r.Fields)+1)
 input["task"] = r.Task
 for key, value := range r.Fields {
  input[key] = value
 }
 return input, nil
}
```

**要点**:`Spec`/`InputSchema`/`ApprovalDecision`/`ManualAction` 4 处 @gotype(workflow domain 黑盒);`ExpectedRevision`/`ExpectedGeneration` int64 直配;`Fields map[string]any` → Struct。

### Task 12: 批 4 — evaluation(9 struct;EvaluationCenterQuery 保留手写)

**Files:**

- Create: `proto/evaluation/evaluation.proto`
- Delete: `api/http/dto/evaluation.go`(**保留 `EvaluationCenterQuery`**——form 契约,迁到 `api/http/dto/gen/query_manual.go` 同包手写文件,原样保留)
- 前端:zod 派生类型保留

**proto 全文:**

```proto
syntax = "proto3";

package stratum.evaluation;

import "google/protobuf/struct.proto";

message EvaluationResourceRef {
  // @binding: required,oneof=skill agent mcp knowledge
  string kind = 1;
  // @binding: required
  string resource_id = 2;
  // @binding: required
  string revision_id = 3;
}

message EvaluationCaseRequest {
  string name = 1;
  // @binding: required
  google.protobuf.Value input = 2;
  // @binding: required
  google.protobuf.Value expected_output = 3;
  // @binding: required,oneof=exact contains regex
  string assertion_mode = 4;
  optional bool enabled = 5; // 手写 *bool,json 无 omitempty
}

message CreateEvaluationSuiteRequest {
  // @binding: required,max=255
  string name = 1;
  // @binding: max=2048
  string description = 2;
  // @binding: required,oneof=skill agent mcp knowledge
  string resource_kind = 3;
  // @binding: required,min=1,dive
  repeated EvaluationCaseRequest cases = 4;
}

message EnqueueEvaluationRunRequest {
  // @binding: required
  EvaluationResourceRef resource = 1;
  // @binding: required
  string suite_revision_id = 2;
  // @binding: required,max=255
  string idempotency_key = 3;
}

message EvaluationJobResponse {
  string job_id = 1;
  string status = 2;
  // @omitempty
  string error_message = 3;
  // @omitempty
  string result_id = 4;
}

message GenerateOptimizationRequest {
  // @binding: omitempty,max=255
  string idempotency_key = 1;
  // @binding: required
  EvaluationResourceRef baseline = 2;
  // @binding: required
  string suite_revision_id = 3;
  // @gotype: map[string][]any
  google.protobuf.Struct search_space = 4;
  // @binding: max=50,dive,max=2048
  repeated string failure_summaries = 5;
}

message CreateEvaluationExperimentRequest {
  // @binding: required
  EvaluationResourceRef stable = 1;
  // @binding: required
  EvaluationResourceRef canary = 2;
  // @binding: required
  string suite_revision_id = 3;
}

message EvaluationCommandRequest {
  // @binding: required,max=2048
  string reason = 1;
  // @binding: required,max=255
  string idempotency_key = 2;
  // @binding: required,min=1
  int64 expected_state_version = 3;
}

message RecordEvaluationFeedbackRequest {
  // @binding: required
  string trace_id = 1;
  // @binding: required,oneof=skill agent mcp knowledge
  string resource_kind = 2;
  // @binding: required
  string resource_id = 3;
  // @binding: min=0,max=1
  double score = 4;
  google.protobuf.Struct outcome = 5;
  // @binding: required,max=255
  string idempotency_key = 6;
  bool security_violation = 7;
}
```

**要点**:`EvaluationCenterQuery`(form tag)迁 `gen/query_manual.go` 保留,不入 proto(§2 非目标);`Cases` 的 `dive` 嵌套 binding 原样走 @binding 注释;`SearchSpace map[string][]any` → `@gotype: map[string][]any`(内建表达式白名单);`Enabled *bool` 无 omitempty → proto `optional bool`(生成器不加自动 omitempty,规则见 Task 2);`Input`/`ExpectedOutput` `any` → Value。

**query_manual.go**(form 契约,原样):

```go
package gen

type EvaluationCenterQuery struct {
 ResourceKind string `form:"resource_kind" binding:"omitempty,oneof=skill agent mcp knowledge"`
 ResourceID   string `form:"resource_id"`
 Status       string `form:"status"`
 Cursor       string `form:"cursor"`
 Limit        int    `form:"limit" binding:"omitempty,min=1,max=100"`
}
```

### Task 13: 批 5 — scheduled_task(5 struct)

**Files:**

- Create: `proto/scheduler/scheduled_task.proto`
- Modify: `api/http/dto/gen/convert.go`(迁 ToScheduledTaskResponse)
- Delete: `api/http/dto/scheduled_task.go`
- 前端:zod/引用类型保留

**proto 全文:**

```proto
syntax = "proto3";

package stratum.scheduler;

import "google/protobuf/timestamp.proto";
import "google/protobuf/struct.proto";

message CreateScheduledTaskRequest {
  // @binding: required
  string name = 1;
  // @binding: required
  string workflowId = 2;
  // @binding: required
  string versionId = 3;
  google.protobuf.Struct inputTemplate = 4;
  // @binding: required
  string cronExpr = 5;
}

message UpdateScheduledTaskRequest {
  // @binding: required
  string name = 1;
  // @binding: required
  string workflowId = 2;
  // @binding: required
  string versionId = 3;
  google.protobuf.Struct inputTemplate = 4;
  // @binding: required
  string cronExpr = 5;
}

message SetScheduledTaskEnabledRequest {
  bool enabled = 1;
}

message ScheduledTaskResponse {
  string id = 1;
  string name = 2;
  string workflowId = 3;
  string versionId = 4;
  google.protobuf.Struct inputTemplate = 5;
  string cronExpr = 6;
  bool enabled = 7;
  google.protobuf.Timestamp nextFireAt = 8;
  // @omitempty
  optional google.protobuf.Timestamp lastRunAt = 9;
  string lastRunStatus = 10;
  // @omitempty
  string lastErrorMessage = 11;
  string createdBy = 12;
  google.protobuf.Timestamp createdAt = 13;
  google.protobuf.Timestamp updatedAt = 14;
}

message ScheduledTaskPageResponse {
  repeated ScheduledTaskResponse tasks = 1;
  int32 total = 2;
  int32 page = 3;
  int32 pageSize = 4;
}
```

**convert.go 迁移**:

```go
func ToScheduledTaskResponse(t scheddomain.ScheduledTask) ScheduledTaskResponse { /* 原样 */ }
```

**要点**:camelCase 字段(workflowId/cronExpr/nextFireAt 等)靠 lint 豁免;`LastRunAt *time.Time` → optional Timestamp + @omitempty;`InputTemplate map[string]any` → Struct;int 分页 → int32。

### Task 14: 批 6 — operation_proposal(2 struct)

**Files:**

- Create: `proto/agent/operation_proposal.proto`
- Modify: `api/http/dto/gen/convert.go`(迁 ToOperationProposalResponse)
- Delete: `api/http/dto/operation_proposal.go`
- 前端:proposal.ts zod 派生保留

**proto 全文:**

```proto
syntax = "proto3";

package stratum.agent;

import "google/protobuf/timestamp.proto";
import "google/protobuf/struct.proto";

message RejectOperationProposalRequest {
  // @binding: required
  string note = 1;
}

message OperationProposalResponse {
  string id = 1;
  string agentId = 2;
  // @omitempty
  string targetAgentId = 3;
  string opType = 4;
  // @omitempty
  string delegation = 5;
  // @omitempty
  double maxDailyCostUsd = 6;
  // @omitempty
  int32 maxDailyExecutions = 7;
  // @gotype: encoding/json.RawMessage
  google.protobuf.Value payloadSummary = 8;
  // @gotype: github.com/byteBuilderX/stratum/internal/agent/domain.OpProposalStatus
  string status = 9;
  string proposerId = 10;
  // @omitempty
  string reviewedBy = 11;
  // @omitempty
  string reviewNote = 12;
  google.protobuf.Timestamp createdAt = 13;
  // @omitempty
  optional google.protobuf.Timestamp resolvedAt = 14;
  // @omitempty
  optional google.protobuf.Timestamp expiresAt = 15;
}
```

**convert.go 迁移**:

```go
func ToOperationProposalResponse(p domain.OperationProposal) OperationProposalResponse { /* 原样 */ }
```

**要点**:`PayloadSummary json.RawMessage` → @gotype RawMessage(注意无 @omitempty——`json:"payloadSummary"`);`Status domain.OpProposalStatus` → @gotype agent/domain;camelCase 密集(opType/maxDailyCostUsd/proposerId 等)。

### Task 15: 批 7 — resource_change_proposal(2 struct)

**Files:**

- Create: `proto/agent/resource_change_proposal.proto`
- Modify: `api/http/dto/gen/convert.go`(迁 NewResourceChangeProposalResponse,多行签名原样)
- Delete: `api/http/dto/resource_change_proposal.go`
- 前端:proposal.ts zod 派生保留

**proto 全文:**

```proto
syntax = "proto3";

package stratum.agent;

import "google/protobuf/timestamp.proto";
import "google/protobuf/struct.proto";

message UpdateResourceChangeProposalRequest {
  // @gotype: encoding/json.RawMessage
  // @binding: required
  google.protobuf.Value payload = 1;
}

message ResourceChangeProposalResponse {
  string id = 1;
  // @omitempty
  string conversationId = 2;
  string proposerId = 3;
  // @omitempty
  string confirmerId = 4;
  // @gotype: github.com/byteBuilderX/stratum/internal/agent/domain.ResourceKind
  string resourceKind = 5;
  // @omitempty
  string resourceId = 6;
  // @gotype: github.com/byteBuilderX/stratum/internal/agent/domain.ProposalOperation
  string operation = 7;
  // @omitempty
  string baselineFingerprint = 8;
  // @gotype: encoding/json.RawMessage
  // @omitempty
  google.protobuf.Value baselineProjection = 9;
  // @gotype: encoding/json.RawMessage
  google.protobuf.Value payload = 10;
  string summary = 11;
  // @gotype: github.com/byteBuilderX/stratum/internal/agent/domain.ProposalStatus
  string status = 12;
  // @omitempty
  string errorCode = 13;
  // @gotype: github.com/byteBuilderX/stratum/internal/agent/domain.ApplyResult
  // @omitempty
  google.protobuf.Value applyResult = 14;
  // @gotype: []github.com/byteBuilderX/stratum/internal/agent/domain.ProposalEvent
  repeated google.protobuf.Value events = 15;
  int32 editCount = 16;
  google.protobuf.Timestamp expiresAt = 17;
  google.protobuf.Timestamp createdAt = 18;
  google.protobuf.Timestamp updatedAt = 19;
}
```

**convert.go 迁移**(多行签名原样,含 OperationCreate 时 baselineProjection 置 nil 的业务逻辑):

```go
func NewResourceChangeProposalResponse(
 proposal domain.ResourceChangeProposal,
 events []domain.ProposalEvent,
) ResourceChangeProposalResponse { /* 原样 */ }
```

**要点**:本批 @gotype 密度最高(6 处:5 个 domain 类型 + 1 个 RawMessage 切片引用);`Events []domain.ProposalEvent` → `@gotype: []github.com/.../domain.ProposalEvent`(白名单 strip `[]` 前缀后通过);`ApplyResult` 是 struct 但手写带 omitempty——encoding/json 对 struct 的 omitempty 无效(永不省略),与手写行为一致,无差异;`EditCount int` → int32。

### Task 16: 批 8 — admin(13 struct)

**Files:**

- Create: `proto/admin/admin.proto`
- Delete: `api/http/dto/admin.go`
- 前端:zod/引用类型保留

**proto 全文(proto/admin/admin.proto):**

```proto
syntax = "proto3";

package stratum.admin;

import "google/protobuf/timestamp.proto";
import "google/protobuf/struct.proto";

message CreateTenantRequest {
  // @binding: required
  string name = 1;
  // @binding: required
  string slug = 2;
  // @binding: required,oneof=free pro enterprise
  string plan = 3;
  // @binding: required,oneof=active suspended
  string status = 4;
}

message UpdateTenantRequest {
  // @binding: omitempty,oneof=free pro enterprise
  string plan = 1;
  // @binding: omitempty,oneof=active suspended
  string status = 2;
}

message TenantResponse {
  string id = 1;
  string name = 2;
  string slug = 3;
  string plan = 4;
  string status = 5;
  int32 member_count = 6;
  google.protobuf.Timestamp created_at = 7;
  // @omitempty
  optional google.protobuf.Timestamp deleted_at = 8;
  bool is_default = 9;
}

message ListTenantsResponse {
  repeated TenantResponse tenants = 1;
  int32 total = 2;
  int32 page = 3;
  int32 page_size = 4;
}

message InviteMemberRequest {
  // @binding: required,email
  string email = 1;
  // @binding: required,oneof=member admin
  string role = 2;
}

message InviteMemberResponse {
  string invitation_code = 1;
  string email = 2;
  string role = 3;
}

message UpdateMemberRoleRequest {
  // @binding: required,oneof=member admin
  string role = 1;
}

message MemberResponse {
  string user_id = 1;
  string github_login = 2;
  string avatar_url = 3;
  string role = 4;
  google.protobuf.Timestamp joined_at = 5;
}

message ListMembersResponse {
  repeated MemberResponse members = 1;
  int32 total = 2;
  int32 page = 3;
  int32 page_size = 4;
}

message UpdateSettingsRequest {
  string name = 1;
  google.protobuf.Struct settings = 2;
}

message SettingsResponse {
  string tenant_id = 1;
  string tenant_name = 2;
  bool is_default = 3;
  google.protobuf.Struct settings = 4;
}

message TenantListItem {
  string tenant_id = 1;
  string name = 2;
  bool is_default = 3;
}

message TenantListResponse {
  repeated TenantListItem tenants = 1;
}
```

**要点**:无 @gotype 无转换器(13 struct 全基础类型 + 同文件嵌套 + Timestamp);`Settings map[string]interface{}` → Struct;snake_case 字段(member_count/created_at/is_default/github_login/avatar_url)逐字节保留。

### Task 17: 批 9 — dashboard + memory(6 struct)

**Files:**

- Create: `proto/dashboard/dashboard.proto`
- Create: `proto/memory/memory.proto`
- Delete: `api/http/dto/dashboard.go`、`api/http/dto/memory.go`
- 前端:zod/引用类型保留

**proto/dashboard/dashboard.proto 全文:**

```proto
syntax = "proto3";

package stratum.dashboard;

message DashboardOverviewResponse {
  int32 agents = 1;
  int32 skills = 2;
  int32 knowledge_workspaces = 3;
  int32 mcp_servers = 4;
  int32 model_providers = 5;
  int32 tenant_members = 6;
  int32 workflows = 7;
  int32 agent_user_messages_7d = 8; // 缩写词典:AgentUserMessages7d
}
```

**proto/memory/memory.proto 全文:**

```proto
syntax = "proto3";

package stratum.memory;

import "google/protobuf/timestamp.proto";

message CreateMemoryRequest {
  // @binding: required
  string content = 1;
  double importance = 2;
}

message MemoryFactResponse {
  string id = 1;
  string scope = 2;
  string content = 3;
  double importance = 4;
  google.protobuf.Timestamp created_at = 5;
  google.protobuf.Timestamp updated_at = 6;
}

message MemorySessionsResponse {
  repeated string sessions = 1;
}

message MemorySummaryResponse {
  string summary = 1;
}

message MemoryStatsResponse {
  int64 total_entries = 1;
  int64 short_term_count = 2;
  int64 long_term_count = 3;
  int64 entity_count = 4;
  int64 sessions_count = 5;
  int64 active_users = 6;
  int64 vector_count = 7;
  google.protobuf.Timestamp last_access_time = 8;
  int64 storage_size_bytes = 9;
}
```

**要点**:`agent_user_messages_7d` 是缩写词典与数字段组合的验证点(`AgentUserMessages7d`);memory stats 全 int64 直配;无转换器无 @gotype。

### Task 18: 批 10 — skill DTO(9 struct)

**Files:**

- Create: `proto/skill/skill.proto`
- Delete: `api/http/dto/request.go`
- Modify: `web/src/modules/skill/model/skill.ts`(CreateSkillDraftPayload 替换)

**proto 全文(proto/skill/skill.proto):**

```proto
syntax = "proto3";

package stratum.skill;

import "google/protobuf/struct.proto";

message SkillRequirements {
  repeated string mcpToolIds = 1;
  repeated string knowledgeWorkspaceIds = 2;
  repeated string memoryScopes = 3;
}

message CreateSkillRequest {
  // @binding: required
  string name = 1;
  // @binding: required
  string goal = 2;
  // @binding: required
  string whenToUse = 3;
  google.protobuf.Value sampleInput = 4;
  google.protobuf.Value expectedOutput = 5;
  // @binding: required
  string instructions = 6;
  SkillRequirements requirements = 7;
}

message UpdateSkillCapabilityRequest {
  string goal = 1;
  string whenToUse = 2;
  string inputSpec = 3;
  string outputSpec = 4;
}

message UpdateSkillActivationRequest {
  string name = 1;
  string description = 2;
  google.protobuf.Struct inputSchema = 3;
  google.protobuf.Struct outputSchema = 4;
  bool confirmed = 5;
}

message UpdateSkillInstructionBundleRequest {
  string instructions = 1;
  SkillRequirements requirements = 2;
}

message SkillWorkspaceResponse {
  repeated string editors = 1;
  SkillProductResponse skill = 2;
  SkillRevisionResponse draft = 3;
}

message SkillProductResponse {
  string id = 1;
  string name = 2;
  string description = 3;
  string status = 4;
  // @omitempty
  string activeRevisionId = 5;
  // @omitempty
  string draftRevisionId = 6;
}

message SkillRevisionResponse {
  string id = 1;
  string skillId = 2;
  // @omitempty
  int32 revisionNo = 3;
  string status = 4;
  google.protobuf.Struct capability = 5;
  google.protobuf.Struct activationContract = 6;
  string instructions = 7;
  google.protobuf.Struct requirements = 8;
  // @omitempty
  google.protobuf.Struct publishChecks = 9;
}

message ErrorResponse {
  int32 code = 1;
  string message = 2;
}
```

**前端替换**(`web/src/modules/skill/model/skill.ts`):`CreateSkillDraftPayload` interface 删除,改 `import { CreateSkillRequest as CreateSkillDraftPayload } from '@/services/gen/skill';`。若字段集不一致,按批 1 原则处理(先对齐 proto,禁止绕过)。

**要点**:`SkillRequirements` 是嵌套 message 引用(CreateSkillRequest.requirements);`SampleInput`/`ExpectedOutput` `any` → Value;camelCase 字段密集(mcpToolIds/whenToUse/inputSchema/activeRevisionId/revisionNo/publishChecks);`RevisionNo int` omitempty → int32 + @omitempty。

### Task 19: 批 11 — rag/knowledge(6 struct;UploadDocumentRequest 保留手写)

**Files:**

- Create: `proto/knowledge/rag.proto`
- Create: `api/http/dto/gen/rag_manual.go`(UploadDocumentRequest 保留)
- Delete: `api/http/dto/rag.go`
- 前端:zod/引用类型保留

**proto 全文(proto/knowledge/rag.proto):**

```proto
syntax = "proto3";

package stratum.knowledge;

import "google/protobuf/timestamp.proto";
import "google/protobuf/struct.proto";

message WorkspaceConfig {
  string embedding_model = 1;
  string chunking_strategy = 2;
  int32 chunk_size = 3;
  int32 chunk_overlap = 4;
  string query_mode = 5;
  int32 top_k = 6;
  string reranking = 7; // "" / "builtin-score-v1" / "provider:model"
  float score_threshold = 8; // float32:映射表 float → float32
  int32 rerank_top_k = 9; // 0 = use TopK
}

message QueryRequest {
  // @binding: required,max=4096
  string question = 1;
  // @binding: required
  string workspace = 2;
  // @binding: required,oneof=vector keyword hybrid
  string mode = 3;
  // @binding: omitempty,min=1,max=20
  int32 topK = 4;
}

message CreateWorkspaceRequest {
  // @binding: required
  string name = 1;
  string description = 2;
  WorkspaceConfig config = 3;
  repeated string editors = 4; // 每个 id 须持 admin/owner 角色
}

message UpdateWorkspaceRequest {
  optional string name = 1;
  optional string description = 2;
  optional WorkspaceConfig config = 3;
}

message IngestDocumentRequest {
  // @binding: required
  string workspace = 1;
  // @binding: required
  bytes document_data = 2;
  // @binding: required
  string filename = 3;
  // @binding: required
  string document_id = 4;
}

message WorkspaceListItem {
  string id = 1;
  string name = 2;
  string description = 3;
  WorkspaceConfig config = 4;
  google.protobuf.Timestamp created_at = 5;
  google.protobuf.Timestamp updated_at = 6;
}
```

**rag_manual.go**(multipart 契约,原样,不入 proto——§2 非目标):

```go
package gen

import "mime/multipart"

// UploadDocumentRequest is bound from POST /knowledge/ingest multipart form.
type UploadDocumentRequest struct {
 Workspace string                `form:"workspace" binding:"required"`
 File      *multipart.FileHeader `form:"file" binding:"required"`
}
```

**要点**:`QueryRequest.TopK` json key 是 `topK`(camelCase 单段,验证命名逻辑);`UpdateWorkspaceRequest` 三个 optional 字段(`*string`/`*WorkspaceConfig`,无 omitempty tag——optional 字段不自动加 omitempty,Task 2 规则);`ScoreThreshold float32` 是映射表 `float → float32` 的验证点(WorkspaceConfig 内);`DocumentData []byte` → bytes(JSON 输出 base64,encoding/json 语义同手写)。

- [ ] **Step: 收尾 parity 测试——dto 包消亡后移除 import 与锚定行**

本批删除了 `api/http/dto/rag.go`,手写 `dto` 包已无任何 JSON 契约(struct 全部迁入 `gen`)。修改 `api/http/dto/gen/parity_test.go`(Task 5 产物):

1. 删除 `dto "github.com/byteBuilderX/stratum/api/http/dto"` import 行;
2. 删除锚定行 `_ = dto.UploadDocumentRequest{}`(dto 包 import 移除后不再需要防自动删 import 的锚定);
3. `parityPairs` 中所有 `dto.Xxx` 引用改为 `gen.Xxx`(此时两包类型已逐字段相等,断言直接对照"生成 vs 自身"纯属形式——真正的守卫已是 Task 2 快照 + 生成器不漂移;`removedStructs` 里的名字保留,`TestRemovedStructsGone` 断言 `api/http/dto/` 下无任何残留)。
4. `UploadDocumentRequest`(gen/rag_manual.go)加入 `removedStructs` 或直接从测试删除——它不再有手写对照物,由 golden 测试守卫行为。

```bash
cd /home/yang/go-projects/stratum-proto-contract
go test -short ./api/...
```

Expected: 全绿——parity 测试退化为终态守卫形态(生成器快照 + dto 包无残留),不引用已消亡的手写包。

- [ ] **Step: 提交**

```bash
git add proto/knowledge/rag.proto api/http/dto/gen/rag_manual.go api/http/dto/gen/parity_test.go
git rm api/http/dto/rag.go
git commit -m "feat(proto): 批 11 — rag/knowledge 全量迁移,dto 包消亡(parity 终态)"
```

---

## 阶段 3 — 收尾

### Task 20: 敏感字段 golden 机械断言——凭证值字段名禁止入响应

**Files:**

- Create: `api/http/sensitive_fields_test.go`

**Interfaces:**

- Consumes: `api/http/testdata/contracts/*.golden.json`(150 个,现有);与 `contract_test.go` 同包 `http_test`。
- Produces: `TestGoldenNoCredentialValueFields`——把 §6"响应侧凭证值字段禁止进 proto 契约"从人工 review 变 CI 强制。

**设计**:响应侧凭证**值**字段(现状 `MCPAuthConfigResponse` 只暴露 `CredentialConfigured bool`)禁止出现在任何 golden 响应体的字段名里——即使值被掩码,字段名的存在也是"下一处 typo 就泄露"的构造缺陷。`api_key_header` 是允许项(它命名的是请求头键名,不是值)。

- [ ] **Step 1: 写 `sensitive_fields_test.go`**

```go
package http_test

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

// bannedCredentialFieldNames are credential value field names that must
// never appear in a golden response body (§6 敏感字段策略). A masked value
// field is one typo away from leaking — ban the name, not the value.
var bannedCredentialFieldNames = map[string]bool{
 "token":          true,
 "secret":         true,
 "api_key_value":  true,
 "client_secret":  true,
 "access_key":     true,
 "password":       true,
 "access_token":   true,
 "refresh_token":  true,
 "secret_key":     true,
}

// allowedCredentialFieldNames names a request header key (not a value).
var allowedCredentialFieldNames = map[string]bool{"api_key_header": true}

func TestGoldenNoCredentialValueFields(t *testing.T) {
 entries, err := os.ReadDir("testdata/contracts")
 if err != nil {
  t.Fatalf("read golden dir: %v", err)
 }
 found := 0
 for _, e := range entries {
  if e.IsDir() || !strings.HasSuffix(e.Name(), ".golden.json") {
   continue
  }
  body, err := os.ReadFile(filepath.Join("testdata/contracts", e.Name()))
  if err != nil {
   t.Fatalf("read %s: %v", e.Name(), err)
  }
  var v any
  if err := json.Unmarshal(body, &v); err != nil {
   t.Fatalf("unmarshal %s: %v", e.Name(), err)
  }
  var walk func(prefix string, v any)
  walk = func(prefix string, v any) {
   switch m := v.(type) {
   case map[string]any:
    for k, val := range m {
     if bannedCredentialFieldNames[k] && !allowedCredentialFieldNames[k] {
      t.Errorf("%s: banned credential field %s%s", e.Name(), prefix, k)
      found++
     }
     walk(prefix+k+".", val)
    }
   case []any:
    for i, val := range m {
     walk(fmt.Sprintf("%s[%d].", prefix, i), val)
    }
   }
  }
  walk("", v)
 }
 if found > 0 {
  t.Errorf("found %d banned credential fields; remove them from the response DTO/proto (fail-closed, no exemptions)", found)
 }
}
```

- [ ] **Step 2: 运行——若有既有命中先清再启用**

```bash
cd /home/yang/go-projects/stratum-proto-contract
go test ./api/http -run TestGoldenNoCredentialValueFields -v
```

Expected: PASS(现状响应已只暴露 `CredentialConfigured bool`,见 §6)。**若出现 FAIL**:在 golden 中命中说明响应 DTO 仍暴露凭证值字段名——禁止豁免、禁止删断言;先改 DTO/proto(把凭证值字段从响应剔除),golden 重新记录,再跑全绿。本测试同时是迁移期间每批的回归守卫(任何批在响应侧引入凭证值字段名即红)。

- [ ] **Step 3: 提交**

```bash
git add api/http/sensitive_fields_test.go
git commit -m "test(api): golden 凭证值字段名机械断言(§6 敏感字段 fail-closed)"
```

---

### Task 21: 残留守卫 + CLAUDE.md 契约记录 + 终验

**Files:**

- Create: `scripts/quality/dto-residue-guard.sh`
- Modify: `Makefile`(guard 挂 `check`)
- Modify: `CLAUDE.md`(契约事实源记录)
- 终验:全量 `go test -race` + `make test-verify-before-pr`

**Interfaces:**

- Consumes: 全部迁移批(Task 8-19)的终态;Task 5 的 `TestRemovedStructsGone` 负责包内残留,本 guard 负责仓库级残留与前端残留。
- Produces: `make check` 的一个守卫,防半迁移态混入 main(§8 阶段 3)。

- [ ] **Step 1: 写 `scripts/quality/dto-residue-guard.sh`**

```bash
#!/usr/bin/env bash
# dto-residue-guard: fail if any hand-written DTO residue survives the proto
# migration (§8 阶段 3). The proto contract in proto/ is the single source
# of truth; generated output lives in gen/ dirs and is not committed.
set -euo pipefail

cd "$(dirname "$0")/../.."

fail=0

# 1. api/http/dto/ must contain nothing but the generated gen/ subdir
leftover_dto=$(find api/http/dto -maxdepth 1 -type f -name '*.go' | wc -l)
if [[ "$leftover_dto" -ne 0 ]]; then
  echo "error: hand-written DTO files remain in api/http/dto/:" >&2
  find api/http/dto -maxdepth 1 -type f -name '*.go' >&2
  fail=1
fi

# 2. no stale import of the old dto package (gen/ files are exempt —
#    they never import the package they live in)
stale=$(grep -rln '"github.com/byteBuilderX/stratum/api/http/dto"' \
  --include='*.go' api cmd internal pkg web 2>/dev/null | grep -v '/gen/' || true)
if [[ -n "$stale" ]]; then
  echo "error: stale imports of the old dto package:" >&2
  echo "$stale" >&2
  fail=1
fi

# 3. migrated frontend contract type declarations must not resurface in
#    web/src/modules (zod schemas are exempt — §7 keeps them hand-written)
migrated_types=(
  'interface CreateCollabRequest'
  'interface CreateSkillDraftPayload'
  'interface ExperimentResponse'
)
for pattern in "${migrated_types[@]}"; do
  hits=$(grep -rn "$pattern" web/src/modules --include='*.ts' 2>/dev/null || true)
  if [[ -n "$hits" ]]; then
    echo "error: migrated contract type still declared: $pattern" >&2
    echo "$hits" >&2
    fail=1
  fi
done

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi
echo "dto-residue-guard: clean"
```

`migrated_types` 清单实施时按各批前端替换点补全(每批删除的 interface 名);`ExperimentResponse` 只在它被 proto 替换后加入——若 zod 双源保留其声明(§7 暂缓),此项删除。**任何新批删除的契约类型都必须登记进清单,guard 才不失效。**

- [ ] **Step 2: Makefile `check` 挂 guard**

```makefile
check: proto-gen
 bash scripts/quality/dto-residue-guard.sh
 # ...既有 check 步骤不变
```

- [ ] **Step 3: CLAUDE.md 记录契约事实源**

在 `## Development and end-to-end verification` 一节追加一条:

```markdown
- HTTP JSON 参数契约的唯一事实源是 `proto/` 下的 .proto 文件;前后端类型由 `protoc-gen-ginstruct` 生成(`api/http/dto/gen/`、`web/src/services/gen/`,不入 git)。改参数契约 = 改 proto 后 `make proto-gen`;绕过 make 直敲 `go test` 且未生成时 import 编译失败,属预期约束(与"生成物不入 git"配套)。
```

- [ ] **Step 4: 终验**

```bash
cd /home/yang/go-projects/stratum-proto-contract
make risk-guardrails
go test -v -race -timeout 30s ./...
bash scripts/quality/dto-residue-guard.sh
```

Expected: 全绿。随后按项目流程 `make test-verify-before-pr`(无头 Chromium 全套)在 clean commit 上运行,全绿后提 PR。

- [ ] **Step 5: 提交**

```bash
git add scripts/quality/dto-residue-guard.sh Makefile CLAUDE.md
git commit -m "feat(proto): 阶段 3 收尾——残留守卫 + CLAUDE.md 契约事实源记录"
```

---

---

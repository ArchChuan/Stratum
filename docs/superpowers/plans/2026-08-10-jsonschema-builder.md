# 类型安全 JSON Schema builder 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新建 `pkg/jsonschema` 类型安全 JSON Schema 构造包,迁移 13 处手写 `map[string]any` schema 构造,错误在编译/构造期暴露。

**Architecture:** JSON Schema 的类型化 Go struct(`Schema`,json tag 直接序列化)+ 构造辅助函数(`Object`/`String`/`Array`/`Enum`/`OneOf` 等,构造期校验)+ `Map()` 转换回 `map[string]any` 喂现有消费端字段。消费端字段类型零改动,输出语义等价。

**Tech Stack:** Go 1.25、标准库 `encoding/json`、现有 `santhosh-tekuri/jsonschema/v6`(消费端,不动)。

## Global Constraints

- 消费端字段类型(`ActivationContract.InputSchema`、`ToolDefinition.InputSchema`、`Tool.InputSchema`)保持 `map[string]any` 不变,构造处用 `.Map()` 转换
- content hash 不变:`SkillSQL` 走 `toMap → compactJSON` 归一化,`builtin_skills_test.go` hash 测试必须零改动通过
- 输出语义等价;struct 字段序与 map 键序可不同,但 `json.Marshal` 数值必须字节一致(`float64(1)` → `1`)
- 不动:`a2a/`、`memory/`、`milvus_adapter` 的 map(业务数据);测试文件的手写 map
- Go 行宽 ≤120;error 用 `fmt.Errorf("jsonschema: ...")` 包装;包内 import 分组 stdlib → third-party → internal
- 新函数圈复杂度 ≤10、长度 ≤120 行
- 禁止在 main 分支提交;本计划全部工作在 worktree `../stratum-jsonschema-builder`(分支 `feat/jsonschema-builder`)执行

---

### Task 1: 修正 spec + 核心类型 `Schema` 与序列化

**Files:**

- Modify: `docs/superpowers/specs/2026-08-10-jsonschema-builder-design.md`(字段修正)
- Create: `pkg/jsonschema/schema.go`
- Create: `pkg/jsonschema/schema_test.go`

**Interfaces:**

- Produces: `type Type string` + 7 个常量(`TypeObject`/`TypeString`/`TypeArray`/`TypeInteger`/`TypeNumber`/`TypeBoolean`/`TypeNull`);`type Schema struct`(字段见代码);`func Ptr[T any](v T) *T`(泛型指针 helper,Task 2 使用)

**背景**:设计确认时遗漏两点,计划先修正 spec 再实现:

1. `AdditionalProperties bool` + omitempty 会吞掉 `false`(我们要输出 `"additionalProperties": false`),必须 `*bool`
2. `Minimum/Maximum *int` 不够——`minProposalMCPRetryBackoffFactor = 1.0` 是 float64,统一 `*float64`(`json.Marshal(float64(1))` 输出 `1`,与现状 int 字面量字节一致)

- [ ] **Step 1: 修正 spec 字段**

在 `docs/superpowers/specs/2026-08-10-jsonschema-builder-design.md` 中:

```go
// 原:
AdditionalProperties bool               `json:"additionalProperties,omitempty"` // false 输出,true 省略(= 默认)
Minimum              *int               `json:"minimum,omitempty"` // 指针避免 0 歧义;现状全 integer
Maximum              *int               `json:"maximum,omitempty"`
// 改为:
AdditionalProperties *bool              `json:"additionalProperties,omitempty"` // nil 省略(默认 true);Ptr(false) 输出 false
Minimum              *float64           `json:"minimum,omitempty"` // 指针区分省略与 0;backoffFactor 为 float64
Maximum              *float64           `json:"maximum,omitempty"`
```

同步修改"设计要点"两条:

- `AdditionalProperties *bool`:nil 省略(JSON Schema 默认 true);`Ptr(false)` 输出 `false`——现状 `additionalProperties:false` 用法
- `Minimum/Maximum *float64`:指针避免 0 被 omitempty 吞掉;`minProposalMCPRetryBackoffFactor` 是 float64(`1.0`),`json.Marshal(float64(1))` 输出 `1` 与现状字节一致

- [ ] **Step 2: 写失败测试 `pkg/jsonschema/schema_test.go`**

```go
package jsonschema

import (
 "encoding/json"
 "testing"
)

// 测试直接 marshal 类型化 Schema 输出标准 JSON Schema(表驱动)。
func TestSchemaMarshal(t *testing.T) {
 cases := []struct {
  name   string
  schema *Schema
  want   string
 }{
  {
   name:   "empty object",
   schema: &Schema{Type: TypeObject},
   want:   `{"type":"object"}`,
  },
  {
   name: "closed object with string prop",
   schema: &Schema{
    Type:                 TypeObject,
    AdditionalProperties: Ptr(false),
    Properties: map[string]*Schema{
     "query": {Type: TypeString, MinLength: 1, MaxLength: 100},
    },
    Required: []string{"query"},
   },
   want: `{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string","minLength":1,"maxLength":100}},"required":["query"]}`,
  },
  {
   name:   "enum string",
   schema: &Schema{Type: TypeString, Enum: []any{"a", "b"}},
   want:   `{"type":"string","enum":["a","b"]}`,
  },
  {
   name:   "array of enum",
   schema: &Schema{Type: TypeArray, Items: &Schema{Type: TypeString, Enum: []any{"x"}}, MinItems: 1, MaxItems: 5, UniqueItems: true},
   want:   `{"type":"array","items":{"type":"string","enum":["x"]},"minItems":1,"maxItems":5,"uniqueItems":true}`,
  },
  {
   name:   "integer with minimum",
   schema: &Schema{Type: TypeInteger, Minimum: Ptr(1.0), Maximum: Ptr(20.0)},
   want:   `{"type":"integer","minimum":1,"maximum":20}`,
  },
  {
   name:   "number with float minimum",
   schema: &Schema{Type: TypeNumber, Minimum: Ptr(1.5)},
   want:   `{"type":"number","minimum":1.5}`,
  },
  {
   name:   "const",
   schema: &Schema{Const: "agent"},
   want:   `{"const":"agent"}`,
  },
  {
   name:   "oneOf branches",
   schema: &Schema{OneOf: []*Schema{{Type: TypeObject}, {Type: TypeObject}}},
   want:   `{"oneOf":[{"type":"object"},{"type":"object"}]}`,
  },
  {
   name:   "nil minimum omitted",
   schema: &Schema{Type: TypeInteger, Minimum: Ptr(0.0)},
   want:   `{"type":"integer","minimum":0}`,
  },
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   got, err := json.Marshal(tc.schema)
   if err != nil {
    t.Fatalf("marshal: %v", err)
   }
   if string(got) != tc.want {
    t.Errorf("got  %s\nwant %s", got, tc.want)
   }
  })
 }
}

// 泛型 Ptr 语义测试(含零值:Ptr(0) 必须是非 nil 指针)。
func TestPtr(t *testing.T) {
 p := Ptr(0)
 if p == nil || *p != 0 {
  t.Fatalf("Ptr(0) = %v, want non-nil pointer to 0", p)
 }
 f := Ptr(1.5)
 if f == nil || *f != 1.5 {
  t.Fatalf("Ptr(1.5) = %v, want non-nil pointer to 1.5", f)
 }
}
```

- [ ] **Step 3: 运行确认失败**

Run: `cd /home/yang/go-projects/stratum-jsonschema-builder && go test ./pkg/jsonschema/`
Expected: FAIL(`undefined: TypeObject`、`undefined: Ptr`)

- [ ] **Step 4: 实现 `pkg/jsonschema/schema.go`**

```go
// Package jsonschema 提供 JSON Schema 的类型安全构造。
//
// 与 github.com/santhosh-tekuri/jsonschema/v6 并存时,本包 import alias 为 jschema。
package jsonschema

// Type 是 JSON Schema type 关键字的值。
type Type string

const (
 TypeObject  Type = "object"
 TypeString  Type = "string"
 TypeArray   Type = "array"
 TypeInteger Type = "integer"
 TypeNumber  Type = "number"
 TypeBoolean Type = "boolean"
 TypeNull    Type = "null"
)

// Schema 是 JSON Schema 的类型化表示;json.Marshal 输出标准 JSON Schema。
// 字段集合 = 代码库现状用到的全部关键字,不超前扩展。
type Schema struct {
 Type                 Type               `json:"type,omitempty"`
 Description          string             `json:"description,omitempty"`
 Properties           map[string]*Schema `json:"properties,omitempty"`
 Required             []string           `json:"required,omitempty"`
 AdditionalProperties *bool              `json:"additionalProperties,omitempty"` // nil 省略(默认 true);Ptr(false) 输出 false
 Enum                 []any              `json:"enum,omitempty"`
 Const                any                `json:"const,omitempty"`
 Items                *Schema            `json:"items,omitempty"`
 MinItems             int                `json:"minItems,omitempty"`
 MaxItems             int                `json:"maxItems,omitempty"`
 UniqueItems          bool               `json:"uniqueItems,omitempty"`
 MinLength            int                `json:"minLength,omitempty"`
 MaxLength            int                `json:"maxLength,omitempty"`
 Minimum              *float64           `json:"minimum,omitempty"` // 指针区分省略与 0;支持 float(backoffFactor)
 Maximum              *float64           `json:"maximum,omitempty"`
 OneOf                []*Schema          `json:"oneOf,omitempty"`
}

// Ptr 返回指向 v 的指针,用于区分"省略"与"零值"(additionalProperties、minimum 等)。
func Ptr[T any](v T) *T { return &v }
```

- [ ] **Step 5: 运行确认通过**

Run: `cd /home/yang/go-projects/stratum-jsonschema-builder && go test ./pkg/jsonschema/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  git add docs/superpowers/specs/2026-08-10-jsonschema-builder-design.md pkg/jsonschema/schema.go pkg/jsonschema/schema_test.go && \
  git commit -m "feat(jsonschema): 类型化 Schema 核心类型与序列化

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: 构造 API 与构造期校验

**Files:**

- Create: `pkg/jsonschema/build.go`
- Create: `pkg/jsonschema/build_test.go`

**Interfaces:**

- Consumes: Task 1 的 `Schema`、`Type` 常量、`Ptr`
- Produces: `Prop`、`RequiredProp(name string, s *Schema) Prop`、`OptionalProp(name string, s *Schema) Prop`、`Object(props ...Prop) (*Schema, error)`、`ClosedObject(props ...Prop) (*Schema, error)`、`String(desc string) *Schema`、`StringRange(minLen, maxLen int, desc string) *Schema`、`Integer(min, max *int, desc string) *Schema`、`Number(min, max *float64, desc string) *Schema`、`Boolean(desc string) *Schema`、`Array(items *Schema, minItems, maxItems int, unique bool, desc string) *Schema`、`Enum[T any](desc string, vals ...T) (*Schema, error)`、`Const(v any) *Schema`、`OneOf(branches ...*Schema) (*Schema, error)`、`(s *Schema) Map() map[string]any`、`Must(s *Schema, err error) *Schema`

**校验规则**(返回 error,不 panic;`Must` 才 panic):

- `Object`:name 非空 / schema 非 nil / name 唯一 / 递归校验子节点(Items、OneOf 分支)
- `Enum`:vals 非空
- `OneOf`:≥1 分支且无 nil
- 递归 `validate`:`array` items 非 nil、`string` minLength ≤ maxLength、object property 非 nil

- [ ] **Step 1: 写失败测试 `pkg/jsonschema/build_test.go`**

```go
package jsonschema

import (
 "encoding/json"
 "strings"
 "testing"
)

func TestObject(t *testing.T) {
 t.Run("collects required and marshals", func(t *testing.T) {
  s, err := Object(
   RequiredProp("query", StringRange(1, 100, "")),
   OptionalProp("area", String("")),
  )
  if err != nil {
   t.Fatalf("Object: %v", err)
  }
  got, _ := json.Marshal(s)
  want := `{"type":"object","properties":{"area":{"type":"string"},"query":{"type":"string","minLength":1,"maxLength":100}},"required":["query"]}`
  if string(got) != want {
   t.Errorf("got  %s\nwant %s", got, want)
  }
 })

 emptyName := []struct{ name string; props []Prop }{
  {"empty name", []Prop{{Name: "", Schema: String("")}}},
  {"nil schema", []Prop{{Name: "x", Schema: nil}}},
  {"duplicate name", []Prop{{Name: "x", Schema: String("")}, {Name: "x", Schema: String("")}}},
 }
 for _, tc := range emptyName {
  t.Run(tc.name, func(t *testing.T) {
   if _, err := Object(tc.props...); err == nil {
    t.Fatalf("Object(%s): want error, got nil", tc.name)
   }
  })
 }

 t.Run("array with nil items rejected", func(t *testing.T) {
  _, err := Object(RequiredProp("x", &Schema{Type: TypeArray}))
  if err == nil || !strings.Contains(err.Error(), "items") {
   t.Fatalf("want items error, got %v", err)
  }
 })

 t.Run("closed object outputs additionalProperties false", func(t *testing.T) {
  s, err := ClosedObject(RequiredProp("query", String("")))
  if err != nil {
   t.Fatalf("ClosedObject: %v", err)
  }
  got, _ := json.Marshal(s)
  want := `{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string"}},"required":["query"]}`
  if string(got) != want {
   t.Errorf("got  %s\nwant %s", got, want)
  }
 })
}

func TestEnum(t *testing.T) {
 t.Run("string enum with inferred type", func(t *testing.T) {
  s, err := Enum("", "agent", "skill")
  if err != nil {
   t.Fatalf("Enum: %v", err)
  }
  got, _ := json.Marshal(s)
  if string(got) != `{"type":"string","enum":["agent","skill"]}` {
   t.Errorf("got %s", got)
  }
 })
 t.Run("empty values rejected", func(t *testing.T) {
  if _, err := Enum(""); err == nil {
   t.Fatal("Enum(): want error, got nil")
  }
 })
 t.Run("dynamic slice expands", func(t *testing.T) {
  vals := []string{"a", "b"}
  s, err := Enum("", vals...)
  if err != nil {
   t.Fatalf("Enum: %v", err)
  }
  if len(s.Enum) != 2 {
   t.Fatalf("Enum len = %d, want 2", len(s.Enum))
  }
 })
}

func TestOneOf(t *testing.T) {
 t.Run("requires at least one branch", func(t *testing.T) {
  if _, err := OneOf(); err == nil {
   t.Fatal("OneOf(): want error, got nil")
  }
 })
 t.Run("rejects nil branch", func(t *testing.T) {
  if _, err := OneOf(&Schema{Type: TypeObject}, nil); err == nil {
   t.Fatal("OneOf(nil): want error, got nil")
  }
 })
}

func TestScalars(t *testing.T) {
 got, err := json.Marshal(Integer(Ptr(1), Ptr(20), ""))
 if err != nil || string(got) != `{"type":"integer","minimum":1,"maximum":20}` {
  t.Errorf("Integer = %s, %v", got, err)
 }
 got, _ = json.Marshal(Number(Ptr(1.5), nil, ""))
 if string(got) != `{"type":"number","minimum":1.5}` {
  t.Errorf("Number = %s", got)
 }
 got, _ = json.Marshal(Array(String(""), 1, 5, true, ""))
 if string(got) != `{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":5,"uniqueItems":true}` {
  t.Errorf("Array = %s", got)
 }
 got, _ = json.Marshal(Boolean(""))
 if string(got) != `{"type":"boolean"}` {
  t.Errorf("Boolean = %s", got)
 }
 got, _ = json.Marshal(Const("agent"))
 if string(got) != `{"const":"agent"}` {
  t.Errorf("Const = %s", got)
 }
 got, _ = json.Marshal(String("带描述"))
 if string(got) != `{"type":"string","description":"带描述"}` {
  t.Errorf("String with desc = %s", got)
 }
 got, _ = json.Marshal(String(""))
 if string(got) != `{"type":"string"}` {
  t.Errorf("String without desc = %s", got)
 }
}

func TestMapRoundTrip(t *testing.T) {
 s, err := Object(RequiredProp("question", String("")))
 if err != nil {
  t.Fatalf("Object: %v", err)
 }
 direct, _ := json.Marshal(s)
 viaMap, _ := json.Marshal(s.Map())
 if string(direct) != string(viaMap) {
  t.Errorf("Map round trip mismatch:\ndirect %s\nviaMap %s", direct, viaMap)
 }
}

func TestMustPanics(t *testing.T) {
 defer func() {
  if recover() == nil {
   t.Fatal("Must: want panic, got nil")
  }
 }()
 Must(nil, OneOf())
}

func TestRecursiveValidation(t *testing.T) {
 // 嵌套 items 的数组作为 prop 时,递归校验应拒绝 nil items。
 _, err := Object(RequiredProp("x", Array(&Schema{Type: TypeArray}, 1, 2, false, "")))
 if err == nil || !strings.Contains(err.Error(), "items") {
  t.Fatalf("want nested items error, got %v", err)
 }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd /home/yang/go-projects/stratum-jsonschema-builder && go test ./pkg/jsonschema/`
Expected: FAIL(`undefined: Object`、`undefined: RequiredProp` 等)

- [ ] **Step 3: 实现 `pkg/jsonschema/build.go`**

```go
package jsonschema

import (
 "encoding/json"
 "fmt"
)

// Prop 描述对象的一个属性;Required=true 自动进入 required 列表。
// "required 引用不存在的字段" 在类型层面不可能发生。
type Prop struct {
 Name     string
 Schema   *Schema
 Required bool
}

// RequiredProp 构造必填属性。
func RequiredProp(name string, s *Schema) Prop { return Prop{Name: name, Schema: s, Required: true} }

// OptionalProp 构造可选属性。
func OptionalProp(name string, s *Schema) Prop { return Prop{Name: name, Schema: s} }

// Object 构造 object schema;required 由 Prop.Required 自动收集。
// 校验:name 非空、schema 非 nil、name 唯一、递归校验子节点。
func Object(props ...Prop) (*Schema, error) {
 s := &Schema{Type: TypeObject, Properties: make(map[string]*Schema, len(props))}
 seen := make(map[string]struct{}, len(props))
 for _, p := range props {
  if p.Name == "" {
   return nil, fmt.Errorf("jsonschema: property name is empty")
  }
  if p.Schema == nil {
   return nil, fmt.Errorf("jsonschema: property %q schema is nil", p.Name)
  }
  if _, dup := seen[p.Name]; dup {
   return nil, fmt.Errorf("jsonschema: duplicate property %q", p.Name)
  }
  seen[p.Name] = struct{}{}
  s.Properties[p.Name] = p.Schema
  if p.Required {
   s.Required = append(s.Required, p.Name)
  }
 }
 if err := validate(s); err != nil {
  return nil, err
 }
 return s, nil
}

// ClosedObject 构造 additionalProperties=false 的 object schema(closed object)。
func ClosedObject(props ...Prop) (*Schema, error) {
 s, err := Object(props...)
 if err != nil {
  return nil, err
 }
 s.AdditionalProperties = Ptr(false)
 return s, nil
}

// String 构造 string schema;desc 为空时省略 description。
func String(desc string) *Schema { return withDesc(&Schema{Type: TypeString}, desc) }

// StringRange 构造带 minLength/maxLength 的 string schema;0 表示不设上限。
func StringRange(minLen, maxLen int, desc string) *Schema {
 return withDesc(&Schema{Type: TypeString, MinLength: minLen, MaxLength: maxLen}, desc)
}

// Integer 构造 integer schema;min/max 为 nil 时省略 minimum/maximum。
func Integer(min, max *int, desc string) *Schema {
 s := &Schema{Type: TypeInteger}
 if min != nil {
  s.Minimum = Ptr(float64(*min))
 }
 if max != nil {
  s.Maximum = Ptr(float64(*max))
 }
 return withDesc(s, desc)
}

// Number 构造 number schema;min/max 为 nil 时省略 minimum/maximum。
func Number(min, max *float64, desc string) *Schema {
 s := &Schema{Type: TypeNumber, Minimum: min, Maximum: max}
 return withDesc(s, desc)
}

// Boolean 构造 boolean schema。
func Boolean(desc string) *Schema { return withDesc(&Schema{Type: TypeBoolean}, desc) }

// Array 构造 array schema;items 必须非 nil(由递归 validate 校验)。
func Array(items *Schema, minItems, maxItems int, unique bool, desc string) *Schema {
 return withDesc(&Schema{Type: TypeArray, Items: items, MinItems: minItems, MaxItems: maxItems, UniqueItems: unique}, desc)
}

// Enum 构造 enum schema;vals 必须非空。type 由元素类型推断(string→string、int→integer、bool→boolean),
// 未匹配类型时省略 type(JSON Schema 合法)。
func Enum[T any](desc string, vals ...T) (*Schema, error) {
 if len(vals) == 0 {
  return nil, fmt.Errorf("jsonschema: enum values are empty")
 }
 enum := make([]any, 0, len(vals))
 for _, v := range vals {
  enum = append(enum, v)
 }
 s := &Schema{Enum: enum}
 switch any(vals[0]).(type) {
 case string:
  s.Type = TypeString
 case int, int8, int16, int32, int64:
  s.Type = TypeInteger
 case bool:
  s.Type = TypeBoolean
 }
 return withDesc(s, desc), nil
}

// Const 构造 const schema(精确值匹配)。
func Const(v any) *Schema { return &Schema{Const: v} }

// OneOf 构造 oneOf 分支组合;至少一个非 nil 分支。
func OneOf(branches ...*Schema) (*Schema, error) {
 if len(branches) == 0 {
  return nil, fmt.Errorf("jsonschema: oneOf requires at least one branch")
 }
 for i, b := range branches {
  if b == nil {
   return nil, fmt.Errorf("jsonschema: oneOf branch %d is nil", i)
  }
 }
 return &Schema{OneOf: branches}, nil
}

// Must 用于初始化/seed 场景;校验失败 panic(builtin_skills 已有 panic 先例)。
func Must(s *Schema, err error) *Schema {
 if err != nil {
  panic(fmt.Sprintf("jsonschema: %v", err))
 }
 return s
}

// Map 返回 map[string]any 表示,喂给现有 map 字段(消费端类型不变)。
// marshal 失败视为编程错误,panic。
func (s *Schema) Map() map[string]any {
 raw, err := json.Marshal(s)
 if err != nil {
  panic(fmt.Sprintf("jsonschema: marshal schema: %v", err))
 }
 var m map[string]any
 if err := json.Unmarshal(raw, &m); err != nil {
  panic(fmt.Sprintf("jsonschema: unmarshal schema: %v", err))
 }
 return m
}

func withDesc(s *Schema, desc string) *Schema {
 if desc != "" {
  s.Description = desc
 }
 return s
}

// validate 递归校验 schema 内部一致性(仅规则,不校验开放未支持的关键字)。
func validate(s *Schema) error {
 switch s.Type {
 case TypeObject:
  for name, child := range s.Properties {
   if child == nil {
    return fmt.Errorf("jsonschema: property %q schema is nil", name)
   }
   if err := validate(child); err != nil {
    return err
   }
  }
 case TypeArray:
  if s.Items == nil {
   return fmt.Errorf("jsonschema: array items is nil")
  }
  if err := validate(s.Items); err != nil {
   return err
  }
 case TypeString:
  if s.MinLength > 0 && s.MaxLength > 0 && s.MinLength > s.MaxLength {
   return fmt.Errorf("jsonschema: minLength %d > maxLength %d", s.MinLength, s.MaxLength)
  }
 }
 for i, b := range s.OneOf {
  if b == nil {
   return fmt.Errorf("jsonschema: oneOf branch %d is nil", i)
  }
  if err := validate(b); err != nil {
   return err
  }
 }
 return nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd /home/yang/go-projects/stratum-jsonschema-builder && go test ./pkg/jsonschema/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  git add pkg/jsonschema/build.go pkg/jsonschema/build_test.go && \
  git commit -m "feat(jsonschema): 构造 API 与构造期校验

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: 迁移 `system_assistant_tools.go`

**Files:**

- Modify: `internal/agent/application/system_assistant_tools.go`

**Interfaces:**

- Consumes: Task 2 全部构造函数(alias `jschema`)
- Produces: `searchDocsSchema() map[string]any`、`diagnoseTenantSchema() map[string]any`;`proposalToolSchema()` 与 `proposalRetrySchema()` 实现内部改为 builder 构造,签名不变

- [ ] **Step 1: 迁移前先读现状文件确认行号与全部文案**

Run: `sed -n '1,180p' /home/yang/go-projects/stratum-jsonschema-builder/internal/agent/application/system_assistant_tools.go`

注意:下列代码块中 `Enum` 的枚举值、`description` 文案、常量名,全部以原文件实际内容为准(摘要/计划中的值是推测,逐字抄原文);`proposalRetrySchema` 的 min/max 常量名与单位以原文件为准。

- [ ] **Step 2: 加 import 并替换 `SystemAssistantToolDefinitions` 的两个 schema**

```go
// import 块追加(与 internal 分组):
 jschema "github.com/byteBuilderX/stratum/pkg/jsonschema"
```

替换 `SystemAssistantToolDefinitions` 内两个 `InputSchema: map[string]any{...}`(原第 28-32 行、38-45 行)为函数调用,并在文件末尾追加:

```go
func searchDocsSchema() map[string]any {
 return jschema.Must(jschema.ClosedObject(
  jschema.RequiredProp("query", jschema.StringRange(1, constants.SystemAssistantQueryMaxRunes, "")),
 )).Map()
}

func diagnoseTenantSchema() map[string]any {
 areas := jschema.Array(
  jschema.Must(jschema.Enum("", "agent", "skill", "mcp", "knowledge", "model")),
  1, constants.SystemAssistantAreasMaxCount, true, "",
 )
 return jschema.Must(jschema.ClosedObject(
  jschema.RequiredProp("areas", areas),
 )).Map()
}
```

- [ ] **Step 3: 替换 `proposalToolSchema` 与 `proposalRetrySchema`**

替换 `proposalToolSchema` 函数体(原第 70-145 行)与 `proposalRetrySchema`(原第 147-170 行)为:

```go
func proposalToolSchema() map[string]any {
 payloads := map[domain.ResourceKind]*jschema.Schema{
  domain.ResourceAgent: jschema.Must(jschema.ClosedObject(
   jschema.RequiredProp("name", jschema.StringRange(1, 0, "")),
   jschema.RequiredProp("description", jschema.String("")),
   jschema.RequiredProp("model", jschema.String("")),
   jschema.RequiredProp("maxIterations", jschema.Integer(jschema.Ptr(1), jschema.Ptr(20), "")),
   jschema.RequiredProp("maxContextTokens", jschema.Integer(jschema.Ptr(1), nil, "")),
   jschema.OptionalProp("skillIds", jschema.Array(jschema.String(""), 0, 0, true, "")),
   jschema.OptionalProp("mcpToolIds", jschema.Array(jschema.String(""), 0, 0, true, "")),
   jschema.OptionalProp("workspaceIds", jschema.Array(jschema.String(""), 0, 0, true, "")),
  )),
  domain.ResourceSkillDraft: jschema.Must(jschema.ClosedObject(
   jschema.RequiredProp("name", jschema.StringRange(1, 0, "")),
   jschema.RequiredProp("description", jschema.String("")),
   jschema.RequiredProp("instructions", jschema.StringRange(1, 0, "")),
  )),
  domain.ResourceMCPConfig: jschema.Must(jschema.ClosedObject(
   jschema.RequiredProp("name", jschema.StringRange(1, 0, "")),
   jschema.RequiredProp("version", jschema.String("")),
   jschema.RequiredProp("transport", jschema.Must(jschema.Enum("", "stdio", "streamable-http"))),
   jschema.RequiredProp("timeoutSec", jschema.Integer(jschema.Ptr(minProposalMCPTimeoutSec), jschema.Ptr(maxProposalMCPTimeoutSec), "")),
   jschema.OptionalProp("command", jschema.String("")),
   jschema.OptionalProp("args", jschema.Array(jschema.String(""), 0, 0, false, "")),
   jschema.OptionalProp("url", jschema.String("")),
   jschema.OptionalProp("capabilities", jschema.Array(jschema.String(""), 0, 0, true, "")),
   jschema.OptionalProp("retry", proposalRetrySchema()),
  )),
  domain.ResourceKnowledgeWorkspace: jschema.Must(jschema.ClosedObject(
   jschema.RequiredProp("name", jschema.StringRange(1, 0, "")),
   jschema.RequiredProp("description", jschema.StringRange(1, 0, "")),
   jschema.OptionalProp("embeddingModel", jschema.String("")),
  )),
 }
 kinds := []domain.ResourceKind{
  domain.ResourceAgent, domain.ResourceSkillDraft, domain.ResourceMCPConfig, domain.ResourceKnowledgeWorkspace,
 }
 branches := make([]*jschema.Schema, 0, len(kinds)*2)
 for _, kind := range kinds {
  for _, operation := range []domain.ProposalOperation{domain.OperationCreate, domain.OperationUpdate} {
   props := []jschema.Prop{
    jschema.RequiredProp("resourceKind", jschema.Const(string(kind))),
    jschema.RequiredProp("operation", jschema.Const(string(operation))),
    jschema.RequiredProp("payload", payloads[kind]),
   }
   if operation == domain.OperationUpdate {
    props = append(props, jschema.RequiredProp("resourceId", jschema.StringRange(1, 0, "")))
   }
   branches = append(branches, jschema.Must(jschema.ClosedObject(props...)))
  }
 }
 return jschema.Must(jschema.OneOf(branches...)).Map()
}

func proposalRetrySchema() *jschema.Schema {
 return jschema.Must(jschema.ClosedObject(
  jschema.RequiredProp("enabled", jschema.Boolean("")),
  jschema.RequiredProp("maxRetries", jschema.Integer(jschema.Ptr(minProposalMCPRetryCount), jschema.Ptr(maxProposalMCPRetryCount), "")),
  jschema.RequiredProp("initialDelayMs", jschema.Integer(jschema.Ptr(int(minProposalMCPRetryInitialDelayMs)), jschema.Ptr(int(maxProposalMCPRetryInitialDelayMs)), "")),
  jschema.RequiredProp("maxDelayMs", jschema.Integer(jschema.Ptr(int(minProposalMCPRetryMaxDelayMs)), jschema.Ptr(int(maxProposalMCPRetryMaxDelayMs)), "")),
  jschema.RequiredProp("backoffFactor", jschema.Number(jschema.Ptr(minProposalMCPRetryBackoffFactor), jschema.Ptr(maxProposalMCPRetryBackoffFactor), "")),
 ))
}
```

注意:`minProposalMCPRetryInitialDelayMs/MaxDelayMs` 是 `int64`,`jschema.Ptr` 泛型直接接 `int64` 但 `Integer` 要 `*int`,故显式 `int()` 转换。

- [ ] **Step 4: 编译 + 相关测试**

Run:

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  go build ./internal/agent/application/ && \
  go test -short ./internal/agent/application/ -run 'SystemAssistant|Proposal' -count=1
```

Expected: BUILD OK、PASS(测试断言 schema 输出/参数解析的用例)

- [ ] **Step 5: 验证输出语义等价(比对原 schema JSON)**

Run:

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && go test -short ./internal/agent/application/ -run 'TestSystemAssistantToolDefinitions' -v -count=1 2>&1 | tail -20
```

Expected: PASS。若存在对 `InputSchema` 键序/字节的断言,检查是否等价(键序变化可接受,语义必须一致)。

- [ ] **Step 6: Commit**

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  git add internal/agent/application/system_assistant_tools.go && \
  git commit -m "refactor(agent): system_assistant 工具 schema 迁移到 jsonschema builder

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: 迁移 `agent.go` 3 处工具 schema

**Files:**

- Modify: `internal/agent/application/agent.go`(约 1246-1290 行,以 Task 3 Step 1 实际行号为准)

**Interfaces:**

- Consumes: Task 2 构造函数;`enumVals` 为方法内动态 `[]string` 变量

- [ ] **Step 1: 读现状确认行号、`enumVals` 类型与全部 description 文案**

Run: `sed -n '1220,1300p' /home/yang/go-projects/stratum-jsonschema-builder/internal/agent/application/agent.go`

注意:下列代码块的 description 文案是推测值,以原文件实际文案逐字为准;`top_k`/`limit` 的 min/max 以原文为准(原文有 minimum/maximum 就加 `Integer` 参数,没有就 `nil`)。

- [ ] **Step 2: 加 import 并替换三处 `InputSchema: map[string]interface{}{...}`**

import 块追加:

```go
 jschema "github.com/byteBuilderX/stratum/pkg/jsonschema"
```

替换 `stratum_search_knowledge`(原约 1246-1266 行):

```go
   InputSchema: jschema.Must(jschema.Object(
    jschema.RequiredProp("workspaces", jschema.Array(
     jschema.Must(jschema.Enum("", enumVals...)),
     1, 0, false, "Knowledge workspaces to search (one or more)",
    )),
    jschema.RequiredProp("query", jschema.String("Search query")),
    jschema.OptionalProp("top_k", jschema.Integer(nil, nil, "Number of results per workspace (1-20, default 5)")),
   )).Map(),
```

替换 `stratum_recall_memory`(原约 1270-1282 行):

```go
   InputSchema: jschema.Must(jschema.Object(
    jschema.RequiredProp("query", jschema.String("Search query to find relevant memories")),
    jschema.OptionalProp("limit", jschema.Integer(nil, nil, "Max results (1-20, default 5)")),
   )).Map(),
```

替换 `stratum_continue_reasoning`(原约 1287-1290 行):

```go
   InputSchema: jschema.Must(jschema.Object()).Map(),
```

注意:原 `stratum_continue_reasoning` 输出 `"properties":{},"required":[]`(非 nil 空值),迁移后省略空 properties/required(omitempty),输出 `{"type":"object"}`——语义等价(JSON Schema 空 properties/required 无约束)。若相关测试断言字节,语义等价可接受。

- [ ] **Step 3: 编译 + 相关测试**

Run:

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  go build ./internal/agent/application/ && \
  go test -short ./internal/agent/application/ -run 'SearchKnowledge|RecallMemory|ContinueReasoning|AssistantTools' -count=1
```

Expected: BUILD OK、PASS

- [ ] **Step 4: Commit**

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  git add internal/agent/application/agent.go && \
  git commit -m "refactor(agent): 内置工具 schema 迁移到 jsonschema builder

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: 迁移 `builtin_skills.go` 4 个 ActivationContract

**Files:**

- Modify: `internal/skill/infrastructure/seeds/builtin_skills.go`

**Interfaces:**

- Consumes: Task 2 构造函数

- [ ] **Step 1: 加 import 并替换 4 处 `ActivationContract` 的 InputSchema/OutputSchema**

import 块追加:

```go
 jschema "github.com/byteBuilderX/stratum/pkg/jsonschema"
```

4 处 `InputSchema: map[string]any{...}` 替换(platformGuide/tenantDiagnostic/resourceChange/toolExecution):

```go
// platformGuide 处(替换原 55-59 行)
   InputSchema: jschema.Must(jschema.Object(
    jschema.RequiredProp("question", jschema.String("")),
   )).Map(),
```

```go
// tenantDiagnostic 处(替换原 107-110 行)
   InputSchema: jschema.Must(jschema.Object(
    jschema.OptionalProp("area", jschema.String("")),
   )).Map(),
```

```go
// resourceChange 处(替换原 159-162 行)
   InputSchema: jschema.Must(jschema.Object(
    jschema.RequiredProp("resourceKind", jschema.String("")),
   )).Map(),
```

```go
// toolExecution 处(替换原 211-214 行)
   InputSchema: jschema.Must(jschema.Object(
    jschema.RequiredProp("tool", jschema.String("")),
   )).Map(),
```

`OutputSchema: map[string]any{"type": "object"}`(4 处,原 60/111/163/215 行)替换:

```go
   OutputSchema: jschema.Must(jschema.Object()).Map(),
```

- [ ] **Step 2: 编译 + hash 测试(兼容性关键守护)**

Run:

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  go build ./internal/skill/... && \
  go test -short ./internal/skill/infrastructure/seeds/ -run 'BuiltinSkills' -v -count=1 2>&1 | tail -15
```

Expected: BUILD OK、PASS。**`TestBuiltinSkillsContentHash` 必须零改动通过**(验证 `SkillSQL` 的 toMap 归一化 + 输出语义等价 → hash 不变)。

- [ ] **Step 3: Commit**

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  git add internal/skill/infrastructure/seeds/builtin_skills.go && \
  git commit -m "refactor(skill): 内置 skill activation contract 迁移到 jsonschema builder

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: 迁移 e2e 测试桩 + mcp 类型统一

**Files:**

- Modify: `cmd/e2e-mcp-server/main.go:432`
- Modify: `internal/mcp/domain/mcp.go:20`

**Interfaces:**

- Consumes: Task 2 构造函数

- [ ] **Step 1: 迁移 e2e 桩**

`cmd/e2e-mcp-server/main.go` 加 import:

```go
 jschema "github.com/byteBuilderX/stratum/pkg/jsonschema"
```

替换(原 432 行附近):

```go
   "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
```

为:

```go
   "inputSchema": jschema.Must(jschema.Object(
    jschema.RequiredProp("text", jschema.String("")),
   )).Map(),
```

- [ ] **Step 2: mcp 类型统一**

`internal/mcp/domain/mcp.go:20`:

```go
// 原:
 InputSchema  map[string]interface{} `json:"inputSchema"`
// 改为(等值类型,编译零影响):
 InputSchema  map[string]any `json:"inputSchema"`
```

- [ ] **Step 3: 编译验证**

Run:

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  go build ./cmd/e2e-mcp-server/ ./internal/mcp/... && \
  go test -short ./internal/mcp/... ./api/wiring/ -count=1
```

Expected: BUILD OK、PASS(`api/wiring/evaluation_mcp_adapter_test.go` 用的 `map[string]any{"type":"object"}` 字面量仍兼容)

- [ ] **Step 4: Commit**

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  git add cmd/e2e-mcp-server/main.go internal/mcp/domain/mcp.go && \
  git commit -m "refactor(mcp): e2e 桩 schema 迁移 + InputSchema 类型统一为 any

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: 全量验证与收尾

**Files:**

- 无新增;验证性任务

- [ ] **Step 1: 全量快速验证**

Run:

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  go vet ./... && go test -short ./...
```

Expected: 全部 PASS。重点确认:

- `internal/skill/infrastructure/seeds/` hash 测试
- `api/http/contract_test.go` golden(golden 含受影响 schema 时需核对语义;若仅键序/空值省略差异,按 spec 兼容性声明接受或按项目流程重新生成 golden——**先报告差异再决定,禁止静默刷新**)

- [ ] **Step 2: 代码质量门禁**

Run: `cd /home/yang/go-projects/stratum-jsonschema-builder && bash scripts/quality/risk-regression-guard.sh --explain && make code-quality`
Expected: 无新增超限(新函数 ≤10 圈复杂度、≤120 行)

- [ ] **Step 3: 核对迁移完整性**

Run:

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && \
  grep -rn '"type": "object"' internal/ --include='*.go' | grep -v '_test.go' | grep -v 'pkg/jsonschema'
```

Expected: 仅剩 spec 声明不迁移的位置(如 `internal/mcp/infrastructure/client.go` 若含运行时解析 schema 属数据不动;`internal/skill/application/version_service.go:128` 的兜底 `map[string]any{"type":"object"}` 属用户数据兜底,不动)。逐条确认剩余匹配是"数据/兜底"而非"代码定义"。

- [ ] **Step 4: 提交最终状态并汇报**

```bash
cd /home/yang/go-projects/stratum-jsonschema-builder && git status && git log --oneline -8
```

汇报:各任务 commit、验证结果、golden 差异(若有)、后续步骤(push + PR + `make test-verify-before-pr` 由 PR 流程执行)。

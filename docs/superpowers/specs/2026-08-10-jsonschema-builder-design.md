# 类型安全 JSON Schema builder 设计

日期:2026-08-10
状态:已批准(待实现计划)

## 背景与问题

代码库多处手写 JSON Schema 为 `map[string]any` 字面量,存在真实缺陷:

- **键名错字编译期不报**(如 `"requird"`),运行时才在消费端 jsonschema 编译时暴露
- **类型不一致**:`"required": []any{"question"}` 与 `[]string{"question"}` 混用;`map[string]interface{}` 与 `map[string]any` 混用
- **结构错误延迟暴露**:`proposalToolSchema` 这类动态拼接的 oneOf 结构错误无法在编译/构造期发现

消费端(`tool_execution_guard.go`、`tool_result_guard.go`)使用 `santhosh-tekuri/jsonschema/v6` 运行时编译,合法性问题要等 LLM 调用才炸。

## 现状盘点

手写 JSON Schema 构造点(Go,非测试):

| 位置 | 内容 |
|---|---|
| `internal/agent/application/system_assistant_tools.go` | 3 个 tool schema + `proposalToolSchema`(循环动态 oneOf,`const`/嵌套 payload)+ `proposalRetrySchema` |
| `internal/agent/application/agent.go:1246/1270/1287` | `stratum_search_knowledge`、`stratum_recall_memory` 等 3 处(enum 数组、`[]string`/`[]interface{}` 混用) |
| `internal/skill/infrastructure/seeds/builtin_skills.go` | 4 个 ActivationContract |
| `cmd/e2e-mcp-server/main.go:432` | e2e 测试桩 1 处 |
| `internal/mcp/domain/mcp.go:20` | `Tool.InputSchema map[string]interface{}` 类型定义 |

实际用到的 JSON Schema 关键字:`type`(object/string/array/integer/number/boolean)、`properties`、`required`、`additionalProperties:false`、`min/maxLength`、`min/maxItems`、`uniqueItems`、`items`、`minimum/maximum`(均 integer)、`enum`、`const`、`oneOf`。

不动:`a2a/`、`memory/`、`milvus_adapter` 等的 `map[string]any` 是普通业务数据,不是 JSON Schema;测试文件中的 `map[string]any{"type":"object"}` 保留。

## 方案对比与选择

| 方案 | 描述 | 结论 |
|---|---|---|
| A: 类型化 AST struct | JSON Schema 的 Go 结构体映射 + 构造辅助 + 构造期校验,零依赖 | **选定** |
| B: 链式 builder(zod 风格) | 类型安全同 A,但 Go 无链式语法糖,构造期校验与链式冲突 | 否决 |
| C: struct tag 反射(invopop/jsonschema) | 适合大量静态 struct 定义;本项目手写点仅 ~13 处且偏值对象,反射框架偏重,required 一致性校验需补 | 否决 |

选定理由:零依赖、迁移成本最低、构造期校验好做;`proposalToolSchema` 需要程序化构造(循环),A 天然支持。

## 设计

### 包与核心类型

新包 `pkg/jsonschema`。与 `github.com/santhosh-tekuri/jsonschema/v6` 同文件并存时,本地包 import alias 为 `jschema`(包文档写明)。

```go
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
 AdditionalProperties bool               `json:"additionalProperties,omitempty"` // false 输出,true 省略(= 默认)
 Enum                 []any              `json:"enum,omitempty"`
 Const                any                `json:"const,omitempty"`
 Items                *Schema            `json:"items,omitempty"`
 MinItems             int                `json:"minItems,omitempty"`
 MaxItems             int                `json:"maxItems,omitempty"`
 UniqueItems          bool               `json:"uniqueItems,omitempty"`
 MinLength            int                `json:"minLength,omitempty"`
 MaxLength            int                `json:"maxLength,omitempty"`
 Minimum              *int               `json:"minimum,omitempty"` // 指针避免 0 歧义;现状全 integer
 Maximum              *int               `json:"maximum,omitempty"`
 OneOf                []*Schema          `json:"oneOf,omitempty"`
}
```

设计要点:

- `AdditionalProperties bool`:`false` 输出,`true` 省略(JSON Schema 默认即为 true)——与现状 `additionalProperties:false` 用法一致,无需指针
- `Minimum/Maximum *int`:避免 0 被 omitempty 吞掉;现状全部 integer 用法
- `Enum []any`:元素类型多样(string/integer)
- `MinItems/MaxItems int` + omitempty:0 省略等价默认,语义无歧义

### 构造 API 与校验

```go
// Prop 描述对象的一个属性;Required=true 自动进入 required 列表。
// "required 引用不存在的字段" 在类型层面不可能发生。
type Prop struct {
 Name     string
 Schema   *Schema
 Required bool
}

func Object(props ...Prop) (*Schema, error) // 校验:name 非空、schema 非 nil、name 唯一、递归校验子节点
func RequiredProp(name string, s *Schema) Prop
func OptionalProp(name string, s *Schema) Prop

func String(desc string) *Schema
func StringRange(minLen, maxLen int, desc string) *Schema
func Integer(min, max *int, desc string) *Schema
func Number(min, max *int, desc string) *Schema
func Boolean(desc string) *Schema
func Array(items *Schema, minItems, maxItems int, unique bool, desc string) *Schema
func Enum[T any](desc string, vals ...T) (*Schema, error) // 校验:非空
func Const(v any) *Schema
func OneOf(branches ...*Schema) (*Schema, error)          // 校验:≥1 分支

func (s *Schema) Map() map[string]any   // marshal→unmarshal,喂给现有 map 字段
func Must(s *Schema, err error) *Schema // seed/初始化场景,校验失败 panic(builtin_skills 已有 panic 先例)
```

构造期校验规则(返回 error,不 panic;`Must` 才 panic):

- `Object`:prop name 非空 / schema 非 nil / name 唯一 / 递归校验子节点
- `Enum`:vals 非空(空 enum 永不匹配,几乎必是 bug)
- `OneOf`:≥1 分支
- `Array`:items 非 nil

迁移示例(builtin_skills):

```go
// 现状
InputSchema: map[string]any{
 "type":       "object",
 "properties": map[string]any{"question": map[string]any{"type": "string"}},
 "required":   []any{"question"},
},

// 迁移后
InputSchema: jschema.Must(jschema.Object(
 jschema.RequiredProp("question", jschema.String("")),
)).Map(),
```

### 迁移清单

| # | 位置 | 内容 |
|---|---|---|
| 1 | `pkg/jsonschema` | 新建包 |
| 2 | `internal/agent/application/system_assistant_tools.go` | 3 个 tool schema + `proposalToolSchema` + `proposalRetrySchema` |
| 3 | `internal/agent/application/agent.go:1246/1270/1287` | 3 处(enum 数组、`[]string`/`[]interface{}` 混用区) |
| 4 | `internal/skill/infrastructure/seeds/builtin_skills.go` | 4 个 ActivationContract |
| 5 | `cmd/e2e-mcp-server/main.go:432` | e2e 测试桩 |
| 6 | `internal/mcp/domain/mcp.go:20` | `map[string]interface{}` → `map[string]any`(等值类型,顺手统一) |

`proposalToolSchema` 的循环动态构造迁移:每个分支用 `Object` + `Const` 构造,`Required` 随操作分支追加,最终 `OneOf` 组合。

### 兼容性保证

1. **消费端字段类型零改动**:`ActivationContract.InputSchema`、`ToolDefinition.InputSchema`、`Tool.InputSchema` 保持 `map[string]any`,构造处用 `.Map()` 转换。`santhosh.Compile`、类型断言、DTO 序列化全部原样。
2. **content hash 不变**:`SkillSQL` 走 `toMap → compactJSON` 归一化(marshal→unmarshal→marshal),`[]any{"question"}` 与 `[]string{"question"}` 序列化结果字节相同;`builtin_skills_test.go` 的 hash 测试自动守护。
3. **契约 golden 核对**:struct 字段序与 map 键序不同,若 `api/http/testdata/contracts/*.golden.json` 含受影响 schema 需重新生成(语义等价,仅键序变化;map 键本按字典序输出,大部分场景字节级不变)。

### 测试与验证

- `pkg/jsonschema` 表驱动单测:
  - 序列化 golden:每个构造函数的输出与现状手写 map 的 `json.Marshal` 结果语义等价
  - 校验规则:空 enum、重复 prop name、空 name、items nil、OneOf 0 分支、required 自动收集正确性
  - `Map()` 往返:`json.Marshal(s.Map())` 与 `json.Marshal(s)` 字节等价
- 消费端回归:`builtin_skills_test.go` content hash 测试零改动通过、`go test -short ./...`、`make test-verify-before-pr`
- 不新增运行时规范校验(消费端 `tool_execution_guard` 已有 jsonschema 编译兜底,避免重复)

## 范围外

- **HTTP API 契约化(OpenAPI spec-first)**:独立子项目,另行 brainstorm。衔接点:OpenAPI 3.1 内嵌 JSON Schema 2020-12,builder 产物可复用为 OpenAPI 的 schema 组件。
- **用户上传的 schema**(skill ActivationContract 经 HTTP API 上传)是运行时数据,不经过 builder,原样流转。
- **workflow `InputSchema`**:结构化 Field 类型,非 JSON Schema,不在本任务内。

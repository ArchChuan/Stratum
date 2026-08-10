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

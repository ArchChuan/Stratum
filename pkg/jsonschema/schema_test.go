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
			want: `{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":100}},"required":["query"],"additionalProperties":false}`,
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

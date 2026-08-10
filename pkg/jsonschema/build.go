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
	if err := validateType(s); err != nil {
		return err
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

// validateType 校验 type 特有结构并递归子节点。
func validateType(s *Schema) error {
	switch s.Type {
	case TypeObject:
		return validateObject(s)
	case TypeArray:
		return validateArray(s)
	case TypeString:
		return validateStringRange(s)
	}
	return nil
}

// validateObject 校验 properties 全部非 nil 并递归。
func validateObject(s *Schema) error {
	for name, child := range s.Properties {
		if child == nil {
			return fmt.Errorf("jsonschema: property %q schema is nil", name)
		}
		if err := validate(child); err != nil {
			return err
		}
	}
	return nil
}

// validateArray 校验 items 非 nil 并递归。
func validateArray(s *Schema) error {
	if s.Items == nil {
		return fmt.Errorf("jsonschema: array items is nil")
	}
	return validate(s.Items)
}

// validateStringRange 校验 minLength ≤ maxLength。
func validateStringRange(s *Schema) error {
	if s.MinLength > 0 && s.MaxLength > 0 && s.MinLength > s.MaxLength {
		return fmt.Errorf("jsonschema: minLength %d > maxLength %d", s.MinLength, s.MaxLength)
	}
	return nil
}

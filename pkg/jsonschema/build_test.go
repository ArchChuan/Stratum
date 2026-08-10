package jsonschema

import (
	"encoding/json"
	"reflect"
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

	emptyName := []struct {
		name  string
		props []Prop
	}{
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
		want := `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`
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
		if _, err := Enum[string](""); err == nil {
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
	got, _ = json.Marshal(UntypedArray(""))
	if string(got) != `{"type":"array"}` {
		t.Errorf("UntypedArray = %s", got)
	}
	// UntypedArray 通过 Object 递归校验(有意无 items 合法)。
	untyped, err := Object(RequiredProp("nodes", UntypedArray("")))
	if err != nil {
		t.Errorf("Object(UntypedArray): %v", err)
	} else if !untyped.Properties["nodes"].untypedArray {
		t.Errorf("UntypedArray marker lost")
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
	// Map() 输出 map[string]any(键序字典序),与 struct 直出(字段序)语义等价;
	// 消费端(手写 map 现状)序列化本就是字典序,.Map() 与其字节一致。
	direct, _ := json.Marshal(s)
	var directMap map[string]any
	if err := json.Unmarshal(direct, &directMap); err != nil {
		t.Fatalf("unmarshal direct: %v", err)
	}
	viaMap := s.Map()
	if !reflect.DeepEqual(viaMap, directMap) {
		t.Errorf("Map round trip mismatch:\nviaMap  %#v\ndirect %#v", viaMap, directMap)
	}
	// 字典序键序与现状手写 map 一致(properties < required < type)。
	got, _ := json.Marshal(viaMap)
	want := `{"properties":{"question":{"type":"string"}},"required":["question"],"type":"object"}`
	if string(got) != want {
		t.Errorf("Map marshal = %s\nwant %s", got, want)
	}
}

func TestMustPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Must: want panic, got nil")
		}
	}()
	_, err := OneOf()
	Must(nil, err)
}

func TestRecursiveValidation(t *testing.T) {
	// 嵌套 items 的数组作为 prop 时,递归校验应拒绝 nil items。
	_, err := Object(RequiredProp("x", Array(&Schema{Type: TypeArray}, 1, 2, false, "")))
	if err == nil || !strings.Contains(err.Error(), "items") {
		t.Fatalf("want nested items error, got %v", err)
	}
}

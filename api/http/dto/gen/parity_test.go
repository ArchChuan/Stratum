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
	_ = gen.CreateCollabRequest{} // 锚定 gen 包 import 存在;Task 8 Step 3 取消注释试点对偶后移除
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
	pattern := "type (" + joinPattern(names) + ") struct"
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
			out += "|"
		}
		out += n
	}
	return out
}

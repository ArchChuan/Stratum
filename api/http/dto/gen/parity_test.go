package gen_test

import (
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/dto"
)

// parityPairs 登记 (gen struct, 手写 struct) 类型对偶。
// 每批迁移工作流:写 proto → make proto-gen → 在此登记对偶 → parity 全绿 →
// 删除手写 struct → 整条对偶移除(手写类型不存在后编译失败)、名字转入
// removedStructs。
var parityPairs = []struct {
	name string
	gen  reflect.Type
	hw   reflect.Type
}{}

// removedStructs 登记"已从 dto 包删除"的 struct 名。
var removedStructs = map[string]bool{
	"CreateCollabRequest":               true,
	"CollabResponse":                    true,
	"TaskStepResponse":                  true,
	"CreateAgentRequest":                true,
	"AgentResponse":                     true,
	"ExecuteAgentRequest":               true,
	"ExecuteAgentResponse":              true,
	"AgentStep":                         true,
	"MCPServerConfigRequest":            true,
	"MCPAuthConfigResponse":             true,
	"MCPServerConfigResponse":           true,
	"CreateWorkflowRequest":             true,
	"UpdateWorkflowRequest":             true,
	"StartWorkflowRunRequest":           true,
	"WorkflowControlRequest":            true,
	"WorkflowApprovalDecisionRequest":   true,
	"WorkflowManualResolveRequest":      true,
	"EvaluationResourceRef":             true,
	"EvaluationCaseRequest":             true,
	"CreateEvaluationSuiteRequest":      true,
	"EnqueueEvaluationRunRequest":       true,
	"EvaluationJobResponse":             true,
	"GenerateOptimizationRequest":       true,
	"CreateEvaluationExperimentRequest": true,
	"EvaluationCommandRequest":          true,
	"RecordEvaluationFeedbackRequest":   true,
	"CreateScheduledTaskRequest":        true,
	"UpdateScheduledTaskRequest":        true,
	"SetScheduledTaskEnabledRequest":    true,
	"ScheduledTaskResponse":             true,
	"ScheduledTaskPageResponse":         true,
}

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
// 命名 struct 类型(gen.AgentStep vs dto.AgentStep)跨包不等价,按字段集
// 结构等价递归比较(嵌套 message 场景,如 ExecuteAgentResponse.Steps)。
func compatibleType(a, b reflect.Type) bool {
	return compatibleTypeDepth(a, b, 0)
}

func compatibleTypeDepth(a, b reflect.Type, depth int) bool {
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
		return compatibleTypeDepth(a.Elem(), b.Elem(), depth+1)
	}
	if a.Kind() == reflect.Ptr && b.Kind() == reflect.Ptr {
		// proto3 optional message → *T(映射表):*gen.T vs *dto.T 递归到 struct 等价
		return compatibleTypeDepth(a.Elem(), b.Elem(), depth+1)
	}
	if a.Kind() == reflect.Map && b.Kind() == reflect.Map {
		return compatibleTypeDepth(a.Key(), b.Key(), depth+1) && compatibleTypeDepth(a.Elem(), b.Elem(), depth+1)
	}
	if a.Kind() == reflect.Struct && b.Kind() == reflect.Struct {
		// 防御递归类型(如链表)导致的栈溢出;迁移契约无自引用,深度封顶 8
		if depth >= 8 {
			return false
		}
		return compatibleStruct(a, b, depth)
	}
	return false
}

// compatibleStruct 比较两个 struct 类型的字段集:同名且类型兼容
// (按名查找,不依赖字段声明顺序)。字段 json/binding tag 由对偶各自的
// 顶层 compareStructs 覆盖(嵌套 struct 的字段同时是另一条对偶的顶层字段)。
func compatibleStruct(a, b reflect.Type, depth int) bool {
	if a.NumField() != b.NumField() {
		return false
	}
	bfields := make(map[string]reflect.Type, b.NumField())
	for i := 0; i < b.NumField(); i++ {
		f := b.Field(i)
		bfields[f.Name] = f.Type
	}
	for i := 0; i < a.NumField(); i++ {
		af := a.Field(i)
		bt, ok := bfields[af.Name]
		if !ok || !compatibleTypeDepth(af.Type, bt, depth+1) {
			return false
		}
	}
	return true
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
	// --exclude-dir=gen:守卫只扫手写 dto 包,生成物(gen/ 子目录)与手写同格式,
	// 纳入会假阳性自堵迁移。
	out, err := exec.Command("grep", "-rn", "--exclude-dir=gen", "-E", pattern, "../").CombinedOutput()
	if err == nil {
		t.Errorf("dto package still contains migrated structs:\n%s", out)
		return
	}
	// fail-closed:exit 1 = 无命中(通过);exit 0 = 命中(违规,上方已报);
	// 其余退出码(如 2,grep 出错/路径错)也必须暴露,禁止当通过。
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return
	}
	t.Errorf("grep error: %v\n%s", err, out)
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

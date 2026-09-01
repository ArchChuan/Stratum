package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
)

// 编译断言：port.ToolObservation 是 domain.ToolObservation 的真实别名（非定义
// 类型），二者可互赋值，现有 evalport.ToolObservation{...} 复合字面量与
// []evalport.ToolObservation 切片照常编译。放在外部测试包（domain_test）以
// 避免 domain(test) → port → domain 的导入环。
var _ port.ToolObservation = domain.ToolObservation{}

// TestToolObservationAliasCompat 进一步锁定别名互操作性：两个名称指向同一
// 类型，字面量与切片在两侧都能构造。
func TestToolObservationAliasCompat(t *testing.T) {
	byPort := port.ToolObservation{ToolName: "read"}
	byDomain := domain.ToolObservation{ToolName: "read"}

	if byPort.ToolName != "read" || byDomain.ToolName != "read" {
		t.Fatalf("composite literals not interchangeable: port=%+v domain=%+v", byPort, byDomain)
	}

	_ = []port.ToolObservation{domain.ToolObservation{ToolName: "a"}}
	_ = []domain.ToolObservation{port.ToolObservation{ToolName: "b"}}

	var portSlice []port.ToolObservation
	portSlice = append(portSlice, domain.ToolObservation{StepIndex: 1})
	if len(portSlice) != 1 {
		t.Fatal("domain value did not append into port slice")
	}

	// JSON 契约跟随 domain 结构体上的 tag：非 omitempty 字段（tool_type /
	// provider_type / capability_id）始终出现，与迁移前 port 定义一致。
	raw, err := json.Marshal(domain.ToolObservation{ToolName: "search", StepIndex: 2})
	if err != nil {
		t.Fatalf("marshal tool observation: %v", err)
	}
	want := `{"tool_name":"search","tool_type":"","step_index":2,"provider_type":"","capability_id":""}`
	if string(raw) != want {
		t.Fatalf("unexpected JSON projection: %s", raw)
	}
}

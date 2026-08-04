package domain

import (
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestAppendUniqueCitations(t *testing.T) {
	c1 := Citation{DocumentID: "d1", Section: "s", URL: "u"}
	// 极端情况：重复引用（同 doc/section/url）只保留一次。
	got := appendUniqueCitations(nil, c1, c1)
	if len(got) != 1 {
		t.Fatalf("duplicate citation kept: %d", len(got))
	}
	// 不同 section 视为不同引用。
	c1b := Citation{DocumentID: "d1", Section: "other", URL: "u"}
	got = appendUniqueCitations(got, c1b)
	if len(got) != 2 {
		t.Errorf("distinct section must be kept: %d", len(got))
	}
}

func TestAppendUniqueCitationsCap(t *testing.T) {
	// 极端情况：达到上限后丢弃多余引用。
	dst := make([]Citation, constants.SystemAssistantCitationMaxCount)
	got := appendUniqueCitations(dst, Citation{DocumentID: "overflow"})
	if len(got) != constants.SystemAssistantCitationMaxCount {
		t.Errorf("cap violated: %d", len(got))
	}
}

func TestAppendUniqueFacts(t *testing.T) {
	f1 := DiagnosticFact{Area: DiagnosticAreaAgent, ObjectID: "o1", Statement: "stmt", Source: "src"}
	// 重复事实去重。
	got := appendUniqueFacts(nil, f1, f1)
	if len(got) != 1 {
		t.Fatalf("duplicate fact kept: %d", len(got))
	}
	// 同 area 不同 object 保留。
	got = appendUniqueFacts(got, DiagnosticFact{Area: DiagnosticAreaAgent, ObjectID: "o2", Statement: "stmt", Source: "src"})
	if len(got) != 2 {
		t.Errorf("distinct fact lost: %d", len(got))
	}
}

func TestAppendUniqueGaps(t *testing.T) {
	g1 := EvidenceGap{Area: DiagnosticAreaModel, Source: "src", Code: "E1"}
	// 重复 gap 去重。
	got := appendUniqueGaps(nil, g1, g1)
	if len(got) != 1 {
		t.Fatalf("duplicate gap kept: %d", len(got))
	}
	// 不同 code 保留。
	got = appendUniqueGaps(got, EvidenceGap{Area: DiagnosticAreaModel, Source: "src", Code: "E2"})
	if len(got) != 2 {
		t.Errorf("distinct gap lost: %d", len(got))
	}
}

func TestAppendUniqueGapsCap(t *testing.T) {
	dst := make([]EvidenceGap, constants.SystemAssistantDiagnosticGapsMaxCount)
	got := appendUniqueGaps(dst, EvidenceGap{Area: DiagnosticAreaModel, Code: "overflow"})
	if len(got) != constants.SystemAssistantDiagnosticGapsMaxCount {
		t.Errorf("cap violated: %d", len(got))
	}
}

func TestDiagnosticAreaValid(t *testing.T) {
	valid := []DiagnosticArea{DiagnosticAreaAgent, DiagnosticAreaSkill, DiagnosticAreaMCP, DiagnosticAreaKnowledge, DiagnosticAreaModel}
	for _, a := range valid {
		if !a.Valid() {
			t.Errorf("%s must be valid", a)
		}
	}
	if (DiagnosticArea("bogus")).Valid() {
		t.Error("bogus area must be invalid")
	}
	if (DiagnosticArea("")).Valid() {
		t.Error("empty area must be invalid")
	}
}

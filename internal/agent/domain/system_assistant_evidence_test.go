package domain

import (
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestRequiresApproval(t *testing.T) {
	cases := []struct {
		level ToolRiskLevel
		want  bool
	}{
		{ToolRiskRead, false},
		{ToolRiskWriteReversible, false},
		{ToolRiskDestructive, true},
		{ToolRiskUnclassified, true},
		{ToolRiskLevel("unknown"), false},
	}
	for _, tc := range cases {
		t.Run(string(tc.level), func(t *testing.T) {
			if got := tc.level.RequiresApproval(); got != tc.want {
				t.Errorf("RequiresApproval(%q) = %v, want %v", tc.level, got, tc.want)
			}
		})
	}
}

func TestBoundCitationsCapsAndRedacts(t *testing.T) {
	// 极端情况：超过上限的 citations 截断，敏感字段脱敏，字段长度受限。
	var in []Citation
	for i := 0; i < constants.SystemAssistantCitationMaxCount+5; i++ {
		in = append(in, Citation{
			DocumentID:     "doc-id",
			Title:          strings.Repeat("t", constants.SystemAssistantEvidenceFieldMaxRunes+50),
			ProductVersion: "v1",
			Section:        "s",
			URL:            "https://example.com?api_key=SECRET",
			Excerpt:        "excerpt",
		})
	}
	out := BoundCitations(in)
	if len(out) != constants.SystemAssistantCitationMaxCount {
		t.Errorf("len = %d, want cap %d", len(out), constants.SystemAssistantCitationMaxCount)
	}
	if len([]rune(out[0].Title)) > constants.SystemAssistantEvidenceFieldMaxRunes {
		t.Errorf("title not bounded: %d runes", len([]rune(out[0].Title)))
	}
	if strings.Contains(out[0].URL, "SECRET") {
		t.Error("credential must be redacted from URL")
	}
}

func TestBoundCitationsEmptyAndShort(t *testing.T) {
	// 极端情况：空输入和短输入原样返回。
	if out := BoundCitations(nil); len(out) != 0 {
		t.Errorf("nil input must yield empty output")
	}
	in := []Citation{{DocumentID: "d"}}
	out := BoundCitations(in)
	if len(out) != 1 || out[0].DocumentID != "d" {
		t.Errorf("short input must pass through: %+v", out)
	}
}

func TestBoundDiagnosticEvidenceCaps(t *testing.T) {
	// 极端情况：facts/gaps/areaResults 各自截断，SubjectUserID 必须清空。
	ev := DiagnosticEvidence{
		Facts:       make([]DiagnosticFact, constants.SystemAssistantDiagnosticFactsMaxCount+3),
		Gaps:        make([]EvidenceGap, constants.SystemAssistantDiagnosticGapsMaxCount+3),
		AreaResults: make([]DiagnosticAreaResult, constants.SystemAssistantDiagnosticAreaResultsMaxCount+3),
	}
	for i := range ev.Facts {
		ev.Facts[i] = DiagnosticFact{ObjectID: "o", Statement: "stmt", Source: "src", SubjectUserID: "user-secret"}
	}
	out := BoundDiagnosticEvidence(ev)
	if len(out.Facts) != constants.SystemAssistantDiagnosticFactsMaxCount {
		t.Errorf("facts len = %d, want %d", len(out.Facts), constants.SystemAssistantDiagnosticFactsMaxCount)
	}
	if len(out.Gaps) != constants.SystemAssistantDiagnosticGapsMaxCount {
		t.Errorf("gaps len = %d, want %d", len(out.Gaps), constants.SystemAssistantDiagnosticGapsMaxCount)
	}
	if len(out.AreaResults) != constants.SystemAssistantDiagnosticAreaResultsMaxCount {
		t.Errorf("area results len = %d, want %d", len(out.AreaResults), constants.SystemAssistantDiagnosticAreaResultsMaxCount)
	}
	if out.Facts[0].SubjectUserID != "" {
		t.Error("SubjectUserID must be cleared")
	}
}

func TestBoundDiagnosticEvidenceZeroValue(t *testing.T) {
	// 极端情况：零值 evidence 不 panic，字段原样传递。
	out := BoundDiagnosticEvidence(DiagnosticEvidence{})
	if out.Facts != nil || out.Gaps != nil || out.AreaResults != nil {
		t.Errorf("zero value must stay zero: %+v", out)
	}
}

func TestBoundEvidenceStringRedactsAndBounds(t *testing.T) {
	// 直接验证底层字符串边界函数：脱敏 + rune 截断。
	if got := boundEvidenceString("password=hunter2"); strings.Contains(got, "hunter2") {
		t.Error("credential must be redacted")
	}
	long := strings.Repeat("界", constants.SystemAssistantEvidenceFieldMaxRunes+10)
	got := boundEvidenceString(long)
	if len([]rune(got)) != constants.SystemAssistantEvidenceFieldMaxRunes {
		t.Errorf("rune bound = %d, want %d", len([]rune(got)), constants.SystemAssistantEvidenceFieldMaxRunes)
	}
	if got := boundEvidenceString(""); got != "" {
		t.Errorf("empty must stay empty")
	}
}

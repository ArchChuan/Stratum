package factcheck

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/stretchr/testify/require"
)

// P3 幻觉校验 judge 解析：剥 code fence + 三 verdict 固定枚举 + 容错空白。
// judge 走 json_object（P1 A 层）时无 code fence，但少数 provider 可能返回
// markdown 包裹——解析必须容错，失败按「不校验」降级（nil），不阻塞执行。
func TestParseClaimVerdicts(t *testing.T) {
	t.Run("plain json three verdicts", func(t *testing.T) {
		verdicts, err := ParseClaimVerdicts(`{"claims":[
			{"text":"A","verdict":"SUPPORTED","risk":0},
			{"text":"B","verdict":"CONTRADICTED","risk":4},
			{"text":"C","verdict":"UNSUPPORTED","risk":2}
		]}`)
		require.NoError(t, err)
		require.Len(t, verdicts, 3)
		require.Equal(t, domain.ClaimVerdict{Text: "A", Verdict: VerdictSupported, Risk: 0}, verdicts[0])
		require.Equal(t, domain.ClaimVerdict{Text: "B", Verdict: VerdictContradicted, Risk: 4}, verdicts[1])
		require.Equal(t, domain.ClaimVerdict{Text: "C", Verdict: VerdictUnsupported, Risk: 2}, verdicts[2])
	})

	t.Run("code fence stripped", func(t *testing.T) {
		verdicts, err := ParseClaimVerdicts("```json\n{\"claims\":[{\"text\":\"X\",\"verdict\":\"SUPPORTED\",\"risk\":1}]}\n```")
		require.NoError(t, err)
		require.Len(t, verdicts, 1)
		require.Equal(t, "X", verdicts[0].Text)
	})

	t.Run("empty claims becomes empty slice", func(t *testing.T) {
		verdicts, err := ParseClaimVerdicts(`{"claims":null}`)
		require.NoError(t, err)
		require.Empty(t, verdicts)
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		_, err := ParseClaimVerdicts(`{"claims": not-json}`)
		require.Error(t, err)
	})
}

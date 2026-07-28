package alerting

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderCardUsesAllowlistedFieldsAndStatus(t *testing.T) {
	group := AlertGroup{
		Status: "firing",
		CommonLabels: map[string]string{
			"alertname": "StratumPublicEndpointDown",
			"severity":  "critical",
			"token":     "must-not-leak",
		},
		CommonAnnotations: map[string]string{
			"summary":     "远端入口不可用",
			"runbook_url": "https://example.invalid/runbook",
			"secret":      "must-not-leak",
		},
		Alerts: []Alert{{Status: "firing", Labels: map[string]string{"service": "stratum"}}},
	}

	card, err := RenderCard(group)
	require.NoError(t, err)
	require.Equal(t, "interactive", card.MsgType)
	require.Contains(t, card.Card.Header.Title.Content, "FIRING")
	rendered := mustJSON(t, card)
	require.Contains(t, rendered, "StratumPublicEndpointDown")
	require.Contains(t, rendered, "远端入口不可用")
	require.NotContains(t, rendered, "must-not-leak")
}

func TestRenderCardUsesResolvedStatusAndGreenHeader(t *testing.T) {
	card, err := RenderCard(AlertGroup{
		Status: "resolved",
		CommonLabels: map[string]string{
			"alertname": "StratumPublicEndpointDown",
		},
	})

	require.NoError(t, err)
	require.Contains(t, card.Card.Header.Title.Content, "RESOLVED")
	require.Equal(t, "green", card.Card.Header.Template)
}

func TestRenderCardRejectsUnsupportedStatus(t *testing.T) {
	_, err := RenderCard(AlertGroup{Status: "unknown"})
	require.ErrorIs(t, err, ErrInvalidAlertGroup)
}

func TestRenderCardTruncatesAndEscapesFields(t *testing.T) {
	card, err := RenderCard(AlertGroup{
		Status: "firing",
		CommonAnnotations: map[string]string{
			"description": "<script>**pwn**" + strings.Repeat("x", maxFieldRunes+50),
		},
	})

	require.NoError(t, err)
	rendered := mustJSON(t, card)
	require.NotContains(t, rendered, "<script>")
	require.NotContains(t, rendered, "**pwn**")
	require.LessOrEqual(t, len([]rune(rendered)), maxFieldRunes+500)
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return string(b)
}

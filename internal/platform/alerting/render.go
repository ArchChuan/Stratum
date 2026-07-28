package alerting

import (
	"fmt"
	"html"
	"strings"
)

var allowedLabelKeys = []string{"alertname", "severity", "service", "environment", "component", "instance"}

var allowedAnnotationKeys = []string{"summary", "description", "dashboard_url", "runbook_url"}

var larkMarkdownEscaper = strings.NewReplacer(
	"\\", "\\\\",
	"*", "\\*",
	"_", "\\_",
	"~", "\\~",
	"`", "\\`",
	"[", "\\[",
	"]", "\\]",
)

func RenderCard(group AlertGroup) (FeishuMessage, error) {
	template, status, err := statusPresentation(group.Status)
	if err != nil {
		return FeishuMessage{}, err
	}

	alertName := cleanField(group.CommonLabels["alertname"])
	if alertName == "" {
		alertName = "Stratum alert"
	}
	title := fmt.Sprintf("[%s] %s", status, alertName)

	lines := renderFields(group.CommonLabels, group.CommonAnnotations)
	for index, alert := range group.Alerts {
		if index >= maxAlertsPerMessage {
			lines = append(lines, fmt.Sprintf("- 其余 %d 条告警已省略", len(group.Alerts)-index))
			break
		}
		fields := renderFields(alert.Labels, alert.Annotations)
		if len(fields) > 0 {
			lines = append(lines, fmt.Sprintf("**告警 %d**", index+1))
			lines = append(lines, fields...)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "- 无可展示的告警字段")
	}

	return FeishuMessage{
		MsgType: "interactive",
		Card: FeishuCard{
			Header: FeishuCardHeader{
				Template: template,
				Title:    FeishuCardText{Tag: "plain_text", Content: title},
			},
			Elements: []FeishuCardElement{{
				Tag:  "div",
				Text: FeishuCardText{Tag: "lark_md", Content: strings.Join(lines, "\n")},
			}},
		},
	}, nil
}

func statusPresentation(status string) (string, string, error) {
	switch status {
	case "firing":
		return "red", "FIRING", nil
	case "resolved":
		return "green", "RESOLVED", nil
	default:
		return "", "", ErrInvalidAlertGroup
	}
}

func renderFields(labels, annotations map[string]string) []string {
	lines := make([]string, 0, len(allowedLabelKeys)+len(allowedAnnotationKeys))
	for _, key := range allowedLabelKeys {
		if value := cleanField(labels[key]); value != "" {
			lines = append(lines, fmt.Sprintf("- **%s**: %s", key, value))
		}
	}
	for _, key := range allowedAnnotationKeys {
		if value := cleanField(annotations[key]); value != "" {
			lines = append(lines, fmt.Sprintf("- **%s**: %s", key, value))
		}
	}
	return lines
}

func cleanField(value string) string {
	runes := []rune(value)
	if len(runes) > maxFieldRunes {
		runes = append(runes[:maxFieldRunes-3], '.', '.', '.')
	}
	return larkMarkdownEscaper.Replace(html.EscapeString(string(runes)))
}

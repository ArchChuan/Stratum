package alerting

import (
	"errors"
	"time"
)

var ErrInvalidAlertGroup = errors.New("invalid alert group")

const (
	maxAlertsPerMessage = 20
	maxFieldRunes       = 500
)

type AlertGroup struct {
	Status            string            `json:"status"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	Alerts            []Alert           `json:"alerts"`
}

type Alert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
}

type FeishuMessage struct {
	MsgType string     `json:"msg_type"`
	Card    FeishuCard `json:"card"`
}

type FeishuCard struct {
	Header   FeishuCardHeader    `json:"header"`
	Elements []FeishuCardElement `json:"elements"`
}

type FeishuCardHeader struct {
	Template string         `json:"template"`
	Title    FeishuCardText `json:"title"`
}

type FeishuCardElement struct {
	Tag  string         `json:"tag"`
	Text FeishuCardText `json:"text"`
}

type FeishuCardText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "valid", value: "https://open.feishu.cn/open-apis/bot/v2/hook/redacted"},
		{name: "empty", wantErr: true},
		{name: "plaintext", value: "http://open.feishu.cn/hook", wantErr: true},
		{name: "userinfo", value: "https://user:password@open.feishu.cn/hook", wantErr: true},
		{name: "fragment", value: "https://open.feishu.cn/hook#secret", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWebhookURL(tt.value)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

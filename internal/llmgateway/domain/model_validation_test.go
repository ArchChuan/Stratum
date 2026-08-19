package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int64) *int64       { return &v }

func TestValidateMaxTemperature(t *testing.T) {
	cases := []struct {
		name string
		v    *float64
		want string // 期望错误子串；空=通过
	}{
		{name: "nil allowed", v: nil, want: ""},
		{name: "zero allowed", v: floatPtr(0), want: ""},
		{name: "one allowed", v: floatPtr(1), want: ""},
		{name: "mid range", v: floatPtr(0.7), want: ""},
		{name: "negative rejected", v: floatPtr(-0.1), want: "out of range"},
		{name: "above one rejected", v: floatPtr(1.5), want: "out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMaxTemperature(tc.v)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestValidateSamplingWrite(t *testing.T) {
	cases := []struct {
		name    string
		p       *SamplingParams
		maxTemp *float64
		want    string
	}{
		{name: "nil params ok", p: nil, maxTemp: nil, want: ""},
		{name: "temperature in range", p: &SamplingParams{Temperature: floatPtr(0.7)}, maxTemp: nil, want: ""},
		{name: "temperature too high", p: &SamplingParams{Temperature: floatPtr(1.5)}, maxTemp: nil, want: "temperature"},
		{name: "top_p too high", p: &SamplingParams{TopP: floatPtr(1.1)}, maxTemp: nil, want: "top_p"},
		{name: "frequency penalty out of range", p: &SamplingParams{FrequencyPenalty: floatPtr(2.5)}, maxTemp: nil, want: "frequency_penalty"},
		{name: "presence penalty out of range", p: &SamplingParams{PresencePenalty: floatPtr(-2.5)}, maxTemp: nil, want: "presence_penalty"},
		{name: "temperature exceeds max_temperature", p: &SamplingParams{Temperature: floatPtr(0.8)}, maxTemp: floatPtr(0.5), want: "max_temperature"},
		{name: "temperature equals max_temperature ok", p: &SamplingParams{Temperature: floatPtr(0.5)}, maxTemp: floatPtr(0.5), want: ""},
		{name: "max_temperature zero forbids temperature", p: &SamplingParams{Temperature: floatPtr(0.1)}, maxTemp: floatPtr(0), want: "not support"},
		{name: "max_temperature zero without temperature ok", p: &SamplingParams{TopP: floatPtr(0.9)}, maxTemp: floatPtr(0), want: ""},
		{name: "seed negative ok", p: &SamplingParams{Seed: intPtr(-1)}, maxTemp: nil, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSamplingWrite(tc.p, tc.maxTemp)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}

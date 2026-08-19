package infrastructure

import (
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// encodeSamplingParams 序列化模型采样参数：nil → '{}'（NOT NULL 约束），
// 保证 UPDATE 不会写入 NULL 违反约束。
func encodeSamplingParams(p *domain.SamplingParams) (string, error) {
	if p == nil {
		return "{}", nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal sampling_params: %w", err)
	}
	return string(b), nil
}

// decodeSamplingParams 反序列化 JSONB 采样参数：'{}'/NULL → nil（未配置）。
func decodeSamplingParams(raw []byte) (*domain.SamplingParams, error) {
	if len(raw) == 0 || string(raw) == "{}" {
		return nil, nil
	}
	var p domain.SamplingParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("unmarshal sampling_params: %w", err)
	}
	return &p, nil
}

// encodeJSONMap 序列化 provider JSONB map 字段（default_sampling）：
// nil → '{}'（NOT NULL 约束）。
func encodeJSONMap(m map[string]any) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal json map: %w", err)
	}
	return string(b), nil
}

// encodeStringMap 序列化 provider JSONB 字符串 map（extra_headers）：
// nil → '{}'（NOT NULL 约束）。
func encodeStringMap(m map[string]string) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal string map: %w", err)
	}
	return string(b), nil
}

// decodeStringMap 反序列化 JSONB 键值 map：'{}'/NULL → nil。
func decodeStringMap(raw []byte) (map[string]string, error) {
	if len(raw) == 0 || string(raw) == "{}" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("unmarshal string map: %w", err)
	}
	return m, nil
}

// decodeJSONMap 反序列化 JSONB 任意值 map（provider default_sampling）：
// '{}'/NULL → nil。
func decodeJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "{}" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("unmarshal json map: %w", err)
	}
	return m, nil
}

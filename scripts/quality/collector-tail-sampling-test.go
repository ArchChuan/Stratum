//go:build ignore

package main

// 校验三份 OTEL Collector 配置的 tail_sampling 基础采样策略语义一致，防止演进漂移。
//
// 三份配置（本地 docker / k8s tracing / k8s opik）是独立环境的独立产物，不做全量模板合一；
// tail_sampling 的 6 个基础策略（evaluation/experiment/security/error/slow/default）是唯一
// 必须三处同步的内容。chat-always 为 k8s 特有，不属于三处共享基础集合，故排除。
// 归一化采用 json.Marshal(map)，键按字典序稳定输出，容忍缩进与字段顺序差异。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// basePolicyExclude 是三处共享基础集合之外的 k8s 特有策略。
const basePolicyExclude = "chat-always"

type collectorConfig struct {
	Processors processors `yaml:"processors"`
}

type processors struct {
	TailSampling *tailSampling `yaml:"tail_sampling"`
}

type tailSampling struct {
	Policies []map[string]any `yaml:"policies"`
}

type source struct {
	label string
	path  string
}

func loadLocalConfig(path string) (collectorConfig, error) {
	var cfg collectorConfig
	blob, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(blob, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// extractConfigMapData 从 k8s 清单（多文档 YAML）中提取指定 ConfigMap 的 data.config.yaml。
// yaml.v3 的 Unmarshal 不接受多文档流，必须用 Decoder 逐文档解码。
func extractConfigMapData(path, name string) (string, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dec := yaml.NewDecoder(bytes.NewReader(blob))
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("%s: %w", path, err)
		}
		if kind, _ := doc["kind"].(string); kind != "ConfigMap" {
			continue
		}
		meta, _ := doc["metadata"].(map[string]any)
		metaName, _ := meta["name"].(string)
		if metaName != name {
			continue
		}
		data, _ := doc["data"].(map[string]any)
		cfg, _ := data["config.yaml"].(string)
		return cfg, nil
	}
	return "", fmt.Errorf("ConfigMap %q not found in %s", name, path)
}

// basePolicies 提取 tail_sampling 基础策略并归一化（去 chat-always、按名称排序、JSON 稳定序列化）。
func basePolicies(cfg collectorConfig) ([]string, error) {
	ts := cfg.Processors.TailSampling
	if ts == nil {
		return nil, fmt.Errorf("tail_sampling processor not found")
	}
	var normalized []string
	for _, p := range ts.Policies {
		if policyName, _ := p["name"].(string); policyName == basePolicyExclude {
			continue
		}
		blob, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, string(blob))
	}
	sort.Strings(normalized)
	return normalized, nil
}

func loadBasePolicies(s source) ([]string, error) {
	if strings.Contains(s.path, "/k8s/") || strings.HasPrefix(s.path, "k8s/") {
		cfgName := "otel-collector-config"
		if strings.Contains(s.path, "opik") {
			cfgName = "opik-otel-collector-config"
		}
		inner, err := extractConfigMapData(s.path, cfgName)
		if err != nil {
			return nil, err
		}
		var cfg collectorConfig
		if err := yaml.Unmarshal([]byte(inner), &cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", s.path, err)
		}
		return basePolicies(cfg)
	}
	cfg, err := loadLocalConfig(s.path)
	if err != nil {
		return nil, err
	}
	return basePolicies(cfg)
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "usage: %s <local-config> <k8s-tracing> <k8s-opik>\n", os.Args[0])
		os.Exit(2)
	}
	sources := []source{
		{label: "local", path: os.Args[1]},
		{label: "k8s-tracing", path: os.Args[2]},
		{label: "k8s-opik", path: os.Args[3]},
	}
	policies := make(map[string][]string, len(sources))
	for _, s := range sources {
		ps, err := loadBasePolicies(s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s (%s): %v\n", s.label, s.path, err)
			os.Exit(1)
		}
		policies[s.label] = ps
	}
	base := policies["local"]
	for _, s := range sources[1:] {
		if !reflect.DeepEqual(base, policies[s.label]) {
			fmt.Fprintf(os.Stderr, "tail_sampling base policies drifted: local != %s\n", s.label)
			for _, src := range sources {
				fmt.Fprintf(os.Stderr, "--- %s (%s) ---\n%s\n", src.label, src.path, strings.Join(policies[src.label], "\n"))
			}
			os.Exit(1)
		}
	}
	fmt.Printf("tail_sampling base policies identical across %d sources (%d policies each)\n", len(sources), len(base))
}

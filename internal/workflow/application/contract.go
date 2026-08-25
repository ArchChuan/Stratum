package application

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
)

// referencedOutputKeys 静态收集 spec 中每个节点被下游以 nodes.<up>.output.<key>
// 引用到的输出字段。字段级引用驱动输出契约注入（见 injectOutputContract）。
func referencedOutputKeys(spec domain.Spec) map[string][]string {
	refs := make(map[string][]string)
	seen := make(map[string]map[string]struct{})
	for _, node := range spec.Nodes {
		for _, reference := range node.InputMapping {
			parts := strings.Split(reference, ".")
			if len(parts) < 4 || parts[0] != "nodes" || parts[2] != "output" {
				continue
			}
			upstream, key := parts[1], parts[3]
			if seen[upstream] == nil {
				seen[upstream] = make(map[string]struct{})
			}
			if _, ok := seen[upstream][key]; ok {
				continue
			}
			seen[upstream][key] = struct{}{}
			refs[upstream] = append(refs[upstream], key)
		}
	}
	return refs
}

// injectOutputContract 当下游以 nodes.<up>.output.<key> 引用本节点输出字段时，
// 在输入提示词末尾追加 JSON 输出格式要求，提高字段级引用解析成功率。无 keyed
// 引用时原样返回 input，不影响存量运行。
func injectOutputContract(refs map[string][]string, node domain.Node, input string) string {
	keys := refs[node.ID]
	if len(keys) == 0 {
		return input
	}
	fields := append([]string(nil), keys...)
	sort.Strings(fields)
	requirement := "请以合法 JSON 对象输出以下字段：" + strings.Join(fields, ", ") + "。只输出 JSON，不要额外解释或包裹 markdown 代码块。"
	if input == "" {
		return "[系统要求] " + requirement
	}
	if !strings.HasSuffix(input, "\n") {
		input += "\n"
	}
	return input + "[系统要求] " + requirement
}

// extractOutputField 从上游 OutputSummary（应为 JSON 对象）中提取字段 key。
// 上游输出非 JSON 或缺字段时返回可解释的错误，由调用方触发上游重试。
func extractOutputField(summary, key string) (any, error) {
	var object map[string]any
	if err := json.Unmarshal([]byte(summary), &object); err != nil {
		return nil, fmt.Errorf("upstream output is not a valid JSON object: %w", err)
	}
	value, ok := object[key]
	if !ok {
		return nil, fmt.Errorf("upstream output lacks field %q", key)
	}
	return value, nil
}

// nodeByID 返回 spec 中指定 id 的节点。
func nodeByID(spec domain.Spec, id string) (domain.Node, bool) {
	for _, node := range spec.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return domain.Node{}, false
}

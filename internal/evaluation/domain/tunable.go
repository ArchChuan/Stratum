package domain

import "fmt"

// TunableCategory groups tunable parameters by domain area.
type TunableCategory string

const (
	CatModelConfig   TunableCategory = "model_config"
	CatContextMemory TunableCategory = "context_memory"
	CatToolExec      TunableCategory = "tool_exec"
	CatRAG           TunableCategory = "rag"
	CatPlanning      TunableCategory = "planning"
	CatCompaction    TunableCategory = "compaction"
	CatPrompt        TunableCategory = "prompt"
)

// AllTunableCategories is the ordered list of all categories, used for stable
// iteration in registries, UI rendering, and serialization.
var AllTunableCategories = []TunableCategory{
	CatModelConfig,
	CatContextMemory,
	CatToolExec,
	CatRAG,
	CatPlanning,
	CatCompaction,
	CatPrompt,
}

// ResourceTunableCategories maps a ResourceKind to the categories relevant to
// that resource. Resources not explicitly listed default to model_config only.
var ResourceTunableCategories = map[ResourceKind][]TunableCategory{
	ResourceKindAgent: {
		CatModelConfig, CatContextMemory, CatToolExec,
		CatRAG, CatPlanning, CatCompaction, CatPrompt,
	},
}

// VisualHint describes how a tunable should be rendered in a UI.
type VisualHint struct {
	Control string `json:"control"` // slider | select | toggle | range | textarea
	Min     any    `json:"min,omitempty"`
	Max     any    `json:"max,omitempty"`
	Step    any    `json:"step,omitempty"`
	Options []any  `json:"options,omitempty"`
	Unit    string `json:"unit,omitempty"` // tokens | ms | % | ""
}

// SearchRange defines the valid search/optimization space for a tunable.
type SearchRange struct {
	Discrete []any   `json:"discrete,omitempty"`
	Min      float64 `json:"min,omitempty"`
	Max      float64 `json:"max,omitempty"`
	Step     float64 `json:"step,omitempty"`
}

// Tunable is a single optimizable parameter. Each implementation corresponds to
// one key in an AgentRevision or other resource snapshot. The interface is the
// extension point: adding a new parameter means implementing Tunable and
// registering it — no pipeline changes needed.
type Tunable interface {
	Key() string
	DisplayName() string
	Category() TunableCategory
	DefaultValue() any
	VisualHint() VisualHint

	// Read extracts the current value from a resource snapshot.
	Read(resource map[string]any) (any, error)
	// Write applies a candidate value into the resource snapshot (mutates).
	Write(resource map[string]any, value any) error
	// Validate checks whether value is within the allowed range/set.
	Validate(value any) error
	// SearchSpace returns the optimization grid for grid/random searches.
	SearchSpace() SearchRange
}

// baseTunable provides default no-op search space for tunables
// whose search space is LLM-driven (e.g. prompts).
type baseTunable struct{}

func (b baseTunable) SearchSpace() SearchRange { return SearchRange{} }

// ——— Model config tunables ———

type temperatureTunable struct{}

func (t temperatureTunable) Key() string               { return "temperature" }
func (t temperatureTunable) DisplayName() string       { return "温度" }
func (t temperatureTunable) Category() TunableCategory { return CatModelConfig }
func (t temperatureTunable) DefaultValue() any         { return 0.7 }
func (t temperatureTunable) VisualHint() VisualHint {
	return VisualHint{Control: "slider", Min: 0.0, Max: 2.0, Step: 0.1, Unit: ""}
}
func (t temperatureTunable) Read(resource map[string]any) (any, error) {
	params, _ := resource["model_parameters"].(map[string]any)
	if params == nil {
		return 0.7, nil
	}
	v, ok := params["temperature"]
	if !ok {
		return 0.7, nil
	}
	return v, nil
}
func (t temperatureTunable) Write(resource map[string]any, value any) error {
	v, ok := value.(float64)
	if !ok {
		return fmt.Errorf("temperature: expected float64, got %T", value)
	}
	params, _ := resource["model_parameters"].(map[string]any)
	if params == nil {
		params = map[string]any{}
		resource["model_parameters"] = params
	}
	params["temperature"] = v
	return nil
}
func (t temperatureTunable) Validate(value any) error {
	v, ok := value.(float64)
	if !ok {
		return fmt.Errorf("temperature: expected float64")
	}
	if v < 0 || v > 2 {
		return fmt.Errorf("temperature: must be in [0, 2]")
	}
	return nil
}
func (t temperatureTunable) SearchSpace() SearchRange {
	return SearchRange{Min: 0, Max: 2, Step: 0.1}
}

type maxTokensTunable struct{}

func (t maxTokensTunable) Key() string               { return "max_tokens" }
func (t maxTokensTunable) DisplayName() string       { return "最大 Token 数" }
func (t maxTokensTunable) Category() TunableCategory { return CatModelConfig }
func (t maxTokensTunable) DefaultValue() any         { return 4096 }
func (t maxTokensTunable) VisualHint() VisualHint {
	return VisualHint{Control: "slider", Min: 256.0, Max: 131072.0, Step: 256.0, Unit: "tokens"}
}
func (t maxTokensTunable) Read(resource map[string]any) (any, error) {
	params, _ := resource["model_parameters"].(map[string]any)
	if params == nil {
		return 4096, nil
	}
	v, ok := params["max_tokens"]
	if !ok {
		return 4096, nil
	}
	return v, nil
}
func (t maxTokensTunable) Write(resource map[string]any, value any) error {
	v, ok := value.(float64)
	if !ok {
		return fmt.Errorf("max_tokens: expected float64, got %T", value)
	}
	params, _ := resource["model_parameters"].(map[string]any)
	if params == nil {
		params = map[string]any{}
		resource["model_parameters"] = params
	}
	params["max_tokens"] = v
	return nil
}
func (t maxTokensTunable) Validate(value any) error {
	v, ok := value.(float64)
	if !ok {
		return fmt.Errorf("max_tokens: expected float64")
	}
	if v < 256 || v > 131072 {
		return fmt.Errorf("max_tokens: must be in [256, 131072]")
	}
	return nil
}
func (t maxTokensTunable) SearchSpace() SearchRange {
	return SearchRange{Min: 256, Max: 131072, Step: 256}
}

// ——— Prompt tunable (LLM-rewritten, no grid search) ———

// promptTunable wraps a free-text prompt field. Search is LLM-driven, so
// SearchSpace returns empty. Validate only checks non-emptiness.
type promptTunable struct {
	baseTunable
	key         string
	displayName string
	fieldPath   string // dot-separated path in the resource map
}

func (p promptTunable) Key() string               { return p.key }
func (p promptTunable) DisplayName() string       { return p.displayName }
func (p promptTunable) Category() TunableCategory { return CatPrompt }
func (p promptTunable) DefaultValue() any         { return "" }
func (p promptTunable) VisualHint() VisualHint {
	return VisualHint{Control: "textarea"}
}
func (p promptTunable) Read(resource map[string]any) (any, error) {
	v, ok := resource[p.fieldPath]
	if !ok {
		return "", nil
	}
	s, _ := v.(string)
	return s, nil
}
func (p promptTunable) Write(resource map[string]any, value any) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s: expected string", p.key)
	}
	resource[p.fieldPath] = s
	return nil
}
func (p promptTunable) Validate(value any) error {
	s, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s: expected string", p.key)
	}
	if s == "" {
		return fmt.Errorf("%s: must not be empty", p.key)
	}
	return nil
}

// Prompt tunable keys.
const (
	TunableSystemPrompt           = "system_prompt"
	TunableMemoryExtractionPrompt = "memory_extraction_prompt"
	TunableMemorySummaryPrompt    = "memory_summary_prompt"
	TunableMemoryEnrichmentPrompt = "memory_enrichment_prompt"
	TunableCompactionPrompt       = "compaction_prompt"
)

package domain

// TunableRegistry centralizes all tunable parameter definitions indexed by key
// and category. It is the single source of truth for what can be optimized.
type TunableRegistry struct {
	byKey      map[string]Tunable
	byCategory map[TunableCategory][]Tunable
}

// NewTunableRegistry creates a registry pre-populated with all built-in
// tunables. Callers that need custom tunables can call Register afterwards.
func NewTunableRegistry() *TunableRegistry {
	r := &TunableRegistry{
		byKey:      make(map[string]Tunable),
		byCategory: make(map[TunableCategory][]Tunable),
	}
	r.registerModelConfig()
	r.registerContextAndCompaction()
	r.registerPrompts()
	return r
}

func (r *TunableRegistry) registerContextAndCompaction() {
	for _, t := range []Tunable{
		maxContextTokensTunable{},
	} {
		r.Register(t)
	}
}

func (r *TunableRegistry) registerModelConfig() {
	for _, t := range []Tunable{
		temperatureTunable{},
		maxTokensTunable{},
	} {
		r.Register(t)
	}
}

func (r *TunableRegistry) registerPrompts() {
	for _, t := range []Tunable{
		promptTunable{key: TunableSystemPrompt, displayName: "系统提示词", fieldPath: "system_prompt"},
		promptTunable{key: TunableMemoryExtractionPrompt, displayName: "记忆抽取提示词", fieldPath: "memory_extraction_prompt"},
		promptTunable{key: TunableMemorySummaryPrompt, displayName: "记忆摘要提示词", fieldPath: "memory_summary_prompt"},
		promptTunable{key: TunableMemoryEnrichmentPrompt, displayName: "记忆富化提示词", fieldPath: "memory_enrichment_prompt"},
	} {
		r.Register(t)
	}
}

// Register adds a tunable to the registry. Duplicate keys are silently
// overwritten (last-write-wins).
func (r *TunableRegistry) Register(t Tunable) {
	r.byKey[t.Key()] = t
	r.byCategory[t.Category()] = append(r.byCategory[t.Category()], t)
}

// Get returns the tunable for key, or nil.
func (r *TunableRegistry) Get(key string) Tunable {
	return r.byKey[key]
}

// ForResource returns all tunables relevant to a resource kind.
func (r *TunableRegistry) ForResource(kind ResourceKind) []Tunable {
	categories, ok := ResourceTunableCategories[kind]
	if !ok {
		categories = []TunableCategory{CatModelConfig}
	}
	var result []Tunable
	seen := make(map[string]struct{})
	for _, cat := range categories {
		for _, t := range r.byCategory[cat] {
			if _, exists := seen[t.Key()]; !exists {
				seen[t.Key()] = struct{}{}
				result = append(result, t)
			}
		}
	}
	return result
}

// Categories returns all categories that have at least one registered tunable.
func (r *TunableRegistry) Categories() []TunableCategory {
	cats := make([]TunableCategory, 0, len(AllTunableCategories))
	for _, cat := range AllTunableCategories {
		if len(r.byCategory[cat]) > 0 {
			cats = append(cats, cat)
		}
	}
	return cats
}

// ReadSnapshot reads the current value of every registered tunable from a
// resource snapshot map, returning a map of key → value.
func (r *TunableRegistry) ReadSnapshot(resource map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(r.byKey))
	for key, t := range r.byKey {
		v, err := t.Read(resource)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

// ApplyPatches applies a set of key→value patches to a resource snapshot,
// returning a structured list of what changed. The resource map is mutated.
// Unknown keys are silently skipped.
func (r *TunableRegistry) ApplyPatches(
	resource map[string]any, patches map[string]any,
) ([]TunableChange, error) {
	changes := make([]TunableChange, 0, len(patches))
	for key, newValue := range patches {
		t := r.Get(key)
		if t == nil {
			continue
		}
		oldValue, err := t.Read(resource)
		if err != nil {
			return nil, err
		}
		if err := t.Validate(newValue); err != nil {
			return nil, err
		}
		if err := t.Write(resource, newValue); err != nil {
			return nil, err
		}
		changes = append(changes, TunableChange{
			TunableKey:  key,
			Category:    t.Category(),
			DisplayName: t.DisplayName(),
			OldValue:    oldValue,
			NewValue:    newValue,
			Impact:      classifyImpact(t.Category(), oldValue, newValue),
			VisualHint:  t.VisualHint(),
		})
	}
	return changes, nil
}

func classifyImpact(cat TunableCategory, _, _ any) ChangeImpact {
	switch cat {
	case CatPrompt:
		return ImpactMajor
	case CatModelConfig:
		return ImpactMinor
	default:
		return ImpactModerate
	}
}

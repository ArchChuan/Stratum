package infrastructure

// modelSpec holds static capabilities for a well-known model.
type modelSpec struct {
	ContextWindow   int
	MaxOutputTokens int
}

// modelCatalog provides a static fallback for model capabilities when the
// provider API does not return context-window metadata (e.g. OpenAI-compat
// /models endpoint returns only ID).
//
// Sources: official provider docs, assessed 2025-01.
var modelCatalog = map[string]modelSpec{
	// ── OpenAI ──────────────────────────────────────────────────────
	"gpt-4o":                 {128_000, 16_384},
	"gpt-4o-mini":            {128_000, 16_384},
	"gpt-4o-2024-08-06":      {128_000, 16_384},
	"gpt-4o-2024-11-20":      {128_000, 16_384},
	"gpt-4-turbo":            {128_000, 4_096},
	"gpt-4-turbo-2024-04-09": {128_000, 4_096},
	"gpt-4":                  {8_192, 8_192},
	"gpt-4-32k":              {32_768, 4_096},
	"gpt-3.5-turbo":          {16_385, 4_096},
	"gpt-3.5-turbo-16k":      {16_385, 4_096},
	"gpt-4.1":                {1_047_576, 32_768},
	"gpt-4.1-mini":           {1_047_576, 32_768},
	"gpt-4.1-nano":           {1_047_576, 32_768},
	"o4-mini":                {200_000, 100_000},
	"o3":                     {200_000, 100_000},
	"o3-mini":                {200_000, 100_000},
	"o1":                     {200_000, 100_000},
	"o1-mini":                {128_000, 65_536},
	"o1-preview":             {128_000, 32_768},
	"chatgpt-4o-latest":      {128_000, 16_384},

	// ── Anthropic (as served through OpenAI-compat proxies) ────────
	"claude-sonnet-4-5":          {200_000, 16_384},
	"claude-opus-4-8":            {200_000, 16_384},
	"claude-haiku-4-5":           {200_000, 16_384},
	"claude-3-5-sonnet":          {200_000, 4_096},
	"claude-3-5-sonnet-20241022": {200_000, 8_192},
	"claude-3-5-haiku":           {200_000, 4_096},
	"claude-3-5-haiku-20241022":  {200_000, 8_192},
	"claude-3-opus":              {200_000, 4_096},
	"claude-3-sonnet":            {200_000, 4_096},
	"claude-3-haiku":             {200_000, 4_096},

	// ── Qwen (阿里通义千问) ─────────────────────────────────────────
	"qwen-max":          {32_768, 8_192},
	"qwen-max-latest":   {32_768, 8_192},
	"qwen-plus":         {131_072, 8_192},
	"qwen-plus-latest":  {131_072, 8_192},
	"qwen-turbo":        {131_072, 8_192},
	"qwen-turbo-latest": {131_072, 8_192},
	"qwen3-235b-a22b":   {131_072, 8_192},
	"qwen3-32b":         {131_072, 8_192},
	"qwen3-14b":         {32_768, 8_192},
	"qwen3-8b":          {32_768, 8_192},
	"qwen2.5-72b":       {131_072, 8_192},
	"qwen2.5-32b":       {131_072, 8_192},
	"qwen2.5-14b":       {32_768, 8_192},
	"qwen2.5-7b":        {32_768, 8_192},
	"qwen-coder-plus":   {131_072, 8_192},
	"qwen-coder-turbo":  {131_072, 8_192},
	"qwq-plus":          {131_072, 8_192},
	"qwq-32b":           {131_072, 8_192},

	// ── DeepSeek ────────────────────────────────────────────────────
	"deepseek-v3":       {65_536, 8_192},
	"deepseek-v3-0324":  {65_536, 8_192},
	"deepseek-r1":       {65_536, 8_192},
	"deepseek-r1-0528":  {65_536, 8_192},
	"deepseek-v2":       {32_768, 4_096},
	"deepseek-chat":     {65_536, 8_192},
	"deepseek-reasoner": {65_536, 8_192},
	"deepseek-coder":    {16_384, 8_192},

	// ── GLM (智谱) ──────────────────────────────────────────────────
	"glm-4":        {128_000, 4_096},
	"glm-4-plus":   {128_000, 4_096},
	"glm-4-0520":   {128_000, 4_096},
	"glm-4-air":    {128_000, 4_096},
	"glm-4-airx":   {8_192, 4_096},
	"glm-4-flash":  {128_000, 4_096},
	"glm-4-flashx": {128_000, 4_096},
	"glm-4-long":   {1_000_000, 4_096},
	"glm-4v":       {8_192, 4_096},
	"glm-4v-plus":  {8_192, 4_096},
	"glm-3-turbo":  {128_000, 4_096},

	// ── Moonshot / Kimi ─────────────────────────────────────────────
	"moonshot-v1-8k":   {8_192, 4_096},
	"moonshot-v1-32k":  {32_768, 4_096},
	"moonshot-v1-128k": {128_000, 4_096},

	// ── Mistral ─────────────────────────────────────────────────────
	"mistral-large":  {128_000, 4_096},
	"mistral-medium": {32_000, 4_096},
	"mistral-small":  {32_000, 4_096},
	"mistral-tiny":   {8_192, 512},
	"mixtral-8x7b":   {32_000, 4_096},
	"mixtral-8x22b":  {64_000, 4_096},

	// ── Yi / 零一万物 ──────────────────────────────────────────────
	"yi-large":     {32_768, 4_096},
	"yi-medium":    {16_384, 4_096},
	"yi-spark":     {16_384, 4_096},
	"yi-lightning": {16_384, 4_096},

	// ── Embedding models ────────────────────────────────────────────
	"text-embedding-3-small": {8_191, 0},
	"text-embedding-3-large": {8_191, 0},
	"text-embedding-ada-002": {8_191, 0},
	"text-embedding-v3":      {8_191, 0}, // qwen embedding
	"text-embedding-v4":      {8_191, 0},
}

// lookupModelSpec returns the static spec for name, or 0/0 if unknown.
// Matching is case-insensitive on the full model ID.
func lookupModelSpec(name string) (ctxWin, maxOut int) {
	if s, ok := modelCatalog[name]; ok {
		return s.ContextWindow, s.MaxOutputTokens
	}
	// Try lowercase fallback for models not in the catalog with exact case.
	if s, ok := modelCatalog[toLower(name)]; ok {
		return s.ContextWindow, s.MaxOutputTokens
	}
	return 0, 0
}

// toLower is a simple ASCII-only lowercasing helper to avoid importing strings.
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}

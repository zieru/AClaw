package webadmin

import (
	"strings"
)

type ThinkingOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type ThinkingConfig struct {
	Supported     bool             `json:"supported"`
	ParameterName string           `json:"parameter_name"`
	ParameterType string           `json:"parameter_type"` // "string", "object", "integer"
	Options       []ThinkingOption `json:"options"`
	DefaultValue  string           `json:"default_value"`
	Note          string           `json:"note,omitempty"`
}

type ModelDetail struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	ProviderID     string          `json:"provider_id"`
	ThinkingConfig *ThinkingConfig `json:"thinking_config,omitempty"`
}

type ProviderWithModels struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Type   string        `json:"type"`
	Models []ModelDetail `json:"models"`
}

// GetThinkingCatalog returns the predefined thinking catalog for all supported AI providers & models
func GetThinkingCatalog() map[string]*ThinkingConfig {
	catalog := make(map[string]*ThinkingConfig)

	// 1. Google (Gemini)
	googleThinking := &ThinkingConfig{
		Supported:     true,
		ParameterName: "thinking_level",
		ParameterType: "string",
		DefaultValue:  "medium",
		Options: []ThinkingOption{
			{Value: "low", Label: "Low", Description: "Penalaran cepat, hemat token, respons instan."},
			{Value: "medium", Label: "Medium", Description: "Keseimbangan antara logika dan kecepatan (default untuk Flash)."},
			{Value: "high", Label: "High", Description: "Penalaran mendalam maksimal (default untuk Pro)."},
		},
		Note: "Untuk model lini Gemini 2.5, gunakan parameter 'thinking_budget' dengan nilai integer.",
	}
	for _, m := range []string{"gemini-3.7-flash", "gemini-3.8-flash", "gemini-3.1-pro", "gemini-2.5-flash", "gemini-2.5-pro"} {
		catalog[m] = googleThinking
	}

	// 2. OpenAI
	openaiThinking := &ThinkingConfig{
		Supported:     true,
		ParameterName: "reasoning_effort",
		ParameterType: "string",
		DefaultValue:  "medium",
		Options: []ThinkingOption{
			{Value: "low", Label: "Low", Description: "Membatasi proses berpikir seminimal mungkin."},
			{Value: "medium", Label: "Medium", Description: "Pengaturan seimbang standar pabrikan."},
			{Value: "high", Label: "High", Description: "Berpikir mendalam maksimal untuk logika rumit."},
		},
		Note: "Fitur berpikir pada seri model 'o' tidak dapat dimatikan sepenuhnya (nilai minimum adalah 'low').",
	}
	for _, m := range []string{"o1", "o1-mini", "o3-mini"} {
		catalog[m] = openaiThinking
	}

	// 3. DeepSeek
	deepseekThinking := &ThinkingConfig{
		Supported:     true,
		ParameterName: "thinking_mode",
		ParameterType: "string",
		DefaultValue:  "think_high",
		Options: []ThinkingOption{
			{Value: "disabled", Label: "Disabled", Description: "Matikan fitur berpikir untuk jawaban instan tanpa CoT."},
			{Value: "think_high", Label: "Think High", Description: "Mengaktifkan rantai pemikiran (Chain of Thought) standar."},
			{Value: "think_max", Label: "Think Max", Description: "Kapasitas penalaran maksimal untuk matematika dan logika kompleks."},
		},
		Note: "Beberapa aggregator API memetakan parameter ini menggunakan nama 'thinking_effort'.",
	}
	for _, m := range []string{"deepseek-r1", "deepseek-v4"} {
		catalog[m] = deepseekThinking
	}

	// 4. Anthropic
	anthropicThinking := &ThinkingConfig{
		Supported:     true,
		ParameterName: "thinking",
		ParameterType: "object",
		DefaultValue:  "4096",
		Options: []ThinkingOption{
			{Value: "2048", Label: "2048 Tokens", Description: "Penalaran cepat hemat budget."},
			{Value: "4096", Label: "4096 Tokens", Description: "Standar penalaran berimbang."},
			{Value: "8192", Label: "8192 Tokens", Description: "Penalaran mendalam."},
			{Value: "16384", Label: "16384 Tokens", Description: "Penalaran kompleks maksimal."},
		},
		Note: "Anthropic tidak menggunakan opsi teks (string), melainkan kuota jumlah token yang presisi.",
	}
	for _, m := range []string{"claude-4-opus", "claude-4-sonnet", "claude-3-5-sonnet-20241022", "claude-3-7-sonnet"} {
		catalog[m] = anthropicThinking
	}

	// 5. MiniMax
	minimaxThinking := &ThinkingConfig{
		Supported:     true,
		ParameterName: "thinking",
		ParameterType: "string",
		DefaultValue:  "adaptive",
		Options: []ThinkingOption{
			{Value: "disabled", Label: "Disabled", Description: "Fitur berpikir dinonaktifkan demi mengejar latensi rendah."},
			{Value: "adaptive", Label: "Adaptive", Description: "AI menentukan secara otomatis apakah perlu berpikir mendalam atau tidak."},
			{Value: "enabled", Label: "Enabled", Description: "Memaksa fitur penalaran mendalam (Interleaved Thinking) selalu menyala."},
		},
		Note: "Jika diakses lewat wrapper OpenRouter/Antigravity, parameter ini biasanya diterjemahkan otomatis dari skema 'reasoning_effort'.",
	}
	for _, m := range []string{"minimax-m1", "minimax-m2", "minimax-m3"} {
		catalog[m] = minimaxThinking
	}

	// 6. xAI (Grok)
	xaiThinking := &ThinkingConfig{
		Supported:     true,
		ParameterName: "reasoning_effort",
		ParameterType: "string",
		DefaultValue:  "high",
		Options: []ThinkingOption{
			{Value: "high", Label: "High", Description: "Level penalaran maksimal (bawaan pabrik/default). Menghabiskan token CoT lebih banyak."},
			{Value: "medium", Label: "Medium", Description: "Proses berpikir tingkat menengah untuk efisiensi latensi."},
			{Value: "low", Label: "Low", Description: "Proses berpikir minimal untuk respons cepat."},
		},
		Note: "Pada model penalaran Grok, fitur berpikir tidak bisa dimatikan sepenuhnya.",
	}
	for _, m := range []string{"grok-4.5", "grok-4.6", "grok-4.6-reasoning"} {
		catalog[m] = xaiThinking
	}

	// 7. Alibaba Cloud (Qwen)
	alibabaThinking := &ThinkingConfig{
		Supported:     true,
		ParameterName: "reasoning_effort",
		ParameterType: "string",
		DefaultValue:  "medium",
		Options: []ThinkingOption{
			{Value: "xhigh", Label: "Extra High", Description: "Penalaran dengan intensitas sangat tinggi (default untuk seri Max)."},
			{Value: "medium", Label: "Medium", Description: "Penalaran intensitas sedang."},
			{Value: "low", Label: "Low", Description: "Penalaran intensitas rendah."},
			{Value: "none", Label: "None", Description: "Mematikan mode berpikir sepenuhnya untuk jawaban instan."},
		},
		Note: "Qwen juga mendukung parameter 'enable_thinking' atau 'preserve_thinking'.",
	}
	for _, m := range []string{"qwen3.5-instruct", "qwen3.8-max-preview", "qwen3.8-max"} {
		catalog[m] = alibabaThinking
	}

	// 8. Mistral AI
	mistralThinking := &ThinkingConfig{
		Supported:     true,
		ParameterName: "reasoning_effort",
		ParameterType: "string",
		DefaultValue:  "high",
		Options: []ThinkingOption{
			{Value: "high", Label: "High", Description: "Mengaktifkan penalaran mendalam langkah-demi-langkah (disarankan untuk coding)."},
			{Value: "none", Label: "None", Description: "Model berpikir seminimal mungkin dan menyembunyikan pemikiran."},
		},
		Note: "Untuk seri 'Magistral', fiturnya Native Reasoning dan parameter tidak perlu diisi.",
	}
	for _, m := range []string{"mistral-small-4", "mistral-medium-3.5"} {
		catalog[m] = mistralThinking
	}

	return catalog
}

// ResolveThinkingForModel returns ThinkingConfig for a given model
func ResolveThinkingForModel(modelID string) *ThinkingConfig {
	catalog := GetThinkingCatalog()
	lower := strings.ToLower(strings.TrimSpace(modelID))

	// Direct match
	if cfg, ok := catalog[lower]; ok {
		return cfg
	}

	// Fuzzy/Prefix match
	if strings.Contains(lower, "gemini-3") || strings.Contains(lower, "gemini-2.5") {
		return catalog["gemini-3.8-flash"]
	}
	if strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") {
		return catalog["o3-mini"]
	}
	if strings.Contains(lower, "deepseek-r1") || strings.Contains(lower, "deepseek-v") {
		return catalog["deepseek-r1"]
	}
	if strings.Contains(lower, "claude-4") || strings.Contains(lower, "claude-3-7") {
		return catalog["claude-4-sonnet"]
	}
	if strings.Contains(lower, "grok") && strings.Contains(lower, "reasoning") {
		return catalog["grok-4.6"]
	}
	if strings.Contains(lower, "qwen") && (strings.Contains(lower, "max") || strings.Contains(lower, "reasoning")) {
		return catalog["qwen3.8-max"]
	}

	return nil
}

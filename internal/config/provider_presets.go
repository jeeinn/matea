package config

// ProviderPreset is a ready-to-use LLM provider template surfaced in the
// first-run wizard and the SystemConfig "add provider" dialog (C-11).
// Selecting one pre-fills base_url + default model so the user only supplies
// an API key.
//
// This is the single source of truth for presets. The frontend fetches it
// from GET /api/(config|setup)/provider-presets instead of hardcoding the
// list (which previously lived duplicated in SetupWizard.vue and the docs).
type ProviderPreset struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	Type     string `json:"type"`
}

// DefaultProviderPresets returns the built-in LLM provider presets.
func DefaultProviderPresets() []ProviderPreset {
	return []ProviderPreset{
		{Key: "deepseek", Label: "DeepSeek（云端）", Provider: "deepseek", BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-v4-flash", Type: "openai_compatible"},
		{Key: "openai", Label: "OpenAI（云端）", Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini", Type: "openai_compatible"},
		{Key: "anthropic", Label: "Anthropic Claude（云端）", Provider: "claude", BaseURL: "https://api.anthropic.com", Model: "claude-sonnet-4-5", Type: "anthropic"},
		{Key: "sensenova", Label: "SenseNova 商汤（云端）", Provider: "sensenova", BaseURL: "https://api.sensenova.cn/compatible-mode/v1", Model: "deepseek-v4-flash", Type: "openai_compatible"},
		{Key: "ollama", Label: "Ollama（本地）", Provider: "ollama", BaseURL: "http://localhost:11434/v1", Model: "", Type: "openai_compatible"},
		{Key: "custom", Label: "自定义…", Provider: "", BaseURL: "", Model: "", Type: "openai_compatible"},
	}
}

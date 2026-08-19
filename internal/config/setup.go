package config

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// GenerateWebhookSecret returns a new random 32-byte hex secret for
// gitea.webhook_secret (C-6). Called when the setup wizard completes without
// an explicit secret, or when webhook ingress starts with an empty one.
func GenerateWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// SetupStatus reports whether essential Gitea / LLM settings are incomplete.
type SetupStatus struct {
	SetupRequired bool     `json:"setup_required"`
	Missing       []string `json:"missing"`
	GiteaOK       bool     `json:"gitea_ok"`
	LLMOK         bool     `json:"llm_ok"`
}

// CheckSetup detects incomplete Gitea URL/token/secret or LLM providers/defaults.
// Gateway can still start; callers use this for Web UI banners / API guidance.
func CheckSetup(cfg *Config) SetupStatus {
	if cfg == nil {
		return SetupStatus{
			SetupRequired: true,
			Missing:       []string{"config"},
		}
	}

	var missing []string

	if strings.TrimSpace(cfg.Gitea.URL) == "" {
		missing = append(missing, "gitea.url")
	}
	if strings.TrimSpace(cfg.Gitea.AdminToken) == "" {
		missing = append(missing, "gitea.admin_token")
	}
	// C-6 (Phase 2.5): gitea.webhook_secret is NOT required for setup — when
	// absent it is auto-generated (32-byte hex) at setup completion or on first
	// webhook-ingress use. Blocking first-run on a secret the user never typed
	// violates "能自动的绝不手动".

	giteaOK := len(missing) == 0

	llmMissing := checkLLMSetup(cfg)
	missing = append(missing, llmMissing...)
	llmOK := len(llmMissing) == 0

	return SetupStatus{
		SetupRequired: len(missing) > 0,
		Missing:       missing,
		GiteaOK:       giteaOK,
		LLMOK:         llmOK,
	}
}

func checkLLMSetup(cfg *Config) []string {
	var missing []string
	if len(cfg.LLM.Providers) == 0 {
		return []string{"llm.providers"}
	}

	providerName := strings.TrimSpace(cfg.LLM.Defaults.Provider)
	if providerName == "" {
		missing = append(missing, "llm.defaults.provider")
		return missing
	}

	provider, ok := cfg.LLM.Providers[providerName]
	if !ok {
		missing = append(missing, "llm.providers."+providerName)
		return missing
	}

	if strings.TrimSpace(cfg.LLM.Defaults.Model) == "" {
		missing = append(missing, "llm.defaults.model")
	}

	// Local / keyless endpoints may omit api_key; require base_url at minimum.
	if strings.TrimSpace(provider.BaseURL) == "" {
		missing = append(missing, "llm.providers."+providerName+".base_url")
	}
	// Treat empty api_key as incomplete unless type looks like a local server
	// without auth (ollama-style). Prefer requiring a key when base_url is remote.
	if strings.TrimSpace(provider.APIKey) == "" && !isLikelyLocalLLM(provider.BaseURL) {
		missing = append(missing, "llm.providers."+providerName+".api_key")
	}
	return missing
}

func isLikelyLocalLLM(baseURL string) bool {
	u := strings.ToLower(strings.TrimSpace(baseURL))
	if u == "" {
		return false
	}
	// Loopback
	if strings.Contains(u, "127.0.0.1") ||
		strings.Contains(u, "localhost") ||
		strings.Contains(u, "0.0.0.0") ||
		strings.Contains(u, "[::1]") ||
		strings.Contains(u, "://::1") {
		return true
	}
	// Common private LAN prefixes (Ollama / local gateways on LAN)
	privateHints := []string{
		"://10.",
		"://192.168.",
		"://172.16.", "://172.17.", "://172.18.", "://172.19.",
		"://172.20.", "://172.21.", "://172.22.", "://172.23.",
		"://172.24.", "://172.25.", "://172.26.", "://172.27.",
		"://172.28.", "://172.29.", "://172.30.", "://172.31.",
	}
	for _, p := range privateHints {
		if strings.Contains(u, p) {
			return true
		}
	}
	return false
}

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jeeinn/matea/internal/llm"
)

// --- Local service auto-detection (Phase 2.5: C-4 Ollama, C-5 OpenCode) ---
//
// Best-effort probes with tight timeouts, run only behind the Setup Token.
// Detection never blocks setup — it pre-fills wizard defaults.

const (
	ollamaDefaultURL = "http://localhost:11434"
	detectTimeout    = 1500 * time.Millisecond
)

// openCodeDefaultPorts: 4096 is `opencode serve` default; 8081 appeared in
// older Matea docs. Configured hub-opencode backend URLs are probed first.
var openCodeDefaultPorts = []int{4096, 8081}

type ollamaDetect struct {
	OK     bool     `json:"ok"`
	URL    string   `json:"url"`
	Models []string `json:"models,omitempty"`
	Error  string   `json:"error,omitempty"`
}

type openCodeDetect struct {
	OK      bool   `json:"ok"`
	URL     string `json:"url,omitempty"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

type detectResult struct {
	Ollama   ollamaDetect   `json:"ollama"`
	OpenCode openCodeDetect `json:"opencode"`
}

func detectHTTPClient() *http.Client {
	return &http.Client{Timeout: detectTimeout}
}

// probeOllama GETs /api/tags and lists installed model names (C-4).
func probeOllama(ctx context.Context, baseURL string) ollamaDetect {
	res := ollamaDetect{URL: baseURL}
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/tags", nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, err := detectHTTPClient().Do(req)
	if err != nil {
		res.Error = "未检测到（连接失败）"
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return res
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || json.Unmarshal(body, &tags) != nil {
		res.Error = "响应解析失败"
		return res
	}
	res.OK = true
	const maxModels = 20
	for i, m := range tags.Models {
		if i >= maxModels {
			break
		}
		if m.Name != "" {
			res.Models = append(res.Models, m.Name)
		}
	}
	return res
}

// probeOpenCode GETs {base}/health and treats any non-5xx HTTP response as
// "reachable" (C-5). Version is extracted when the body carries one.
func probeOpenCode(ctx context.Context, baseURL string) openCodeDetect {
	res := openCodeDetect{URL: baseURL}
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/health", nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, err := detectHTTPClient().Do(req)
	if err != nil {
		res.Error = "未检测到（连接失败）"
		res.URL = ""
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		res.URL = ""
		return res
	}
	res.OK = true
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var parsed struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		res.Version = parsed.Version
	}
	return res
}

// openCodeProbeURLs builds the deduped probe list: configured hub-opencode
// backend base URLs first, then default ports on localhost.
func (h *Handler) openCodeProbeURLs() []string {
	seen := map[string]bool{}
	var urls []string
	add := func(u string) {
		u = strings.TrimRight(strings.TrimSpace(u), "/")
		if u != "" && !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	if h.cfgManager != nil {
		cfg := h.cfgManager.Get()
		for _, b := range cfg.Agents.Backends.Backends {
			if b.Type == "hub-opencode" && b.BaseURL != "" {
				add(b.BaseURL)
			}
		}
	}
	for _, port := range openCodeDefaultPorts {
		add("http://localhost:" + strconv.Itoa(port))
	}
	return urls
}

// detectLocalServices handles GET /api/setup/detect (setup-token gated).
func (h *Handler) detectLocalServices(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	result := detectResult{}
	result.Ollama = probeOllama(ctx, ollamaDefaultURL)

	for _, u := range h.openCodeProbeURLs() {
		if oc := probeOpenCode(ctx, u); oc.OK {
			result.OpenCode = oc
			break
		}
	}
	if !result.OpenCode.OK {
		result.OpenCode = openCodeDetect{Error: "未检测到（已探测 " + strings.Join(h.openCodeProbeURLs(), ", ") + "）"}
	}

	writeJSON(w, 200, result)
}

// --- LLM connectivity test for the wizard (payload self-contained, nothing
// read from saved config — during setup there is none yet) ---

func (h *Handler) testSetupLLM(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Type    string `json:"type"`
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		Model   string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	baseURL := strings.TrimRight(strings.TrimSpace(payload.BaseURL), "/")
	apiKey := strings.TrimSpace(payload.APIKey)
	model := strings.TrimSpace(payload.Model)
	providerType := strings.TrimSpace(payload.Type)

	if model == "" {
		writeJSON(w, 400, map[string]interface{}{"ok": false, "message": "model 不能为空"})
		return
	}
	if baseURL == "" && !strings.EqualFold(providerType, "anthropic") {
		writeJSON(w, 400, map[string]interface{}{"ok": false, "message": "base_url 不能为空"})
		return
	}
	if apiKey == "" && !isLikelyLocalBaseURL(baseURL) {
		writeJSON(w, 400, map[string]interface{}{"ok": false, "message": "远程 LLM 必须填写 api_key"})
		return
	}

	var provider llm.Provider
	if strings.EqualFold(providerType, "anthropic") {
		provider = llm.NewAnthropicProvider(apiKey)
	} else {
		provider = llm.NewOpenAICompatibleProvider(baseURL, apiKey)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	resp, err := provider.ChatCompletion(ctx, &llm.ChatRequest{
		Model:       model,
		Messages:    []llm.Message{{Role: "user", Content: "ping"}},
		MaxTokens:   8,
		Temperature: 0,
	})
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"ok":      true,
		"message": fmt.Sprintf("连接成功，模型响应: %s", strings.TrimSpace(resp.Content)),
	})
}

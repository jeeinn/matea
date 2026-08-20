package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

var configKeys = []string{
	"gitea.url",
	"gitea.admin_token",
	"gitea.webhook_secret",
	"gitea.auto_provision",
	"server.public_url",
	"llm.defaults.provider",
	"llm.defaults.model",
	"llm.providers",
	"dispatcher.max_concurrent",
	"dispatcher.task_retry_count",
	"dispatcher.rate_limit_backoff",
	"dispatcher.agent_concurrency",
	"llm.rate_limit_retries",
	"agents.defaults.provider",
	"agents.defaults.model",
	"agents.defaults.max_output_tokens",
	"agents.defaults.max_input_tokens",
	"agents.defaults.temperature",
	"agents.defaults.timeout",
	"agents.loop.max_iterations",
	"agents.loop.total_timeout",
	"agents.loop.iteration_interval",
	"agents.loop.no_progress_limit",
	"agents.loop.verify_commands",
	"agents.loop.independent_checker",
	"agents.backends",
	"debug.conversation_log.enabled",
	"debug.conversation_log.max_content_chars",
	"workflow.preset",
	"workflow.gates",
	"deliver.webhook_url",
	"deliver.timeout",
	"deliver.max_retries",
}

// IsConfigKey returns true if the key is a recognized config field (not a data key like prompt.templates).
func IsConfigKey(key string) bool {
	for _, k := range configKeys {
		if k == key {
			return true
		}
	}
	return false
}

func parseConfigValue(key, value string) (interface{}, error) {
	switch key {
	case "dispatcher.max_concurrent", "dispatcher.task_retry_count", "dispatcher.rate_limit_backoff",
		"llm.rate_limit_retries",
		"agents.defaults.max_output_tokens", "agents.defaults.max_input_tokens",
		"agents.loop.max_iterations", "agents.loop.iteration_interval",
		"agents.loop.no_progress_limit",
		"debug.conversation_log.max_content_chars":
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("not a number: %s", value)
		}
		return n, nil
	case "dispatcher.agent_concurrency":
		switch value {
		case AgentConcurrencyParallel, AgentConcurrencySerialQueue:
			return value, nil
		default:
			return nil, fmt.Errorf("invalid agent_concurrency %q (want parallel|serial_queue)", value)
		}
	case "agents.defaults.temperature":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("not a float: %s", value)
		}
		return f, nil
	case "debug.conversation_log.enabled":
		b, err := parseBoolValue(value)
		if err != nil {
			return nil, err
		}
		return b, nil
	case "agents.loop.independent_checker":
		b, err := parseBoolValue(value)
		if err != nil {
			return nil, err
		}
		return b, nil
	case "llm.providers":
		var providers map[string]ProviderConfig
		if err := json.Unmarshal([]byte(value), &providers); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return providers, nil
	case "agents.loop.verify_commands":
		var cmds []string
		if err := json.Unmarshal([]byte(value), &cmds); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return cmds, nil
	case "gitea.auto_provision":
		b, err := parseBoolValue(value)
		if err != nil {
			return nil, err
		}
		return b, nil
	case "deliver.max_retries":
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("not a number: %s", value)
		}
		return n, nil
	case "deliver.webhook_url", "deliver.timeout":
		return value, nil
	case "agents.backends":
		parsed, err := ParseAgentBackendsJSON(value)
		if err != nil {
			return nil, err
		}
		s, err := MarshalAgentBackendsJSON(parsed)
		if err != nil {
			return nil, err
		}
		return s, nil
	default:
		return value, nil
	}
}

func getConfigValueTyped(cfg *Config, key string) interface{} {
	switch key {
	case "gitea.url":
		return cfg.Gitea.URL
	case "gitea.admin_token":
		return cfg.Gitea.AdminToken
	case "gitea.webhook_secret":
		return cfg.Gitea.WebhookSecret
	case "gitea.auto_provision":
		return cfg.Gitea.AutoProvision
	case "server.public_url":
		return cfg.Server.PublicURL
	case "llm.defaults.provider":
		return cfg.LLM.Defaults.Provider
	case "llm.defaults.model":
		return cfg.LLM.Defaults.Model
	case "llm.providers":
		return cfg.LLM.Providers
	case "dispatcher.max_concurrent":
		return cfg.Dispatcher.MaxConcurrent
	case "dispatcher.task_retry_count":
		return cfg.Dispatcher.TaskRetryCount
	case "dispatcher.rate_limit_backoff":
		return cfg.Dispatcher.RateLimitBackoff
	case "dispatcher.agent_concurrency":
		return cfg.Dispatcher.AgentConcurrency
	case "llm.rate_limit_retries":
		return cfg.LLM.RateLimitRetries
	case "agents.defaults.provider":
		return cfg.Agents.Defaults.Provider
	case "agents.defaults.model":
		return cfg.Agents.Defaults.Model
	case "agents.defaults.max_output_tokens":
		return cfg.Agents.Defaults.MaxOutputTokens
	case "agents.defaults.max_input_tokens":
		return cfg.Agents.Defaults.MaxInputTokens
	case "agents.defaults.temperature":
		return cfg.Agents.Defaults.Temperature
	case "agents.defaults.timeout":
		return cfg.Agents.Defaults.Timeout
	case "agents.loop.max_iterations":
		return cfg.Agents.Loop.MaxIterations
	case "agents.loop.total_timeout":
		return cfg.Agents.Loop.TotalTimeout
	case "agents.loop.iteration_interval":
		return cfg.Agents.Loop.IterationInterval
	case "agents.loop.no_progress_limit":
		return cfg.Agents.Loop.NoProgressLimit
	case "agents.loop.verify_commands":
		return cfg.Agents.Loop.VerifyCommands
	case "agents.loop.independent_checker":
		return cfg.Agents.Loop.IndependentChecker
	case "agents.backends":
		s, _ := MarshalAgentBackendsJSON(cfg.Agents.Backends)
		return s
	case "debug.conversation_log.enabled":
		return cfg.Debug.ConversationLog.Enabled
	case "debug.conversation_log.max_content_chars":
		return cfg.Debug.ConversationLog.MaxContentChars
	case "workflow.preset":
		return cfg.Workflow.Preset
	case "workflow.gates":
		return cfg.Workflow.Gates
	case "deliver.webhook_url":
		return cfg.Deliver.WebhookURL
	case "deliver.timeout":
		return cfg.Deliver.Timeout
	case "deliver.max_retries":
		return cfg.Deliver.MaxRetries
	default:
		return ""
	}
}

// applyConfigEntry sets a single config key on the Config struct.
func applyConfigEntry(cfg *Config, key, value string) error {
	switch key {
	case "gitea.url":
		cfg.Gitea.URL = value
	case "gitea.admin_token":
		cfg.Gitea.AdminToken = value
	case "gitea.webhook_secret":
		cfg.Gitea.WebhookSecret = value
	case "gitea.auto_provision":
		b, err := parseBoolValue(value)
		if err != nil {
			return fmt.Errorf("not a boolean: %s", value)
		}
		cfg.Gitea.AutoProvision = b
	case "server.public_url":
		cfg.Server.PublicURL = value
	case "llm.defaults.provider":
		cfg.LLM.Defaults.Provider = value
	case "llm.defaults.model":
		cfg.LLM.Defaults.Model = value
	case "llm.providers":
		providers, err := ParseProvidersJSON(value)
		if err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		cfg.LLM.Providers = providers
	case "dispatcher.max_concurrent":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.Dispatcher.MaxConcurrent = n
	case "dispatcher.task_retry_count":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.Dispatcher.TaskRetryCount = n
	case "dispatcher.rate_limit_backoff":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.Dispatcher.RateLimitBackoff = n
	case "dispatcher.agent_concurrency":
		switch value {
		case AgentConcurrencyParallel, AgentConcurrencySerialQueue:
			cfg.Dispatcher.AgentConcurrency = value
		default:
			return fmt.Errorf("invalid agent_concurrency %q (want parallel|serial_queue)", value)
		}
	case "llm.rate_limit_retries":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.LLM.RateLimitRetries = n
	case "agents.defaults.provider":
		cfg.Agents.Defaults.Provider = value
	case "agents.defaults.model":
		cfg.Agents.Defaults.Model = value
	case "agents.defaults.max_output_tokens":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.Agents.Defaults.MaxOutputTokens = n
	case "agents.defaults.max_input_tokens":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.Agents.Defaults.MaxInputTokens = n
	case "agents.defaults.temperature":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("not a float: %s", value)
		}
		cfg.Agents.Defaults.Temperature = f
	case "agents.defaults.timeout":
		cfg.Agents.Defaults.Timeout = value
	case "agents.loop.max_iterations":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.Agents.Loop.MaxIterations = n
		if err := ValidateAgentLoopConfig(cfg.Agents.Loop); err != nil {
			return err
		}
	case "agents.loop.total_timeout":
		cfg.Agents.Loop.TotalTimeout = value
		if err := ValidateAgentLoopConfig(cfg.Agents.Loop); err != nil {
			return err
		}
	case "agents.loop.iteration_interval":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.Agents.Loop.IterationInterval = n
	case "agents.loop.no_progress_limit":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.Agents.Loop.NoProgressLimit = n
	case "agents.loop.verify_commands":
		var cmds []string
		if err := json.Unmarshal([]byte(value), &cmds); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		cfg.Agents.Loop.VerifyCommands = cmds
	case "agents.loop.independent_checker":
		b, err := parseBoolValue(value)
		if err != nil {
			return fmt.Errorf("not a boolean: %s", value)
		}
		cfg.Agents.Loop.IndependentChecker = b
	case "agents.backends":
		if err := ApplyAgentBackendsJSON(cfg, value); err != nil {
			return err
		}
	case "debug.conversation_log.enabled":
		b, err := parseBoolValue(value)
		if err != nil {
			return fmt.Errorf("not a boolean: %s", value)
		}
		cfg.Debug.ConversationLog.Enabled = b
	case "debug.conversation_log.max_content_chars":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.Debug.ConversationLog.MaxContentChars = n
	case "workflow.preset":
		cfg.Workflow.Preset = value
	case "workflow.gates":
		var gates map[string]string
		if err := json.Unmarshal([]byte(value), &gates); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		cfg.Workflow.Gates = gates
	case "deliver.webhook_url":
		cfg.Deliver.WebhookURL = value
	case "deliver.timeout":
		cfg.Deliver.Timeout = value
	case "deliver.max_retries":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a number: %s", value)
		}
		cfg.Deliver.MaxRetries = n
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

// getConfigEntry reads a single config value from a Config struct.
func getConfigEntry(cfg *Config, key string) string {
	switch key {
	case "gitea.url":
		return cfg.Gitea.URL
	case "gitea.admin_token":
		return cfg.Gitea.AdminToken
	case "gitea.webhook_secret":
		return cfg.Gitea.WebhookSecret
	case "gitea.auto_provision":
		return strconv.FormatBool(cfg.Gitea.AutoProvision)
	case "server.public_url":
		return cfg.Server.PublicURL
	case "llm.defaults.provider":
		return cfg.LLM.Defaults.Provider
	case "llm.defaults.model":
		return cfg.LLM.Defaults.Model
	case "llm.providers":
		data, _ := json.Marshal(cfg.LLM.Providers)
		return string(data)
	case "dispatcher.max_concurrent":
		return strconv.Itoa(cfg.Dispatcher.MaxConcurrent)
	case "dispatcher.task_retry_count":
		return strconv.Itoa(cfg.Dispatcher.TaskRetryCount)
	case "dispatcher.rate_limit_backoff":
		return strconv.Itoa(cfg.Dispatcher.RateLimitBackoff)
	case "dispatcher.agent_concurrency":
		return cfg.Dispatcher.AgentConcurrency
	case "llm.rate_limit_retries":
		return strconv.Itoa(cfg.LLM.RateLimitRetries)
	case "agents.defaults.provider":
		return cfg.Agents.Defaults.Provider
	case "agents.defaults.model":
		return cfg.Agents.Defaults.Model
	case "agents.defaults.max_output_tokens":
		return strconv.Itoa(cfg.Agents.Defaults.MaxOutputTokens)
	case "agents.defaults.max_input_tokens":
		return strconv.Itoa(cfg.Agents.Defaults.MaxInputTokens)
	case "agents.defaults.temperature":
		return fmt.Sprintf("%g", cfg.Agents.Defaults.Temperature)
	case "agents.defaults.timeout":
		return cfg.Agents.Defaults.Timeout
	case "agents.loop.max_iterations":
		return strconv.Itoa(cfg.Agents.Loop.MaxIterations)
	case "agents.loop.total_timeout":
		return cfg.Agents.Loop.TotalTimeout
	case "agents.loop.iteration_interval":
		return strconv.Itoa(cfg.Agents.Loop.IterationInterval)
	case "agents.loop.no_progress_limit":
		return strconv.Itoa(cfg.Agents.Loop.NoProgressLimit)
	case "agents.loop.verify_commands":
		data, _ := json.Marshal(cfg.Agents.Loop.VerifyCommands)
		return string(data)
	case "agents.loop.independent_checker":
		return strconv.FormatBool(cfg.Agents.Loop.IndependentChecker)
	case "agents.backends":
		s, _ := MarshalAgentBackendsJSON(cfg.Agents.Backends)
		return s
	case "debug.conversation_log.enabled":
		return strconv.FormatBool(cfg.Debug.ConversationLog.Enabled)
	case "debug.conversation_log.max_content_chars":
		return strconv.Itoa(cfg.Debug.ConversationLog.MaxContentChars)
	case "deliver.webhook_url":
		return cfg.Deliver.WebhookURL
	case "deliver.timeout":
		return cfg.Deliver.Timeout
	case "deliver.max_retries":
		return strconv.Itoa(cfg.Deliver.MaxRetries)
	default:
		return ""
	}
}

func parseBoolValue(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off", "":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean: %s", value)
	}
}

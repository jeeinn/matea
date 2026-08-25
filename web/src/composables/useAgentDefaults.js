import { ref, computed } from 'vue'
import api from '../api'

export const DEFAULT_AGENT_MAX_OUTPUT_TOKENS = 8192
export const DEFAULT_AGENT_MAX_INPUT_TOKENS = 115200 // 128K * 0.9
export const DEFAULT_AGENT_TIMEOUT = '20m'

export const DEFAULT_LOOP_CONFIG = {
  max_iterations: 20,
  total_timeout: '30m',
  iteration_interval: 0,
  no_progress_limit: 3,
  verify_commands: []
}

export function normalizeProviders(raw) {
  const out = {}
  for (const [name, cfg] of Object.entries(raw || {})) {
    if (!cfg || typeof cfg !== 'object') continue
    out[name] = {
      base_url: cfg.base_url || cfg.BaseURL || '',
      api_key: cfg.api_key || cfg.APIKey || '',
      type: cfg.type || cfg.Type || 'openai_compatible',
      models: cfg.models || cfg.Models || undefined,
      default_params: cfg.default_params || cfg.DefaultParams || undefined
    }
  }
  return out
}

function resolveDefaultProvider(data, providerKeys) {
  const agentsProvider = (data['agents.defaults.provider'] || '').trim()
  const llmProvider = (data['llm.defaults.provider'] || '').trim()

  if (agentsProvider && providerKeys.includes(agentsProvider)) return agentsProvider
  if (llmProvider && providerKeys.includes(llmProvider)) return llmProvider
  if (agentsProvider) return agentsProvider
  if (llmProvider) return llmProvider
  if (providerKeys.length > 0) return providerKeys[0]
  return 'deepseek'
}

function parseLoopDefaults(data) {
  return {
    max_iterations: Number(data['agents.loop.max_iterations']) || DEFAULT_LOOP_CONFIG.max_iterations,
    total_timeout: (data['agents.loop.total_timeout'] || DEFAULT_LOOP_CONFIG.total_timeout).trim(),
    iteration_interval: data['agents.loop.iteration_interval'] !== undefined && data['agents.loop.iteration_interval'] !== ''
      ? Number(data['agents.loop.iteration_interval'])
      : DEFAULT_LOOP_CONFIG.iteration_interval,
    no_progress_limit: data['agents.loop.no_progress_limit'] !== undefined && data['agents.loop.no_progress_limit'] !== ''
      ? Number(data['agents.loop.no_progress_limit'])
      : DEFAULT_LOOP_CONFIG.no_progress_limit,
    verify_commands: data['agents.loop.verify_commands'] !== undefined && data['agents.loop.verify_commands'] !== ''
      ? data['agents.loop.verify_commands']
      : DEFAULT_LOOP_CONFIG.verify_commands
  }
}

export function useAgentDefaults() {
  const providers = ref({})
  const backends = ref([])
  const backendDefault = ref('builtin')
  const agentDefaults = ref({
    provider: 'deepseek',
    model: 'deepseek-v4-flash',
    max_output_tokens: DEFAULT_AGENT_MAX_OUTPUT_TOKENS,
    max_input_tokens: DEFAULT_AGENT_MAX_INPUT_TOKENS,
    temperature: 0.3,
    timeout: DEFAULT_AGENT_TIMEOUT
  })
  const loopDefaults = ref({ ...DEFAULT_LOOP_CONFIG })

  const providerNames = computed(() => Object.keys(providers.value))

  const loadAgentConfig = async () => {
    try {
      const data = await api.get('/config')
      providers.value = normalizeProviders(data?.['llm.providers'])
      const keys = Object.keys(providers.value)

      agentDefaults.value = {
        provider: resolveDefaultProvider(data, keys),
        model: (data['agents.defaults.model'] || data['llm.defaults.model'] || 'deepseek-v4-flash').trim(),
        max_output_tokens: Number(data['agents.defaults.max_output_tokens']) || DEFAULT_AGENT_MAX_OUTPUT_TOKENS,
        max_input_tokens: Number(data['agents.defaults.max_input_tokens']) || DEFAULT_AGENT_MAX_INPUT_TOKENS,
        temperature: data['agents.defaults.temperature'] !== undefined
          ? Number(data['agents.defaults.temperature'])
          : 0.3,
        timeout: (data['agents.defaults.timeout'] || DEFAULT_AGENT_TIMEOUT).trim()
      }
      loopDefaults.value = parseLoopDefaults(data)

      if (data?._meta?.backends && Array.isArray(data._meta.backends)) {
        backends.value = data._meta.backends
      } else {
        backends.value = [{ name: 'builtin', type: 'builtin' }]
      }
      backendDefault.value = data?._meta?.backends_default || 'builtin'
    } catch {
      providers.value = {}
      backends.value = [{ name: 'builtin', type: 'builtin' }]
      backendDefault.value = 'builtin'
    }
  }

  const effectiveProviderNames = (currentProvider) => {
    const names = [...providerNames.value]
    const current = (currentProvider || '').trim()
    if (current && !names.includes(current)) {
      names.unshift(current)
    }
    return names
  }

  const createEmptyAgentForm = () => {
    const lc = { ...loopDefaults.value }
    // New agents should inherit verify_commands from system config, not override with []
    delete lc.verify_commands
    lc.verify_commands_override = false
    lc.verify_commands_text = ''
    return {
      name: '',
      gitea_username: '',
      role: 'analyze',
      provider: agentDefaults.value.provider,
      model: agentDefaults.value.model,
      backend: backendDefault.value || 'builtin',
      // 0 = optional override off → resolve from model meta at runtime
      max_output_tokens: 0,
      max_input_tokens: 0,
      temperature: agentDefaults.value.temperature,
      timeout: agentDefaults.value.timeout,
      system_prompt: '',
      user_template: '',
      status: 'active',
      repos: [],
      backend_options: {},
      loop_config: lc
    }
  }

  const isLoopRole = (role) => role === 'coder'

  return {
    providers,
    providerNames,
    backends,
    backendDefault,
    agentDefaults,
    loopDefaults,
    loadAgentConfig,
    effectiveProviderNames,
    createEmptyAgentForm,
    isLoopRole,
    defaultLoopConfig: DEFAULT_LOOP_CONFIG
  }
}

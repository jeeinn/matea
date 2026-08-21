import axios from 'axios'

// Setup API client — deliberately separate from the main api instance:
// no JWT attachment and NO 401→/login interceptor (a 401 here means a bad
// Setup Token, and the wizard must handle it inline).
const setupApi = axios.create({
  baseURL: '/api',
  timeout: 30000,
  headers: { 'Content-Type': 'application/json' }
})

setupApi.interceptors.response.use(
  (response) => response.data,
  (error) => {
    const data = error.response?.data
    const message = data?.error || data?.message || error.message || '请求失败'
    const err = new Error(message)
    err.status = error.response?.status
    err.payload = data
    return Promise.reject(err)
  }
)

// Public: initialization status (no token needed).
export function fetchSetupStatus() {
  return setupApi.get('/setup/status')
}

function withToken(token) {
  return { headers: { 'X-Setup-Token': token } }
}

export function verifySetupToken(token) {
  return setupApi.post('/setup/verify', {}, withToken(token))
}

export function detectLocalServices(token) {
  return setupApi.get('/setup/detect', withToken(token))
}

export function testSetupGitea(token, url, giteaToken) {
  return setupApi.post('/setup/test/gitea', { url, token: giteaToken }, withToken(token))
}

export function testSetupLLM(token, payload) {
  return setupApi.post('/setup/test/llm', payload, withToken(token))
}

export function completeSetup(token, payload) {
  return setupApi.post('/setup/complete', payload, withToken(token))
}

// Provider presets (C-11): single source of truth lives in the backend.
export function getProviderPresets(token) {
  return setupApi.get('/setup/provider-presets', withToken(token))
}

// C-12: discover models for an (unsaved) provider by base_url/type/api_key.
export function discoverSetupModels(token, payload) {
  return setupApi.post('/setup/discover-models', payload, withToken(token))
}

// C-21: detect which known environment variables are present in the process.
export function detectEnv(token) {
  return setupApi.get('/setup/env-detection', withToken(token))
}

// C-21: absorb the selected (or all detected) environment variables into config.
export function applyEnv(token, keys) {
  return setupApi.post('/setup/apply-env', { keys: keys || [] }, withToken(token))
}

export default setupApi

import axios from 'axios'
import { useAuthStore } from '../stores/auth'
import router from '../router'

const api = axios.create({
  baseURL: '/api',
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json'
  }
})

// Request interceptor - add auth token
api.interceptors.request.use(
  (config) => {
    const authStore = useAuthStore()
    if (authStore.token) {
      config.headers.Authorization = `Bearer ${authStore.token}`
    }
    return config
  },
  (error) => {
    return Promise.reject(error)
  }
)

// Response interceptor - handle errors
api.interceptors.response.use(
  (response) => response.data,
  (error) => {
    const status = error.response?.status
    const code = error.response?.data?.code
    if (status === 403 && code === 'must_change_password') {
      const authStore = useAuthStore()
      if (authStore.user) {
        authStore.user = { ...authStore.user, must_change_password: true }
        localStorage.setItem('user', JSON.stringify(authStore.user))
      }
      router.push('/change-password')
      return Promise.reject(error)
    }
    if (status === 401) {
      const authStore = useAuthStore()
      authStore.logout()
      router.push('/login')
    }
    // 错误归一化（参照 api/setup.js）：把后端返回的 error/message 字段提升为
    // 易读的 error.message，并附带 status/payload；保留原 axios error 结构
    // （response/config 等）以免破坏现有调用方。
    const data = error.response?.data
    if (data && (data.error || data.message)) {
      error.message = data.error || data.message
    }
    error.status = status
    error.payload = data
    return Promise.reject(error)
  }
)

export default api

// Provider presets (C-11): single source of truth lives in the backend.
export function getProviderPresets() {
  return api.get('/config/provider-presets')
}

// C-12: discover models for an (unsaved) provider by base_url/type/api_key.
export function discoverModels(payload) {
  return api.post('/config/discover-models', payload)
}

// C-18/C-19/C-22: aggregate health summary of every external dependency.
export function getHealthSummary() {
  return api.get('/health/summary')
}

// C-20: export the admin-managed flat config (returns { format, config,
// masked }). Secrets are masked unless includeSecrets is explicitly true.
export function exportConfig(includeSecrets = false) {
  return api.get(includeSecrets ? '/config/export?include_secrets=1' : '/config/export')
}

// C-20: import a previously exported (or hand-edited) flat config map.
export function importConfig(config) {
  return api.post('/config/import', { config })
}

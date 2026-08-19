<template>
  <div class="setup-page">
    <div class="setup-container">
      <div class="setup-header">
        <h1>Matea 初始化向导</h1>
        <p>三步完成初始配置：Gitea 连接 → LLM 模型 → 确认</p>
      </div>

      <!-- Token gate (C-2): proves console access -->
      <el-card v-if="!tokenVerified" class="setup-card">
        <template #header>
          <span>🔐 输入 Setup Token</span>
        </template>
        <el-alert type="info" :closable="false" class="mb-16">
          <p>首次启动时，服务控制台打印了一次性 <strong>Setup Token</strong>（30 分钟有效）。</p>
          <p>它用来确认你拥有这台服务器的操作权限。过期后新 Token 会重新打印到控制台。</p>
        </el-alert>
        <el-form @submit.prevent="verifyToken">
          <el-form-item label="Setup Token">
            <el-input
              v-model="token"
              placeholder="粘贴控制台打印的 Token"
              size="large"
              autofocus
              @keyup.enter="verifyToken"
            />
          </el-form-item>
          <el-alert v-if="tokenError" :title="tokenError" type="error" :closable="false" class="mb-16" />
          <el-button type="primary" size="large" :loading="verifying" style="width: 100%" @click="verifyToken">
            验证并开始配置
          </el-button>
        </el-form>
      </el-card>

      <!-- Wizard -->
      <template v-else-if="!finished">
        <el-steps :active="step" align-center class="setup-steps">
          <el-step title="Gitea 连接" />
          <el-step title="LLM 模型" />
          <el-step title="确认完成" />
        </el-steps>

        <!-- Step 1: Gitea -->
        <el-card v-show="step === 0" class="setup-card">
          <template #header><span>第 1 步：连接 Gitea</span></template>
          <el-form label-position="top">
            <el-form-item label="Gitea 地址">
              <el-input v-model="gitea.url" placeholder="http://localhost:3000" size="large" />
            </el-form-item>
            <el-form-item label="管理员 Token">
              <el-input
                v-model="gitea.token"
                type="password"
                show-password
                placeholder="在 Gitea → 设置 → 应用中生成（需要 write:admin 与 repo 权限）"
                size="large"
              />
            </el-form-item>
            <el-alert
              v-if="giteaTest.message"
              :title="giteaTest.message"
              :type="giteaTest.ok ? 'success' : 'error'"
              :closable="false"
              class="mb-16"
            />
            <div class="btn-row">
              <el-button :loading="giteaTest.testing" @click="testGitea">测试连接</el-button>
              <el-button type="primary" :disabled="!gitea.url || !gitea.token" @click="step = 1; enterLLMStep()">
                下一步
              </el-button>
            </div>
          </el-form>
        </el-card>

        <!-- Step 2: LLM -->
        <el-card v-show="step === 1" class="setup-card">
          <template #header><span>第 2 步：配置 LLM</span></template>

          <!-- Auto-detection (C-4/C-5) -->
          <div v-if="detect.loading" class="detect-line">正在检测本地模型服务…</div>
          <template v-else>
            <el-alert v-if="detect.ollama?.ok" type="success" :closable="false" class="mb-16">
              <p>✅ 检测到本地 <strong>Ollama</strong>（{{ detect.ollama.url }}）</p>
              <div v-if="detect.ollama.models?.length" class="detect-models">
                <span>已安装模型：</span>
                <el-tag
                  v-for="m in detect.ollama.models"
                  :key="m"
                  size="small"
                  class="model-tag"
                  @click="pickOllamaModel(m)"
                >{{ m }}</el-tag>
              </div>
              <p v-else>尚未安装模型，可先执行 <code>ollama pull qwen3</code>，或改用云端 Provider。</p>
            </el-alert>
            <el-alert v-else type="info" :closable="false" class="mb-16" title="未检测到本地 Ollama（可在向导完成后随时修改配置）" />
          </template>

          <el-form label-position="top">
            <el-form-item label="Provider 预设">
              <el-select v-model="llm.preset" size="large" style="width: 100%" @change="applyPreset">
                <el-option v-for="p in presets" :key="p.key" :label="p.label" :value="p.key" />
              </el-select>
            </el-form-item>
            <el-form-item v-if="llm.preset === 'custom'" label="Provider 名称（小写字母/数字/连字符）">
              <el-input v-model="llm.provider" placeholder="my-provider" size="large" />
            </el-form-item>
            <el-form-item label="Base URL">
              <el-input v-model="llm.base_url" size="large" />
            </el-form-item>
            <el-form-item :label="isLocalBaseURL ? 'API Key（本地服务可留空）' : 'API Key'">
              <el-input v-model="llm.api_key" type="password" show-password size="large" />
            </el-form-item>
            <el-form-item label="默认模型">
              <el-input v-model="llm.model" size="large" />
            </el-form-item>
            <el-alert
              v-if="llmTest.message"
              :title="llmTest.message"
              :type="llmTest.ok ? 'success' : 'error'"
              :closable="false"
              class="mb-16"
            />
            <div class="btn-row">
              <el-button @click="step = 0">上一步</el-button>
              <el-button :loading="llmTest.testing" @click="testLLM">测试连接</el-button>
              <el-button type="primary" :disabled="!llm.provider || !llm.model || !llm.base_url" @click="step = 2">
                下一步
              </el-button>
            </div>
          </el-form>
        </el-card>

        <!-- Step 3: confirm -->
        <el-card v-show="step === 2" class="setup-card">
          <template #header><span>第 3 步：确认并完成</span></template>
          <el-descriptions :column="1" border class="mb-16">
            <el-descriptions-item label="Gitea 地址">{{ gitea.url }}</el-descriptions-item>
            <el-descriptions-item label="Gitea Token">••••••••（已填写）</el-descriptions-item>
            <el-descriptions-item label="LLM Provider">{{ llm.provider }}</el-descriptions-item>
            <el-descriptions-item label="Base URL">{{ llm.base_url }}</el-descriptions-item>
            <el-descriptions-item label="默认模型">{{ llm.model }}</el-descriptions-item>
            <el-descriptions-item label="Webhook Secret">
              自动生成了 32 字节随机密钥，完成后显示一次
            </el-descriptions-item>
          </el-descriptions>
          <el-alert v-if="completeError" :title="completeError" type="error" :closable="false" class="mb-16" />
          <div class="btn-row">
            <el-button @click="step = 1">上一步</el-button>
            <el-button type="primary" size="large" :loading="completing" @click="finish">
              完成初始化
            </el-button>
          </div>
        </el-card>
      </template>

      <!-- Success -->
      <el-card v-else class="setup-card">
        <el-result icon="success" title="初始化完成 🎉">
          <template #sub-title>
            <p>{{ finishMessage }}</p>
          </template>
        </el-result>
        <el-alert v-if="generatedSecret" type="warning" :closable="false" class="mb-16">
          <p><strong>Webhook Secret（仅显示一次，请保存）：</strong></p>
          <div class="secret-row">
            <code>{{ generatedSecret }}</code>
            <el-button size="small" @click="copySecret">复制</el-button>
          </div>
          <p class="secret-hint">请在 Gitea 仓库/站点 Webhook 设置中填写此 Secret，用于签名验证。</p>
        </el-alert>
        <el-alert type="info" :closable="false" class="mb-16">
          <p>接下来：使用默认账号 <code>admin / admin123</code> 登录，系统会强制你修改密码。</p>
        </el-alert>
        <el-button type="primary" size="large" style="width: 100%" @click="goLogin">前往登录</el-button>
      </el-card>
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useSetupStore } from '../stores/setup'
import {
  verifySetupToken,
  detectLocalServices,
  testSetupGitea,
  testSetupLLM,
  completeSetup
} from '../api/setup'

const router = useRouter()
const setupStore = useSetupStore()

// --- token gate ---
const token = ref('')
const tokenVerified = ref(false)
const tokenError = ref('')
const verifying = ref(false)

async function verifyToken() {
  if (!token.value.trim()) {
    tokenError.value = '请输入 Setup Token'
    return
  }
  verifying.value = true
  tokenError.value = ''
  try {
    await verifySetupToken(token.value.trim())
    tokenVerified.value = true
  } catch (e) {
    tokenError.value = e.message || 'Token 验证失败'
  } finally {
    verifying.value = false
  }
}

// --- wizard state ---
const step = ref(0)
const finished = ref(false)
const completing = ref(false)
const completeError = ref('')
const finishMessage = ref('')
const generatedSecret = ref('')

const gitea = ref({ url: 'http://localhost:3000', token: '' })
const giteaTest = ref({ testing: false, ok: false, message: '' })

const presets = [
  { key: 'deepseek', label: 'DeepSeek（云端）', provider: 'deepseek', base_url: 'https://api.deepseek.com/v1', model: 'deepseek-v4-flash', type: 'openai_compatible' },
  { key: 'openai', label: 'OpenAI（云端）', provider: 'openai', base_url: 'https://api.openai.com/v1', model: 'gpt-4o-mini', type: 'openai_compatible' },
  { key: 'anthropic', label: 'Anthropic Claude（云端）', provider: 'claude', base_url: 'https://api.anthropic.com', model: 'claude-sonnet-4-5', type: 'anthropic' },
  { key: 'sensenova', label: 'SenseNova 商汤（云端）', provider: 'sensenova', base_url: 'https://api.sensenova.cn/compatible-mode/v1', model: 'deepseek-v4-flash', type: 'openai_compatible' },
  { key: 'ollama', label: 'Ollama（本地）', provider: 'ollama', base_url: 'http://localhost:11434/v1', model: '', type: 'openai_compatible' },
  { key: 'custom', label: '自定义…', provider: '', base_url: '', model: '', type: 'openai_compatible' }
]

const llm = ref({ preset: 'deepseek', provider: 'deepseek', base_url: 'https://api.deepseek.com/v1', api_key: '', model: 'deepseek-v4-flash', type: 'openai_compatible' })
const llmTest = ref({ testing: false, ok: false, message: '' })
const detect = ref({ loading: false, ollama: null, opencode: null })
let detectStarted = false

const isLocalBaseURL = computed(() => {
  const u = (llm.value.base_url || '').toLowerCase()
  return u.includes('localhost') || u.includes('127.0.0.1') || u.includes('://10.') || u.includes('://192.168.')
})

function applyPreset(key) {
  const p = presets.find((x) => x.key === key)
  if (!p) return
  llm.value.provider = p.provider
  llm.value.base_url = p.base_url
  llm.value.model = p.model
  llm.value.type = p.type
  llmTest.value = { testing: false, ok: false, message: '' }
}

function pickOllamaModel(name) {
  llm.value.preset = 'ollama'
  applyPreset('ollama')
  llm.value.model = name
  ElMessage.success(`已选择本地模型 ${name}`)
}

async function enterLLMStep() {
  if (detectStarted) return
  detectStarted = true
  detect.value.loading = true
  try {
    const res = await detectLocalServices(token.value.trim())
    detect.value = { loading: false, ollama: res.ollama, opencode: res.opencode }
    // Prefill Ollama when nothing was touched yet and a model is available.
    if (res.ollama?.ok && res.ollama.models?.length && !llm.value.api_key && llm.value.preset === 'deepseek') {
      // keep default cloud preset, just inform — user picks explicitly via tag click
    }
  } catch (e) {
    detect.value.loading = false
  }
}

async function testGitea() {
  giteaTest.value = { testing: true, ok: false, message: '' }
  try {
    const res = await testSetupGitea(token.value.trim(), gitea.value.url.trim(), gitea.value.token.trim())
    giteaTest.value = { testing: false, ok: !!res.ok, message: res.message || (res.ok ? '连接成功' : '连接失败') }
  } catch (e) {
    giteaTest.value = { testing: false, ok: false, message: e.payload?.message || e.message }
  }
}

async function testLLM() {
  llmTest.value = { testing: true, ok: false, message: '' }
  try {
    const res = await testSetupLLM(token.value.trim(), {
      type: llm.value.type,
      base_url: llm.value.base_url.trim(),
      api_key: llm.value.api_key.trim(),
      model: llm.value.model.trim()
    })
    llmTest.value = { testing: false, ok: !!res.ok, message: res.message || '连接成功' }
  } catch (e) {
    llmTest.value = { testing: false, ok: false, message: e.payload?.message || e.message }
  }
}

async function finish() {
  completing.value = true
  completeError.value = ''
  try {
    const res = await completeSetup(token.value.trim(), {
      gitea: { url: gitea.value.url.trim(), token: gitea.value.token.trim() },
      llm: {
        provider: llm.value.provider.trim(),
        model: llm.value.model.trim(),
        base_url: llm.value.base_url.trim(),
        api_key: llm.value.api_key.trim(),
        type: llm.value.type
      }
    })
    finishMessage.value = res.message || '配置已生效'
    if (res.webhook_secret_generated) {
      generatedSecret.value = res.webhook_secret
    }
    setupStore.markComplete()
    finished.value = true
  } catch (e) {
    completeError.value = e.payload?.message || e.message || '初始化失败'
  } finally {
    completing.value = false
  }
}

async function copySecret() {
  try {
    await navigator.clipboard.writeText(generatedSecret.value)
    ElMessage.success('已复制')
  } catch (e) {
    ElMessage.warning('复制失败，请手动选择复制')
  }
}

function goLogin() {
  router.push('/login')
}
</script>

<style scoped>
.setup-page {
  min-height: 100vh;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 40px 16px;
  box-sizing: border-box;
}

.setup-container {
  width: 100%;
  max-width: 640px;
}

.setup-header {
  text-align: center;
  color: #fff;
  margin-bottom: 24px;
}

.setup-header h1 {
  margin: 0 0 8px 0;
  font-size: 28px;
}

.setup-header p {
  margin: 0;
  opacity: 0.85;
}

.setup-steps {
  background: rgba(255, 255, 255, 0.9);
  border-radius: 8px;
  padding: 16px 8px;
  margin-bottom: 16px;
}

.setup-card {
  border-radius: 8px;
}

.mb-16 {
  margin-bottom: 16px;
}

.btn-row {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}

.detect-line {
  color: #909399;
  margin-bottom: 16px;
}

.detect-models {
  margin-top: 6px;
}

.model-tag {
  margin: 4px 6px 0 0;
  cursor: pointer;
}

.secret-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 8px 0;
}

.secret-row code {
  word-break: break-all;
  background: rgba(0, 0, 0, 0.06);
  padding: 6px 8px;
  border-radius: 4px;
}

.secret-hint {
  margin: 4px 0 0 0;
  color: #606266;
}
</style>

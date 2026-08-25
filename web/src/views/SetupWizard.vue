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
        <!-- C-21: import secrets already present in the environment -->
        <div class="env-import-bar">
          <el-button :loading="envDetect.loading" @click="openEnvImport">
            从环境变量导入配置
          </el-button>
          <span class="env-import-hint">若服务器已用环境变量配置 Gitea / LLM / Hub 密钥，可一键吸入（值不会显示）</span>
        </div>

        <el-dialog v-model="envDetect.dialogVisible" title="从环境变量导入" width="640px" :close-on-click-modal="false">
          <el-alert type="info" :closable="false" class="mb-16">
            勾选要吸入的环境变量，点击「应用」写入系统配置。变量值不会回显，仅写入对应配置项。
          </el-alert>
          <div v-loading="envDetect.loading">
            <el-empty v-if="!envDetect.loading && !envDetect.items.some(i => i.present)" description="未检测到已知环境变量" />
            <el-checkbox-group v-model="envDetect.selected">
              <div v-for="item in envDetect.items" :key="item.env" class="env-item">
                <el-checkbox :value="item.env" :disabled="!item.present">
                  <span class="env-name">{{ item.env }}</span>
                  <el-tag size="small" :type="item.present ? 'success' : 'info'">{{ item.present ? '已检测' : '未设置' }}</el-tag>
                  <span class="env-title">{{ item.title }}</span>
                </el-checkbox>
              </div>
            </el-checkbox-group>
          </div>
          <template #footer>
            <el-button @click="envDetect.dialogVisible = false">取消</el-button>
            <el-button type="primary" :loading="envDetect.applying" :disabled="envDetect.selected.length === 0" @click="applyEnvSelected">
              应用所选 ({{ envDetect.selected.length }})
            </el-button>
          </template>
        </el-dialog>

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
                placeholder="在 Gitea → 设置 → 应用 中生成"
                size="large"
              />
              <!-- 与后端 gitea.RequiredTokenScopes 保持一致（internal/gitea/user.go） -->
              <div class="form-tip">
                Gitea ≥1.22 细粒度权限，各 scope 相互独立，需逐项勾选：
                <div class="scope-list">
                  <div><code>read:user</code> — 验证 Token 身份、查询用户</div>
                  <div><code>write:repository</code> — 仓库 / 分支 / PR / 部署密钥（含读权限）</div>
                  <div><code>write:issue</code> — Issue 读取、评论与标签（含读权限）</div>
                  <div><code>write:admin</code> — 自动创建 Agent 账号、站点级 Webhook（需站点管理员账号；缺失则降级手动管理）</div>
                </div>
              </div>
            </el-form-item>
            <el-alert
              v-if="giteaTest.message"
              :title="giteaTest.message"
              :type="giteaTest.ok ? 'success' : 'error'"
              :closable="false"
              class="mb-16"
            />
            <ul v-if="giteaTest.checks.length" class="perm-checks mb-16">
              <li v-for="c in giteaTest.checks" :key="c.key">
                <span :class="['perm-icon', checkClass(c)]">{{ checkIcon(c) }}</span>
                <span>{{ c.label }}</span>
                <code class="perm-scope">{{ c.scope }}</code>
                <span v-if="c.detail" class="perm-detail">{{ c.detail }}</span>
              </li>
            </ul>
            <div class="btn-row">
              <el-button
                type="primary"
                :loading="giteaTest.testing"
                :disabled="!gitea.url || !gitea.token"
                @click="testGitea"
              >
                测试连接（含权限检查）
              </el-button>
              <el-button type="success" :disabled="!giteaPassed" @click="step = 1; enterLLMStep()">
                下一步
              </el-button>
            </div>
            <div v-if="!giteaPassed" class="form-tip next-tip">
              需先通过「测试连接」才能进入下一步——权限问题会在此处逐项列出，不必等到最后一步才发现。
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
              <div class="model-field">
                <el-select
                  v-model="llm.model"
                  filterable
                  allow-create
                  default-first-option
                  placeholder="选择或输入模型"
                  size="large"
                  style="flex: 1"
                >
                  <el-option v-for="m in modelOptions" :key="m.id" :label="m.name || m.id" :value="m.id" />
                </el-select>
                <el-button :loading="discovering" size="large" @click="discoverModels">拉取模型</el-button>
              </div>
              <div class="form-tip">填写 Base URL / 类型 / API Key 后点击「拉取模型」，按当前连接从 /models 接口拉取；也可直接输入自定义模型名。</div>
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
            <el-button v-if="completeError" @click="step = 0">返回第 1 步修改 Gitea 配置</el-button>
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
        <el-alert
          v-for="(w, i) in finishWarnings"
          :key="i"
          :title="'Gitea 权限警告：' + w"
          type="warning"
          :closable="false"
          class="mb-16"
        />
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
  completeSetup,
  getProviderPresets,
  discoverSetupModels,
  detectEnv,
  applyEnv
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
const finishWarnings = ref([])
const generatedSecret = ref('')

const gitea = ref({ url: 'http://localhost:3000', token: '' })
const giteaTest = ref({ testing: false, ok: false, message: '', checks: [] })
// Snapshot of the url/token pair that last passed the connection test;
// editing either field afterwards invalidates the pass.
const giteaTestedOK = ref(null)
const giteaPassed = computed(() =>
  !!giteaTestedOK.value &&
  giteaTestedOK.value.url === gitea.value.url.trim() &&
  giteaTestedOK.value.token === gitea.value.token.trim()
)

function checkIcon(c) {
  if (c.skipped) return '−'
  if (c.ok) return '✓'
  return c.required ? '✗' : '⚠'
}
function checkClass(c) {
  if (c.skipped) return 'perm-skip'
  if (c.ok) return 'perm-ok'
  return c.required ? 'perm-bad' : 'perm-warn'
}

// Presets are fetched from the backend (single source of truth, C-11); the
// static list below is only a fallback so the UI still renders if the endpoint
// is momentarily unreachable.
const fallbackPresets = [
  { key: 'deepseek', label: 'DeepSeek（云端）', provider: 'deepseek', base_url: 'https://api.deepseek.com/v1', model: 'deepseek-v4-flash', type: 'openai_compatible' },
  { key: 'openai', label: 'OpenAI（云端）', provider: 'openai', base_url: 'https://api.openai.com/v1', model: 'gpt-4o-mini', type: 'openai_compatible' },
  { key: 'anthropic', label: 'Anthropic Claude（云端）', provider: 'claude', base_url: 'https://api.anthropic.com', model: 'claude-sonnet-4-5', type: 'anthropic' },
  { key: 'sensenova', label: 'SenseNova 商汤（云端）', provider: 'sensenova', base_url: 'https://api.sensenova.cn/compatible-mode/v1', model: 'deepseek-v4-flash', type: 'openai_compatible' },
  { key: 'ollama', label: 'Ollama（本地）', provider: 'ollama', base_url: 'http://localhost:11434/v1', model: '', type: 'openai_compatible' },
  { key: 'custom', label: '自定义…', provider: '', base_url: '', model: '', type: 'openai_compatible' }
]
const presets = ref(fallbackPresets)

const llm = ref({ preset: 'deepseek', provider: 'deepseek', base_url: 'https://api.deepseek.com/v1', api_key: '', model: 'deepseek-v4-flash', type: 'openai_compatible' })
const llmTest = ref({ testing: false, ok: false, message: '' })
const detect = ref({ loading: false, ollama: null, opencode: null })
const modelOptions = ref([])
const discovering = ref(false)
let detectStarted = false

// C-21: absorb environment variables into config.
const envDetect = ref({
  loading: false,
  applying: false,
  dialogVisible: false,
  items: [],
  selected: []
})

async function openEnvImport() {
  envDetect.value.dialogVisible = true
  envDetect.value.loading = true
  envDetect.value.selected = []
  try {
    const res = await detectEnv(token.value.trim())
    envDetect.value.items = res.detected || []
  } catch (e) {
    ElMessage.error('环境变量检测失败：' + (e.message || e))
  } finally {
    envDetect.value.loading = false
  }
}

async function applyEnvSelected() {
  if (envDetect.value.selected.length === 0) return
  envDetect.value.applying = true
  try {
    const res = await applyEnv(token.value.trim(), envDetect.value.selected)
    if (res.errors && res.errors.length) {
      ElMessage.warning('部分应用失败：' + res.errors.join('；'))
    } else {
      ElMessage.success(res.message || '已应用')
      if (res.skipped && res.skipped.length) {
        ElMessage.info(`已跳过 ${res.skipped.length} 项：${res.skipped.join('；')}`)
      }
      if (res.note) {
        ElMessage.warning(res.note)
      }
    }
    envDetect.value.dialogVisible = false
    // 重新拉取初始化状态：若环境变量已补齐全部必需配置，可直接跳到确认步骤
    await setupStore.refresh()
    if (!setupStore.setupRequired) {
      step.value = 2
      ElMessage.success('所需配置已由环境变量补齐，可直接进入「确认完成」步骤')
    }
  } catch (e) {
    ElMessage.error('应用失败：' + (e.message || e))
  } finally {
    envDetect.value.applying = false
  }
}

const isLocalBaseURL = computed(() => {
  const u = (llm.value.base_url || '').toLowerCase()
  return u.includes('localhost') || u.includes('127.0.0.1') || u.includes('://10.') || u.includes('://192.168.')
})

function applyPreset(key) {
  const p = presets.value.find((x) => x.key === key)
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
  // Refresh provider presets from the backend (single source of truth, C-11).
  try {
    const pr = await getProviderPresets(token.value.trim())
    if (Array.isArray(pr.presets) && pr.presets.length) presets.value = pr.presets
  } catch (e) {
    // keep the static fallback already in `presets`
  }
}

async function testGitea() {
  giteaTest.value = { testing: true, ok: false, message: '', checks: [] }
  giteaTestedOK.value = null
  const url = gitea.value.url.trim()
  const tok = gitea.value.token.trim()
  try {
    const res = await testSetupGitea(token.value.trim(), url, tok)
    giteaTest.value = {
      testing: false,
      ok: !!res.ok,
      message: res.message || (res.ok ? '连接成功' : '连接失败'),
      checks: res.checks || []
    }
    if (res.ok) giteaTestedOK.value = { url, token: tok }
  } catch (e) {
    // 400 responses carry the same structured body (message + checks).
    giteaTest.value = {
      testing: false,
      ok: false,
      message: e.payload?.message || e.message,
      checks: e.payload?.checks || []
    }
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

// C-12: live-discover models from the current (unsaved) provider connection.
async function discoverModels() {
  if (!llm.value.base_url.trim()) {
    ElMessage.warning('请先填写 Base URL')
    return
  }
  discovering.value = true
  try {
    const res = await discoverSetupModels(token.value.trim(), {
      provider: llm.value.provider,
      base_url: llm.value.base_url.trim(),
      api_key: llm.value.api_key.trim(),
      type: llm.value.type
    })
    if (res.success && Array.isArray(res.models) && res.models.length) {
      modelOptions.value = res.models
      const ids = res.models.map((m) => m.id)
      if (!ids.includes(llm.value.model)) llm.value.model = res.models[0].id
      ElMessage.success(`已拉取 ${res.models.length} 个模型`)
    } else if (res.error) {
      ElMessage.warning(`拉取失败：${res.error}`)
    } else {
      ElMessage.info('未返回模型列表')
    }
  } catch (e) {
    ElMessage.warning(e.payload?.error || e.message || '拉取失败')
  } finally {
    discovering.value = false
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
    finishWarnings.value = res.gitea_warnings || []
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

.model-field {
  display: flex;
  gap: 12px;
  align-items: center;
  width: 100%;
}

.form-tip {
  font-size: 12px;
  color: #909399;
  margin-top: 6px;
  line-height: 1.5;
}

.next-tip {
  margin-top: 10px;
  text-align: right;
}

.scope-list {
  margin-top: 4px;
}

.scope-list code,
.perm-scope {
  background: rgba(0, 0, 0, 0.06);
  padding: 1px 4px;
  border-radius: 3px;
}

.perm-checks {
  list-style: none;
  margin: 0;
  padding: 0;
  font-size: 13px;
}

.perm-checks li {
  display: flex;
  align-items: baseline;
  gap: 6px;
  flex-wrap: wrap;
  padding: 3px 0;
}

.perm-icon {
  font-weight: 700;
}

.perm-ok {
  color: #67c23a;
}

.perm-bad {
  color: #f56c6c;
}

.perm-warn {
  color: #e6a23c;
}

.perm-skip {
  color: #909399;
}

.perm-detail {
  color: #909399;
  flex-basis: 100%;
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

.env-import-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  flex-wrap: wrap;
}
.env-import-hint {
  color: #909399;
  font-size: 13px;
}
.env-item {
  padding: 6px 0;
}
.env-name {
  font-family: monospace;
  font-weight: 600;
  margin-right: 8px;
}
.env-title {
  color: #909399;
  font-size: 13px;
  margin-left: 8px;
}
</style>

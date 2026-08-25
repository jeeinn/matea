<template>
  <div class="agents-page">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>Agent 管理</span>
          <el-button type="primary" @click="openCreateDialog">
            <el-icon><Plus /></el-icon>
            创建 Agent
          </el-button>
        </div>
      </template>

      <el-table :data="paginatedAgents" style="width: 100%">
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="name" label="名称">
          <template #default="{ row }">
            <el-link type="primary" @click="router.push(`/agents/${row.id}`)">{{ row.name }}</el-link>
          </template>
        </el-table-column>
        <el-table-column prop="gitea_username" label="Gitea 用户" />
        <el-table-column label="角色" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.role === 'analyze'" type="primary" size="small">分析</el-tag>
            <el-tag v-else-if="row.role === 'coder'" type="success" size="small">开发</el-tag>
            <el-tag v-else-if="row.role === 'review'" type="warning" size="small">审查</el-tag>
            <span v-else class="text-muted">-</span>
          </template>
        </el-table-column>
        <el-table-column prop="provider" label="Provider" width="100" />
        <el-table-column prop="model" label="模型" />
        <el-table-column prop="backend" label="Backend" width="120">
          <template #default="{ row }">
            <el-tag size="small" :type="row.backend === 'builtin' ? 'info' : 'primary'">
              {{ row.backend || 'builtin' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="status" label="状态" width="80">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'info'" size="small">{{ row.status === 'active' ? '活跃' : '禁用' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="250">
          <template #default="{ row }">
            <el-button size="small" type="primary" link @click="router.push(`/agents/${row.id}`)">详情</el-button>
            <el-button size="small" @click="editAgent(row)">编辑</el-button>
            <el-button size="small" type="danger" @click="deleteAgent(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="pagination-bar">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :total="agents.length"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next"
          small
        />
      </div>
    </el-card>

    <!-- Create/Edit Dialog -->
    <el-dialog v-model="showCreateDialog" :title="editingAgent ? '编辑 Agent' : '创建 Agent'" width="700px" top="5vh"
      :close-on-click-modal="false" :close-on-press-escape="false">
      <el-form :model="form" label-width="120px">
        <!-- 基本信息 -->
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="如 code-reviewer" />
        </el-form-item>
        <el-form-item label="Gitea 用户名">
          <el-input v-model="form.gitea_username" :disabled="!!editingAgent" placeholder="自动创建 Gitea 账号" />
          <div v-if="editingAgent" class="form-tip">创建后不可修改</div>
        </el-form-item>
        <el-form-item label=" ">
          <el-checkbox v-model="form.take_over_gitea_user">接管已存在的 Gitea 账号</el-checkbox>
          <div class="form-tip">仅当确认该账号可交由 Matea 管理时勾选：将通过管理员 API 重置其密码并生成新 Token；不勾选时遇到同名非托管账号会报错（防劫持保护）</div>
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="form.role" placeholder="选择角色" style="width: 100%" @change="onRoleChange">
            <el-option label="分析 (analyze)" value="analyze" />
            <el-option label="开发 (coder)" value="coder" />
            <el-option label="审查 (review)" value="review" />
          </el-select>
          <div class="form-tip">角色决定 Assign 后的行为：分析=只读分析，开发=读写代码，审查=只读审查</div>
          <el-button
            v-if="!editingAgent"
            type="primary"
            link
            size="small"
            @click="applyRoleWizard(form.role)"
            style="margin-top: 8px"
          >
            <el-icon><MagicStick /></el-icon>
            一键填充 {{ roleLabel(form.role) }} 向导（命名 + Prompt）
          </el-button>
        </el-form-item>
        <el-form-item label="Coding Backend">
          <el-select v-model="form.backend" placeholder="选择后端" style="width: 100%" @change="onBackendChange">
            <el-option
              v-for="b in backends"
              :key="b.name"
              :label="`${b.name} (${backendTypeLabel(b.type)})`"
              :value="b.name"
            />
          </el-select>
          <div class="form-tip">
            编码阶段的执行后端。builtin 为内置 AgentLoop，hub-opencode 为远程 OpenCode 服务。切换后端会清空下方后端专属配置。
          </div>
        </el-form-item>
        <el-form-item label="关联仓库">
          <el-select v-model="form.repos" multiple filterable placeholder="选择仓库（可多选）" style="width: 100%">
            <el-option v-for="r in repoList" :key="r.full_name" :label="r.full_name" :value="r.full_name" />
          </el-select>
          <div class="form-tip">自动将 Agent 添加为仓库协作者（用于创建 PR）。也可以在 Gitea 仓库设置 → 协作者中手动添加</div>
          <el-alert v-if="!form.repos || form.repos.length === 0" title="Agent 需要至少关联一个仓库才能获得协作者权限，用于创建 PR" type="warning" :closable="false" show-icon style="margin-top: 8px" />
        </el-form-item>
        <!-- builtin 后端：Provider / Model 来自系统 LLM 配置（llm.providers） -->
        <el-form-item label="Provider" v-if="isBuiltinBackend">
          <el-col :span="11">
            <el-select
              v-model="form.provider"
              placeholder="选择 Provider"
              style="width: 100%"
              @change="onProviderChange"
            >
              <el-option v-for="name in effectiveProviderNames(form.provider)" :key="name" :label="name" :value="name" />
            </el-select>
          </el-col>
          <el-col :span="2" style="text-align: center; line-height: 32px">:</el-col>
          <el-col :span="11">
            <div class="model-select-wrapper">
              <el-select
                v-model="form.model"
                placeholder="选择模型"
                style="width: 100%"
                filterable
                allow-create
                default-first-option
                :loading="modelLoading"
              >
                <template #header>
                  <div class="model-source-hint">
                    <el-tag v-if="modelSource === 'api'" size="small" type="primary">API 发现</el-tag>
                    <el-tag v-else-if="modelSource === 'builtin'" size="small" type="info">内置目录</el-tag>
                    <el-tag v-else-if="modelSource === 'custom'" size="small" type="success">自定义</el-tag>
                    <el-tag v-else size="small" type="warning">未配置</el-tag>
                    <span v-if="modelError" class="model-error" :title="modelError">获取失败</span>
                  </div>
                </template>
                <el-option
                  v-for="m in currentModels"
                  :key="m.id"
                  :label="m.id"
                  :value="m.id"
                >
                  <div class="model-option">
                    <span class="model-option-id">{{ m.id }}</span>
                    <span class="model-tags">
                      <el-tag v-if="m.is_reasoning" size="small" type="warning" class="model-tag">推理</el-tag>
                      <el-tag v-if="m.supports_tools" size="small" type="success" class="model-tag">工具</el-tag>
                      <el-tag v-if="m.context_window" size="small" type="info" class="model-tag">{{ formatContextWindow(m.context_window) }}</el-tag>
                    </span>
                  </div>
                </el-option>
                <template #empty>
                  <div class="model-empty">
                    <p v-if="modelLoading">加载中...</p>
                    <p v-else-if="modelError">{{ modelError }}</p>
                    <p v-else>暂无可用模型，可直接输入模型 ID</p>
                  </div>
                </template>
              </el-select>
              <el-button
                :loading="modelLoading"
                :disabled="!form.provider"
                size="small"
                @click="refreshModels"
                style="margin-left: 8px"
              >
                <el-icon><Refresh /></el-icon>
              </el-button>
            </div>
          </el-col>
          <el-alert
            v-if="form.role === 'coder' && selectedModelMeta && !selectedModelMeta.supports_tools"
            title="开发角色需要工具调用"
            type="warning"
            :closable="false"
            show-icon
            style="margin-top: 8px; width: 100%"
          />
        </el-form-item>

        <!-- hub-opencode 后端：仅覆盖提交到 OpenCode 的模型/Provider（服务端实际消费的键） -->
        <template v-if="isHubOpenCode">
          <el-alert type="info" :closable="false" style="margin-bottom: 12px">
            <template #title>
              连接配置（URL / 鉴权 / 工作区模式）在服务器端 agents.backends.&lt;后端名&gt; 中按命名后端统一设置，
              此处仅可覆盖提交到 OpenCode 的模型与 Provider。
            </template>
          </el-alert>
          <el-form-item label="OpenCode 模型">
            <el-input v-model="form.backend_options.opencode_model" placeholder="覆盖 opencode 端 model（可选）" />
          </el-form-item>
          <el-form-item label="OpenCode Provider">
            <el-input v-model="form.backend_options.opencode_provider" placeholder="覆盖 opencode 端 provider（可选）" />
          </el-form-item>
          <el-form-item label="OpenCode Agent">
            <el-input v-model="form.backend_options.opencode_agent" placeholder="提示性：opencode agent 名（可选）" />
          </el-form-item>
        </template>

        <!-- Prompt -->
        <el-form-item label="从模板导入">
          <el-select v-model="selectedTemplate" placeholder="选择内置模板快速填充" @change="applyTemplate" clearable style="width: 100%">
            <el-option v-for="tmpl in builtinTemplates" :key="tmpl.name" :label="tmpl.name" :value="tmpl.name" />
          </el-select>
        </el-form-item>
        <!-- System Prompt / User Template 为 Agent 级人设与指令，所有后端均显示 -->
        <el-form-item label="System Prompt">
          <el-input v-model="form.system_prompt" type="textarea" :rows="5" placeholder="Agent 的系统提示词" />
        </el-form-item>

        <!-- 折叠：高级配置 -->
        <el-collapse v-model="advancedOpen">
          <el-collapse-item title="高级配置" name="advanced">
            <!-- hub-hermes 后端连接配置（Phase 2 支持；仅 hub-hermes 显示） -->
            <template v-if="isHubHermes">
              <el-divider content-position="left">Hermes 后端（Phase 2，未接入服务端，仅占位）</el-divider>
              <el-form-item label="Hermes URL">
                <el-input v-model="form.backend_options.url" placeholder="https://hermes.example.com" />
              </el-form-item>
              <el-form-item label="Skill">
                <el-input v-model="form.backend_options.skill" placeholder="如 dev" />
              </el-form-item>
              <el-form-item label="API Key">
                <el-input v-model="form.backend_options.api_key" type="password" show-password />
              </el-form-item>
              <el-form-item label="记忆键 (Memory Keys)">
                <el-input v-model="form.backend_options.memory_keys" type="textarea" :rows="2" placeholder="逗号分隔，如 user:123,repo:owner/foo" />
              </el-form-item>
            </template>
            <el-form-item label="状态">
              <el-select v-model="form.status">
                <el-option label="活跃" value="active" />
                <el-option label="禁用" value="inactive" />
              </el-select>
            </el-form-item>
            <el-divider content-position="left">LLM Token（可选覆盖）</el-divider>
            <el-alert type="info" :closable="false" style="margin-bottom: 16px">
              <template #title>
                设为 0 表示不覆盖：按所选模型自动适配（输入≈上下文 90%，输出=模型上限）。
                无模型元数据时回退系统默认（输入 {{ systemDefaultInput }} / 输出 {{ systemDefaultOutput }}）。
                字段为整数，无法真·留空，0 即「自动」。
              </template>
            </el-alert>
            <el-form-item label="最大输出 Tokens">
              <el-input-number v-model="form.max_output_tokens" :min="0" :max="128000" :step="512" />
              <div class="form-tip">
                <template v-if="selectedModelMeta?.max_output">
                  当前为 0 → 使用模型上限 {{ formatContextWindow(selectedModelMeta.max_output) }}；填正数则覆盖（不超过该上限）
                </template>
                <template v-else>
                  当前为 0 → 无模型元数据时使用系统默认 {{ systemDefaultOutput }}
                </template>
              </div>
            </el-form-item>
            <el-form-item label="最大输入 Tokens">
              <el-input-number v-model="form.max_input_tokens" :min="0" :max="2000000" :step="1024" />
              <div class="form-tip">
                <template v-if="selectedModelMeta?.context_window">
                  当前为 0 → 自动使用 {{ Math.floor(selectedModelMeta.context_window * 0.9).toLocaleString() }}（模型上下文 {{ formatContextWindow(selectedModelMeta.context_window) }} 的 90%）
                </template>
                <template v-else>
                  当前为 0 → 无模型元数据时使用系统默认 {{ systemDefaultInput }}
                </template>
              </div>
            </el-form-item>
            <el-form-item label="Temperature" v-if="isBuiltinBackend">
              <el-slider v-model="form.temperature" :min="0" :max="2" :step="0.1" show-input style="width: 100%" />
              <div v-if="selectedModelMeta?.default_params?.temperature !== undefined" class="form-tip">
                模型默认 {{ selectedModelMeta.default_params.temperature }}
              </div>
            </el-form-item>
            <el-form-item label="单次任务超时">
              <el-input v-model="form.timeout" placeholder="20m" style="width: 200px" />
            </el-form-item>
            <el-form-item label="User Template">
              <el-input v-model="form.user_template" type="textarea" :rows="3" placeholder="用户消息模板（可选）" />
              <el-button type="primary" link size="small" style="margin-top: 4px" @click="$refs.templateHelp.show()">查看模板变量说明</el-button>
            </el-form-item>
          </el-collapse-item>

          <el-collapse-item v-if="isBuiltinBackend && form.role === 'coder'" title="Agent Loop 配置" name="loop">
            <el-form-item label="最大迭代轮数">
              <el-input-number v-model="form.loop_config.max_iterations" :min="1" :max="100" :step="1" />
              <div class="form-tip">默认 20</div>
            </el-form-item>
            <el-form-item label="Loop 总超时">
              <el-input v-model="form.loop_config.total_timeout" placeholder="30m" style="width: 200px" />
            </el-form-item>
            <el-form-item label="轮次间隔">
              <el-input-number v-model="form.loop_config.iteration_interval" :min="0" :max="300" :step="1" />
              <div class="form-tip">秒；每轮 Loop 之间的等待时间，0 表示不等待</div>
            </el-form-item>

            <el-divider content-position="left">Harness 验证门禁</el-divider>
            <el-form-item label="无进展退出上限">
              <el-input-number v-model="form.loop_config.no_progress_limit" :min="0" :max="100" />
              <div class="form-tip">0 = 关闭检测（继承系统默认）</div>
            </el-form-item>
            <el-form-item label="覆盖系统校验命令">
              <el-switch v-model="form.loop_config.verify_commands_override" />
              <div class="form-tip">关闭时继承系统默认校验命令；开启后可自定义（留空 = 禁用校验）</div>
            </el-form-item>
            <el-form-item v-if="form.loop_config.verify_commands_override" label="校验命令">
              <el-input
                v-model="form.loop_config.verify_commands_text"
                type="textarea"
                :rows="4"
                placeholder='每行一条命令，例如：
go test ./...
npm test'
              />
              <div class="form-tip">每行一条 shell 命令；编码后、commit/PR 前执行；留空 = 禁用校验</div>
            </el-form-item>
          </el-collapse-item>
        </el-collapse>
      </el-form>
      <template #footer>
        <el-button @click="closeDialog">取消</el-button>
        <el-button type="primary" @click="saveAgent">保存</el-button>
      </template>
    </el-dialog>
    <TemplateHelp ref="templateHelp" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import api from '../api'
import { Plus, Refresh, MagicStick } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import TemplateHelp from '../components/TemplateHelp.vue'
import { useAgentDefaults, DEFAULT_AGENT_MAX_OUTPUT_TOKENS, DEFAULT_AGENT_MAX_INPUT_TOKENS } from '../composables/useAgentDefaults'

const router = useRouter()
const {
  loadAgentConfig,
  effectiveProviderNames,
  createEmptyAgentForm,
  loopDefaults,
  defaultLoopConfig,
  backends
} = useAgentDefaults()

const systemDefaultOutput = DEFAULT_AGENT_MAX_OUTPUT_TOKENS
const systemDefaultInput = DEFAULT_AGENT_MAX_INPUT_TOKENS
const agents = ref([])
const currentPage = ref(1)
const pageSize = ref(20)

const paginatedAgents = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return agents.value.slice(start, start + pageSize.value)
})
const showCreateDialog = ref(false)
const editingAgent = ref(null)
const builtinTemplates = ref([])
const selectedTemplate = ref('')
const advancedOpen = ref([])
const repoList = ref([])

const form = ref(createEmptyAgentForm())

// Model selection state
const currentModels = ref([])
const modelLoading = ref(false)
const modelSource = ref('')
const modelError = ref('')
const modelCatalog = ref({})

// 当前选中后端的类型，来自服务端 _meta.backends 的 type 字段（builtin | hub-opencode | hub-hermes）。
// 据此决定 Agent 编辑表单显示哪一组字段。
const selectedBackendType = computed(() => {
  const b = backends.value.find(x => x.name === form.value.backend)
  return b?.type || 'builtin'
})
const isBuiltinBackend = computed(() => selectedBackendType.value === 'builtin')
const isHubOpenCode = computed(() => selectedBackendType.value === 'hub-opencode')
const isHubHermes = computed(() => selectedBackendType.value === 'hub-hermes')

// 切换后端时清空后端专属配置：不同后端的 backend_options 键集不同，跨类型保留无意义。
const onBackendChange = () => {
  form.value.backend_options = {}
}

/** Normalize API/catalog model objects to always expose official id. */
const normalizeModels = (list) => {
  if (!Array.isArray(list)) return []
  return list
    .map((m) => {
      if (!m || typeof m !== 'object') return null
      const id = String(m.id || m.ID || m.Id || '').trim()
      if (!id) return null
      return {
        ...m,
        id,
        name: m.name || m.Name || id,
        context_window: m.context_window || m.ContextWindow || 0,
        max_output: m.max_output || m.MaxOutput || 0,
        supports_tools: !!(m.supports_tools ?? m.SupportsTools),
        is_reasoning: !!(m.is_reasoning ?? m.IsReasoning),
        default_params: m.default_params || m.DefaultParams || undefined
      }
    })
    .filter(Boolean)
}

const selectedModelMeta = computed(() => {
  if (!form.value.model || !form.value.provider) return null
  const models = currentModels.value.length
    ? currentModels.value
    : normalizeModels(modelCatalog.value[form.value.provider] || [])
  return models.find(m => m.id === form.value.model) || null
})

const formatContextWindow = (n) => {
  if (n >= 1000) return (n / 1000).toFixed(0) + 'K'
  return n.toString()
}

const backendTypeLabel = (type) => {
  if (type === 'builtin') return '内置'
  if (type === 'hub-opencode') return 'OpenCode'
  if (type === 'hub-hermes') return 'Hermes'
  return type
}

const roleLabel = (role) => {
  if (role === 'analyze') return '分析'
  if (role === 'coder') return '开发'
  if (role === 'review') return '审查'
  return ''
}

const onRoleChange = (role) => {
  if (!editingAgent.value && !form.value.name) {
    // 使用 matea-* 命名，避免 matea 单独作为总入口的误解
    form.value.name = role === 'analyze' ? 'matea-analyst' : role === 'coder' ? 'matea-coder' : 'matea-review'
  }
}

const applyRoleWizard = (role) => {
  let templateName = ''
  let name = ''

  if (role === 'analyze') {
    templateName = 'analyze_issue'
    name = 'matea-analyst'
  } else if (role === 'coder') {
    templateName = 'solve_issue'
    name = 'matea-coder'
  } else if (role === 'review') {
    templateName = 'review_pr'
    name = 'matea-review'
  }

  if (templateName) {
    const tmpl = builtinTemplates.value.find(t => t.name === templateName)
    if (tmpl) {
      form.value.system_prompt = tmpl.system_prompt || ''
      form.value.user_template = tmpl.user_template || ''
    }
  }
  form.value.name = name
  // 设置 backend 为 builtin（与 1.2.6 的归一化函数配合，无顺序依赖）
  form.value.backend = 'builtin'
  // 设置 gitea_username 为与 name 相同（配合任务 1.1.3 的自动映射）
  form.value.gitea_username = name
  ElMessage.success(`已应用 ${roleLabel(role)} 向导：命名 + Backend + Gitea 用户名 + 默认 Prompt`)
}

const loadModelCatalog = async () => {
  try {
    const data = await api.get('/config')
    if (data?._meta?.models) {
      const catalog = {}
      for (const [provider, list] of Object.entries(data._meta.models)) {
        catalog[provider] = normalizeModels(list)
      }
      modelCatalog.value = catalog
    }
  } catch {
    modelCatalog.value = {}
  }
}

const loadModelsForProvider = async (providerName) => {
  if (!providerName) {
    currentModels.value = []
    modelSource.value = ''
    modelError.value = ''
    return
  }
  modelError.value = ''
  // Optimistic UI from catalog while API resolves (supports models:[] discovery)
  if (modelCatalog.value[providerName]?.length) {
    currentModels.value = normalizeModels(modelCatalog.value[providerName])
    modelSource.value = 'builtin'
  }
  modelLoading.value = true
  try {
    const data = await api.get(`/config/providers/${providerName}/models`)
    if (data?.models) {
      currentModels.value = normalizeModels(data.models)
      modelSource.value = data.source || 'builtin'
      if (!data.success && data.error) {
        modelError.value = data.error
      }
    } else if (!currentModels.value.length) {
      currentModels.value = []
      modelSource.value = ''
    }
  } catch (err) {
    if (!currentModels.value.length) {
      currentModels.value = []
      modelSource.value = ''
    }
    modelError.value = err.response?.data?.error || err.message || '加载失败'
  } finally {
    modelLoading.value = false
  }
}

const onProviderChange = (providerName) => {
  form.value.model = ''
  loadModelsForProvider(providerName)
}

const refreshModels = async () => {
  if (!form.value.provider) return
  modelError.value = ''
  modelLoading.value = true
  try {
    const data = await api.get(`/config/providers/${form.value.provider}/models`)
    if (data?.models) {
      currentModels.value = normalizeModels(data.models)
      modelSource.value = data.source || 'builtin'
      if (!data.success && data.error) {
        modelError.value = data.error
        ElMessage.warning(`获取模型列表失败：${data.error}`)
      } else {
        ElMessage.success(`已获取 ${data.models.length} 个模型`)
      }
    }
  } catch (err) {
    modelError.value = err.response?.data?.error || err.message || '加载失败'
    ElMessage.error(modelError.value)
  } finally {
    modelLoading.value = false
  }
}

const loadAgents = async () => {
  agents.value = await api.get('/agents')
}

const loadTemplates = async () => {
  try {
    const data = await api.get('/prompt-templates')
    if (data && typeof data === 'object') {
      builtinTemplates.value = Object.entries(data).map(([key, value]) => ({
        name: key,
        ...value
      }))
    }
  } catch {
    builtinTemplates.value = []
  }
}

const loadRepos = async () => {
  try {
    repoList.value = await api.get('/repos') || []
  } catch {
    repoList.value = []
  }
}

const loadProviders = loadAgentConfig

const applyTemplate = (name) => {
  if (!name) return
  const tmpl = builtinTemplates.value.find(t => t.name === name)
  if (tmpl) {
    form.value.system_prompt = tmpl.system_prompt || ''
    form.value.user_template = tmpl.user_template || ''
    ElMessage.success(`已应用模板：${name}`)
  }
}

const openCreateDialog = async () => {
  await loadAgentConfig()
  await loadModelCatalog()
  editingAgent.value = null
  form.value = createEmptyAgentForm()
  selectedTemplate.value = ''
  showCreateDialog.value = true
  await loadModelsForProvider(form.value.provider)
}

const editAgent = async (agent) => {
  await loadAgentConfig()
  await loadModelCatalog()
  editingAgent.value = agent
  // Override must be derived from the agent's stored loop_config only.
  // Merging system defaults first would inject verify_commands and force the switch on.
  const agentLoop = agent.loop_config || {}
  const hasVerifyOverride = agentLoop.verify_commands !== null && agentLoop.verify_commands !== undefined
  const loopConfig = { ...loopDefaults.value, ...defaultLoopConfig, ...agentLoop }
  if (hasVerifyOverride) {
    loopConfig.verify_commands_override = true
    loopConfig.verify_commands_text = Array.isArray(agentLoop.verify_commands)
      ? agentLoop.verify_commands.join('\n')
      : ''
  } else {
    loopConfig.verify_commands_override = false
    loopConfig.verify_commands_text = ''
    delete loopConfig.verify_commands
  }
  form.value = {
    ...agent,
    loop_config: loopConfig,
    backend_options: agent.backend_options || {}
  }
  selectedTemplate.value = ''
  showCreateDialog.value = true
  await loadModelsForProvider(form.value.provider)
}

const closeDialog = () => {
  showCreateDialog.value = false
  editingAgent.value = null
  form.value = createEmptyAgentForm()
}

const saveAgent = async () => {
  try {
    const payload = { ...form.value }
    payload.loop_config = { ...payload.loop_config }
    // 仅 hub 后端需要 backend_options；builtin 省略以避免清空既有值
    if (isBuiltinBackend.value) {
      delete payload.backend_options
    } else {
      payload.backend_options = form.value.backend_options || {}
    }
    if (payload.loop_config.verify_commands_override) {
      payload.loop_config.verify_commands = payload.loop_config.verify_commands_text
        ? payload.loop_config.verify_commands_text.split('\n').map(s => s.trim()).filter(Boolean)
        : []
    } else {
      delete payload.loop_config.verify_commands
    }
    delete payload.loop_config.verify_commands_override
    delete payload.loop_config.verify_commands_text

    if (editingAgent.value) {
      await api.put(`/agents/${editingAgent.value.id}`, payload)
      ElMessage.success('更新成功')
    } else {
      const res = await api.post('/agents', payload)
      if (res?.repo_warnings?.length > 0) {
        ElMessage.warning(`创建成功，但部分仓库关联失败：${res.repo_warnings.join('; ')}`)
      } else {
        ElMessage.success('创建成功')
      }
    }
    closeDialog()
    loadAgents()
  } catch (error) {
    ElMessage.error(error.response?.data?.error || '操作失败')
  }
}

const deleteAgent = async (agent) => {
  try {
    await ElMessageBox.confirm('确定要删除这个 Agent 吗？', '确认')
    await api.delete(`/agents/${agent.id}`)
    ElMessage.success('删除成功')
    loadAgents()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('删除失败')
    }
  }
}

onMounted(() => {
  loadAgents()
  loadTemplates()
  loadProviders()
  loadRepos()
})
</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.form-tip {
  font-size: 12px;
  color: #909399;
  margin-top: 6px;
  line-height: 1.5;
}

/* el-form-item__content 为 flex；提示单独占一行，避免贴在输入控件右侧 */
.el-form-item__content > .form-tip {
  flex-basis: 100%;
  width: 100%;
}

.text-muted {
  font-size: 12px;
  color: #c0c4cc;
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.model-source-hint {
  padding: 4px 12px;
  border-bottom: 1px solid #e4e7ed;
}

.model-select-wrapper {
  display: flex;
  align-items: center;
}

.model-option {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.model-option-id {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
}

.model-tags {
  margin-left: auto;
  display: inline-flex;
  gap: 4px;
}

.model-tag {
  margin-left: 0;
}
</style>

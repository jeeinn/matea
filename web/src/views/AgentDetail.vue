<template>
  <div class="agent-detail-page">
    <el-page-header @back="router.push('/agents')" :title="'返回 Agent 列表'">
      <template #content>
        <span class="page-title">{{ agent?.name || '加载中...' }}</span>
        <el-tag v-if="agent?.status === 'active'" type="success" size="small" style="margin-left: 12px">活跃</el-tag>
        <el-tag v-else type="info" size="small" style="margin-left: 12px">禁用</el-tag>
      </template>
    </el-page-header>

    <el-tabs v-if="agent" v-model="activeTab" style="margin-top: 20px">
      <!-- Tab 1: 基本信息 -->
      <el-tab-pane label="基本信息" name="info">
        <el-card>
          <el-form :model="form" label-width="140px" style="max-width: 700px">
            <el-divider content-position="left">基本信息</el-divider>
            <el-form-item label="名称">
              <el-input v-model="form.name" />
            </el-form-item>
            <el-form-item label="状态">
              <el-select v-model="form.status">
                <el-option label="活跃" value="active" />
                <el-option label="禁用" value="inactive" />
              </el-select>
            </el-form-item>
            <el-form-item label="角色">
              <el-select v-model="form.role" style="width: 100%">
                <el-option label="分析 (analyze)" value="analyze" />
                <el-option label="开发 (coder)" value="coder" />
                <el-option label="审查 (review)" value="review" />
              </el-select>
              <div class="form-tip">Assign Agent 后的角色行为</div>
            </el-form-item>
            <el-form-item label="Gitea 用户">
              <el-input :model-value="form.gitea_username" disabled />
            </el-form-item>
            <el-form-item label="关联仓库">
              <el-select v-model="form.repos" multiple filterable placeholder="选择仓库（可多选）" style="width: 100%">
                <el-option v-for="r in repoList" :key="r.full_name" :label="r.full_name" :value="r.full_name" />
              </el-select>
              <div class="form-tip">自动将 Agent 添加为仓库协作者（用于创建 PR）。也可以在 Gitea 仓库设置 → 协作者中手动添加</div>
              <el-alert v-if="!form.repos || form.repos.length === 0" title="Agent 需要至少关联一个仓库才能获得协作者权限，用于创建 PR" type="warning" :closable="false" show-icon style="margin-top: 8px" />
            </el-form-item>

            <el-divider content-position="left">LLM 配置</el-divider>
            <el-form-item label="Provider">
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
                    placeholder="选择模型 ID"
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
                  </el-select>
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
            <el-form-item label="最大输出 Tokens">
              <el-input-number v-model="form.max_output_tokens" :min="0" :max="128000" :step="512" />
              <div class="form-tip">每次调用上限。设为 0：按模型上限自动适配；无元数据时回退系统默认 8192</div>
            </el-form-item>
            <el-form-item label="最大输入 Tokens">
              <el-input-number v-model="form.max_input_tokens" :min="0" :max="2000000" :step="1024" />
              <div class="form-tip">设为 0：按模型上下文 90% 自动适配；无元数据时回退系统默认 115200</div>
            </el-form-item>
            <el-form-item label="Temperature">
              <el-slider v-model="form.temperature" :min="0" :max="2" :step="0.1" show-input style="width: 100%" />
            </el-form-item>
            <el-form-item label="单次任务超时">
              <el-input v-model="form.timeout" placeholder="20m" style="width: 200px" />
            </el-form-item>

            <template v-if="form.role === 'coder'">
              <el-divider content-position="left">Agent Loop</el-divider>
              <el-form-item label="最大迭代轮数">
                <el-input-number v-model="form.loop_config.max_iterations" :min="1" :max="100" />
              </el-form-item>
              <el-form-item label="Loop 总超时">
                <el-input v-model="form.loop_config.total_timeout" placeholder="30m" />
              </el-form-item>
              <el-form-item label="轮次间隔">
                <el-input-number v-model="form.loop_config.iteration_interval" :min="0" :max="300" :step="1" />
                <div class="form-tip">每轮 Loop 之间的等待秒数，0 表示不等待</div>
              </el-form-item>

              <el-divider content-position="left">Harness 验证门禁</el-divider>
              <el-form-item label="无进展退出上限">
                <el-input-number v-model="form.loop_config.no_progress_limit" :min="0" :max="100" />
                <div class="form-tip">覆盖系统默认值；0 = 关闭检测</div>
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
            </template>

            <el-divider content-position="left">Prompt</el-divider>
            <el-form-item label="System Prompt">
              <el-input v-model="form.system_prompt" type="textarea" :rows="6" />
            </el-form-item>
            <el-form-item label="User Template">
              <el-input v-model="form.user_template" type="textarea" :rows="4" />
              <el-button type="primary" link size="small" style="margin-top: 4px" @click="$refs.templateHelp.show()">查看模板变量说明</el-button>
            </el-form-item>

            <el-form-item label=" ">
              <el-button type="primary" :loading="saving" @click="saveAgent">保存修改</el-button>
            </el-form-item>
          </el-form>
        </el-card>
      </el-tab-pane>

      <!-- Tab 2: Prompt 版本 -->
      <el-tab-pane label="Prompt 版本" name="prompts">
        <el-card>
          <el-empty v-if="!prompts.length" description="暂无 Prompt 版本记录" />
          <el-table v-else :data="paginatedPrompts" style="width: 100%">
            <el-table-column prop="version" label="版本" width="80" />
            <el-table-column prop="note" label="备注" />
            <el-table-column prop="is_active" label="状态" width="100">
              <template #default="{ row }">
                <el-tag :type="row.is_active ? 'success' : 'info'" size="small">{{ row.is_active ? '活跃' : '历史' }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="created_at" label="创建时间" width="180" />
            <el-table-column label="操作" width="180">
              <template #default="{ row }">
                <el-button size="small" type="primary" link @click="viewPromptDetail(row)">详情</el-button>
                <el-button v-if="!row.is_active" size="small" @click="rollbackPrompt(row)">回滚</el-button>
                <el-button size="small" type="danger" @click="deletePrompt(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
          <div v-if="prompts.length > 10" class="pagination-bar">
            <el-pagination v-model:current-page="promptPage" :page-size="10" :total="prompts.length" layout="prev, pager, next" small />
          </div>
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <!-- Prompt 详情对话框 -->
    <el-dialog v-model="showPromptDetail" title="Prompt 版本详情" width="700px" :close-on-click-modal="false">
      <el-descriptions :column="2" border style="margin-bottom: 16px">
        <el-descriptions-item label="版本">{{ viewingPrompt?.version }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="viewingPrompt?.is_active ? 'success' : 'info'" size="small">{{ viewingPrompt?.is_active ? '活跃' : '历史' }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="备注">{{ viewingPrompt?.note || '-' }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ viewingPrompt?.created_at }}</el-descriptions-item>
      </el-descriptions>
      <h4>System Prompt</h4>
      <el-input :model-value="viewingPrompt?.system_prompt" type="textarea" :rows="8" readonly />
      <h4 style="margin-top: 16px">User Template</h4>
      <el-input :model-value="viewingPrompt?.user_template" type="textarea" :rows="4" readonly />
    </el-dialog>

    <TemplateHelp ref="templateHelp" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import api from '../api'
import { ElMessage, ElMessageBox } from 'element-plus'
import TemplateHelp from '../components/TemplateHelp.vue'
import { useAgentDefaults } from '../composables/useAgentDefaults'

const route = useRoute()
const router = useRouter()
const agentId = ref(route.params.id)
const {
  loadAgentConfig,
  effectiveProviderNames,
  createEmptyAgentForm,
  loopDefaults,
  defaultLoopConfig
} = useAgentDefaults()

const activeTab = ref('info')
const agent = ref(null)
const prompts = ref([])
const repoList = ref([])
const promptPage = ref(1)

const paginatedPrompts = computed(() => {
  const start = (promptPage.value - 1) * 10
  return prompts.value.slice(start, start + 10)
})
const saving = ref(false)
const showPromptDetail = ref(false)
const viewingPrompt = ref(null)

const defaultForm = createEmptyAgentForm()

const form = ref({ ...defaultForm, loop_config: { ...defaultLoopConfig } })

const currentModels = ref([])
const modelLoading = ref(false)
const modelSource = ref('')

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
        context_window: m.context_window || m.ContextWindow || 0,
        supports_tools: !!(m.supports_tools ?? m.SupportsTools),
        is_reasoning: !!(m.is_reasoning ?? m.IsReasoning)
      }
    })
    .filter(Boolean)
}

const selectedModelMeta = computed(() => {
  if (!form.value.model || !currentModels.value.length) return null
  return currentModels.value.find((m) => m.id === form.value.model) || null
})

const formatContextWindow = (n) => {
  if (n >= 1000) return (n / 1000).toFixed(0) + 'K'
  return n.toString()
}

const loadModelsForProvider = async (providerName) => {
  if (!providerName) {
    currentModels.value = []
    modelSource.value = ''
    return
  }
  modelLoading.value = true
  try {
    const data = await api.get(`/config/providers/${providerName}/models`)
    currentModels.value = normalizeModels(data?.models)
    modelSource.value = data?.source || ''
  } catch {
    currentModels.value = []
    modelSource.value = ''
  } finally {
    modelLoading.value = false
  }
}

const onProviderChange = (providerName) => {
  form.value.model = ''
  loadModelsForProvider(providerName)
}

const loadRepos = async () => {
  try {
    repoList.value = await api.get('/repos') || []
  } catch {
    repoList.value = []
  }
}

const loadAgent = async () => {
  try {
    const data = await api.get(`/agents/${agentId.value}`)
    agent.value = data
    // Override must be derived from the agent's stored loop_config only.
    // Merging system defaults first would inject verify_commands and force the switch on.
    const agentLoop = data.loop_config || {}
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
      ...defaultForm,
      ...data,
      loop_config: loopConfig
    }
  } catch (error) {
    ElMessage.error('加载 Agent 信息失败')
    router.push('/agents')
  }
}

const loadPrompts = async () => {
  try {
    const data = await api.get(`/agents/${agentId.value}/prompts`)
    prompts.value = Array.isArray(data) ? data : []
  } catch {
    prompts.value = []
  }
}

const saveAgent = async () => {
  saving.value = true
  try {
    const payload = { ...form.value }
    payload.loop_config = { ...payload.loop_config }
    if (payload.loop_config.verify_commands_override) {
      // Override mode: parse text → array (empty text → [] = disable)
      payload.loop_config.verify_commands = payload.loop_config.verify_commands_text
        ? payload.loop_config.verify_commands_text
            .split('\n')
            .map(s => s.trim())
            .filter(Boolean)
        : []
    } else {
      // Inherit mode: omit verify_commands so backend uses system default
      delete payload.loop_config.verify_commands
    }
    delete payload.loop_config.verify_commands_override
    delete payload.loop_config.verify_commands_text
    const res = await api.put(`/agents/${agentId.value}`, payload)
    if (res?.repo_warnings?.length > 0) {
      ElMessage.warning(`保存成功，但部分仓库关联失败：${res.repo_warnings.join('; ')}`)
    } else {
      ElMessage.success('保存成功')
    }
    await loadAgent()
  } catch (error) {
    ElMessage.error(error.response?.data?.error || '保存失败')
  } finally {
    saving.value = false
  }
}

const viewPromptDetail = (prompt) => {
  viewingPrompt.value = prompt
  showPromptDetail.value = true
}

const rollbackPrompt = async (prompt) => {
  try {
    await api.post(`/prompts/${prompt.id}/activate`)
    ElMessage.success('回滚成功')
    await loadPrompts()
  } catch {
    ElMessage.error('回滚失败')
  }
}

const deletePrompt = async (prompt) => {
  try {
    await api.delete(`/prompts/${prompt.id}`)
    ElMessage.success('删除成功')
    await loadPrompts()
  } catch {
    ElMessage.error('删除失败')
  }
}

// Reload data when tab changes
watch(activeTab, (tab) => {
  if (tab === 'prompts') loadPrompts()
})

onMounted(async () => {
  await loadAgentConfig()
  await loadAgent()
  await loadModelsForProvider(form.value.provider)
  loadRepos()
})
</script>

<style scoped>
.page-title {
  font-size: 18px;
  font-weight: 600;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
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

.model-select-wrapper {
  display: flex;
  align-items: center;
}

.model-source-hint {
  padding: 4px 12px;
  border-bottom: 1px solid #e4e7ed;
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

<template>
  <div class="workflow-page">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>工作流详情</span>
          <el-button @click="loadContexts">
            <el-icon><Refresh /></el-icon>
            刷新
          </el-button>
        </div>
      </template>

      <div class="search-bar">
        <el-input v-model="searchRepo" placeholder="仓库名（如 owner/repo）" style="width: 300px" @keyup.enter="onSearch" />
        <el-input v-model="searchIssue" type="number" placeholder="Issue 编号" style="width: 150px" />
        <el-button type="primary" @click="onSearch">查询</el-button>
        <el-button @click="clearSearch">清除</el-button>
      </div>

      <div v-if="contexts.length > 0" class="context-list">
        <el-table :data="contexts" style="width: 100%" @row-click="selectContext">
          <el-table-column prop="issue_id" label="Issue#" width="80" />
          <el-table-column label="状态" width="140">
            <template #default="{ row }">
              <el-tag :type="semanticStatus(row).type" size="small">{{ semanticStatus(row).label }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="active_role" label="活跃角色" width="100">
            <template #default="{ row }">
              <el-tag v-if="row.active_role" size="small" type="info">{{ roleLabels[row.active_role] || row.active_role }}</el-tag>
              <span v-else class="text-gray">-</span>
            </template>
          </el-table-column>
          <el-table-column prop="active_agent_id" label="活跃 Agent" width="120">
            <template #default="{ row }">
              {{ agentMap[row.active_agent_id] || (row.active_agent_id || '-') }}
            </template>
          </el-table-column>
          <el-table-column prop="pr_id" label="关联 PR" width="100">
            <template #default="{ row }">
              <span v-if="row.pr_id">{{ row.pr_id }}</span>
              <span v-else class="text-gray">-</span>
            </template>
          </el-table-column>
          <el-table-column prop="updated_at" label="更新时间" width="180">
            <template #default="{ row }">
              {{ formatDate(row.updated_at) }}
            </template>
          </el-table-column>
        </el-table>
      </div>

      <div v-else class="empty-state">
        <el-empty description="暂无工作流数据" />
      </div>
    </el-card>

    <el-card v-if="selectedContext" class="detail-card">
      <template #header>
        <div class="card-header">
          <span>Issue #{{ selectedContext.issue_id }} 详情</span>
          <el-button type="warning" link @click="resetWorkflow">重置工作流</el-button>
        </div>
      </template>

      <el-descriptions :column="2" border>
        <el-descriptions-item label="仓库">{{ selectedContext.repo }}</el-descriptions-item>
        <el-descriptions-item label="Issue">#{{ selectedContext.issue_id }}</el-descriptions-item>
        <el-descriptions-item label="状态" :span="2">
          <div class="stage-display">
            <el-tag :type="semanticStatus(selectedContext).type" size="medium">{{ semanticStatus(selectedContext).label }}</el-tag>
            <span class="status-hint">{{ semanticStatus(selectedContext).hint }}</span>
          </div>
        </el-descriptions-item>
        <el-descriptions-item label="活跃角色">{{ roleLabels[selectedContext.active_role] || selectedContext.active_role || '-' }}</el-descriptions-item>
        <el-descriptions-item label="活跃 Agent">{{ agentMap[selectedContext.active_agent_id] || selectedContext.active_agent_id || '-' }}</el-descriptions-item>
        <el-descriptions-item label="关联 PR">{{ selectedContext.pr_id || '-' }}</el-descriptions-item>
        <el-descriptions-item label="更新时间">{{ formatDate(selectedContext.updated_at) }}</el-descriptions-item>
      </el-descriptions>

      <!-- 诊断（高级）：内部 stage 机不作为默认用户可见面，仅排障时展开 -->
      <el-collapse v-model="diagnosticsOpen" class="diagnostics">
        <el-collapse-item name="diagnostics">
          <template #title>
            <span class="diagnostics-title">诊断（高级）</span>
            <span class="diagnostics-sub">内部工作流阶段，仅用于排障</span>
          </template>

          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="内部阶段">
              <el-tag :type="getStageType(selectedContext.stage)" size="small">{{ stageLabels[selectedContext.stage] || selectedContext.stage }}</el-tag>
              <code class="raw-value">{{ selectedContext.stage }}</code>
            </el-descriptions-item>
            <el-descriptions-item label="前一阶段">
              <span v-if="selectedContext.previous_stage">{{ stageLabels[selectedContext.previous_stage] || selectedContext.previous_stage }}</span>
              <span v-else class="text-gray">-</span>
            </el-descriptions-item>
            <el-descriptions-item label="会话 ID" :span="2">
              <span v-if="selectedContext.session_id" class="session-id">{{ selectedContext.session_id }}</span>
              <span v-else class="text-gray">-</span>
            </el-descriptions-item>
          </el-descriptions>

          <div class="stage-flow">
            <h4>工作流阶段流程</h4>
            <div class="flow-steps">
              <div
                v-for="step in stageFlow"
                :key="step.stage"
                :class="['flow-step', {
                  active: selectedContext.stage === step.stage,
                  passed: isStagePassed(selectedContext.stage, step.stage),
                  pending: !isStagePassed(selectedContext.stage, step.stage) && selectedContext.stage !== step.stage
                }]"
              >
                <div class="step-circle">
                  <el-icon v-if="selectedContext.stage === step.stage"><CircleCheck /></el-icon>
                  <span v-else-if="isStagePassed(selectedContext.stage, step.stage)">✓</span>
                  <span v-else>{{ step.order }}</span>
                </div>
                <div class="step-label">{{ step.label }}</div>
              </div>
            </div>
          </div>
        </el-collapse-item>
      </el-collapse>

      <div v-if="relatedTasks.length > 0" class="related-tasks">
        <h4>相关任务</h4>
        <el-table :data="relatedTasks" style="width: 100%">
          <el-table-column prop="id" label="ID" width="60" />
          <el-table-column prop="task_type" label="类型" width="120">
            <template #default="{ row }">
              <el-tag size="small" type="info">{{ row.task_type }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="status" label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="getStatusType(row.status)" size="small">{{ statusLabels[row.status] || row.status }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="created_at" label="创建时间" width="180">
            <template #default="{ row }">
              {{ formatDate(row.created_at) }}
            </template>
          </el-table-column>
          <el-table-column label="操作" width="80">
            <template #default="{ row }">
              <el-button size="small" type="primary" link @click="viewTask(row)">详情</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </el-card>

    <el-dialog v-model="showTaskDetail" title="任务详情" width="700px">
      <el-descriptions :column="2" border>
        <el-descriptions-item label="ID">{{ taskDetail?.id }}</el-descriptions-item>
        <el-descriptions-item label="类型">{{ taskDetail?.task_type }}</el-descriptions-item>
        <el-descriptions-item label="仓库">{{ taskDetail?.repo }}</el-descriptions-item>
        <el-descriptions-item label="Issue">{{ taskDetail?.issue_id }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="getStatusType(taskDetail?.status)">{{ taskDetail?.status }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatDate(taskDetail?.created_at) }}</el-descriptions-item>
      </el-descriptions>

      <div v-if="taskDetail?.result" class="task-result">
        <h4>执行结果</h4>
        <el-input type="textarea" :model-value="taskDetail.result" :rows="8" readonly />
      </div>

      <div v-if="taskDetail?.error" class="task-error">
        <h4>错误信息</h4>
        <el-alert :title="taskDetail.error" type="error" :closable="false" />
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import api from '../api'
import { Refresh, CircleCheck } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const route = useRoute()

const contexts = ref([])
const selectedContext = ref(null)
const relatedTasks = ref([])
const agents = ref([])
const searchRepo = ref('')
const searchIssue = ref('')
const showTaskDetail = ref(false)
const taskDetail = ref(null)
// 诊断折叠默认收起：普通用户只看语义状态，排障时才展开内部 stage
const diagnosticsOpen = ref([])

const stageLabels = {
  idle: '空闲',
  analyzing: '分析中',
  analyzed: '已分析',
  developing: '开发中',
  reviewing: '评审中',
  done: '已完成'
}

const roleLabels = {
  analyze: '分析',
  coder: '编码',
  review: '评审'
}

const statusLabels = { pending: '待处理', running: '运行中', success: '成功', partial: '部分完成', failed: '失败' }

const stageFlow = [
  { stage: 'idle', label: '空闲', order: 1 },
  { stage: 'analyzing', label: '分析中', order: 2 },
  { stage: 'analyzed', label: '已分析', order: 3 },
  { stage: 'developing', label: '开发中', order: 4 },
  { stage: 'reviewing', label: '评审中', order: 5 },
  { stage: 'done', label: '已完成', order: 6 }
]

const stageOrder = { idle: 0, analyzing: 1, analyzed: 2, developing: 3, reviewing: 4, done: 5 }

const agentMap = computed(() => {
  const map = {}
  for (const a of agents.value) map[a.id] = a.name
  return map
})

// semanticStatus 把内部 stage 机映射为用户可理解的语义状态。
// 1.5.2：用户只需感知「Agent 正在处理 / 已回复 / 已创建 PR」，
// analyzing/analyzed/developing/reviewing 这类内部阶段收进诊断折叠。
const semanticStatus = (ctx) => {
  if (!ctx) return { key: 'idle', label: '空闲', type: 'info', hint: '' }
  const stage = ctx.stage
  if (stage === 'analyzing' || stage === 'developing' || stage === 'reviewing') {
    return { key: 'processing', label: 'Agent 正在处理', type: 'warning', hint: '任务进行中，完成后会在 Gitea 留言' }
  }
  if (stage === 'done') {
    return { key: 'done', label: '已完成', type: 'success', hint: '' }
  }
  if (ctx.pr_id) {
    return { key: 'pr_opened', label: '已创建 PR', type: 'success', hint: `关联 PR #${ctx.pr_id}` }
  }
  if (stage === 'analyzed') {
    return { key: 'replied', label: '已回复', type: 'success', hint: 'Agent 已给出分析结论' }
  }
  return { key: 'idle', label: '空闲', type: 'info', hint: '等待 Assign 或 @提及触发' }
}

const getStageType = (stage) => {
  const types = { idle: 'info', analyzing: 'warning', analyzed: 'success', developing: 'warning', reviewing: 'warning', done: 'success' }
  return types[stage] || 'info'
}

const getStatusType = (status) => {
  const types = { pending: 'warning', running: 'info', success: 'success', partial: 'warning', failed: 'danger' }
  return types[status] || 'info'
}

const isStagePassed = (currentStage, targetStage) => {
  return stageOrder[currentStage] > stageOrder[targetStage]
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString('zh-CN')
}

const loadContexts = async () => {
  if (!searchRepo.value) {
    ElMessage.warning('请输入仓库名')
    return
  }
  try {
    let url = '/workflow-context'
    url += `?repo=${encodeURIComponent(searchRepo.value)}`
    if (searchIssue.value) {
      url += `&issue=${searchIssue.value}`
    }
    const res = await api.get(url)
    contexts.value = Array.isArray(res) ? res : [res]
  } catch (error) {
    ElMessage.error(error.response?.data?.error || '加载失败')
    contexts.value = []
  }
}

const onSearch = () => {
  if (!searchRepo.value) {
    ElMessage.warning('请输入仓库名')
    return
  }
  loadContexts()
}

const clearSearch = () => {
  searchRepo.value = ''
  searchIssue.value = ''
  contexts.value = []
  selectedContext.value = null
  relatedTasks.value = []
}

const selectContext = async (ctx) => {
  selectedContext.value = ctx
  await loadRelatedTasks(ctx.repo, ctx.issue_id)
}

const loadRelatedTasks = async (repo, issueID) => {
  try {
    const res = await api.get(`/tasks?limit=20&repo=${encodeURIComponent(repo)}&issue=${issueID}`)
    relatedTasks.value = res?.data || []
  } catch {
    relatedTasks.value = []
  }
}

const viewTask = async (task) => {
  showTaskDetail.value = true
  try {
    const res = await api.get(`/tasks/${task.id}`)
    taskDetail.value = res?.task || task
  } catch {
    taskDetail.value = task
  }
}

const resetWorkflow = async () => {
  if (!selectedContext.value) return
  try {
    await ElMessageBox.confirm(
      `将 Issue #${selectedContext.value.issue_id} 的工作流重置为空闲状态：取消进行中的任务、归档会话，并允许重新触发。确认重置？`,
      '重置工作流',
      { type: 'warning', confirmButtonText: '重置', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  try {
    await api.post(`/sessions/reset?repo=${encodeURIComponent(selectedContext.value.repo)}&issue=${selectedContext.value.issue_id}`)
    ElMessage.success('工作流已重置')
    selectedContext.value = null
    relatedTasks.value = []
    await loadContexts()
  } catch (error) {
    ElMessage.error(error.response?.data?.error || '重置失败')
  }
}

const loadAgents = async () => {
  agents.value = await api.get('/agents') || []
}

onMounted(() => {
  loadAgents()
  if (route.query.repo) {
    searchRepo.value = route.query.repo
  }
  if (route.query.issue) {
    searchIssue.value = route.query.issue
  }
  if (searchRepo.value) {
    loadContexts()
  }
})

watch(() => [route.query.repo, route.query.issue], ([repo, issue]) => {
  if (repo) searchRepo.value = repo
  if (issue) searchIssue.value = issue
  if (repo) loadContexts()
})
</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.search-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}

.text-gray {
  color: #909399;
}

.context-list {
  margin-top: 16px;
}

.empty-state {
  padding: 40px 0;
}

.detail-card {
  margin-top: 20px;
}

.stage-display {
  display: flex;
  align-items: center;
  gap: 12px;
}

.status-hint {
  font-size: 12px;
  color: #909399;
}

.diagnostics {
  margin-top: 20px;
}

.diagnostics-title {
  font-weight: 600;
  margin-right: 10px;
}

.diagnostics-sub {
  font-size: 12px;
  color: #909399;
}

.raw-value {
  margin-left: 8px;
  font-family: monospace;
  font-size: 12px;
  color: #909399;
}

.session-id {
  font-family: monospace;
  font-size: 12px;
  color: #409eff;
}

.stage-flow {
  margin-top: 24px;
}

.stage-flow h4 {
  margin-bottom: 16px;
}

.flow-steps {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20px 0;
}

.flow-step {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  flex: 1;
}

.step-circle {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  font-weight: bold;
}

.flow-step.passed .step-circle {
  background-color: #67c23a;
  color: #fff;
}

.flow-step.active .step-circle {
  background-color: #409eff;
  color: #fff;
}

.flow-step.pending .step-circle {
  background-color: #e4e7ed;
  color: #909399;
}

.step-label {
  font-size: 12px;
  color: #606266;
}

.flow-step.pending .step-label {
  color: #c0c4cc;
}

.related-tasks {
  margin-top: 24px;
}

.related-tasks h4 {
  margin-bottom: 12px;
}

.task-result,
.task-error {
  margin-top: 16px;
}

.task-result h4,
.task-error h4 {
  margin-bottom: 8px;
}
</style>

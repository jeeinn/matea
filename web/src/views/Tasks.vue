<template>
  <div class="tasks-page">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>任务列表</span>
          <el-button @click="loadTasks">
            <el-icon><Refresh /></el-icon>
            刷新
          </el-button>
        </div>
      </template>

      <!-- 筛选栏 -->
      <div class="filter-bar">
        <el-select v-model="filterStatus" placeholder="状态" clearable style="width: 140px" @change="onFilterChange">
          <el-option label="待处理" value="pending" />
          <el-option label="运行中" value="running" />
          <el-option label="成功" value="success" />
          <el-option label="部分完成" value="partial" />
          <el-option label="失败" value="failed" />
        </el-select>
        <el-select v-model="filterType" placeholder="任务类型" clearable style="width: 160px" @change="onFilterChange">
          <el-option v-for="t in taskTypes" :key="t" :label="t" :value="t" />
        </el-select>
        <el-select v-model="filterAgent" placeholder="Agent" clearable style="width: 180px" @change="onFilterChange">
          <el-option v-for="a in agents" :key="a.id" :label="a.name" :value="a.id" />
        </el-select>
        <span class="filter-count">共 {{ total }} 条</span>
      </div>

      <el-table v-loading="loading" :data="tasks" style="width: 100%">
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="task_type" label="类型" width="120">
          <template #default="{ row }">
            <el-tag size="small" type="info">{{ row.task_type }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="Agent" width="120">
          <template #default="{ row }">
            {{ agentMap[row.agent_id] || row.agent_id }}
          </template>
        </el-table-column>
        <el-table-column prop="repo" label="仓库" />
        <el-table-column prop="issue_id" label="Issue#" width="80" />
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
        <el-table-column label="操作" width="200">
          <template #default="{ row }">
            <el-button size="small" type="primary" link @click="viewTask(row)">详情</el-button>
            <el-button size="small" type="primary" link @click="viewTask(row, true)">对话</el-button>
            <el-button
              v-if="row.status === 'pending' || row.status === 'running' || row.status === 'partial'"
              size="small"
              type="warning"
              link
              :loading="resettingId === row.id"
              @click="resetTask(row)"
            >重置</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="pagination-bar">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @current-change="loadTasks"
          @size-change="onPageSizeChange"
        />
      </div>
    </el-card>

    <!-- Task Detail Dialog -->
    <el-dialog v-model="showDetail" title="任务详情" width="860px" :close-on-click-modal="false">
      <el-descriptions :column="2" border>
        <el-descriptions-item label="ID">{{ selectedTask?.id }}</el-descriptions-item>
        <el-descriptions-item label="类型">{{ selectedTask?.task_type }}</el-descriptions-item>
        <el-descriptions-item label="仓库">{{ selectedTask?.repo }}</el-descriptions-item>
        <el-descriptions-item label="Issue">{{ selectedTask?.issue_id }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="getStatusType(selectedTask?.status)">{{ selectedTask?.status }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatDate(selectedTask?.created_at) }}</el-descriptions-item>
      </el-descriptions>

      <div v-if="selectedTask?.result" class="task-result">
        <h4>执行结果</h4>
        <el-input type="textarea" :model-value="selectedTask.result" :rows="10" readonly />
      </div>

      <div v-if="selectedTask?.error" class="task-error">
        <h4>错误信息</h4>
        <el-alert :title="selectedTask.error" type="error" :closable="false" />
      </div>

      <div v-if="taskUsage" class="task-usage">
        <h4>Token 使用统计</h4>
        <el-descriptions :column="3" border>
          <el-descriptions-item label="Provider" :span="3">{{ taskUsage.provider }}</el-descriptions-item>
          <el-descriptions-item label="模型">{{ taskUsage.model }}</el-descriptions-item>
          <el-descriptions-item label="调用次数">{{ taskUsage.call_count }}</el-descriptions-item>
          <el-descriptions-item label="成本">
            <span class="cost-value">{{ formatCost(taskUsage.total_cost) }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="输入 Tokens">
            <span class="token-value">{{ taskUsage.total_prompt_tokens.toLocaleString() }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="输出 Tokens">
            <span class="token-value">{{ taskUsage.total_completion_tokens.toLocaleString() }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="总计 Tokens">
            <span class="token-value">{{ taskUsage.total_tokens.toLocaleString() }}</span>
          </el-descriptions-item>
        </el-descriptions>
      </div>

      <div ref="conversationSection" class="task-conversation">
        <div class="conversation-header">
          <h4>Agent 对话日志</h4>
          <el-tag v-if="conversationCount > 0" size="small" type="info">{{ conversationCount }} 条</el-tag>
        </div>
        <div v-loading="conversationLoading">
          <el-alert
            v-if="!conversationLoading && conversationMessages.length === 0"
            type="info"
            :closable="false"
            show-icon
            title="暂无对话日志"
            description="请在「系统配置」开启 debug.conversation_log.enabled 后重新跑任务；仅多轮 Agent Loop（如 solve_issue / solve_comment）会写入。"
          />
          <el-collapse v-else v-model="openIterations">
            <el-collapse-item
              v-for="group in conversationByIteration"
              :key="group.iteration"
              :name="String(group.iteration)"
              :title="`第 ${group.iteration} 轮（${group.messages.length} 条消息）`"
            >
              <div
                v-for="msg in group.messages"
                :key="msg.id"
                class="conv-msg"
              >
                <div class="conv-msg-meta">
                  <el-tag size="small" :type="roleTagType(msg.role)">{{ msg.role }}</el-tag>
                  <span v-if="msg.tool_call_id" class="conv-meta-extra">tool_call_id={{ msg.tool_call_id }}</span>
                  <span class="conv-meta-extra">seq={{ msg.seq }}</span>
                </div>
                <pre v-if="msg.content" class="conv-content">{{ msg.content }}</pre>
                <div v-if="msg.tool_calls" class="conv-tools">
                  <div class="conv-tools-label">tool_calls</div>
                  <pre class="conv-content">{{ formatToolCalls(msg.tool_calls) }}</pre>
                </div>
              </div>
            </el-collapse-item>
          </el-collapse>
        </div>
      </div>

      <div v-if="selectedTask?.repo && selectedTask?.issue_id" class="task-workflow">
        <el-button type="primary" link @click="goToWorkflow">查看工作流详情</el-button>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, nextTick, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import api from '../api'
import { Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const router = useRouter()

const tasks = ref([])
const agents = ref([])
const showDetail = ref(false)
const selectedTask = ref(null)
const taskUsage = ref(null)
const conversationMessages = ref([])
const conversationCount = ref(0)
const conversationLoading = ref(false)
const openIterations = ref([])
const conversationSection = ref(null)
const loading = ref(false)
const resettingId = ref(null)
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)

const filterStatus = ref('')
const filterType = ref('')
const filterAgent = ref('')

const statusLabels = { pending: '待处理', running: '运行中', success: '成功', partial: '部分完成', failed: '失败' }

const agentMap = computed(() => {
  const map = {}
  for (const a of agents.value) map[a.id] = a.name
  return map
})

const taskTypes = ref(['analyze_issue', 'review_pr', 'reply_comment', 'solve_issue', 'fix_bug', 'solve_comment', 'trigger'])

const conversationByIteration = computed(() => {
  const map = new Map()
  for (const msg of conversationMessages.value) {
    const iter = msg.iteration ?? 0
    if (!map.has(iter)) map.set(iter, [])
    map.get(iter).push(msg)
  }
  return [...map.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([iteration, messages]) => ({ iteration, messages }))
})

const loadTasks = async () => {
  loading.value = true
  try {
    const offset = (currentPage.value - 1) * pageSize.value
    let params = `?limit=${pageSize.value}&offset=${offset}`
    if (filterStatus.value) params += `&status=${filterStatus.value}`
    if (filterType.value) params += `&type=${filterType.value}`
    if (filterAgent.value) params += `&agent_id=${filterAgent.value}`
    const res = await api.get(`/tasks${params}`)
    tasks.value = res?.data || []
    total.value = res?.total || 0
  } catch {
    tasks.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

const getStatusType = (status) => {
  const types = { pending: 'warning', running: 'info', success: 'success', partial: 'warning', failed: 'danger' }
  return types[status] || 'info'
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString('zh-CN')
}

const onFilterChange = () => {
  currentPage.value = 1
  loadTasks()
}

const onPageSizeChange = () => {
  currentPage.value = 1
  loadTasks()
}

const loadAgents = async () => {
  agents.value = await api.get('/agents') || []
}

const roleTagType = (role) => {
  const types = { system: 'info', user: '', assistant: 'success', tool: 'warning' }
  return types[role] || 'info'
}

const formatToolCalls = (raw) => {
  if (!raw) return ''
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

const loadConversation = async (taskId) => {
  conversationLoading.value = true
  conversationMessages.value = []
  try {
    const res = await api.get(`/tasks/${taskId}/conversation`)
    conversationMessages.value = res?.messages || []
    conversationCount.value = res?.count || conversationMessages.value.length
    openIterations.value = conversationByIteration.value.map((g) => String(g.iteration))
  } catch {
    conversationMessages.value = []
    conversationCount.value = 0
  } finally {
    conversationLoading.value = false
  }
}

const viewTask = async (task, scrollToConversation = false) => {
  showDetail.value = true
  selectedTask.value = task
  taskUsage.value = null
  conversationMessages.value = []
  conversationCount.value = 0
  openIterations.value = []
  try {
    const res = await api.get(`/tasks/${task.id}`)
    if (res?.task) {
      selectedTask.value = res.task
      taskUsage.value = res.usage || null
      conversationCount.value = res.conversation_count || 0
    }
  } catch {
    // 忽略错误，使用列表中的数据
  }
  await loadConversation(task.id)
  if (scrollToConversation) {
    await nextTick()
    conversationSection.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }
}

const formatCost = (cost) => {
  if (!cost || cost <= 0) return '-'
  if (cost < 0.01) return cost.toFixed(6) + ' USD'
  if (cost < 1) return cost.toFixed(4) + ' USD'
  return cost.toFixed(2) + ' USD'
}

const resetTask = async (task) => {
  try {
    await ElMessageBox.confirm(
      `将任务 #${task.id}（${task.status}）标记为失败，以便重新触发该 Issue。确认重置？`,
      '重置任务状态',
      { type: 'warning', confirmButtonText: '重置', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  resettingId.value = task.id
  try {
    const res = await api.post(`/tasks/${task.id}/reset`)
    ElMessage.success(res?.message || '已重置')
    await loadTasks()
  } catch (error) {
    ElMessage.error(error.response?.data?.error || '重置失败')
  } finally {
    resettingId.value = null
  }
}

const goToWorkflow = () => {
  showDetail.value = false
  router.push({
    path: '/workflows',
    query: { repo: selectedTask.value.repo, issue: selectedTask.value.issue_id }
  })
}

onMounted(() => {
  loadTasks()
  loadAgents()
})
</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.filter-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}

.filter-count {
  font-size: 13px;
  color: #909399;
  margin-left: auto;
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.task-result,
.task-error {
  margin-top: 20px;
}

.task-result h4,
.task-error h4,
.task-usage h4,
.task-conversation h4 {
  margin-bottom: 10px;
}

.task-usage,
.task-conversation {
  margin-top: 20px;
}

.conversation-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}

.conversation-header h4 {
  margin: 0;
}

.conv-msg {
  border: 1px solid #ebeef5;
  border-radius: 6px;
  padding: 10px 12px;
  margin-bottom: 10px;
  background: #fafafa;
}

.conv-msg-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.conv-meta-extra {
  font-size: 12px;
  color: #909399;
}

.conv-content {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.5;
  max-height: 320px;
  overflow: auto;
  background: #fff;
  border-radius: 4px;
  padding: 8px;
}

.conv-tools {
  margin-top: 8px;
}

.conv-tools-label {
  font-size: 12px;
  color: #909399;
  margin-bottom: 4px;
}

.token-value {
  color: #409eff;
  font-weight: 600;
}

.cost-value {
  color: #f56c6c;
  font-weight: 600;
}
</style>

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
        <el-table-column label="操作" width="220">
          <template #default="{ row }">
            <el-button size="small" type="primary" link @click="viewTaskDetail(row)">详情</el-button>
            <el-button size="small" type="success" link @click="viewConversation(row)">对话</el-button>
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

    <!-- Task Detail Dialog - 仅显示基本信息、结果、用量统计 -->
    <el-dialog v-model="showDetail" title="任务详情" width="800px" :close-on-click-modal="false">
      <el-descriptions :column="2" border>
        <el-descriptions-item label="ID">{{ selectedTask?.id }}</el-descriptions-item>
        <el-descriptions-item label="类型">
          <el-tag size="small" type="info">{{ selectedTask?.task_type }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="Agent">
          {{ agentMap[selectedTask?.agent_id] || selectedTask?.agent_id }}
        </el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="getStatusType(selectedTask?.status)">{{ selectedTask?.status }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="仓库">{{ selectedTask?.repo }}</el-descriptions-item>
        <el-descriptions-item label="Issue">{{ selectedTask?.issue_id }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatDate(selectedTask?.created_at) }}</el-descriptions-item>
        <el-descriptions-item label="完成时间">{{ formatDate(selectedTask?.finished_at) }}</el-descriptions-item>
      </el-descriptions>

      <div v-if="selectedTask?.result" class="task-result">
        <h4>执行结果</h4>
        <el-input type="textarea" :model-value="selectedTask.result" :rows="12" readonly />
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

      <template #footer>
        <div class="dialog-footer">
          <div class="footer-left">
            <el-button
              v-if="selectedTask?.repo && selectedTask?.issue_id"
              type="primary"
              link
              @click="goToWorkflow"
            >
              <el-icon><Connection /></el-icon>
              查看工作流详情
            </el-button>
          </div>
          <div class="footer-right">
            <el-button
              v-if="conversationCount > 0"
              type="success"
              @click="openConversationFromDetail"
            >
              <el-icon><ChatDotRound /></el-icon>
              查看对话 ({{ conversationCount }})
            </el-button>
            <el-button @click="showDetail = false">关闭</el-button>
          </div>
        </div>
      </template>
    </el-dialog>

    <!-- Task Conversation Dialog - 独立的多轮对话弹窗 -->
    <TaskConversation
      v-model="showConversation"
      :task="selectedTask"
    />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import api from '../api'
import { Refresh, ChatDotRound, Connection } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import TaskConversation from '../components/TaskConversation.vue'

const route = useRoute()
const router = useRouter()

const tasks = ref([])
const agents = ref([])
const showDetail = ref(false)
const showConversation = ref(false)
const selectedTask = ref(null)
const taskUsage = ref(null)
const conversationCount = ref(0)
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

// 详情请求竞态保护：仅最新请求可回写 selectedTask
let detailRequestId = 0

const invalidateDetailRequest = () => {
  detailRequestId++
}

// 查看详情（独立弹窗）
const viewTaskDetail = async (task) => {
  // 如果对话弹窗打开，关闭它避免双弹窗抢同一 selectedTask
  if (showConversation.value) {
    showConversation.value = false
  }
  const requestId = ++detailRequestId
  showDetail.value = true
  selectedTask.value = task
  taskUsage.value = null
  conversationCount.value = 0
  try {
    const res = await api.get(`/tasks/${task.id}`)
    // 已切换任务 / 已关闭详情 / 已打开对话时，丢弃过期响应
    if (requestId !== detailRequestId || !showDetail.value) return
    if (res?.task) {
      selectedTask.value = res.task
      taskUsage.value = res.usage || null
      conversationCount.value = res.conversation_count || 0
    }
  } catch {
    // 忽略错误，使用列表中的数据
  }
}

// 查看对话（独立弹窗）- 与详情互斥
const viewConversation = (task) => {
  // 关闭详情弹窗，避免双弹窗抢同一 selectedTask
  showDetail.value = false
  invalidateDetailRequest()
  selectedTask.value = task
  showConversation.value = true
}

// 从详情弹窗打开对话
const openConversationFromDetail = () => {
  showDetail.value = false
  invalidateDetailRequest()
  showConversation.value = true
}

// 跳转到工作流详情
const goToWorkflow = () => {
  showDetail.value = false
  invalidateDetailRequest()
  router.push({
    path: '/workflows',
    query: { repo: selectedTask.value.repo, issue: selectedTask.value.issue_id }
  })
}

const openTaskFromQuery = async () => {
  const raw = route.query.task
  if (raw == null || raw === '') return
  const id = Number(Array.isArray(raw) ? raw[0] : raw)
  if (!Number.isFinite(id) || id <= 0) return
  // Prefer list row when present; otherwise fetch by id.
  const fromList = tasks.value.find((t) => t.id === id)
  if (fromList) {
    await viewTaskDetail(fromList)
    return
  }
  try {
    const res = await api.get(`/tasks/${id}`)
    if (res?.task) {
      await viewTaskDetail(res.task)
    }
  } catch {
    ElMessage.warning(`任务 #${id} 不存在或无法加载`)
  }
}

onMounted(async () => {
  await Promise.all([loadTasks(), loadAgents()])
  await openTaskFromQuery()
})

watch(
  () => route.query.task,
  () => {
    openTaskFromQuery()
  }
)
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
.task-usage h4 {
  margin-bottom: 10px;
}

.task-usage {
  margin-top: 20px;
}

.token-value {
  color: #409eff;
  font-weight: 600;
}

.cost-value {
  color: #f56c6c;
  font-weight: 600;
}

.dialog-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
}

.footer-left {
  display: flex;
  align-items: center;
}

.footer-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
</style>

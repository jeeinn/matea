<template>
  <div class="operation-logs">
    <div class="page-header">
      <div>
        <h2>操作审计日志</h2>
        <p class="subtitle">
          记录 Agent 在任务中执行的命令与关键动作（来源：<code>operation_logs</code> 表，由沙箱审计自动写入）。
        </p>
      </div>
      <div class="header-actions">
        <el-input
          v-model="keyword"
          class="keyword-input"
          placeholder="搜索动作 / 详情 / Agent / 任务"
          clearable
          :prefix-icon="Search"
          @clear="onKeywordClear"
          @keyup.enter="runSearch"
        />
        <el-button type="primary" @click="runSearch">搜索</el-button>
        <el-button :icon="Refresh" @click="reload">刷新</el-button>
      </div>
    </div>

    <el-alert
      v-if="searchMode"
      type="info"
      :closable="false"
      show-icon
      class="search-banner"
      title="搜索模式：已拉取最近一批日志做客户端过滤（非全库检索）"
      :description="`当前窗口最多 ${searchFetchLimit} 条（按时间倒序）。`"
    />

    <el-alert
      v-if="!loading && filteredLogs.length === 0"
      type="info"
      :closable="false"
      show-icon
      :title="logs.length === 0 ? '暂无审计日志' : '当前筛选无匹配记录'"
      :description="logs.length === 0 ? '任务运行后，Agent 执行的命令会写入此处。' : '尝试调整搜索关键字或切换分页。'"
    />

    <el-table
      v-else
      v-loading="loading"
      :data="displayLogs"
      stripe
      border
      size="default"
      class="log-table"
    >
      <el-table-column type="expand">
        <template #default="props">
          <pre class="log-detail">{{ props.row.detail || '(无详情)' }}</pre>
        </template>
      </el-table-column>
      <el-table-column prop="id" label="ID" width="80" />
      <el-table-column label="时间" width="180">
        <template #default="scope">
          <span>{{ formatDate(scope.row.created_at) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="Agent" width="140">
        <template #default="scope">
          <span v-if="scope.row.agent_id > 0">
            {{ agentLabel(scope.row.agent_id) }}
          </span>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column label="任务" width="90">
        <template #default="scope">
          <router-link
            v-if="scope.row.task_id > 0"
            :to="{ path: '/tasks', query: { task: String(scope.row.task_id) } }"
            class="task-link"
          >
            #{{ scope.row.task_id }}
          </router-link>
          <span v-else>-</span>
        </template>
      </el-table-column>
      <el-table-column label="动作" width="120">
        <template #default="scope">
          <el-tag size="small" :type="actionTagType(scope.row.action)">{{ scope.row.action }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="详情预览" min-width="280">
        <template #default="scope">
          <span class="detail-preview">{{ previewDetail(scope.row.detail) }}</span>
        </template>
      </el-table-column>
    </el-table>

    <div class="pager">
      <el-pagination
        v-if="!searchMode"
        layout="total, sizes, prev, pager, next"
        :total="totalHint"
        :page-size="pageSize"
        :current-page="page"
        :page-sizes="[20, 50, 100, 200]"
        @size-change="onPageSizeChange"
        @current-change="onPageChange"
      />
      <span v-else class="filter-hint">搜索命中 {{ filteredLogs.length }} 条（窗口内）</span>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import api from '../api'
import { Refresh, Search } from '@element-plus/icons-vue'

const searchFetchLimit = 500

const logs = ref([])
const agents = ref([])
const loading = ref(false)
const keyword = ref('')
const appliedKeyword = ref('')
const page = ref(1)
const pageSize = ref(50)
const totalHint = ref(0)

const searchMode = computed(() => appliedKeyword.value.trim() !== '')

const agentMap = computed(() => {
  const map = {}
  for (const a of agents.value) map[a.id] = a.name
  return map
})

const agentLabel = (id) => {
  const name = agentMap.value[id]
  return name ? `${name} (#${id})` : `#${id}`
}

const filteredLogs = computed(() => {
  const kw = appliedKeyword.value.trim().toLowerCase()
  if (!kw) return logs.value
  return logs.value.filter((l) => {
    const agentName = (agentMap.value[l.agent_id] || '').toLowerCase()
    return (
      String(l.action || '').toLowerCase().includes(kw) ||
      String(l.detail || '').toLowerCase().includes(kw) ||
      String(l.agent_id || '').includes(kw) ||
      agentName.includes(kw) ||
      String(l.task_id || '').includes(kw)
    )
  })
})

const displayLogs = computed(() => filteredLogs.value)

const loadAgents = async () => {
  try {
    agents.value = (await api.get('/agents')) || []
  } catch {
    agents.value = []
  }
}

const loadLogs = async () => {
  loading.value = true
  try {
    let limit = pageSize.value
    let offset = (page.value - 1) * pageSize.value
    if (searchMode.value) {
      limit = searchFetchLimit
      offset = 0
    }
    const res = await api.get(`/logs?limit=${limit}&offset=${offset}`)
    const arr = Array.isArray(res) ? res : (res?.data || [])

    // 分页越过末尾：回退到上一页，避免空页
    if (!searchMode.value && page.value > 1 && arr.length === 0) {
      page.value = page.value - 1
      totalHint.value = (page.value) * pageSize.value
      loading.value = false
      await loadLogs()
      return
    }

    logs.value = arr
    if (searchMode.value) {
      totalHint.value = arr.length
    } else if (arr.length === pageSize.value) {
      // 无 total：满页时保留「下一页」入口
      totalHint.value = page.value * pageSize.value + 1
    } else {
      totalHint.value = offset + arr.length
    }
  } catch {
    logs.value = []
    totalHint.value = 0
  } finally {
    loading.value = false
  }
}

const reload = () => {
  if (searchMode.value) {
    runSearch()
  } else {
    loadLogs()
  }
}

const runSearch = async () => {
  appliedKeyword.value = keyword.value.trim()
  page.value = 1
  await loadLogs()
}

const onKeywordClear = async () => {
  keyword.value = ''
  appliedKeyword.value = ''
  page.value = 1
  await loadLogs()
}

const onPageChange = (p) => {
  page.value = p
  loadLogs()
}

const onPageSizeChange = (size) => {
  pageSize.value = size
  page.value = 1
  loadLogs()
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  const d = new Date(dateStr)
  if (isNaN(d.getTime())) return dateStr
  return d.toLocaleString('zh-CN')
}

const actionTagType = (action) => {
  const types = {
    command: 'warning',
    reset: 'info',
    assign: 'success',
    unassign: 'info'
  }
  return types[action] || 'info'
}

const previewDetail = (detail) => {
  if (!detail) return '-'
  const oneLine = detail.replace(/\s+/g, ' ').trim()
  return oneLine.length > 120 ? oneLine.slice(0, 120) + '…' : oneLine
}

onMounted(async () => {
  await loadAgents()
  await loadLogs()
})
</script>

<style scoped>
.operation-logs {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.page-header h2 {
  margin: 0 0 4px;
  font-size: 20px;
}

.subtitle {
  margin: 0;
  color: #909399;
  font-size: 13px;
}

.subtitle code {
  background: #f0f2f5;
  padding: 1px 5px;
  border-radius: 3px;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.keyword-input {
  width: 280px;
}

.search-banner {
  margin-bottom: 0;
}

.log-table {
  width: 100%;
}

.detail-preview {
  color: #606266;
  font-size: 13px;
  word-break: break-all;
}

.log-detail {
  margin: 0;
  padding: 12px 16px;
  background: #1e1e1e;
  color: #d4d4d4;
  border-radius: 4px;
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 420px;
  overflow: auto;
}

.task-link {
  color: #409eff;
  text-decoration: none;
}

.task-link:hover {
  text-decoration: underline;
}

.pager {
  display: flex;
  align-items: center;
  justify-content: flex-end;
}

.filter-hint {
  color: #909399;
  font-size: 13px;
}
</style>

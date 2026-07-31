<template>
  <el-dialog
    v-model="visible"
    title="Agent 对话日志"
    width="900px"
    :close-on-click-modal="false"
    class="conversation-dialog"
    top="5vh"
    :destroy-on-close="true"
  >
    <div class="conversation-container">
      <!-- 顶部信息栏 -->
      <div class="conversation-toolbar">
        <div class="toolbar-left">
          <el-tag size="small" type="info">{{ task?.task_type }}</el-tag>
          <span class="toolbar-repo">{{ task?.repo || '-' }}#{{ task?.issue_id || '-' }}</span>
        </div>
        <div class="toolbar-right">
          <el-tag v-if="conversationCount > 0" size="small" type="primary" effect="plain">
            {{ conversationCount }} 条消息 · {{ iterationCount }} 轮
          </el-tag>
          <el-radio-group v-if="viewModeOptions.length > 1" v-model="viewMode" size="small">
            <el-radio-button label="iteration">按轮次</el-radio-button>
            <el-radio-button label="timeline">时间线</el-radio-button>
          </el-radio-group>
        </div>
      </div>

      <!-- 加载中 -->
      <div v-loading="conversationLoading" class="conversation-body">
        <!-- 错误状态 -->
        <el-alert
          v-if="conversationError && !conversationLoading"
          type="error"
          :closable="false"
          show-icon
          :title="conversationError"
        >
          <template #default>
            <el-button size="small" type="primary" plain @click="retryLoad">重试</el-button>
          </template>
        </el-alert>

        <!-- 空状态 -->
        <el-empty
          v-else-if="!conversationLoading && conversationMessages.length === 0"
          description="暂无对话日志"
        >
          <template #description>
            <p>请在「系统配置」开启 debug.conversation_log.enabled 后重新跑任务</p>
            <p class="empty-hint">仅多轮 Agent Loop（如 solve_issue / solve_comment）会写入对话日志</p>
          </template>
        </el-empty>

        <!-- 按轮次视图 -->
        <div v-else-if="viewMode === 'iteration'" class="iteration-view">
          <el-collapse v-model="openIterations">
            <el-collapse-item
              v-for="group in conversationByIteration"
              :key="group.iteration"
              :name="String(group.iteration)"
            >
              <template #title>
                <div class="iteration-header">
                  <el-icon class="iteration-icon"><Operation /></el-icon>
                  <span class="iteration-title">第 {{ group.iteration }} 轮</span>
                  <el-tag size="small" type="info" effect="plain">{{ group.messages.length }} 条消息</el-tag>
                </div>
              </template>
              <div class="iteration-messages">
                <div
                  v-for="msg in group.messages"
                  :key="msg.id"
                  class="conv-msg"
                  :class="`msg-role-${msg.role}`"
                >
                  <div class="conv-msg-meta">
                    <el-tag size="small" :type="roleTagType(msg.role)" effect="dark">{{ roleLabel(msg.role) }}</el-tag>
                    <span v-if="msg.tool_call_id" class="meta-badge">tool_call: {{ msg.tool_call_id }}</span>
                    <span class="meta-badge">seq: {{ msg.seq }}</span>
                  </div>
                  <div v-if="msg.content" class="conv-content-wrapper">
                    <div class="content-header">
                      <span class="content-label">内容</span>
                      <el-button
                        size="small"
                        link
                        aria-label="复制内容"
                        title="复制内容"
                        @click="copyToClipboard(msg.content)"
                      >
                        <el-icon><CopyDocument /></el-icon>
                      </el-button>
                    </div>
                    <pre class="conv-content">{{ msg.content }}</pre>
                  </div>
                  <div v-if="msg.tool_calls" class="conv-content-wrapper">
                    <div class="content-header">
                      <span class="content-label">工具调用</span>
                      <el-button
                        size="small"
                        link
                        aria-label="复制工具调用"
                        title="复制工具调用"
                        @click="copyToClipboard(getFormattedToolCalls(msg.tool_calls, msg.id))"
                      >
                        <el-icon><CopyDocument /></el-icon>
                      </el-button>
                    </div>
                    <pre class="conv-content tool-calls">{{ getFormattedToolCalls(msg.tool_calls, msg.id) }}</pre>
                  </div>
                </div>
              </div>
            </el-collapse-item>
          </el-collapse>
        </div>

        <!-- 时间线视图 -->
        <div v-else class="timeline-view">
          <el-timeline>
            <el-timeline-item
              v-for="msg in conversationMessages"
              :key="msg.id"
              :type="timelineType(msg.role)"
              :timestamp="`#${msg.seq} · 第${msg.iteration}轮`"
              placement="top"
            >
              <div class="timeline-msg">
                <div class="msg-header">
                  <el-tag size="small" :type="roleTagType(msg.role)" effect="dark">
                    {{ roleLabel(msg.role) }}
                  </el-tag>
                  <span v-if="msg.tool_call_id" class="meta-badge">tool_call: {{ msg.tool_call_id }}</span>
                </div>
                <div v-if="msg.content" class="conv-content-wrapper">
                  <pre class="conv-content">{{ msg.content }}</pre>
                </div>
                <div v-if="msg.tool_calls" class="conv-content-wrapper">
                  <div class="content-header">
                    <span class="content-label">工具调用</span>
                  </div>
                  <pre class="conv-content tool-calls">{{ getFormattedToolCalls(msg.tool_calls, msg.id) }}</pre>
                </div>
              </div>
            </el-timeline-item>
          </el-timeline>
        </div>
      </div>
    </div>
  </el-dialog>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { Operation, CopyDocument } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import api from '../api'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  task: { type: Object, default: null }
})

const emit = defineEmits(['update:modelValue'])

const visible = computed({
  get: () => props.modelValue,
  set: (val) => emit('update:modelValue', val)
})

const conversationMessages = ref([])
const conversationCount = ref(0)
const conversationLoading = ref(false)
const conversationError = ref('')
const openIterations = ref([])
const viewMode = ref('iteration')

// 请求竞态保护：递增 requestId，仅最新请求生效
let currentRequestId = 0

const viewModeOptions = ['iteration', 'timeline']

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

const iterationCount = computed(() => conversationByIteration.value.length)

// 缓存 formatToolCalls 结果，避免重复解析
const toolCallsCache = new Map()

const getFormattedToolCalls = (raw, msgId) => {
  if (toolCallsCache.has(msgId)) {
    return toolCallsCache.get(msgId)
  }
  let result = ''
  if (!raw) {
    result = ''
  } else {
    try {
      result = JSON.stringify(JSON.parse(raw), null, 2)
    } catch {
      result = raw
    }
  }
  toolCallsCache.set(msgId, result)
  return result
}

const roleTagType = (role) => {
  const types = { system: 'info', user: '', assistant: 'success', tool: 'warning' }
  return types[role] || 'info'
}

const roleLabel = (role) => {
  const labels = { system: '系统', user: '用户', assistant: '助手', tool: '工具结果' }
  return labels[role] || role
}

const timelineType = (role) => {
  const types = { system: 'info', user: 'primary', assistant: 'success', tool: 'warning' }
  return types[role] || 'info'
}

const copyToClipboard = async (text) => {
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success('已复制到剪贴板')
  } catch {
    ElMessage.error('复制失败')
  }
}

const loadConversation = async (taskId) => {
  // 生成新的请求 ID，使旧请求失效
  const requestId = ++currentRequestId
  conversationLoading.value = true
  conversationError.value = ''
  conversationMessages.value = []
  // 清除缓存
  toolCallsCache.clear()
  try {
    const res = await api.get(`/tasks/${taskId}/conversation`)
    // 检查是否为最新请求，防止后发先至
    if (requestId !== currentRequestId) return
    conversationMessages.value = res?.messages || []
    conversationCount.value = res?.count || conversationMessages.value.length
    // 默认只展开最后一轮
    const iterations = conversationByIteration.value
    openIterations.value = iterations.length > 0 ? [String(iterations[iterations.length - 1].iteration)] : []
  } catch (error) {
    // 检查是否为最新请求
    if (requestId !== currentRequestId) return
    conversationMessages.value = []
    conversationCount.value = 0
    // 区分网络错误和权限错误
    const status = error?.response?.status
    if (status === 403) {
      conversationError.value = '无权限查看对话日志'
    } else if (status === 404) {
      conversationError.value = '任务不存在'
    } else if (status && status >= 500) {
      conversationError.value = '服务器错误，请稍后重试'
    } else {
      conversationError.value = error?.message || '加载对话日志失败'
    }
  } finally {
    if (requestId === currentRequestId) {
      conversationLoading.value = false
    }
  }
}

const retryLoad = () => {
  if (props.task?.id) {
    loadConversation(props.task.id)
  }
}

// 同时监听可见性与 task.id，确保切换任务时重新加载
watch(
  () => [props.modelValue, props.task?.id],
  ([visible, taskId]) => {
    if (visible && taskId) {
      loadConversation(taskId)
    } else if (!visible) {
      // 关闭时作废进行中请求，并清理数据释放内存
      currentRequestId++
      conversationLoading.value = false
      conversationMessages.value = []
      conversationCount.value = 0
      conversationError.value = ''
      toolCallsCache.clear()
    }
  }
)
</script>

<style scoped>
.conversation-dialog :deep(.el-dialog__body) {
  padding: 0;
}

.conversation-container {
  display: flex;
  flex-direction: column;
  height: 70vh;
}

.conversation-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 20px;
  border-bottom: 1px solid #ebeef5;
  background: #fafafa;
}

.toolbar-left {
  display: flex;
  align-items: center;
  gap: 12px;
}

.toolbar-repo {
  font-size: 14px;
  color: #606266;
}

.toolbar-right {
  display: flex;
  align-items: center;
  gap: 16px;
}

.conversation-body {
  flex: 1;
  overflow: auto;
  padding: 16px 20px;
}

.empty-hint {
  font-size: 12px;
  color: #909399;
  margin-top: 8px;
}

/* 迭代视图 */
.iteration-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.iteration-icon {
  color: #409eff;
}

.iteration-title {
  font-weight: 600;
}

.iteration-messages {
  padding: 8px 0;
}

.conv-msg {
  border-radius: 8px;
  padding: 12px 16px;
  margin-bottom: 12px;
  border-left: 4px solid #dcdfe6;
  background: #fafafa;
}

.conv-msg.msg-role-system {
  border-left-color: #909399;
  background: #f4f4f5;
}

.conv-msg.msg-role-user {
  border-left-color: #409eff;
  background: #ecf5ff;
}

.conv-msg.msg-role-assistant {
  border-left-color: #67c23a;
  background: #f0f9eb;
}

.conv-msg.msg-role-tool {
  border-left-color: #e6a23c;
  background: #fdf6ec;
}

.conv-msg-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.meta-badge {
  font-size: 11px;
  color: #909399;
  background: #f4f4f5;
  padding: 2px 6px;
  border-radius: 4px;
}

.conv-content-wrapper {
  margin-top: 8px;
}

.content-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 4px;
}

.content-label {
  font-size: 12px;
  color: #909399;
  font-weight: 500;
}

.conv-content {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
  line-height: 1.6;
  max-height: 300px;
  overflow: auto;
  background: #fff;
  border-radius: 6px;
  padding: 12px;
  border: 1px solid #ebeef5;
}

.conv-content.tool-calls {
  max-height: 200px;
  background: #1e1e1e;
  color: #d4d4d4;
  border-color: #333;
}

/* 时间线视图 */
.timeline-view {
  padding: 0 8px;
}

.timeline-msg {
  background: #fafafa;
  border-radius: 8px;
  padding: 12px;
  border: 1px solid #ebeef5;
}

.msg-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
</style>

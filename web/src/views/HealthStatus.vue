<script setup>
import { ref, onMounted, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { getHealthSummary } from '../api'

const loading = ref(false)
const summary = ref(null)

const COMPONENT_LABELS = {
  gitea: 'Gitea',
  llm: 'LLM 模型',
  hub_backends: 'Hub 编码后端',
  deliver: '出站投递',
  database: '数据库',
  disk: '磁盘空间'
}

const STATUS_META = {
  ok: { label: '正常', type: 'success' },
  degraded: { label: '降级', type: 'warning' },
  unconfigured: { label: '未配置', type: 'info' },
  disabled: { label: '未启用', type: 'info' },
  error: { label: '错误', type: 'danger' }
}

const orderedKeys = ['gitea', 'llm', 'hub_backends', 'deliver', 'database', 'disk']

const overallMeta = computed(() => {
  if (!summary.value) return STATUS_META.unconfigured
  return summary.value.healthy ? STATUS_META.ok : STATUS_META.error
})

function statusMeta(status) {
  return STATUS_META[status] || STATUS_META.unconfigured
}

function humanBytes(n) {
  if (!n && n !== 0) return '-'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

async function refresh() {
  loading.value = true
  try {
    summary.value = await getHealthSummary()
  } catch (e) {
    ElMessage.error('获取健康状态失败：' + (e.message || e))
  } finally {
    loading.value = false
  }
}

onMounted(refresh)
defineExpose({ refresh })
</script>

<template>
  <div class="health-status">
    <div class="health-header">
      <div class="health-title">
        <h3>系统健康状态</h3>
        <el-tag v-if="summary" :type="overallMeta.type" effect="dark" size="small">
          {{ overallMeta.label }}
        </el-tag>
      </div>
      <el-button :loading="loading" size="small" @click="refresh">重新检测</el-button>
    </div>

    <el-alert
      v-if="summary && summary.warnings && summary.warnings.length"
      type="warning"
      :closable="false"
      show-icon
      class="health-warnings"
    >
      <template #title>
        <div v-for="(w, i) in summary.warnings" :key="i" class="warn-line">{{ w }}</div>
      </template>
    </el-alert>

    <div v-if="summary" class="health-grid">
      <el-card
        v-for="key in orderedKeys"
        :key="key"
        class="health-card"
        shadow="hover"
      >
        <div class="health-card-head">
          <span class="health-card-title">{{ COMPONENT_LABELS[key] || key }}</span>
          <el-tag :type="statusMeta(summary.components[key]?.status).type" size="small">
            {{ statusMeta(summary.components[key]?.status).label }}
          </el-tag>
        </div>
        <div class="health-card-msg">{{ summary.components[key]?.message }}</div>

        <template v-if="key === 'disk' && summary.components[key]?.detail">
          <el-progress
            :percentage="Math.round(summary.components[key].detail.used_pct || 0)"
            :status="summary.components[key].status === 'degraded' ? 'exception' : ''"
            :stroke-width="10"
          />
          <div class="disk-detail">
            已用 {{ humanBytes(summary.components[key].detail.used_bytes) }} /
            共 {{ humanBytes(summary.components[key].detail.total_bytes) }}
          </div>
        </template>

        <template v-if="key === 'hub_backends' && summary.hub_backends && summary.hub_backends.length">
          <ul class="hub-list">
            <li v-for="hb in summary.hub_backends" :key="hb.name">
              <el-tag :type="statusMeta(hb.status).type" size="small">{{ statusMeta(hb.status).label }}</el-tag>
              <span class="hub-name">{{ hb.name }}</span>
              <span class="hub-type">{{ hb.type }}</span>
              <span v-if="hb.message" class="hub-msg">{{ hb.message }}</span>
            </li>
          </ul>
        </template>
      </el-card>
    </div>

    <div v-else-if="!loading" class="health-empty">暂无数据</div>
  </div>
</template>

<style scoped>
.health-status {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.health-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.health-title {
  display: flex;
  align-items: center;
  gap: 10px;
}
.health-title h3 {
  margin: 0;
  font-size: 16px;
}
.health-warnings .warn-line {
  line-height: 1.6;
}
.health-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 12px;
}
.health-card {
  font-size: 13px;
}
.health-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}
.health-card-title {
  font-weight: 600;
}
.health-card-msg {
  color: var(--el-text-color-secondary);
  line-height: 1.5;
  min-height: 20px;
}
.disk-detail {
  margin-top: 6px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.hub-list {
  list-style: none;
  padding: 0;
  margin: 8px 0 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.hub-list li {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.hub-name {
  font-weight: 600;
}
.hub-type {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.hub-msg {
  color: var(--el-color-danger);
  font-size: 12px;
}
.health-empty {
  color: var(--el-text-color-secondary);
  padding: 16px;
  text-align: center;
}
</style>

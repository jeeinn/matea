<template>
  <el-dialog v-model="visible" title="LLM Provider 配置说明" width="720px" :close-on-click-modal="false">
    <p>
      使用 JSON 配置多个 Provider。顶层 key 为 Provider 名称（创建 Agent 时选择），每个 Provider 包含
      <code>base_url</code> 与 <code>api_key</code> 两个字段。
    </p>
    <el-alert
      title="字段名请使用 base_url / api_key（小写+下划线）。保存后系统会统一规范化。"
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 12px"
    />
    <div class="example-header">
      <span>配置示例（可复制）</span>
      <el-button size="small" type="primary" @click="copyExample">复制示例</el-button>
    </div>
    <el-input type="textarea" :rows="16" :model-value="exampleJson" readonly class="example-box" />
    <h4 style="margin-top: 16px">常见 Provider</h4>
    <el-table :data="providerTips" border size="small">
      <el-table-column prop="name" label="名称" width="120" />
      <el-table-column prop="base_url" label="base_url" />
      <el-table-column prop="note" label="说明" />
    </el-table>
  </el-dialog>
</template>

<script setup>
import { ref } from 'vue'
import { copyToClipboard } from '../utils/clipboard'

const visible = ref(false)

const exampleJson = `{
  "deepseek": {
    "base_url": "https://api.deepseek.com/v1",
    "api_key": "sk-xxx"
  },
  "openai": {
    "base_url": "https://api.openai.com/v1",
    "api_key": "sk-xxx"
  },
  "claude": {
    "base_url": "",
    "api_key": "sk-ant-xxx"
  },
  "ollama": {
    "base_url": "http://localhost:11434/v1",
    "api_key": "ollama"
  },
  "sensenova": {
    "base_url": "https://token.sensenova.cn/v1",
    "api_key": "sk-xxx"
  }
}`

const providerTips = [
  { name: 'deepseek', base_url: 'https://api.deepseek.com/v1', note: 'OpenAI 兼容，模型如 deepseek-v4-flash' },
  { name: 'openai', base_url: 'https://api.openai.com/v1', note: '官方 OpenAI API' },
  { name: 'claude', base_url: '（可留空）', note: 'Anthropic，仅需 api_key' },
  { name: 'ollama', base_url: 'http://localhost:11434/v1', note: '本地模型，api_key 填 ollama' },
  { name: '自定义', base_url: '服务商文档中的 /v1 地址', note: 'key 名称可自定，如 sensenova' }
]

const show = () => { visible.value = true }

const copyExample = async () => {
  await copyToClipboard(exampleJson, '示例已复制到剪贴板')
}

defineExpose({ show })
</script>

<style scoped>
.example-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  font-size: 13px;
  color: #606266;
}

.example-box :deep(textarea) {
  font-family: Consolas, Monaco, monospace;
  font-size: 12px;
}
</style>

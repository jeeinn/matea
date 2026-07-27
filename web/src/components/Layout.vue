<template>
  <el-container class="layout-container">
    <el-aside width="200px" class="aside">
      <div class="logo">
        <h3>Agent Gateway</h3>
      </div>
      <el-menu
        :default-active="activeMenu"
        router
        background-color="#304156"
        text-color="#bfcbd9"
        active-text-color="#409eff"
      >
        <el-menu-item index="/">
          <el-icon><Monitor /></el-icon>
          <span>仪表盘</span>
        </el-menu-item>
        <el-menu-item index="/tasks">
          <el-icon><List /></el-icon>
          <span>任务列表</span>
        </el-menu-item>
        <el-menu-item index="/workflows">
          <el-icon><Link /></el-icon>
          <span>工作流</span>
        </el-menu-item>
        <el-menu-item index="/logs">
          <el-icon><Document /></el-icon>
          <span>审计日志</span>
        </el-menu-item>
        <el-menu-item index="/agents">
          <el-icon><User /></el-icon>
          <span>Agent 管理</span>
        </el-menu-item>
        <el-menu-item v-if="authStore.isAdmin" index="/users">
          <el-icon><UserFilled /></el-icon>
          <span>用户管理</span>
        </el-menu-item>
        <el-menu-item v-if="authStore.isAdmin" index="/config">
          <el-icon><Tools /></el-icon>
          <span>系统配置</span>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="header">
        <div class="header-left">
          <el-breadcrumb separator="/">
            <el-breadcrumb-item :to="{ path: '/' }">首页</el-breadcrumb-item>
            <el-breadcrumb-item v-if="currentRoute">{{ currentRoute }}</el-breadcrumb-item>
          </el-breadcrumb>
        </div>
        <div class="header-right">
          <el-dropdown @command="handleCommand">
            <span class="user-info">
              <el-avatar :size="32" icon="UserFilled" />
              <span class="username">{{ authStore.user?.display_name || authStore.user?.username }}</span>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="profile">个人信息</el-dropdown-item>
                <el-dropdown-item command="change-password">修改密码</el-dropdown-item>
                <el-dropdown-item command="logout" divided>退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="main">
        <el-alert
          v-if="setupRequired"
          class="setup-banner"
          type="warning"
          show-icon
          :closable="false"
          title="系统尚未完成初始化配置"
        >
          <template #default>
            <div class="setup-banner-body">
              <span>{{ setupDescription }}</span>
              <el-button v-if="authStore.isAdmin" type="primary" size="small" @click="router.push('/config')">
                前往系统配置
              </el-button>
            </div>
          </template>
        </el-alert>
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import api from '../api'
import { Monitor, User, List, Link, UserFilled, Tools, Document } from '@element-plus/icons-vue'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()

const setupStatus = ref(null)

const activeMenu = computed(() => route.path)
const currentRoute = computed(() => {
  const name = route.name
  return name !== 'Dashboard' ? name : null
})

const setupRequired = computed(() => !!setupStatus.value?.setup_required)
const setupDescription = computed(() => {
  const missing = setupStatus.value?.missing || []
  if (!missing.length) {
    return '请在系统配置中填写 Gitea 与 LLM 信息后再接收 Webhook。'
  }
  const parts = []
  if (!setupStatus.value?.gitea_ok) parts.push('Gitea（URL / Token / Webhook Secret）')
  if (!setupStatus.value?.llm_ok) parts.push('LLM（Providers / 默认模型）')
  return `仍缺少：${parts.join('、') || missing.join(', ')}。配置完成前任务与 Webhook 可能无法正常工作。`
})

const loadSetupStatus = async () => {
  if (!authStore.isAuthenticated || authStore.mustChangePassword) {
    setupStatus.value = null
    return
  }
  try {
    setupStatus.value = await api.get('/setup/status')
  } catch {
    setupStatus.value = null
  }
}

onMounted(loadSetupStatus)
watch(() => route.path, (path) => {
  if (path === '/config' || path === '/') {
    loadSetupStatus()
  }
})

const handleCommand = async (command) => {
  if (command === 'logout') {
    await authStore.logout()
    router.push('/login')
  } else if (command === 'change-password') {
    router.push('/change-password')
  }
}
</script>

<style scoped>
.layout-container {
  height: 100vh;
}

.aside {
  background-color: #304156;
  overflow-y: auto;
}

.logo {
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
}

.logo h3 {
  margin: 0;
  font-size: 16px;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #fff;
  border-bottom: 1px solid #e6e6e6;
  padding: 0 20px;
}

.header-left {
  display: flex;
  align-items: center;
}

.header-right {
  display: flex;
  align-items: center;
}

.user-info {
  display: flex;
  align-items: center;
  cursor: pointer;
}

.username {
  margin-left: 8px;
  font-size: 14px;
}

.main {
  background-color: #f5f7fa;
  padding: 20px;
}

.setup-banner {
  margin-bottom: 16px;
}

.setup-banner-body {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
</style>

import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useSetupStore } from '../stores/setup'

const routes = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('../views/Login.vue'),
    meta: { guest: true }
  },
  {
    // First-run wizard (Phase 2.5 C-1). Guest route — the router guard below
    // sends everyone here while the backend reports setup_required, and
    // bounces initialized instances to /login.
    path: '/setup',
    name: 'Setup',
    component: () => import('../views/SetupWizard.vue'),
    meta: { guest: true }
  },
  {
    path: '/change-password',
    name: 'ChangePassword',
    component: () => import('../views/ChangePassword.vue'),
    meta: { requiresAuth: true, allowPasswordChange: true }
  },
  {
    path: '/',
    name: 'Layout',
    component: () => import('../components/Layout.vue'),
    meta: { requiresAuth: true },
    children: [
      {
        path: '',
        name: 'Dashboard',
        component: () => import('../views/Dashboard.vue')
      },
      {
        path: 'agents',
        name: 'Agents',
        component: () => import('../views/Agents.vue')
      },
      {
        path: 'agents/:id',
        name: 'AgentDetail',
        component: () => import('../views/AgentDetail.vue')
      },
      {
        path: 'tasks',
        name: 'Tasks',
        component: () => import('../views/Tasks.vue')
      },
      {
        path: 'workflows',
        name: 'Workflows',
        component: () => import('../views/WorkflowDetail.vue')
      },
      {
        path: 'logs',
        name: 'OperationLogs',
        component: () => import('../views/OperationLogs.vue')
      },
      {
        path: 'users',
        name: 'Users',
        component: () => import('../views/Users.vue'),
        meta: { roles: ['admin'] }
      },
      {
        path: 'config',
        name: 'SystemConfig',
        component: () => import('../views/SystemConfig.vue'),
        meta: { roles: ['admin'] }
      }
    ]
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

router.beforeEach(async (to, from, next) => {
  const setupStore = useSetupStore()
  const authStore = useAuthStore()

  // C-3: first-run redirect. While the instance is uninitialized every page
  // lands on /setup; once initialized, /setup itself is closed off.
  await setupStore.ensureLoaded()
  if (setupStore.setupRequired && to.path !== '/setup') {
    next('/setup')
    return
  }
  if (!setupStore.setupRequired && !setupStore.loadFailed && to.path === '/setup') {
    next('/login')
    return
  }

  if (to.meta.requiresAuth && !authStore.isAuthenticated) {
    next('/login')
    return
  }

  if (to.meta.guest && authStore.isAuthenticated) {
    next(authStore.mustChangePassword ? '/change-password' : '/')
    return
  }

  if (authStore.isAuthenticated && authStore.mustChangePassword && !to.meta.allowPasswordChange) {
    next('/change-password')
    return
  }

  if (to.meta.roles && !to.meta.roles.includes(authStore.user?.role)) {
    next('/')
    return
  }

  next()
})

export default router

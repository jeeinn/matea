import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const routes = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('../views/Login.vue'),
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

router.beforeEach((to, from, next) => {
  const authStore = useAuthStore()

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

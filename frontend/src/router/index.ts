import { createRouter, createWebHistory } from 'vue-router'
import { getToken } from '../api'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('../views/LoginView.vue'), meta: { title: '登录', public: true } },
    { path: '/', name: 'dashboard', component: () => import('../views/DashboardView.vue'), meta: { title: '仪表盘' } },
    { path: '/devices', name: 'devices', component: () => import('../views/DevicesView.vue'), meta: { title: '设备' } },
    { path: '/devices/:id', name: 'device-detail', component: () => import('../views/DeviceDetailView.vue'), meta: { title: '设备详情' } },
    { path: '/templates', name: 'templates', component: () => import('../views/TemplatesView.vue'), meta: { title: '模板库' } },
    { path: '/cluster', name: 'cluster', component: () => import('../views/ClusterView.vue'), meta: { title: '集群管理' } },
    { path: '/settings', name: 'settings', component: () => import('../views/SettingsView.vue'), meta: { title: '设置' } },
    { path: '/admin', name: 'admin', component: () => import('../views/AdminView.vue'), meta: { title: '系统管理', roles: ['admin'] } },
    { path: '/automation', name: 'automation', component: () => import('../views/AutomationView.vue'), meta: { title: '自动化' } },
    { path: '/terminal/:sessionId', name: 'terminal', component: () => import('../views/TerminalView.vue'), meta: { title: '终端' } },
  ],
})

// 路由守卫：未登录去 /login；角色不匹配提示
router.beforeEach(to => {
  if (!to.meta.public && !getToken()) return { name: 'login' }
  return true
})

export default router

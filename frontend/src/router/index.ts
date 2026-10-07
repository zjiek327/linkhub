import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'dashboard', component: () => import('../views/DashboardView.vue'), meta: { title: '仪表盘' } },
    { path: '/devices', name: 'devices', component: () => import('../views/DevicesView.vue'), meta: { title: '设备' } },
    { path: '/devices/:id', name: 'device-detail', component: () => import('../views/DeviceDetailView.vue'), meta: { title: '设备详情' } },
    { path: '/templates', name: 'templates', component: () => import('../views/TemplatesView.vue'), meta: { title: '模板库' } },
    { path: '/cluster', name: 'cluster', component: () => import('../views/ClusterView.vue'), meta: { title: '集群管理' } },
    { path: '/settings', name: 'settings', component: () => import('../views/SettingsView.vue'), meta: { title: '设置' } },
    { path: '/terminal/:sessionId', name: 'terminal', component: () => import('../views/TerminalView.vue'), meta: { title: '终端' } },
  ],
})

export default router

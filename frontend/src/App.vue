<template>
  <el-container v-if="$route.path !== '/login'" style="height: 100vh">
    <el-aside :width="collapsed ? '64px' : '220px'" class="sidebar">
      <div class="logo" :class="{ mini: collapsed }">
        <span class="oct">🐙</span>
        <span v-if="!collapsed">灵枢 LinkHub<br /><small>连连 · 设备连接平台</small></span>
      </div>
      <el-menu :default-active="$route.path" router :collapse="collapsed" :collapse-transition="false"
               background-color="#001529" text-color="#a6adb4" active-text-color="#fff"
               style="border-right:none">
        <el-menu-item index="/"><el-icon><Monitor /></el-icon><template #title>{{ $t('menu.dashboard') }}</template></el-menu-item>
        <el-menu-item index="/devices"><el-icon><Cpu /></el-icon><template #title>{{ $t('menu.devices') }}</template></el-menu-item>
        <el-menu-item index="/templates"><el-icon><Files /></el-icon><template #title>{{ $t('menu.templates') }}</template></el-menu-item>
        <el-menu-item index="/automation"><el-icon><VideoPlay /></el-icon><template #title>{{ $t('menu.automation') }}</template></el-menu-item>
        <el-menu-item index="/cluster"><el-icon><Connection /></el-icon><template #title>{{ $t('menu.cluster') }}</template></el-menu-item>
        <el-menu-item index="/settings"><el-icon><Setting /></el-icon><template #title>{{ $t('menu.settings') }}</template></el-menu-item>
        <el-menu-item v-if="me?.role === 'admin'" index="/admin"><el-icon><UserFilled /></el-icon><template #title>{{ $t('menu.admin') }}</template></el-menu-item>
      </el-menu>
      <div class="collapse-btn" @click="toggleCollapse" :title="collapsed ? '展开菜单' : '收起菜单'">
        <el-icon><Expand v-if="collapsed" /><Fold v-else /></el-icon>
        <span v-if="!collapsed">收起菜单</span>
      </div>
    </el-aside>
    <el-container>
      <el-header style="display:flex;align-items:center;justify-content:space-between;border-bottom:1px solid var(--el-border-color)">
        <span style="font-size:16px;font-weight:600">{{ $route.meta.title }}</span>
        <div style="display:flex;align-items:center;gap:12px">
          <el-tag type="success" effect="plain" size="small">🐙 连连在线</el-tag>
          <el-tag size="small" effect="plain">{{ me?.name }} · {{ me?.role }}</el-tag>
          <el-button size="small" text @click="logout">{{ $t('menu.logout') }}</el-button>
        </div>
      </el-header>
      <el-main style="padding:0">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
  <router-view v-else />
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Monitor, Cpu, Files, Connection, Setting, UserFilled, VideoPlay, Fold, Expand } from '@element-plus/icons-vue'
import { api, setToken, type Me } from './api'
import { settings } from './api/settings'
import './api/settings'  // 启动即应用主题

const route = useRoute()
const router = useRouter()
const { locale } = useI18n()
const me = ref<Me | null>(null)

// 侧边栏折叠：只显示图标，悬停显示菜单名（el-menu collapse 内置 tooltip）
const collapsed = ref(localStorage.getItem('linkhub_menu_collapsed') === '1')
function toggleCollapse() {
  collapsed.value = !collapsed.value
  localStorage.setItem('linkhub_menu_collapsed', collapsed.value ? '1' : '0')
}

async function refreshMe() {
  try { me.value = await api.me() } catch { /* 401 拦截器会跳登录 */ }
}

async function logout() {
  setToken('')
  me.value = null
  router.push('/login')
}

onMounted(refreshMe)
watch(() => route.fullPath, refreshMe)
// 设置页改语言即时生效
watch(() => settings.language, v => { locale.value = v === 'system' ? (navigator.language.startsWith('zh') ? 'zh' : 'en') : v })
</script>

<style scoped>
.sidebar { background: #001529; display: flex; flex-direction: column; transition: width .2s; overflow: hidden; }
.logo { display: flex; align-items: center; gap: 10px; font-size: 18px; font-weight: 700; color: #fff; padding: 0 20px; height: 60px; flex-shrink: 0; }
.logo.mini { justify-content: center; padding: 0; }
.logo .oct { font-size: 26px; flex-shrink: 0; }
.logo small { font-weight: 400; opacity: .7; font-size: 12px; }
.collapse-btn { margin-top: auto; display: flex; align-items: center; justify-content: center; gap: 8px;
  height: 44px; color: #a6adb4; cursor: pointer; font-size: 13px; flex-shrink: 0; }
.collapse-btn:hover { color: #fff; background: rgba(255,255,255,.06); }
</style>

<template>
  <el-container v-if="$route.path !== '/login'" style="height: 100vh">
    <el-aside width="220px" style="background: #001529">
      <div class="logo">
        <span class="oct">🐙</span>
        <span>灵枢 LinkHub<br /><small>连连 · 设备连接平台</small></span>
      </div>
      <el-menu :default-active="$route.path" router background-color="#001529"
               text-color="#a6adb4" active-text-color="#fff">
        <el-menu-item index="/"><el-icon><Monitor /></el-icon>仪表盘</el-menu-item>
        <el-menu-item index="/devices"><el-icon><Cpu /></el-icon>设备管理</el-menu-item>
        <el-menu-item index="/templates"><el-icon><Files /></el-icon>模板库</el-menu-item>
        <el-menu-item index="/automation"><el-icon><VideoPlay /></el-icon>自动化</el-menu-item>
        <el-menu-item index="/cluster"><el-icon><Connection /></el-icon>集群管理</el-menu-item>
        <el-menu-item index="/settings"><el-icon><Setting /></el-icon>设置</el-menu-item>
        <el-menu-item v-if="me?.role === 'admin'" index="/admin"><el-icon><UserFilled /></el-icon>系统管理</el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header style="display:flex;align-items:center;justify-content:space-between;border-bottom:1px solid var(--el-border-color)">
        <span style="font-size:16px;font-weight:600">{{ $route.meta.title }}</span>
        <div style="display:flex;align-items:center;gap:12px">
          <el-tag type="success" effect="plain" size="small">🐙 连连在线</el-tag>
          <el-tag size="small" effect="plain">{{ me?.name }} · {{ me?.role }}</el-tag>
          <el-button size="small" text @click="logout">退出</el-button>
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
import { Monitor, Cpu, Files, Connection, Setting, UserFilled, VideoPlay } from '@element-plus/icons-vue'
import { api, setToken, type Me } from './api'
import './api/settings'  // 启动即应用主题

const route = useRoute()
const router = useRouter()
const me = ref<Me | null>(null)

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
</script>

<style scoped>
.logo { display: flex; align-items: center; gap: 10px; font-size: 18px; font-weight: 700; color: #fff; padding: 0 20px; height: 60px; }
.logo .oct { font-size: 26px; }
.logo small { font-weight: 400; opacity: .7; font-size: 12px; }
</style>

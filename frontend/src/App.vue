<template>
  <el-container style="height: 100vh">
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
        <el-menu-item index="/cluster"><el-icon><Connection /></el-icon>集群管理</el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header style="display:flex;align-items:center;justify-content:space-between;border-bottom:1px solid #eee">
        <span style="font-size:16px;font-weight:600">{{ $route.meta.title }}</span>
        <el-tag type="success" effect="plain">🐙 连连在线</el-tag>
      </el-header>
      <el-main style="background:#f5f7fa;padding:0">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { Monitor, Cpu, Files, Connection } from '@element-plus/icons-vue'
</script>

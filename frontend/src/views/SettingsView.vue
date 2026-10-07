<template>
  <div class="page">
    <el-card shadow="never" style="max-width:640px">
      <template #header>外观</template>
      <el-form label-width="110px">
        <el-form-item label="主题模式">
          <el-radio-group v-model="settings.themeMode">
            <el-radio-button value="system">跟随系统</el-radio-button>
            <el-radio-button value="light">浅色</el-radio-button>
            <el-radio-button value="dark">深色</el-radio-button>
          </el-radio-group>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card shadow="never" style="max-width:640px;margin-top:16px">
      <template #header>终端</template>
      <el-form label-width="110px">
        <el-form-item label="字号">
          <el-slider v-model="settings.terminal.fontSize" :min="10" :max="24" show-input />
        </el-form-item>
        <el-form-item label="字体">
          <el-select v-model="settings.terminal.fontFamily" style="width:100%">
            <el-option v-for="f in FONT_FAMILIES" :key="f.value" :label="f.label" :value="f.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="配色主题">
          <div class="theme-list">
            <div v-for="(t, key) in TERMINAL_THEMES" :key="key"
                 class="theme-item" :class="{ active: settings.terminal.theme === key }"
                 :style="{ background: t.background, color: t.foreground }"
                 @click="settings.terminal.theme = key; settings.terminal.background = ''">
              {{ key }}<br />$ ls -la
            </div>
          </div>
        </el-form-item>
        <el-form-item label="自定义背景">
          <el-color-picker v-model="settings.terminal.background" show-alpha />
          <el-button text size="small" style="margin-left:10px"
                     @click="settings.terminal.background = ''">恢复主题默认</el-button>
        </el-form-item>
        <el-form-item label="预览">
          <div class="preview" :style="previewStyle">pi@raspberrypi:~$ uname -a<br/>Linux raspberrypi 6.6.31 aarch64 GNU/Linux</div>
        </el-form-item>
      </el-form>
      <el-alert type="info" :closable="false">设置自动保存，对本浏览器所有新打开/已打开的终端即时生效（重开终端页最佳）。</el-alert>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { FONT_FAMILIES, TERMINAL_THEMES, settings, terminalThemeOf } from '../api/settings'

const previewStyle = computed(() => ({
  background: terminalThemeOf().background,
  color: terminalThemeOf().foreground,
  fontFamily: settings.terminal.fontFamily,
  fontSize: settings.terminal.fontSize + 'px',
}))
</script>

<style scoped>
.theme-list { display: flex; gap: 10px; flex-wrap: wrap; }
.theme-item { padding: 10px 14px; border-radius: 6px; cursor: pointer; border: 2px solid transparent;
  font-family: monospace; font-size: 12px; line-height: 1.5; }
.theme-item.active { border-color: var(--el-color-primary); }
.preview { padding: 14px; border-radius: 6px; line-height: 1.6; width: 100%; }
</style>

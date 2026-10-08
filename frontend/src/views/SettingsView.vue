<template>
  <div class="page">
    <el-card shadow="never" style="max-width:640px">
      <template #header>{{ $t('settings.appearance') }}</template>
      <el-form label-width="110px">
        <el-form-item :label="$t('settings.language')">
          <el-radio-group v-model="settings.language">
            <el-radio-button value="system">{{ $t('settings.system') }}</el-radio-button>
            <el-radio-button value="zh">中文</el-radio-button>
            <el-radio-button value="en">English</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="$t('settings.themeMode')">
          <el-radio-group v-model="settings.themeMode">
            <el-radio-button value="system">{{ $t('settings.system') }}</el-radio-button>
            <el-radio-button value="light">{{ $t('settings.light') }}</el-radio-button>
            <el-radio-button value="dark">{{ $t('settings.dark') }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card shadow="never" style="max-width:640px;margin-top:16px">
      <template #header>终端</template>
      <el-form label-width="110px">
        <el-form-item :label="$t('settings.fontSize')">
          <el-slider v-model="settings.terminal.fontSize" :min="10" :max="24" show-input />
        </el-form-item>
        <el-form-item :label="$t('settings.fontFamily')">
          <el-select v-model="settings.terminal.fontFamily" style="width:100%">
            <el-option v-for="f in FONT_FAMILIES" :key="f.value" :label="f.label" :value="f.value" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('settings.theme')">
          <div class="theme-list">
            <div v-for="(t, key) in TERMINAL_THEMES" :key="key"
                 class="theme-item" :class="{ active: settings.terminal.theme === key }"
                 :style="{ background: t.background, color: t.foreground }"
                 @click="settings.terminal.theme = key; settings.terminal.background = ''">
              {{ key }}<br />$ ls -la
            </div>
          </div>
        </el-form-item>
        <el-form-item :label="$t('settings.customBg')">
          <el-color-picker v-model="settings.terminal.background" show-alpha />
          <el-button text size="small" style="margin-left:10px"
                     @click="settings.terminal.background = ''">{{ $t('settings.resetTheme') }}</el-button>
        </el-form-item>
        <el-form-item :label="$t('settings.autoSyncSize')">
          <el-switch v-model="settings.terminal.autoSyncSize" />
          <span class="hint">窗口变化时自动向远端注入 stty 对齐终端尺寸（tmux/vim 用）；
            注意：登录提示符阶段会注入成用户名，此时请用终端工具栏的「⇲ 适配大小」手动同步</span>
        </el-form-item>
        <el-form-item label="预览">
          <div class="preview" :style="previewStyle">pi@raspberrypi:~$ uname -a<br/>Linux raspberrypi 6.6.31 aarch64 GNU/Linux</div>
        </el-form-item>
      </el-form>
      <el-alert type="info" :closable="false">{{ $t('settings.langTip') }}</el-alert>
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
.hint { font-size: 12px; color: var(--el-text-color-secondary); margin-left: 10px; line-height: 1.4; }
</style>

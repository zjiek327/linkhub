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

    <el-card shadow="never" style="max-width:640px;margin-top:16px">
      <template #header>
        <div style="display:flex;justify-content:space-between;align-items:center">
          <span>发现网络</span>
          <el-button size="small" @click="loadIfaces">刷新网卡</el-button>
        </div>
      </template>
      <el-alert type="info" :closable="false" style="margin-bottom:10px">
        多网卡服务器可选择从哪些网卡发送节点发现广播（UDP 37890）。默认全部网卡。
      </el-alert>
      <el-checkbox-group v-model="beaconIfaces">
        <div v-for="i in ifaces" :key="i.name + i.ip" class="iface">
          <el-checkbox :value="i.broadcast">
            <span class="mono">{{ i.name }}</span>
            <span class="mono" style="margin-left:10px">{{ i.ip }}</span>
            <span style="color:var(--el-text-color-secondary);font-size:12px;margin-left:8px">广播 {{ i.broadcast }}</span>
          </el-checkbox>
        </div>
      </el-checkbox-group>
      <div style="margin-top:10px;color:var(--el-text-color-secondary);font-size:12px">
        已选 {{ beaconIfaces.length }} / {{ ifaces.length }} 张网卡（全不选 = 全部发送）
      </div>
      <el-button type="primary" size="small" style="margin-top:10px" :loading="saving" @click="saveIfaces">
        保存发现网络
      </el-button>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { FONT_FAMILIES, TERMINAL_THEMES, settings, terminalThemeOf } from '../api/settings'
import { http } from '../api'

const previewStyle = computed(() => ({
  background: terminalThemeOf().background,
  color: terminalThemeOf().foreground,
  fontFamily: settings.terminal.fontFamily,
  fontSize: settings.terminal.fontSize + 'px',
}))

// 发现网络（网卡选择）
const ifaces = ref<any[]>([])
const beaconIfaces = ref<string[]>([])
const saving = ref(false)

async function loadIfaces() {
  try {
    ifaces.value = await http.get('/system/interfaces').then(r => r.data)
    const cur = await http.get('/system/beacon-interfaces').then(r => r.data)
    beaconIfaces.value = cur.interfaces
  } catch { /* 无权限或后端未启用 */ }
}
async function saveIfaces() {
  saving.value = true
  try {
    await http.post('/system/beacon-interfaces', { broadcasts: beaconIfaces.value })
    ElMessage.success('已保存，下一轮发现广播生效')
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail ?? '保存失败')
  } finally { saving.value = false }
}

onMounted(loadIfaces)
</script>

<style scoped>
.theme-list { display: flex; gap: 10px; flex-wrap: wrap; }
.theme-item { padding: 10px 14px; border-radius: 6px; cursor: pointer; border: 2px solid transparent;
  font-family: monospace; font-size: 12px; line-height: 1.5; }
.theme-item.active { border-color: var(--el-color-primary); }
.preview { padding: 14px; border-radius: 6px; line-height: 1.6; width: 100%; }
.hint { font-size: 12px; color: var(--el-text-color-secondary); margin-left: 10px; line-height: 1.4; }
.iface { padding: 4px 0; }
.mono { font-family: monospace; }
</style>

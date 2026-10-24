<template>
  <div class="desk-pane">
    <div class="desk-toolbar">
      <el-tag size="small" effect="dark" :type="phase === 'online' ? 'success' : 'info'">
        {{ phaseText }}
      </el-tag>
      <span style="flex:1" />
      <template v-if="phase === 'online'">
        <el-button size="small" @click="sendCAD">Ctrl+Alt+Del</el-button>
        <el-button size="small" @click="toggleFullscreen">⛶ 全屏</el-button>
        <el-button size="small" type="danger" plain @click="disconnect">断开</el-button>
      </template>
    </div>

    <div v-if="phase === 'auth'" class="desk-auth">
      <el-form label-width="90px" style="max-width:360px;margin:40px auto 0">
        <el-form-item label="VNC 密码" required>
          <el-input v-model="password" type="password" show-password autofocus
                    placeholder="目标节点 VNC 服务端的访问密码"
                    @keyup.enter="connect" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :disabled="!password" @click="connect">连接桌面</el-button>
        </el-form-item>
      </el-form>
    </div>

    <div v-show="phase !== 'auth'" ref="screenEl" class="desk-screen" />
    <div v-if="errorMsg" class="desk-error">
      <el-alert type="error" :closable="false">{{ errorMsg }}
        <el-button size="small" style="margin-left:10px" @click="phase = 'auth'; errorMsg = ''">重新输入密码</el-button>
      </el-alert>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { ElButton, ElForm, ElFormItem, ElInput, ElTag, ElAlert } from 'element-plus'
import RFB from '@novnc/novnc'
import { wsUrl } from '../api'

const props = withDefaults(defineProps<{ node?: string; nodeName?: string }>(), { node: '', nodeName: '' })

const phase = ref<'auth' | 'connecting' | 'online' | 'closed'>('auth')
const errorMsg = ref('')
const password = ref('')
const screenEl = ref<HTMLElement>()

let rfb: RFB | null = null

const phaseText = computed(() => ({
  auth: '待输入密码', connecting: '连接中…', online: `已连接 ${props.nodeName || '本机'}`, closed: '已断开',
}[phase.value]))

function connect() {
  phase.value = 'connecting'
  errorMsg.value = ''
  // 等 v-show 把容器渲染出来
  requestAnimationFrame(() => {
    const url = wsUrl(`/ws/desktop?node=${props.node}`)
    rfb = new RFB(screenEl.value!, url, {
      credentials: { username: '', password: password.value, target: '' },
    })
    rfb.scaleViewport = true
    rfb.addEventListener('connect', () => { phase.value = 'online' })
    rfb.addEventListener('disconnect', (e: any) => {
      phase.value = 'closed'
      if (!e.detail?.clean) errorMsg.value = '连接失败或被拒绝（密码错误或 VNC 服务不可达）'
    })
    rfb.addEventListener('securityfailure', (e: any) => {
      errorMsg.value = `VNC 安全协商失败：${e.detail?.reason || '未知原因'}`
    })
  })
}

function sendCAD() { rfb?.sendCtrlAltDel() }
function toggleFullscreen() { screenEl.value?.requestFullscreen?.() }
function disconnect() { rfb?.disconnect(); phase.value = 'closed' }

onUnmounted(() => { rfb?.disconnect(); rfb = null })
</script>

<style scoped>
.desk-pane { display: flex; flex-direction: column; height: 76vh; }
.desk-toolbar { display: flex; align-items: center; gap: 8px; padding-bottom: 8px; }
.desk-screen {
  flex: 1; min-height: 0; background: #000; border-radius: 6px; overflow: hidden;
}
.desk-screen :deep(canvas) { outline: none; }
.desk-error { padding-top: 8px; }
</style>

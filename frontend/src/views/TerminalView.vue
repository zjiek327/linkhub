<template>
  <div class="terminal-page">
    <div class="bar">
      <el-button size="small" @click="$router.back()">← 返回</el-button>
      <span class="title">会话 #{{ sessionId }}</span>
      <el-tag v-if="remoteNode" size="small" type="warning" effect="plain">经节点中继</el-tag>
      <StatusBadge :status="status" />
      <span v-if="lastError" class="err">{{ lastError }}</span>
      <div style="flex:1" />
      <el-button size="small" type="danger" plain :disabled="status === 'closed'" @click="close">关闭会话</el-button>
    </div>
    <div ref="termEl" class="term" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api, wsUrl } from '../api'
import StatusBadge from '../components/StatusBadge.vue'

const route = useRoute()
const sessionId = Number(route.params.sessionId)
const remoteNode = (route.query.node as string) || ''

const termEl = ref<HTMLElement>()
const status = ref('connecting')
const lastError = ref('')

let term: Terminal | null = null
let fit: FitAddon | null = null
let ws: WebSocket | null = null
let resizeObserver: ResizeObserver | null = null
const encoder = new TextEncoder()

onMounted(async () => {
  term = new Terminal({
    fontFamily: "'JetBrains Mono', 'Cascadia Code', Menlo, monospace",
    fontSize: 14, cursorBlink: true, convertEol: false,
    theme: { background: '#0c0c0c' },
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(termEl.value!)
  fit.fit()

  resizeObserver = new ResizeObserver(() => fit?.fit())
  resizeObserver.observe(termEl.value!)

  try {
    if (remoteNode) {
      status.value = 'online'  // 远程会话状态由归属节点维护，中继连上即视为在线
    } else {
      const s = await api.session(sessionId)
      status.value = s.status
      lastError.value = s.last_error
    }
  } catch { /* 会话可能刚关闭 */ }

  const nodeQ = remoteNode ? `?node=${remoteNode}` : ''
  ws = new WebSocket(wsUrl(`/ws/terminal/${sessionId}${nodeQ}`))
  ws.binaryType = 'arraybuffer'
  ws.onopen = () => { status.value = 'online'; term?.focus() }
  ws.onmessage = ev => term?.write(new Uint8Array(ev.data as ArrayBuffer))
  ws.onclose = ev => {
    status.value = 'closed'
    term?.write(`\r\n\x1b[33m[连接已断开${ev.reason ? '：' + ev.reason : ''}]\x1b[0m\r\n`)
  }
  term.onData(d => { if (ws?.readyState === WebSocket.OPEN) ws.send(encoder.encode(d)) })
})

async function close() {
  if (remoteNode) await api.proxyCloseSession(remoteNode, sessionId)
  else await api.closeSession(sessionId)
  status.value = 'closed'
  ws?.close()
}

onUnmounted(() => {
  resizeObserver?.disconnect()
  ws?.close()
  term?.dispose()
})
</script>

<style scoped>
.terminal-page { display: flex; flex-direction: column; height: 100%; background: #0c0c0c; }
.bar { display: flex; align-items: center; gap: 12px; padding: 8px 14px; background: #1d1d1d; color: #ddd; }
.bar .title { font-weight: 600; }
.bar .err { color: #f56c6c; font-size: 12px; }
.term { flex: 1; padding: 6px; overflow: hidden; }
</style>

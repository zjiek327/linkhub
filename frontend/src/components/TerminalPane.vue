<template>
  <div class="pane">
    <div class="bar">
      <span class="title">{{ displayName || `会话 #${sessionId}` }}</span>
      <el-tag v-if="remoteNode" size="small" type="warning" effect="plain">经节点中继</el-tag>
      <StatusBadge :status="status" />
      <el-tag :type="amWriter ? 'success' : 'info'" size="small" effect="dark">
        {{ amWriter ? '✏️ 主控' : '👁 旁观' }}
      </el-tag>
      <el-tag v-if="viewers > 1" size="small" effect="plain">{{ viewers }} 人在看</el-tag>
      <span v-if="lastError" class="err">{{ lastError }}</span>
      <div style="flex:1" />
      <el-button v-if="!amWriter" size="small" type="warning" @click="requestWrite">{{ $t('terminal.requestInput') }}</el-button>
      <el-button v-else size="small" plain @click="releaseWrite">{{ $t('terminal.releaseControl') }}</el-button>
      <el-tooltip content="会话活着则重连数据流；已断开则用同一连接配置秒开新会话" placement="bottom">
        <el-button size="small" type="primary" plain :loading="reconnecting" @click="reconnect">{{ $t('terminal.reconnect') }}</el-button>
      </el-tooltip>
      <el-tooltip content="串口无窗口尺寸协商，tmux/vim/htop 显示异常时点这里（注入 stty rows/cols）" placement="bottom">
        <el-button size="small" @click="syncSize">{{ $t('terminal.syncSize') }}</el-button>
      </el-tooltip>
      <el-button size="small" type="danger" plain :disabled="status === 'closed'" @click="close">{{ $t('terminal.closeSession') }}</el-button>
    </div>
    <div ref="termEl" class="term" />
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElButton, ElMessage, ElNotification } from 'element-plus'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { useI18n } from 'vue-i18n'
import { api, wsUrl } from '../api'
import { settings, terminalThemeOf } from '../api/settings'
import StatusBadge from './StatusBadge.vue'

const { t } = useI18n()
const props = defineProps<{
  sessionId: number
  connId: number | null
  remoteNode: string
  displayName: string
}>()
const emit = defineEmits<{
  status: [s: { online: boolean; name?: string }]
  openNew: [payload: { id: number; connId?: number | null; remoteNode?: string; name?: string }]
}>()

const status = ref('connecting')
const lastError = ref('')
const reconnecting = ref(false)
const termEl = ref<HTMLElement>()

let selfNodeId = ''
let term: Terminal | null = null
let fit: FitAddon | null = null
let ws: WebSocket | null = null
let resizeObserver: ResizeObserver | null = null
let stopWatch: (() => void) | null = null
let sizeSynced = false
let sessionDead = false
const encoder = new TextEncoder()

const cid = (() => {
  let v = sessionStorage.getItem('linkhub_cid')
  if (!v) { v = 'cid-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 12); sessionStorage.setItem('linkhub_cid', v) }
  return v
})()
const myName = '用户-' + cid.replace(/^cid-/, '').slice(0, 6)
const amWriter = ref(true)
const viewers = ref(1)

function syncSize() {
  if (!term || !ws || ws.readyState !== WebSocket.OPEN) return
  ws.send(encoder.encode(`stty rows ${term.rows} cols ${term.cols}\r`))
  sizeSynced = true
}

function refit() {
  fit?.fit()
  if (sizeSynced || settings.terminal.autoSyncSize) syncSize()
}
defineExpose({ refit, close })

function initTerm() {
  if (!term) {
    term = new Terminal({
      fontFamily: settings.terminal.fontFamily,
      fontSize: settings.terminal.fontSize, cursorBlink: true, convertEol: false,
      theme: terminalThemeOf(),
    })
    fit = new FitAddon()
    term.loadAddon(fit)
    term.open(termEl.value!)
    fit.fit()
    resizeObserver = new ResizeObserver(() => refit())
    resizeObserver.observe(termEl.value!)
    stopWatch = watch(() => ({ ...settings.terminal }), () => {
      if (!term) return
      term.options.fontFamily = settings.terminal.fontFamily
      term.options.fontSize = settings.terminal.fontSize
      term.options.theme = terminalThemeOf()
      fit?.fit()
    }, { deep: true })
    term.onData(d => { if (ws?.readyState === WebSocket.OPEN) ws.send(encoder.encode(d)) })
  } else {
    term.reset()
    fit?.fit()
  }
}

function sendCtrl(msg: Record<string, any>) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ lh: msg }))
}
function applyRole() { if (term) term.options.disableStdin = !amWriter.value }
function requestWrite() { sendCtrl({ type: 'request_write', name: myName }) }
function releaseWrite() { sendCtrl({ type: 'release_write' }); ElMessage.info(t('terminal.released')) }

function notifyInputRequest(from: string, name: string) {
  const n = ElNotification({
    title: t('terminal.inputRequest'), type: 'warning', position: 'top-right', duration: 0,
    message: h('div', [
      h('p', { style: 'margin:0 0 8px' }, `${name || from} ${t('terminal.inputRequest')}`),
      h('div', { style: 'display:flex;gap:8px' }, [
        h(ElButton, { size: 'small', type: 'primary', onClick: () => { sendCtrl({ type: 'grant_write', to: from }); n.close() } }, () => t('terminal.approve')),
        h(ElButton, { size: 'small', onClick: () => { sendCtrl({ type: 'deny_write', to: from }); n.close() } }, () => t('terminal.deny')),
      ]),
    ]),
  })
}

function handleCtrl(msg: Record<string, any>) {
  switch (msg.type) {
    case 'role': amWriter.value = msg.writer === cid; applyRole(); break
    case 'presence': viewers.value = msg.viewers; break
    case 'input_request': if (amWriter.value) notifyInputRequest(msg.from, msg.name); break
    case 'role_change': {
      const nowWriter = msg.writer === cid
      if (nowWriter !== amWriter.value) {
        amWriter.value = nowWriter
        applyRole()
        ElMessage[nowWriter ? 'success' : 'info'](nowWriter ? t('terminal.gotInput') : `${t('terminal.inputMovedTo')} ${msg.writer_name || '他人'}`)
      }
      break
    }
    case 'input_denied': if (msg.to === cid) ElMessage.warning(t('terminal.denied')); break
    case 'input_blocked': ElMessage.warning(t('terminal.viewOnlyHint')); break
    case 'writer_free': if (!amWriter.value) ElMessage.info(t('terminal.writerFree')); break
  }
}

function connectWs() {
  ws?.close()
  sessionDead = false
  const nodeQ = props.remoteNode ? `&node=${props.remoteNode}` : ''
  ws = new WebSocket(wsUrl(`/ws/terminal/${props.sessionId}?cid=${cid}&name=${myName}${nodeQ}`))
  ws.binaryType = 'arraybuffer'
  ws.onopen = () => { status.value = 'online'; emit('status', { online: true, name: props.displayName }); term?.focus() }
  ws.onmessage = ev => {
    if (typeof ev.data === 'string') {
      try {
        const body = JSON.parse(ev.data)
        if (body?.lh) { handleCtrl(body.lh); return }
      } catch { /* 非 JSON */ }
      term?.write(ev.data)
      return
    }
    term?.write(new Uint8Array(ev.data as ArrayBuffer))
  }
  ws.onclose = async ev => {
    status.value = 'closed'
    emit('status', { online: false })
    if (ev.code === 4404 || (ev.reason || '').includes('会话已结束')) sessionDead = true
    else if (!props.remoteNode) {
      try {
        const s = await api.session(props.sessionId)
        if (['closed', 'error'].includes(s.status)) sessionDead = true
      } catch { sessionDead = true }
    }
    term?.write(`\r\n\x1b[33m[连接已断开${ev.reason ? '：' + ev.reason : ''}，点「↻ 重连」恢复]\x1b[0m\r\n`)
  }
}

async function loadStatus() {
  if (props.remoteNode) { status.value = 'online'; return }
  try {
    const s = await api.session(props.sessionId)
    status.value = s.status
    lastError.value = s.last_error
  } catch { /* 会话可能刚关闭 */ }
}

async function reconnect() {
  reconnecting.value = true
  try {
    if (!sessionDead) { connectWs(); return }
    if (!props.connId) { ElMessage.warning('缺少连接配置信息，请回设备详情页重新打开'); return }
    const r = props.remoteNode
      ? await api.proxyOpen(props.remoteNode, props.connId)
      : await api.openSession(props.connId)
    const nid = (r as any).node_id
    const node = props.remoteNode || (nid && nid !== 'local' && nid !== selfNodeId ? nid : '')
    ElMessage.success(t('terminal.reconnected'))
    emit('openNew', { id: (r as any).session_id ?? (r as any).id,
                      connId: props.connId, remoteNode: node || props.remoteNode, name: props.displayName })
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail?.message ?? e.response?.data?.detail ?? t('terminal.reconnecting'))
  } finally { reconnecting.value = false }
}

async function close() {
  if (props.remoteNode) await api.proxyCloseSession(props.remoteNode, props.sessionId)
  else await api.closeSession(props.sessionId)
  status.value = 'closed'
  ws?.close()
}

onMounted(async () => {
  try { selfNodeId = (await api.clusterInfo()).node_id } catch { /* 单机 */ }
  initTerm(); connectWs(); loadStatus()
})

watch(() => props.sessionId, (n, o) => {
  if (n && n !== o) { initTerm(); connectWs(); loadStatus() }
})

onUnmounted(() => {
  stopWatch?.()
  resizeObserver?.disconnect()
  ws?.close()
  term?.dispose()
})
</script>

<style scoped>
.pane { display: flex; flex-direction: column; height: 100%; background: #0c0c0c; }
.bar { display: flex; align-items: center; gap: 12px; padding: 8px 14px; background: #1d1d1d; color: #ddd; }
.bar .title { font-weight: 600; }
.bar .err { color: #f56c6c; font-size: 12px; }
.term { flex: 1; padding: 6px; overflow: hidden; }
</style>

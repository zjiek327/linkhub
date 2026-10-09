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
        <el-button size="small" @click="injectStty">{{ $t('terminal.syncSize') }}</el-button>
      </el-tooltip>
      <el-tooltip content="仅写入者或所属节点用户可关闭；旁观者只能观看" placement="bottom">
        <el-button v-if="amWriter || !remoteNode" size="small" type="danger" plain :disabled="status === 'closed'" @click="close">{{ $t('terminal.closeSession') }}</el-button>
      </el-tooltip>
    </div>
    <div ref="termEl" class="term" />

    <!-- 会话内协作聊天：悬浮可拖动，收起为气泡+未读角标+最新一条预览 -->
    <div v-if="chatVisible" class="chat-widget" :style="{ left: chatPos.x + 'px', top: chatPos.y + 'px' }">
      <template v-if="chatOpen">
        <div class="chat-head" @mousedown.prevent="chatDragStart">
          <span>💬 {{ $t('terminal.chatTitle') }}</span>
          <span class="chat-min" title="收起" @click="chatOpen = false">—</span>
        </div>
        <div ref="chatListEl" class="chat-body">
          <div v-for="(m, i) in chatMsgs" :key="i" class="chat-msg" :class="{ me: m.from === cid }">
            <div class="chat-meta">{{ m.from === cid ? '我' : m.name }} · {{ m.ts }}</div>
            <div class="chat-text">{{ m.text }}</div>
          </div>
          <div v-if="!chatMsgs.length" class="chat-empty">{{ $t('terminal.chatEmpty') }}</div>
        </div>
        <div class="chat-input">
          <el-input v-model="chatDraft" size="small" :placeholder="$t('terminal.chatPlaceholder')"
                    maxlength="500" @keydown.enter.prevent="sendChat" />
          <el-button size="small" type="primary" @click="sendChat">{{ $t('terminal.send') }}</el-button>
        </div>
      </template>
      <template v-else>
        <div class="chat-bubble" :title="$t('terminal.chatTip')" @mousedown.prevent="chatDragStart" @click="chatClick">
          💬<span v-if="chatUnread" class="chat-badge">{{ chatUnread > 9 ? '9+' : chatUnread }}</span>
        </div>
        <div v-if="lastChat" class="chat-preview" @click="chatClick">
          <b>{{ lastChat.from === cid ? '我' : lastChat.name }}:</b> {{ lastChat.text }}
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, h, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
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
let closedAnnounced = false
const encoder = new TextEncoder()

const cid = (() => {
  let v = sessionStorage.getItem('linkhub_cid')
  if (!v) { v = 'cid-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 12); sessionStorage.setItem('linkhub_cid', v) }
  return v
})()
const myName = '用户-' + cid.replace(/^cid-/, '').slice(0, 6)

// ---------- 会话内聊天 ----------
const chatVisible = ref(true)
const chatOpen = ref(false)
const chatUnread = ref(0)
const chatDraft = ref('')
const chatMsgs = ref<{ from: string; name: string; text: string; ts: string }[]>([])
const chatListEl = ref<HTMLElement | null>(null)
const chatPos = ref({ x: 0, y: 0 })
const lastChat = computed(() => chatMsgs.value.at(-1) ?? null)

function openChat() { chatOpen.value = true; chatUnread.value = 0; scrollChat() }
let chatDragMoved = false
function chatClick() { if (!chatDragMoved) openChat(); chatDragMoved = false }
function scrollChat() { nextTick(() => { if (chatListEl.value) chatListEl.value.scrollTop = chatListEl.value.scrollHeight }) }
function sendChat() {
  const text = chatDraft.value.trim()
  if (!text) return
  sendCtrl({ type: 'chat', text })
  chatDraft.value = ''
}
function chatDragStart(ev: MouseEvent) {
  const el = (ev.currentTarget as HTMLElement).parentElement!
  const startX = ev.clientX - chatPos.value.x
  const startY = ev.clientY - chatPos.value.y
  chatDragMoved = false
  const move = (e: MouseEvent) => {
    if (Math.abs(e.clientX - ev.clientX) + Math.abs(e.clientY - ev.clientY) > 4) chatDragMoved = true
    const w = el.parentElement?.clientWidth ?? window.innerWidth
    const h = el.parentElement?.clientHeight ?? window.innerHeight
    chatPos.value = {
      x: Math.min(Math.max(0, e.clientX - startX), Math.max(0, w - el.offsetWidth)),
      y: Math.min(Math.max(0, e.clientY - startY), Math.max(0, h - 40)),
    }
  }
  const up = () => {
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
    sessionStorage.setItem('linkhub_chat_pos', JSON.stringify(chatPos.value))
  }
  window.addEventListener('mousemove', move)
  window.addEventListener('mouseup', up)
}
function resetChat() {
  chatMsgs.value = []
  chatUnread.value = 0
  chatOpen.value = false
  chatDraft.value = ''
  const saved = sessionStorage.getItem('linkhub_chat_pos')
  chatPos.value = saved ? JSON.parse(saved) : { x: 0, y: 0 }
}
const amWriter = ref(true)
const viewers = ref(1)

let pendingSizeKey = ''
let sizeTimer: number | undefined

// 自动同步：走 resize 控制消息（服务端调 PTY 尺寸），防抖避免侧栏动画/窗口
// 拖动时连续 refit 把一串 stty 命令打进 shell
function syncSize() {
  if (!term || !ws || ws.readyState !== WebSocket.OPEN) return
  const key = `${term.rows}x${term.cols}`
  if (key === pendingSizeKey) return
  clearTimeout(sizeTimer)
  sizeTimer = window.setTimeout(() => {
    if (!term || !ws || ws.readyState !== WebSocket.OPEN) return
    pendingSizeKey = `${term.rows}x${term.cols}`
    ws.send(JSON.stringify({ lh: { type: 'resize', rows: term.rows, cols: term.cols } }))
  }, 300)
}

// 手动按钮：串口/tmux 等无 PTY 协商的场景，直接注入 stty 命令
function injectStty() {
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
    case 'chat': {
      chatMsgs.value.push({ from: msg.from, name: msg.name, text: msg.text, ts: msg.ts })
      if (!chatOpen.value) chatUnread.value++
      scrollChat()
      break
    }
    case 'input_denied': if (msg.to === cid) ElMessage.warning(t('terminal.denied')); break
    case 'input_blocked': ElMessage.warning(t('terminal.viewOnlyHint')); break
    case 'writer_free': if (!amWriter.value) ElMessage.info(t('terminal.writerFree')); break
    case 'session_status':
      // 会话被关闭（本地或经中继都会收到此控制帧）：立即置死并上屏提示
      if (msg.status === 'closed' && !sessionDead) {
        sessionDead = true
        closedAnnounced = true
        status.value = 'closed'
        emit('status', { online: false })
        term?.write(`\r\n\x1b[33m[会话已被关闭，点「↻ 重连」重新打开]\x1b[0m\r\n`)
      }
      break
  }
}

function connectWs() {
  ws?.close()
  sessionDead = false
  closedAnnounced = false
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
    if (closedAnnounced) return // 关闭提示已由 session_status 控制帧上屏
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
  resetChat()
  if (!sessionStorage.getItem('linkhub_chat_pos')) {
    nextTick(() => {
      const el = termEl.value
      if (el) chatPos.value = { x: Math.max(8, el.clientWidth - 270), y: Math.max(8, el.clientHeight - 330) }
    })
  }
  initTerm(); connectWs(); loadStatus()
})

watch(() => props.sessionId, (n, o) => {
  if (n && n !== o) { resetChat(); initTerm(); connectWs(); loadStatus() }
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
/* ---------- 会话聊天 ---------- */
.pane { position: relative; }
.chat-widget { position: absolute; z-index: 30; width: 270px; }
.chat-bubble { width: 46px; height: 46px; border-radius: 50%; background: #1f6feb; color: #fff;
  display: flex; align-items: center; justify-content: center; font-size: 22px; cursor: pointer;
  box-shadow: 0 2px 10px rgba(0,0,0,.55); user-select: none; }
.chat-bubble:hover { background: #388bfd; }
.chat-badge { position: absolute; top: -5px; right: -5px; background: #f56c6c; color: #fff;
  font-size: 11px; line-height: 18px; border-radius: 9px; padding: 0 5px; font-weight: 600; }
.chat-preview { position: absolute; left: 54px; top: 4px; background: #161616; color: #cfd3d6;
  border: 1px solid #333; border-radius: 8px; padding: 5px 9px; font-size: 12px; max-width: 230px;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis; cursor: pointer; }
.chat-head { background: #1f6feb; color: #fff; padding: 6px 10px; font-size: 12px; cursor: move;
  display: flex; justify-content: space-between; align-items: center; user-select: none;
  border-radius: 8px 8px 0 0; }
.chat-min { cursor: pointer; padding: 0 4px; opacity: .85; }
.chat-min:hover { opacity: 1; }
.chat-body { background: rgba(22,22,22,.96); height: 210px; overflow-y: auto; padding: 8px; }
.chat-msg { margin-bottom: 6px; }
.chat-msg.me { text-align: right; }
.chat-meta { font-size: 10px; color: #8a8f99; margin-bottom: 1px; }
.chat-text { font-size: 12px; color: #e8e8e8; background: #2a2a2a; display: inline-block;
  padding: 4px 9px; border-radius: 7px; max-width: 85%; word-break: break-word; text-align: left; }
.chat-msg.me .chat-text { background: #1f6feb; }
.chat-empty { color: #666; font-size: 12px; text-align: center; padding-top: 80px; }
.chat-input { display: flex; gap: 6px; padding: 6px; background: #161616; border-radius: 0 0 8px 8px; }
</style>
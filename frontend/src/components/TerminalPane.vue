<template>
  <div class="pane">
    <div class="bar">
      <span class="title">{{ displayName || `会话 #${sessionId}` }}</span>
      <el-tag v-if="remoteNode" size="small" type="warning" effect="plain">经节点中继</el-tag>
      <StatusBadge :status="status" />
      <el-tag v-if="connKind" size="small" effect="plain">{{ kindLabel(connKind) }}</el-tag>
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
      <span style="flex:1" />
    </div>
    <div ref="termEl" class="term" />

    <!-- SFTP 文件传输：左=本地源文件，右=设备目录 -->
    <el-dialog v-model="xferDlg" title="文件传输（SFTP）" width="760px">
      <div class="xfer-wrap">
        <div class="xfer-pane">
          <div class="xfer-title">本地（源文件）</div>
          <div class="xfer-local">
            <input ref="fileInputEl" type="file" multiple style="display:none" @change="onFilesChosen" />
            <el-button size="small" style="width:100%" @click="fileInputEl?.click()">选择文件…</el-button>
            <div v-for="(f, i) in localFiles" :key="i" class="xfer-file">
              <span class="xfer-file-name">{{ f.name }}</span>
              <span class="xfer-file-size">{{ fmtSize(f.size) }}</span>
            </div>
            <div v-if="!localFiles.length" class="xfer-empty">尚未选择文件</div>
          </div>
          <el-button type="primary" size="small" style="width:100%;margin-top:8px"
                     :disabled="!localFiles.length || uploading" :loading="uploading" @click="doUpload">
            {{ uploading ? '上传中…' : `上传到设备目录` }}
          </el-button>
        </div>
        <div class="xfer-pane">
          <div class="xfer-title">
            设备目录
            <span class="xfer-path" :title="remotePath">{{ remotePath }}</span>
          </div>
          <div class="xfer-remote">
            <div class="xfer-remote-row" @click="remotePathUp">
              <el-icon><ArrowUp /></el-icon><span>..</span>
            </div>
            <div v-for="e in remoteEntries" :key="e.name" class="xfer-remote-row"
                 :class="{ sel: selectedRemote?.name === e.name && !e.is_dir }"
                 @dblclick="!e.is_dir && (selectedRemote = e)" @click="e.is_dir ? enterRemoteDir(e.name) : (selectedRemote = e)">
              <el-icon v-if="e.is_dir"><Folder /></el-icon>
              <el-icon v-else><Document /></el-icon>
              <span class="xfer-file-name">{{ e.name }}</span>
              <span class="xfer-file-size">{{ e.is_dir ? '' : fmtSize(e.size) }}</span>
            </div>
            <div v-if="!remoteEntries.length" class="xfer-empty">空目录</div>
          </div>
          <el-button size="small" style="width:100%;margin-top:8px"
                     :disabled="!selectedRemote || uploading" @click="doDownload">
            下载选中文件
          </el-button>
        </div>
      </div>
    </el-dialog>

    <!-- 会话内协作聊天：悬浮可拖动；收起为渐变气泡+未读角标+最新消息预览卡 -->
    <div v-if="chatVisible" class="chat-widget" :style="{ left: chatPos.x + 'px', top: chatPos.y + 'px' }">
      <Transition name="chat-pop">
        <div v-if="chatOpen" class="chat-panel">
          <div class="chat-head" @mousedown.prevent="chatDragStart">
            <span class="chat-head-title">
              <el-icon><ChatDotRound /></el-icon>
              {{ $t('terminal.chatTitle') }}
              <span v-if="chatUnread" class="chat-head-unread">{{ chatUnread > 9 ? '9+' : chatUnread }}</span>
            </span>
            <span class="chat-min" :title="$t('terminal.chatMinimize')" @click="chatOpen = false">
              <el-icon><Minus /></el-icon>
            </span>
          </div>
          <div ref="chatListEl" class="chat-body">
            <div v-for="(m, i) in chatMsgs" :key="i" class="chat-msg" :class="{ me: m.from === cid }">
              <div class="chat-meta">{{ m.from === cid ? $t('terminal.chatMe') : m.name }} · {{ m.ts }}</div>
              <div class="chat-text">{{ m.text }}</div>
            </div>
            <div v-if="!chatMsgs.length" class="chat-empty">{{ $t('terminal.chatEmpty') }}</div>
          </div>
          <div class="chat-input">
            <el-tooltip content="文件传输（仅 SSH 会话支持）" placement="top">
              <button class="chat-tool" :disabled="!isSsh" :title="isSsh ? '文件传输' : '仅 SSH 会话支持文件传输'" @click="xferDlg = true">
                <el-icon><FolderOpened /></el-icon>
              </button>
            </el-tooltip>
            <el-input v-model="chatDraft" size="small" :placeholder="$t('terminal.chatPlaceholder')"
                      maxlength="500" @keydown.enter.prevent="sendChat" />
            <button class="chat-send" :disabled="!chatDraft.trim()" :title="$t('terminal.send')" @click="sendChat">
              <el-icon><Promotion /></el-icon>
            </button>
          </div>
        </div>
      </Transition>
      <Transition name="chat-pop">
        <div v-if="!chatOpen" class="chat-fab-row">
          <Transition name="chat-fade">
            <div v-if="lastChat" class="chat-preview" @click="chatClick">
              <span class="chat-preview-name">{{ lastChat.from === cid ? $t('terminal.chatMe') : lastChat.name }}</span>
              <span class="chat-preview-text">{{ lastChat.text }}</span>
            </div>
          </Transition>
          <button class="chat-fab" :title="$t('terminal.chatTip')" @mousedown.prevent="chatDragStart" @click="chatClick">
            <el-icon><ChatDotRound /></el-icon>
            <span v-if="chatUnread" class="chat-badge">{{ chatUnread > 9 ? '9+' : chatUnread }}</span>
          </button>
        </div>
      </Transition>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, h, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElButton, ElMessage, ElNotification } from 'element-plus'
import { ChatDotRound, Minus, Promotion, FolderOpened, Document, ArrowUp, ArrowDown } from '@element-plus/icons-vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { useI18n } from 'vue-i18n'
import { api, wsUrl } from '../api'
import { settings, terminalThemeOf } from '../api/settings'
import StatusBadge from './StatusBadge.vue'

const { t } = useI18n()
const props = withDefaults(defineProps<{
  sessionId: number
  connId: number | null
  remoteNode?: string
  displayName?: string
  readonly?: boolean
  kind?: string
}>(), { remoteNode: '', displayName: '', readonly: false, kind: '' })
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
const barVisible = ref(settings.terminal.toolbarVisible)
// 连接方式 + SFTP 文件传输
const connKind = ref('')
const isSsh = computed(() => connKind.value === 'ssh')
const xferDlg = ref(false)
const fileInputEl = ref<HTMLInputElement | null>(null)
const localFiles = ref<File[]>([])
const remotePath = ref('/')
const remoteEntries = ref<any[]>([])
const selectedRemote = ref<any>(null)
const uploading = ref(false)

function kindLabel(kind: string) {
  return { serial: '串口', ssh: 'SSH', telnet: 'Telnet', mqtt: 'MQTT' }[kind] ?? kind
}
function fmtSize(n: number) {
  if (n < 1024) return `${n} B`
  if (n < 1048576) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1048576).toFixed(1)} MB`
}
async function loadRemote(path = remotePath.value) {
  try {
    const r = await api.sftpList(props.sessionId, path)
    remotePath.value = r.path
    remoteEntries.value = r.entries
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail ?? '读取目录失败')
  }
}
function enterRemoteDir(name: string) {
  loadRemote((remotePath.value === '/' ? '' : remotePath.value) + '/' + name)
}
function remotePathUp() {
  if (remotePath.value === '/') return
  loadRemote(remotePath.value.replace(/\/[^^/]+$/, '') || '/')
}
function onFilesChosen(ev: Event) {
  const files = (ev.target as HTMLInputElement).files
  localFiles.value = files ? Array.from(files) : []
}
async function doUpload() {
  uploading.value = true
  try {
    for (const f of localFiles.value) {
      const form = new FormData()
      form.append('file', f)
      await api.sftpUpload(props.sessionId, remotePath.value, form)
    }
    ElMessage.success('上传完成')
    localFiles.value = []
    loadRemote()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail ?? '上传失败')
  } finally { uploading.value = false }
}
async function doDownload() {
  if (!selectedRemote.value) return
  const p = (remotePath.value === '/' ? '' : remotePath.value) + '/' + selectedRemote.value.name
  await api.sftpDownload(props.sessionId, p, selectedRemote.value.name)
}
function toggleBar() {
  barVisible.value = !barVisible.value
  settings.terminal.toolbarVisible = barVisible.value
  nextTick(() => paneFit())
}
function paneFit() { fit?.fit() }
const encoder = new TextEncoder()

const cid = (() => {
  let v = sessionStorage.getItem('linkhub_cid')
  if (!v) { v = 'cid-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 12); sessionStorage.setItem('linkhub_cid', v) }
  return v
})()
let myName = '用户-' + cid.replace(/^cid-/, '').slice(0, 6)

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
  // 以实际渲染位置为基准（避免任何状态不同步导致拖动时跳位）
  const rect = el.getBoundingClientRect()
  const offX = ev.clientX - rect.left
  const offY = ev.clientY - rect.top
  chatDragMoved = false
  const move = (e: MouseEvent) => {
    if (Math.abs(e.clientX - ev.clientX) + Math.abs(e.clientY - ev.clientY) > 4) chatDragMoved = true
    const w = el.parentElement?.clientWidth ?? window.innerWidth
    const h = el.parentElement?.clientHeight ?? window.innerHeight
    chatPos.value = {
      x: Math.min(Math.max(0, e.clientX - offX), Math.max(0, w - el.offsetWidth)),
      y: Math.min(Math.max(0, e.clientY - offY), Math.max(0, h - 40)),
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
  ws = new WebSocket(wsUrl(`/ws/terminal/${props.sessionId}?cid=${cid}&name=${myName}${nodeQ}${props.readonly ? '&observer=1' : ''}`))
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

// 连接方式：优先 URL 参数（跨节点会话本地查不到会话行），本地会话回退接口
watch(() => [props.sessionId, props.kind], async () => {
  if (props.kind) { connKind.value = props.kind; return }
  try {
    const s = await api.session(props.sessionId)
    connKind.value = s.kind ?? ''
  } catch { connKind.value = '' }
}, { immediate: true })

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
  try {
    const info = await api.clusterInfo()
    selfNodeId = info.node_id
    // 聊天名带集群节点名：多端协作时一眼看出对方在哪个节点
    if (info.name) myName = `${myName}@${info.name}`
  } catch { /* 单机 */ }
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
.pane { display: flex; flex-direction: column; height: 100%; background: var(--lh-workbench-bg); }
.bar { display: flex; align-items: center; gap: 12px; padding: 7px 14px; background: var(--lh-panel-bg);
  color: var(--lh-fg); border-bottom: 1px solid var(--lh-panel-border); }
.bar .title { font-weight: 600; }
.bar .err { color: #f56c6c; font-size: 12px; }
.term { flex: 1; padding: 6px; overflow: hidden; }
/* ---------- 会话聊天（跟随主题强调色） ---------- */
.pane { position: relative; }
.chat-widget { position: absolute; z-index: 30; width: 300px; font-family: inherit; }

/* 收起态：渐变圆钮 + 未读角标 + 预览卡 */
.chat-fab-row { display: flex; align-items: center; gap: 8px; }
.chat-fab { position: relative; width: 46px; height: 46px; border: none; border-radius: 50%;
  display: flex; align-items: center; justify-content: center; font-size: 22px; cursor: pointer;
  color: #fff; background: linear-gradient(135deg, rgba(255,255,255,.16), rgba(0,0,0,.26)), var(--lh-accent);
  box-shadow: 0 4px 18px rgba(0,0,0,.45);
  transition: transform .15s ease, box-shadow .15s ease; }
.chat-fab:hover { transform: scale(1.07); box-shadow: 0 6px 22px rgba(0,0,0,.5); }
.chat-fab:active { transform: scale(.97); }
.chat-badge { position: absolute; top: -4px; right: -4px; min-width: 18px; height: 18px; padding: 0 5px;
  border-radius: 9px; background: linear-gradient(135deg, #ff6b60, #e0483f); color: #fff;
  font-size: 11px; font-weight: 700; line-height: 18px; text-align: center;
  box-shadow: 0 0 0 2px var(--lh-workbench-bg); }
.chat-preview { max-width: 236px; padding: 7px 11px; border-radius: 10px; cursor: pointer;
  background: var(--lh-panel-bg);
  border: 1px solid var(--lh-panel-border); backdrop-filter: blur(8px);
  box-shadow: 0 4px 14px rgba(0,0,0,.35); font-size: 12px;
  display: flex; flex-direction: column; gap: 2px; overflow: hidden; }
.chat-preview:hover { border-color: var(--lh-accent); }
.chat-preview-name { color: var(--lh-accent); font-weight: 600; }
.chat-preview-text { color: var(--lh-fg); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

/* 展开态：面板 */
.chat-panel { width: 300px; border-radius: 12px; overflow: hidden;
  border: 1px solid var(--lh-panel-border);
  box-shadow: 0 10px 36px rgba(0,0,0,.5); }
.chat-head { height: 38px; padding: 0 12px; display: flex; align-items: center; justify-content: space-between;
  cursor: move; user-select: none; color: #fff;
  background: linear-gradient(135deg, rgba(255,255,255,.16), rgba(0,0,0,.26)), var(--lh-accent); }
.chat-head-title { display: flex; align-items: center; gap: 6px; font-size: 13px; font-weight: 600; }
.chat-head-unread { min-width: 17px; height: 17px; padding: 0 4px; border-radius: 9px; background: rgba(255,255,255,.25);
  font-size: 11px; line-height: 17px; text-align: center; font-weight: 700; }
.chat-min { cursor: pointer; display: flex; opacity: .8; padding: 2px; border-radius: 4px; }
.chat-min:hover { opacity: 1; background: rgba(255,255,255,.15); }
.chat-body { height: 230px; overflow-y: auto; padding: 10px;
  background: var(--lh-workbench-bg);
  backdrop-filter: blur(10px); }
.chat-body::-webkit-scrollbar { width: 6px; }
.chat-body::-webkit-scrollbar-thumb { background: rgba(255,255,255,.14); border-radius: 3px; }
.chat-body::-webkit-scrollbar-thumb:hover { background: rgba(255,255,255,.25); }
.chat-msg { margin-bottom: 8px; }
.chat-msg.me { text-align: right; }
.chat-meta { font-size: 10px; color: var(--lh-muted); margin-bottom: 2px; }
.chat-text { display: inline-block; max-width: 82%; padding: 6px 10px; font-size: 12px; line-height: 1.5;
  text-align: left; word-break: break-word; color: var(--lh-fg);
  background: rgba(255,255,255,.07);
  border: 1px solid var(--lh-panel-border); border-radius: 10px 10px 10px 3px; }
.chat-msg.me .chat-text { background: var(--lh-accent); border-color: transparent; color: #fff;
  border-radius: 10px 10px 3px 10px; }
.chat-empty { color: var(--lh-muted); font-size: 12px; text-align: center; padding-top: 92px; }
.chat-input { display: flex; align-items: center; gap: 8px; padding: 8px;
  background: var(--lh-panel-bg); border-top: 1px solid var(--lh-panel-border); }
.chat-input :deep(.el-input__wrapper) { border-radius: 16px; box-shadow: 0 0 0 1px var(--lh-panel-border) inset; }
.chat-send { flex-shrink: 0; width: 30px; height: 30px; border: none; border-radius: 50%; cursor: pointer;
  display: flex; align-items: center; justify-content: center; color: #fff; font-size: 15px;
  background: var(--lh-accent); transition: transform .12s ease, opacity .12s ease; }
.chat-send:hover:not(:disabled) { transform: scale(1.08); }
.chat-send:disabled { opacity: .35; cursor: default; }

/* ---------- SFTP 文件传输 ---------- */
.xfer-wrap { display: flex; gap: 12px; }
.xfer-pane { flex: 1; min-width: 0; border: 1px solid var(--el-border-color); border-radius: 8px; padding: 10px; }
.xfer-title { font-size: 12px; font-weight: 600; margin-bottom: 8px; display: flex; align-items: center; gap: 6px; }
.xfer-path { font-weight: 400; color: var(--el-text-color-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.xfer-local, .xfer-remote { height: 260px; overflow-y: auto; display: flex; flex-direction: column; gap: 4px; }
.xfer-remote-row { display: flex; align-items: center; gap: 6px; padding: 4px 6px; border-radius: 6px;
  cursor: pointer; font-size: 12px; color: var(--el-text-color-primary); }
.xfer-remote-row:hover { background: var(--el-fill-color-light); }
.xfer-remote-row.sel { background: var(--el-color-primary-light-9); color: var(--el-color-primary); }
.xfer-file-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.xfer-file-size { font-size: 11px; color: var(--el-text-color-secondary); }
.xfer-empty { color: var(--el-text-color-secondary); font-size: 12px; text-align: center; padding-top: 24px; }
.chat-tool { flex-shrink: 0; width: 28px; height: 28px; border: none; border-radius: 6px; cursor: pointer;
  display: flex; align-items: center; justify-content: center; color: var(--lh-muted); background: transparent; }
.chat-tool:hover:not(:disabled) { color: var(--lh-fg); background: rgba(128,128,128,.15); }
.chat-tool:disabled { opacity: .35; cursor: not-allowed; }

/* 进出场动画 */
.chat-pop-enter-active { transition: opacity .16s ease, transform .18s cubic-bezier(.34,1.5,.64,1); }
.chat-pop-leave-active { transition: opacity .12s ease, transform .12s ease; }
.chat-pop-enter-from, .chat-pop-leave-to { opacity: 0; transform: translateY(10px) scale(.94); }
.chat-fade-enter-active, .chat-fade-leave-active { transition: opacity .15s ease, transform .15s ease; }
.chat-fade-enter-from, .chat-fade-leave-to { opacity: 0; transform: translateX(-6px); }
</style>
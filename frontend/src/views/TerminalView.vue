<template>
  <div class="terminal-page">
    <div class="bar">
      <el-button size="small" @click="$router.back()">← 返回</el-button>
      <span class="title">会话 #{{ sessionId }}</span>
      <el-tag v-if="remoteNode" size="small" type="warning" effect="plain">经节点中继</el-tag>
      <StatusBadge :status="status" />
      <el-tag :type="amWriter ? 'success' : 'info'" size="small" effect="dark">
        {{ amWriter ? '✏️ 主控' : '👁 旁观' }}
      </el-tag>
      <el-tag v-if="viewers > 1" size="small" effect="plain">{{ viewers }} 人在看</el-tag>
      <span v-if="lastError" class="err">{{ lastError }}</span>
      <div style="flex:1" />
      <el-button v-if="!amWriter" size="small" type="warning" @click="requestWrite">✋ 申请输入</el-button>
      <el-button v-else size="small" plain @click="releaseWrite">✋ 释放控制</el-button>
      <el-tooltip content="会话活着则重连数据流；已断开则用同一连接配置秒开新会话" placement="bottom">
        <el-button size="small" type="primary" plain :loading="reconnecting" @click="reconnect">↻ 重连</el-button>
      </el-tooltip>
      <el-tooltip content="串口无窗口尺寸协商，tmux/vim/htop 显示异常时点这里（注入 stty rows/cols）"
                  placement="bottom">
        <el-button size="small" @click="syncSize">⇲ 适配大小</el-button>
      </el-tooltip>
      <el-button size="small" type="danger" plain :disabled="status === 'closed'" @click="close">关闭会话</el-button>
    </div>
    <div ref="termEl" class="term" />
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElButton, ElMessage, ElNotification } from 'element-plus'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api, wsUrl } from '../api'
import { settings, terminalThemeOf } from '../api/settings'
import StatusBadge from '../components/StatusBadge.vue'

const route = useRoute()
const router = useRouter()

const status = ref('connecting')
const lastError = ref('')
const reconnecting = ref(false)
const termEl = ref<HTMLElement>()
let selfNodeId = ''  // 本机节点 ID，用于区分"真远程"会话

let term: Terminal | null = null
let fit: FitAddon | null = null
let ws: WebSocket | null = null
let resizeObserver: ResizeObserver | null = null
let stopWatch: (() => void) | null = null
let sizeSynced = false  // 手动同步过尺寸后，resize 时持续跟随
let sessionDead = false // 后端会话已终结（4404），重连需重开会话
const encoder = new TextEncoder()

// ---- 多人协作角色：先进为主控（可写），其余旁观（只读） ----
// crypto.randomUUID 仅安全上下文（HTTPS/localhost）可用，局域网 HTTP 访问需要回退
function genClientId(): string {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return 'cid-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 12)
}
const cid = (() => {
  let v = sessionStorage.getItem('linkhub_cid')
  if (!v) { v = genClientId(); sessionStorage.setItem('linkhub_cid', v) }
  return v
})()
const myName = '用户-' + cid.slice(0, 4)
const amWriter = ref(true)
const viewers = ref(1)

const sessionId = computed(() => Number(route.params.sessionId))
const remoteNode = computed(() => (route.query.node as string) || '')
// 连接配置 ID（打开终端时由详情页带上），用于会话终结后重开
const connId = computed(() => Number(route.query.conn) || null)

/** 串口没有窗口尺寸协商（winsize），远端 tty 默认 80x24，
 *  tmux/vim 会缩在左上角。注入 stty 把远端尺寸对齐到当前终端。 */
function syncSize() {
  if (!term || !ws || ws.readyState !== WebSocket.OPEN) return
  ws.send(encoder.encode(`stty rows ${term.rows} cols ${term.cols}\r`))
  sizeSynced = true
}

function refit() {
  fit?.fit()
  if (sizeSynced || settings.terminal.autoSyncSize) syncSize()
}

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

function applyRole() {
  if (term) term.options.disableStdin = !amWriter.value
}

function requestWrite() { sendCtrl({ type: 'request_write', name: myName }) }
function releaseWrite() { sendCtrl({ type: 'release_write' }); ElMessage.info('已释放控制，其他人可申请输入') }

function notifyInputRequest(from: string, name: string) {
  const n = ElNotification({
    title: '✋ 输入权限申请',
    type: 'warning',
    position: 'top-right',
    duration: 0,  // 不自动消失，等主控决策；非模态不打断操作
    message: h('div', [
      h('p', { style: 'margin:0 0 8px' }, `${name || from} 申请输入权限`),
      h('div', { style: 'display:flex;gap:8px' }, [
        h(ElButton, { size: 'small', type: 'primary', onClick: () => { sendCtrl({ type: 'grant_write', to: from }); n.close() } }, () => '同意'),
        h(ElButton, { size: 'small', onClick: () => { sendCtrl({ type: 'deny_write', to: from }); n.close() } }, () => '拒绝'),
      ]),
    ]),
  })
}

function handleCtrl(msg: Record<string, any>) {
  switch (msg.type) {
    case 'role':  // 接入时的角色快照
      amWriter.value = msg.writer === cid
      applyRole()
      break
    case 'presence':
      viewers.value = msg.viewers
      break
    case 'input_request':
      if (amWriter.value) notifyInputRequest(msg.from, msg.name)
      break
    case 'role_change': {
      const nowWriter = msg.writer === cid
      if (nowWriter !== amWriter.value) {
        amWriter.value = nowWriter
        applyRole()
        ElMessage[nowWriter ? 'success' : 'info'](nowWriter ? '你已获得输入权限' : `输入权限已移交给 ${msg.writer_name || '他人'}`)
      }
      break
    }
    case 'input_denied':
      if (msg.to === cid) ElMessage.warning('主控拒绝了你的输入申请')
      break
    case 'input_blocked':
      ElMessage.warning('旁观模式只读，点「✋ 申请输入」获取权限')
      break
    case 'writer_free':
      if (!amWriter.value) ElMessage.info('主控已释放，点「✋ 申请输入」即可获得权限')
      break
  }
}

function connectWs() {
  ws?.close()
  sessionDead = false
  const nodeQ = remoteNode.value ? `&node=${remoteNode.value}` : ''
  ws = new WebSocket(wsUrl(`/ws/terminal/${sessionId.value}?cid=${cid}&name=${myName}${nodeQ}`))
  ws.binaryType = 'arraybuffer'
  ws.onopen = () => { status.value = 'online'; term?.focus() }
  ws.onmessage = ev => {
    if (typeof ev.data === 'string') {  // 文本帧：控制消息（或纯文本终端数据）
      try {
        const body = JSON.parse(ev.data)
        if (body?.lh) { handleCtrl(body.lh); return }
      } catch { /* 非 JSON，按终端数据处理 */ }
      term?.write(ev.data)
      return
    }
    term?.write(new Uint8Array(ev.data as ArrayBuffer))
  }
  ws.onclose = async ev => {
    status.value = 'closed'
    if (ev.code === 4404 || (ev.reason || '').includes('会话已结束')) {
      sessionDead = true
    } else if (!remoteNode.value) {
      // 本地会话以查询为准：后端已终结则下次重连走"重开会话"
      try {
        const s = await api.session(sessionId.value)
        if (['closed', 'error'].includes(s.status)) sessionDead = true
      } catch { sessionDead = true }
    }
    term?.write(`\r\n\x1b[33m[连接已断开${ev.reason ? '：' + ev.reason : ''}，点「↻ 重连」恢复]\x1b[0m\r\n`)
  }
}

async function loadStatus() {
  if (remoteNode.value) { status.value = 'online'; return }  // 远程会话状态由归属节点维护
  try {
    const s = await api.session(sessionId.value)
    status.value = s.status
    lastError.value = s.last_error
  } catch { /* 会话可能刚关闭 */ }
}

/** 重连：会话活着 → 仅重连数据流；已终结 → 同配置秒开新会话 */
async function reconnect() {
  reconnecting.value = true
  try {
    if (!sessionDead) {
      connectWs()
      return
    }
    if (!connId.value) {
      ElMessage.warning('缺少连接配置信息，请回设备详情页重新打开')
      return
    }
    const r = remoteNode.value
      ? await api.proxyOpen(remoteNode.value, connId.value!)
      : await api.openSession(connId.value!)
    const nid = (r as any).node_id
    const node = remoteNode.value || (nid && nid !== 'local' && nid !== selfNodeId ? nid : '')
    ElMessage.success('已重新打开会话')
    router.replace({ path: `/terminal/${r.session_id ?? r.id}`,
                     query: { ...(node ? { node } : {}), ...(connId.value ? { conn: connId.value! } : {}) } })
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail?.message ?? e.response?.data?.detail ?? '重连失败')
  } finally {
    reconnecting.value = false
  }
}

async function close() {
  if (remoteNode.value) await api.proxyCloseSession(remoteNode.value, sessionId.value)
  else await api.closeSession(sessionId.value)
  status.value = 'closed'
  ws?.close()
}

onMounted(async () => {
  try { selfNodeId = (await api.clusterInfo()).node_id } catch { /* 单机 */ }
  initTerm(); connectWs(); loadStatus()
})

// 重开新会话后路由变化 → 原地热切换，不用整页刷新
watch(() => route.params.sessionId, (n, o) => {
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
.terminal-page { display: flex; flex-direction: column; height: 100%; background: #0c0c0c; }
.bar { display: flex; align-items: center; gap: 12px; padding: 8px 14px; background: #1d1d1d; color: #ddd; }
.bar .title { font-weight: 600; }
.bar .err { color: #f56c6c; font-size: 12px; }
.term { flex: 1; padding: 6px; overflow: hidden; }
</style>

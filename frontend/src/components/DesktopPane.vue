<template>
  <div class="desk-pane">
    <div class="desk-toolbar">
      <el-tag size="small" effect="dark" :type="phase === 'online' ? 'success' : 'info'">
        {{ phaseText }}{{ mode === 'builtin' ? ' · 内置采集' : '' }}
      </el-tag>
      <span style="flex:1" />
      <template v-if="phase === 'online'">
        <el-button size="small" @click="sendCAD">Ctrl+Alt+Del</el-button>
        <el-button size="small" title="注入全部按键抬起，解除按键卡死" @click="releaseAllKeys">释放按键</el-button>
        <template v-if="mode === 'builtin'">
          <el-button size="small" title="把本地剪贴板文本发送到远程" @click="sendLocalClip">↑发送剪贴板</el-button>
          <el-button size="small" title="把最近一次远程剪贴板取回到本地" @click="pullRemoteClip">↓取回剪贴板</el-button>
          <el-switch v-model="clipAuto" size="small" title="远程剪贴板变化时自动写入本地（需页面聚焦）" />
        </template>
        <el-dropdown v-if="mode === 'builtin'" size="small" trigger="click" @command="sendCombo">
          <el-button size="small">快捷键 ▾</el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item :command="[0xff09, 0xffe9]">Alt+Tab</el-dropdown-item>
              <el-dropdown-item :command="[0xffc3, 0xffe9]">Alt+F4</el-dropdown-item>
              <el-dropdown-item :command="[0xff1b, 0xffe3, 0xffe1]">Ctrl+Shift+Esc</el-dropdown-item>
              <el-dropdown-item :command="[0x0072, 0xffe7]">Win+R</el-dropdown-item>
              <el-dropdown-item :command="[0x0064, 0xffe7]">Win+D</el-dropdown-item>
              <el-dropdown-item :command="[0x0065, 0xffe7]">Win+E</el-dropdown-item>
              <el-dropdown-item :command="[0xffe7]">Win</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-button size="small" @click="toggleFullscreen">⛶ 全屏</el-button>
        <el-button size="small" type="danger" plain @click="disconnect">断开</el-button>
      </template>
    </div>

    <div v-if="mode === 'vnc'">
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

    <!-- 内置采集模式：canvas 播放器，无密码直连 -->
    <div v-else class="desk-screen builtin" ref="canvasWrap">
      <canvas ref="canvasEl" tabindex="0"
              @mousemove="onMouseMove" @mousedown="onMouseButton(true, $event)"
              @mouseup="onMouseButton(false, $event)"
              @wheel.prevent="onWheel"
              @keydown="onKey(true, $event)" @keyup="onKey(false, $event)"
              @contextmenu.prevent />
      <div v-if="noticeMsg && phase === 'online'" class="desk-notice">{{ noticeMsg }}</div>
      <div v-if="phase !== 'online'" class="builtin-overlay">{{ phaseText }}…</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElButton, ElDropdown, ElDropdownItem, ElDropdownMenu, ElForm, ElFormItem, ElInput, ElSwitch, ElTag, ElAlert } from 'element-plus'
import RFB from '@novnc/novnc'
import { wsUrl } from '../api'

const props = withDefaults(defineProps<{ node?: string; nodeName?: string; mode?: 'builtin' | 'vnc' }>(),
  { node: '', nodeName: '', mode: 'builtin' })

const phase = ref<'auth' | 'connecting' | 'online' | 'closed'>('connecting')
const errorMsg = ref('')
const password = ref('')
const screenEl = ref<HTMLElement>()
const canvasEl = ref<HTMLCanvasElement>()
const canvasWrap = ref<HTMLElement>()

let rfb: RFB | null = null
let ws: WebSocket | null = null
let ctx2d: CanvasRenderingContext2D | null = null
let screenW = 0, screenH = 0

// 修饰键按下追踪。坑：keydown 落在 canvas，随后点击工具栏按钮焦点移走，
// canvas 永远收不到对应 keyup → 远端修饰键卡死，之后敲键全变组合快捷键。
// 用 window 级 keyup 兜底 + 断连补放，双保险（后端会话起止也会 ReleaseAll）。
const MOD_SYMS = new Set([0xffe1, 0xffe2, 0xffe3, 0xffe4, 0xffe9, 0xffea, 0xffe7, 0xffe8])
const downMods = new Set<number>()
function releaseMods() {
  for (const s of downMods) sendInput({ type: 'key', keysym: s, down: false })
  downMods.clear()
}
// window 兜底：焦点不在 canvas 时keyup也到此，补发还按着的修饰键抬起
function onGlobalKeyUp(e: KeyboardEvent) {
  const sym = CODE2SYM[e.code]
  if (sym !== undefined && downMods.has(sym)) {
    downMods.delete(sym)
    sendInput({ type: 'key', keysym: sym, down: false })
  }
}

const phaseText = computed(() => ({
  auth: '待输入密码', connecting: '连接中…', online: `已连接 ${props.nodeName || '本机'}`, closed: '已断开',
}[phase.value]))

// ---------- 内置采集模式 ----------
const noticeMsg = ref('') // 后端推送的采集异常通知（锁屏安全桌面等）
function startBuiltin() {
  if (!canvasEl.value || ws) return
  lastRemoteClip = ''
  noticeMsg.value = ''
  ctx2d = canvasEl.value.getContext('2d')
  ws = new WebSocket(wsUrl(`/ws/desktop?node=${props.node}`))
  ws.binaryType = 'arraybuffer'
  ws.onopen = () => { phase.value = 'online'; canvasEl.value?.focus() }
  ws.onclose = () => { releaseMods(); phase.value = 'closed' }
  ws.onmessage = ev => {
    if (typeof ev.data === 'string') {
      // 剪贴板推送：{"type":"clip","data":"..."}
      try {
        const m = JSON.parse(ev.data)
        if (m.type === 'clip' && typeof m.data === 'string') {
          lastRemoteClip = m.data
          if (clipAuto.value) void writeLocalClip(m.data)
        } else if (m.type === 'notice' && typeof m.data === 'string') {
          noticeMsg.value = m.data
        }
      } catch { /* 非 JSON 文本帧忽略 */ }
      return
    }
    void handleFrame(new Uint8Array(ev.data))
  }
}

// 帧格式：[1][2B w][2B h][2B n] + 每块 [2B x][2B y][2B bw][2B bh][4B len][jpeg]（小端，与后端 put16/put32 一致）
async function handleFrame(f: Uint8Array) {
  if (f[0] !== 1 || !ctx2d || !canvasEl.value) return
  const dv = new DataView(f.buffer, f.byteOffset, f.byteLength)
  const w = dv.getUint16(1, true), h = dv.getUint16(3, true), n = dv.getUint16(5, true)
  if (w !== screenW || h !== screenH) {
    screenW = w; screenH = h
    canvasEl.value.width = w; canvasEl.value.height = h
    ctx2d = canvasEl.value.getContext('2d')
    ctx2d!.fillStyle = '#000'
    ctx2d!.fillRect(0, 0, w, h)
  }
  let off = 7
  for (let i = 0; i < n; i++) {
    const x = dv.getUint16(off, true), y = dv.getUint16(off + 2, true)
    const bw = dv.getUint16(off + 4, true), bh = dv.getUint16(off + 6, true)
    const len = dv.getUint32(off + 8, true)
    off += 12
    try {
      const bm = await createImageBitmap(new Blob([f.slice(off, off + len)], { type: 'image/jpeg' }))
      ctx2d.drawImage(bm, x, y)
      bm.close()
    } catch { /* 解码失败丢块 */ }
    off += len
  }
}

function sendInput(ev: Record<string, unknown>) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(ev))
}

// 画布坐标 → 屏幕坐标（canvas 可能被 CSS 缩放）
function toScreen(e: MouseEvent): { x: number; y: number } {
  const c = canvasEl.value!
  const r = c.getBoundingClientRect()
  return {
    x: Math.round((e.clientX - r.left) * (c.width / r.width)),
    y: Math.round((e.clientY - r.top) * (c.height / r.height)),
  }
}
function onMouseMove(e: MouseEvent) {
  if (phase.value !== 'online') return
  const { x, y } = toScreen(e)
  sendInput({ type: 'mouse_move', x, y })
}
function onMouseButton(down: boolean, e: MouseEvent) {
  if (phase.value !== 'online') return
  canvasEl.value?.focus()
  sendInput({ type: 'mouse_button', button: e.button + 1, down })
}
function onWheel(e: WheelEvent) {
  if (phase.value !== 'online') return
  sendInput({ type: 'wheel', dy: e.deltaY > 0 ? 1 : -1 })
}

// KeyboardEvent.code → X11 keysym（两平台后端统一按 keysym 处理）
const CODE2SYM: Record<string, number> = {
  Backspace: 0xff08, Tab: 0xff09, Enter: 0xff0d, ShiftLeft: 0xffe1, ShiftRight: 0xffe2,
  ControlLeft: 0xffe3, ControlRight: 0xffe4, AltLeft: 0xffe9, AltRight: 0xffea,
  MetaLeft: 0xffe7, MetaRight: 0xffe8, CapsLock: 0xff8b, Escape: 0xff1b,
  Space: 0x0020, PageUp: 0xff55, PageDown: 0xff56, End: 0xff57, Home: 0xff50,
  ArrowLeft: 0xff51, ArrowUp: 0xff52, ArrowRight: 0xff53, ArrowDown: 0xff54,
  Insert: 0xff63, Delete: 0xffff, Pause: 0xff13, ScrollLock: 0xff14,
  F1: 0xffbe, F2: 0xffbf, F3: 0xffc0, F4: 0xffc1, F5: 0xffc2, F6: 0xffc3,
  F7: 0xffc4, F8: 0xffc5, F9: 0xffc6, F10: 0xffc7, F11: 0xffc8, F12: 0xffc9,
  Minus: 0x2d, Equal: 0x3d, BracketLeft: 0x5b, BracketRight: 0x5d, Backslash: 0x5c,
  Semicolon: 0x3b, Quote: 0x27, Backquote: 0x60, Comma: 0x2c, Period: 0x2e, Slash: 0x2f,
  NumpadMultiply: 0xffaa, NumpadAdd: 0xffab, NumpadSubtract: 0xffad, NumpadDivide: 0xffaf,
  NumpadEnter: 0xff8d, NumpadDecimal: 0xffae,
}
for (let i = 0; i <= 9; i++) {
  CODE2SYM['Digit' + i] = 0x30 + i          // 主键盘数字
  CODE2SYM['Numpad' + i] = 0xffb0 + i       // 小键盘数字
}
for (let i = 0; i < 26; i++) CODE2SYM['Key' + String.fromCharCode(65 + i)] = 0x61 + i

function onKey(down: boolean, e: KeyboardEvent) {
  if (phase.value !== 'online') return
  let sym = CODE2SYM[e.code]
  // code 未覆盖（Shift 层符号、非 US 布局）：用 e.key 的 ASCII 兜底；
  // 大写字母回退到小写 keysym（后端经 keymap 得知需补 Shift）
  if (!sym && e.key.length === 1) {
    sym = e.key.charCodeAt(0)
    if (e.key >= 'A' && e.key <= 'Z') sym = 0x61 + (sym - 0x41)
  }
  if (!sym) return
  e.preventDefault()
  if (MOD_SYMS.has(sym)) { down ? downMods.add(sym) : downMods.delete(sym) }
  sendInput({ type: 'key', keysym: sym, down })
}

// ---------- VNC 模式 ----------
function connect() {
  phase.value = 'connecting'
  errorMsg.value = ''
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

const sleep = (ms: number) => new Promise<void>(r => setTimeout(r, ms))
// 组合键：顺序按下、逆序抬起（留间隔让系统处理焦点切换等副作用，如 CAD 唤安全屏）
async function sendCombo(keys: number[]) {
  if (phase.value !== 'online') return
  for (const k of keys) { sendInput({ type: 'key', keysym: k, down: true }); await sleep(30) }
  for (let i = keys.length - 1; i >= 0; i--) { sendInput({ type: 'key', keysym: keys[i], down: false }); await sleep(30) }
}

function sendCAD() {
  if (props.mode === 'vnc') { rfb?.sendCtrlAltDel(); return }
  sendCombo([0xffe3, 0xffe9, 0xffff]) // Ctrl Alt Del
}
// 一键释放全部按键：release_all 走后端 ReleaseAll（修饰键保底），
// 再补发全部已映射键的 keyup 突发，覆盖卡死的普通键
function releaseAllKeys() {
  if (phase.value !== 'online') return
  sendInput({ type: 'release_all' })
  for (const sym of new Set(Object.values(CODE2SYM))) {
    sendInput({ type: 'key', keysym: sym, down: false })
  }
  downMods.clear()
}
// ---------- 剪贴板同步 ----------
const clipAuto = ref(false) // 远程→本地自动写开关（默认关：防远程内容 surprise 覆盖本地）
let lastRemoteClip = ''
async function writeLocalClip(t: string) {
  try { await navigator.clipboard.writeText(t) } catch { /* 无权限/页面未聚焦 */ }
}
async function sendLocalClip() {
  if (phase.value !== 'online') return
  try {
    const t = await navigator.clipboard.readText()
    if (t) sendInput({ type: 'clip_set', data: t.slice(0, 262144) })
  } catch { /* 用户拒绝或浏览器不支持 */ }
}
function pullRemoteClip() {
  if (lastRemoteClip) void writeLocalClip(lastRemoteClip)
}
function toggleFullscreen() { (canvasWrap.value ?? screenEl.value)?.requestFullscreen?.() }
function disconnect() {
  releaseMods()
  rfb?.disconnect(); rfb = null
  ws?.close(); ws = null
  phase.value = 'closed'
}

watch(() => props.mode, m => {
  disconnect()
  phase.value = m === 'vnc' ? 'auth' : 'connecting'
  // nextTick：setup/immediate 阶段 canvas 尚未挂载，等 DOM 就绪再连
  if (m === 'builtin') nextTick(() => startBuiltin())
}, { immediate: true })

onMounted(() => {
  window.addEventListener('keyup', onGlobalKeyUp)
  // immediate watcher 触发时（setup 阶段）canvas 未挂载会直接返回，这里补一次真正连接
  if (props.mode === 'builtin') nextTick(() => startBuiltin())
})

onUnmounted(() => {
  window.removeEventListener('keyup', onGlobalKeyUp)
  disconnect()
})
</script>

<style scoped>
.desk-pane { display: flex; flex-direction: column; height: 76vh; }
.desk-toolbar { display: flex; align-items: center; gap: 8px; padding-bottom: 8px; }
.desk-screen {
  flex: 1; min-height: 0; background: #000; border-radius: 6px; overflow: hidden;
}
.desk-screen :deep(canvas) { outline: none; }
.desk-error { padding-top: 8px; }
.desk-screen.builtin { position: relative; display: flex; align-items: center; justify-content: center; }
.desk-screen.builtin canvas { max-width: 100%; max-height: 100%; }
.builtin-overlay {
  position: absolute; inset: 0; display: flex; align-items: center; justify-content: center;
  color: #aaa; background: rgba(0, 0, 0, .6);
}
.desk-notice {
  position: absolute; top: 8px; left: 8px; right: 8px; z-index: 2;
  background: rgba(245, 158, 11, .94); color: #1c1917;
  padding: 6px 10px; border-radius: 6px; font-size: 12px; line-height: 1.5;
}
</style>

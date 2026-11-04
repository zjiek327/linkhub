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
          <el-button size="small" title="本地与远程剪贴板互传（对话框内 Ctrl+V 粘贴，兼容 http 非安全上下文）" @click="clipDlg = true">剪贴板</el-button>
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

    <!-- 剪贴板同步对话框：本地→远程在框内 Ctrl+V 再发送；远程→本地显示+复制 -->
    <el-dialog v-model="clipDlg" title="剪贴板同步" width="520px" append-to-body>
      <div style="margin-bottom:14px">
        <div style="font-size:12px;color:#888;margin-bottom:4px">本地 → 远程：在此粘贴（Ctrl+V），发送后到远程窗口 Ctrl+V</div>
        <el-input v-model="clipText" type="textarea" :rows="4" placeholder="在此 Ctrl+V 粘贴本地内容" />
        <div style="margin-top:6px;text-align:right">
          <el-button size="small" type="primary" :disabled="!clipText || phase !== 'online'" @click="sendClipText">发送到远程</el-button>
        </div>
      </div>
      <div style="margin-bottom:14px">
        <div style="font-size:12px;color:#888;margin-bottom:4px">文件 → 远程：选文件发送（上限 {{ clipFileLimitMB }} MB），到远程窗口 Ctrl+V 粘贴出文件</div>
        <div style="display:flex;align-items:center;gap:8px">
          <input ref="clipFileEl" type="file" style="font-size:12px;flex:1;min-width:0" @change="onClipFilePick" />
          <el-button size="small" type="primary" :disabled="!clipFile || clipFileStatus === 'uploading'" @click="sendClipFile">发送文件</el-button>
        </div>
        <div v-if="clipFile" style="font-size:12px;margin-top:4px">
          {{ clipFile.name }}（{{ fmtSize(clipFile.size) }}）
          <span v-if="clipFile.size > clipFileLimitMB * 1048576" style="color:#f56c6c">超过上限 {{ clipFileLimitMB }} MB</span>
        </div>
        <el-progress v-if="clipFileStatus === 'uploading'" :percentage="clipFilePct" style="margin-top:6px" />
        <div v-if="clipFileStatus === 'uploading' && clipFilePct >= 99" style="font-size:12px;color:#888">已上传，等待远程写入…</div>
        <div v-if="clipFileStatus === 'done'" style="font-size:12px;color:#67c23a;margin-top:4px">{{ clipFileResult }}</div>
        <div v-if="clipFileStatus === 'error'" style="font-size:12px;color:#f56c6c;margin-top:4px">{{ clipFileErr }}</div>
      </div>
      <div>
        <div style="font-size:12px;color:#888;margin-bottom:4px">远程 → 本地：远程复制后内容每 2s 自动出现在这里</div>
        <textarea ref="remoteClipEl" :value="lastRemoteClip" readonly rows="4"
                  style="width:100%;box-sizing:border-box;resize:vertical;font:inherit;padding:5px 8px;border-radius:4px;border:1px solid #ccc;background:transparent;color:inherit"
                  placeholder="（空）"></textarea>
        <div style="margin-top:6px;display:flex;align-items:center;gap:10px;justify-content:flex-end">
          <el-switch v-model="clipAuto" size="small" inactive-text="变化自动写入本地(仅HTTPS)" />
          <el-button size="small" :disabled="!lastRemoteClip" @click="copyRemoteClip">复制内容</el-button>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElButton, ElDialog, ElDropdown, ElDropdownItem, ElDropdownMenu, ElForm, ElFormItem, ElInput, ElProgress, ElSwitch, ElTag, ElAlert } from 'element-plus'
import RFB from '@novnc/novnc'
import { getToken, http, wsUrl } from '../api'

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
let perfTimer = 0 // E 方案渲染统计回传定时器
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
let directFailed = false // 直连失败后回落中继（每次挂载只试一次，防抖动）

async function startBuiltin() {
  if (!canvasEl.value || ws) return
  lastRemoteClip = ''
  noticeMsg.value = ''
  ctx2d = canvasEl.value.getContext('2d')
  phase.value = 'connecting'
  // 直连优先：浏览器能直达目标节点则跳过中继（省一跳转发延迟与中转 CPU）。
  // WS 不受跨域限制，JWT 走 query 参数各节点通用；直连 2.5s 未通则回落中继
  let url = wsUrl(`/ws/desktop?node=${props.node}`)
  let isDirect = false
  if (!directFailed) {
    const direct = await resolveDirectUrl()
    if (direct) { url = direct; isDirect = true }
  }
  openBuiltinWS(url, isDirect)
}

// 从集群节点表解析目标节点地址构造直连 URL；本机/无地址/查询失败返回 null
async function resolveDirectUrl(): Promise<string | null> {
  try {
    const { data } = await http.get('/cluster/nodes')
    const list = Array.isArray(data) ? data : []
    const n = list.find((x: any) => x.node_id === props.node)
    if (!n || n.is_self || !n.address) return null
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const tok = getToken()
    return `${proto}://${n.address}/ws/desktop?node=${encodeURIComponent(props.node)}${tok ? `&token=${tok}` : ''}`
  } catch { return null }
}

function openBuiltinWS(url: string, isDirect: boolean) {
  const sock = new WebSocket(url)
  ws = sock
  sock.binaryType = 'arraybuffer'
  // 直连探测超时：2.5s 未 open 即关闭走 onclose 回落
  const openTimer = window.setTimeout(() => {
    if (sock.readyState !== WebSocket.OPEN) sock.close()
  }, 2500)
  sock.onopen = () => {
    window.clearTimeout(openTimer)
    phase.value = 'online'
    canvasEl.value?.focus()
  }
  sock.onclose = () => {
    window.clearTimeout(openTimer)
    releaseMods()
    if (isDirect && !directFailed) {
      directFailed = true
      ws = null
      void startBuiltin() // 回落中继，仅一次
      return
    }
    ws = null
    phase.value = 'closed'
  }
  sock.onmessage = ev => {
    if (typeof ev.data === 'string') {
      // 剪贴板推送：{"type":"clip","data":"..."}；采集异常：{"type":"notice","data":"..."}
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
    queueFrame(new Uint8Array(ev.data))
  }
}

// 帧渲染管线：WS 到达快于解码时只保留最新一帧（远程桌面要的是"最新画面"而非
// "每一帧"），延迟恒等于一帧、不随运动累积；帧内所有块并行解码
// （createImageBitmap 原生离屏多线程），绘制严格按块序——消除多帧交错渲染的
// 顺序错乱。12fps 时积压不明显，24fps 下旧实现积压数秒且鼠标反馈看起来"死掉"
let framePending: Uint8Array | null = null
let framePumping = false
// E 方案：渲染耗时统计（窗口期累计，2s 随 perf 消息回传 LAPTOP 打日志）
let perfFrames = 0
let perfRenderMs = 0
let perfDropped = 0

function queueFrame(f: Uint8Array) {
  if (framePending) perfDropped++ // 被覆盖丢弃的中间帧（管线跟不上的量化指标）
  framePending = f // 覆盖未渲染的旧帧
  if (framePumping) return
  framePumping = true
  void (async () => {
    while (framePending) {
      const cur = framePending
      framePending = null
      const t0 = performance.now()
      await renderFrame(cur)
      perfRenderMs += performance.now() - t0
      perfFrames++
    }
    framePumping = false
  })()
}

// 帧格式：[1][2B w][2B h][2B n] + 每块 [2B x][2B y][2B bw][2B bh][4B len][jpeg]（小端，与后端 put16/put32 一致）
async function renderFrame(f: Uint8Array) {
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
  const metas: { x: number; y: number }[] = []
  const decodes: Promise<ImageBitmap | null>[] = []
  for (let i = 0; i < n; i++) {
    const x = dv.getUint16(off, true), y = dv.getUint16(off + 2, true)
    const len = dv.getUint32(off + 8, true)
    off += 12
    metas.push({ x, y })
    decodes.push(createImageBitmap(new Blob([f.slice(off, off + len)], { type: 'image/jpeg' })).catch(() => null))
    off += len
  }
  const bitmaps = await Promise.all(decodes)
  for (let i = 0; i < bitmaps.length; i++) {
    const bm = bitmaps[i]
    if (!bm || !ctx2d) continue
    ctx2d.drawImage(bm, metas[i].x, metas[i].y)
    bm.close()
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
let wheelAcc = 0 // 滚轮像素累加器（见 onWheel）
function onWheel(e: WheelEvent) {
  if (phase.value !== 'online') return
  e.preventDefault()
  // 高分辨率触控板/平滑滚轮一次滑动发多个小增量事件，原实现每个事件一律发一格
  // （后端 ×120=一整格）→ 轻滑一页飞掉。统一折像素累加，满 120（Windows 一格
  // WHEEL_DELTA / noVNC 同款）发一格，剩余留待下个事件
  let d = e.deltaY
  if (e.deltaMode === 1) d *= 16        // DOM_DELTA_LINE：行→像素
  else if (e.deltaMode === 2) d *= 600  // DOM_DELTA_PAGE：页→像素
  wheelAcc += d
  while (Math.abs(wheelAcc) >= 120) {
    sendInput({ type: 'wheel', dy: wheelAcc > 0 ? 1 : -1 })
    wheelAcc -= Math.sign(wheelAcc) * 120
  }
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
// http 非安全上下文 navigator.clipboard 不存在（Clipboard API 限 HTTPS/localhost），
// 故本地→远程走"对话框内 Ctrl+V 粘贴再发送"的通用路径；远程→本地用 execCommand 复制兜底
const clipDlg = ref(false)
const clipAuto = ref(false) // 远程→本地自动写开关（仅安全上下文有效，默认关）
const clipText = ref('')    // 待发送到远程
const remoteClipEl = ref<HTMLTextAreaElement>()
let lastRemoteClip = ''
function writeLocalClip(t: string) {
  void navigator.clipboard?.writeText(t)?.catch?.(() => { /* 非安全上下文/无权限 */ })
}
function sendClipText() {
  if (phase.value !== 'online' || !clipText.value) return
  sendInput({ type: 'clip_set', data: clipText.value.slice(0, 262144) })
  clipText.value = ''
}
function copyRemoteClip() {
  const el = remoteClipEl.value
  if (!el || !lastRemoteClip) return
  el.focus()
  el.select()
  try { document.execCommand('copy') } catch { /* 老浏览器兜底也失败则忽略 */ }
}
// ---------- 剪贴板文件传输 ----------
// XHR 上传（非 fetch：fetch 的 upload progress 支持差），同源 POST 到所在节点，
// 集群内流式转发到目标节点落盘并写入其系统剪贴板（进度=到入口节点；99% 后等最终响应）
const clipFileEl = ref<HTMLInputElement>()
const clipFile = ref<File | null>(null)
const clipFileLimitMB = ref(10)
const clipFilePct = ref(0)
const clipFileStatus = ref<'' | 'uploading' | 'done' | 'error'>('')
const clipFileResult = ref('')
const clipFileErr = ref('')
let clipLimitLoaded = false

watch(clipDlg, v => {
  if (v && !clipLimitLoaded) {
    clipLimitLoaded = true
    void http.get('/cluster/clip-limit').then(({ data }: any) => {
      if (data?.max_mb > 0) clipFileLimitMB.value = data.max_mb
    }).catch(() => { /* 查询失败用默认 10MB */ })
  }
})
function fmtSize(n: number): string {
  if (n >= 1048576) return (n / 1048576).toFixed(1) + ' MB'
  if (n >= 1024) return (n / 1024).toFixed(1) + ' KB'
  return n + ' B'
}
function onClipFilePick() {
  clipFile.value = clipFileEl.value?.files?.[0] ?? null
  clipFileStatus.value = ''
  clipFileErr.value = ''
  clipFilePct.value = 0
}
function sendClipFile() {
  const f = clipFile.value
  if (!f || clipFileStatus.value === 'uploading') return
  clipFilePct.value = 0
  clipFileStatus.value = 'uploading'
  clipFileErr.value = ''
  const xhr = new XMLHttpRequest()
  xhr.open('POST', `/api/cluster/proxy/${encodeURIComponent(props.node || 'local')}/clipboard-file?filename=${encodeURIComponent(f.name)}`)
  const tok = getToken()
  if (tok) xhr.setRequestHeader('Authorization', `Bearer ${tok}`)
  xhr.upload.onprogress = ev => {
    if (ev.lengthComputable) clipFilePct.value = Math.min(99, Math.round((ev.loaded / ev.total) * 100))
  }
  xhr.onload = () => {
    let m: any = null
    try { m = JSON.parse(xhr.responseText) } catch { /* 非 JSON 响应 */ }
    if (xhr.status >= 200 && xhr.status < 300 && m?.ok) {
      clipFilePct.value = 100
      clipFileStatus.value = 'done'
      clipFileResult.value = m.clip
        ? '已发送，到远程窗口 Ctrl+V 粘贴出文件'
        : `已发送到远程：${m.path}（剪贴板引用写入失败，请按路径取文件）`
    } else {
      clipFileStatus.value = 'error'
      clipFileErr.value = m?.error || `HTTP ${xhr.status}`
    }
  }
  xhr.onerror = () => { clipFileStatus.value = 'error'; clipFileErr.value = '网络错误' }
  xhr.send(f)
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
  // E 方案：每 2s 把窗口期渲染统计回传 LAPTOP（[desktop] browser 日志行）
  perfTimer = window.setInterval(() => {
    if (ws?.readyState === WebSocket.OPEN && perfFrames > 0) {
      sendInput({ type: 'perf', render_ms: Math.round(perfRenderMs), frames: perfFrames, dropped: perfDropped, buf: ws.bufferedAmount })
    }
    perfFrames = 0; perfRenderMs = 0; perfDropped = 0
  }, 2000)
  // immediate watcher 触发时（setup 阶段）canvas 未挂载会直接返回，这里补一次真正连接
  if (props.mode === 'builtin') nextTick(() => startBuiltin())
})

onUnmounted(() => {
  window.removeEventListener('keyup', onGlobalKeyUp)
  window.clearInterval(perfTimer)
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

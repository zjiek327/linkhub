<template>
  <div class="page">
    <div class="toolbar">
      <el-select v-model="nodeId" style="width:200px" @change="reload">
        <el-option label="本机" value="" />
        <el-option v-for="n in nodes" :key="n.node_id" :value="n.node_id" :label="n.name"
                   :disabled="n.status !== 'online'" />
      </el-select>
      <el-select v-model="interval" style="width:110px" @change="restartTimer">
        <el-option v-for="o in INTERVALS" :key="o.value" :value="o.value" :label="o.label" />
      </el-select>
      <el-button @click="reload">刷新</el-button>
      <el-switch v-model="live" active-text="实时" @change="restartTimer" />
      <span style="flex:1" />
      <el-tag v-if="error" type="danger" effect="plain">{{ error }}</el-tag>
      <template v-else>
        <el-tag effect="plain">↑ {{ fmtRate(totalRate.sent) }}/s</el-tag>
        <el-tag type="success" effect="plain">↓ {{ fmtRate(totalRate.recv) }}/s</el-tag>
        <el-tag type="info" effect="plain">会话累计 ↑{{ fmtSize(totalSent) }} ↓{{ fmtSize(totalRecv) }}</el-tag>
      </template>
    </div>

    <el-card shadow="never">
      <template #header>
        <div style="display:flex;justify-content:space-between;align-items:center">
          <span>📈 流量趋势（{{ intervalLabel }}粒度，窗口 {{ windowLabel }}）</span>
          <span class="legend"><i class="sw s" />上行(发送)　<i class="sw r" />下行(接收)</span>
        </div>
      </template>
      <div class="chart-wrap" v-loading="loading">
        <svg :viewBox="`0 0 ${W} ${H}`" preserveAspectRatio="none" class="chart">
          <line v-for="i in 4" :key="'g'+i" :x1="0" :y1="H*i/4" :x2="W" :y2="H*i/4"
                stroke="var(--lh-panel-border,#222b34)" stroke-width="1" />
          <text v-for="i in 4" :key="'t'+i" :x="4" :y="H*i/4 - 3" class="tick">{{ fmtAxis(maxY * (4-i) / 4) }}</text>
          <path v-if="areaPath.recv" :d="areaPath.recv" class="area r" />
          <path v-if="areaPath.sent" :d="areaPath.sent" class="area s" />
          <path v-if="linePath.recv" :d="linePath.recv" class="line r" />
          <path v-if="linePath.sent" :d="linePath.sent" class="line s" />
        </svg>
        <div class="xaxis">
          <span v-for="(l, i) in xTicks" :key="i" :style="{left: l.pct + '%'}">{{ l.label }}</span>
        </div>
        <div v-if="!history.length" class="empty">暂无数据（等首个采样周期）</div>
      </div>
    </el-card>

    <el-card shadow="never" style="margin-top:14px">
      <template #header>
        <div style="display:flex;justify-content:space-between;align-items:center">
          <span>📡 实时速率（按进程）</span>
          <el-radio-group v-model="protoFilter" size="small">
            <el-radio-button value="">全部</el-radio-button>
            <el-radio-button value="6">TCP</el-radio-button>
            <el-radio-button value="17">UDP</el-radio-button>
          </el-radio-group>
        </div>
      </template>
      <el-table :data="filteredRates" v-loading="loading" height="320" empty-text="暂无流量（或节点空闲）">
        <el-table-column label="进程" min-width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.name }}</span>
            <span style="color:var(--lh-muted);margin-left:8px">#{{ row.pid }}</span>
          </template>
        </el-table-column>
        <el-table-column label="协议" width="80">
          <template #default="{ row }">
            <el-tag size="small" effect="plain" :type="row.proto === 6 ? 'primary' : 'warning'">
              {{ row.proto === 6 ? 'TCP' : 'UDP' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="↑ 发送" width="130">
          <template #default="{ row }">{{ fmtRate(row.rate.sent) }}/s</template>
        </el-table-column>
        <el-table-column label="↓ 接收" width="130">
          <template #default="{ row }">{{ fmtRate(row.rate.recv) }}/s</template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { api } from '../api'

// 刷新粒度=曲线采样粒度；窗口=粒度×点数；窗口>1h 时后端自动切分钟桶（秒桶仅保留1h）
const INTERVALS = [
  { value: 5000, label: '5秒', points: 60, window: '5分钟' },
  { value: 10000, label: '10秒', points: 60, window: '10分钟' },
  { value: 30000, label: '30秒', points: 60, window: '30分钟' },
  { value: 60000, label: '1分钟', points: 60, window: '1小时' },
  { value: 300000, label: '5分钟', points: 60, window: '5小时' },
  { value: 900000, label: '15分钟', points: 48, window: '12小时' },
  { value: 1800000, label: '30分钟', points: 48, window: '24小时' },
  { value: 3600000, label: '1小时', points: 48, window: '2天' },
]

const nodeId = ref('')
const live = ref(true)
const loading = ref(false)
const error = ref('')
const rates = ref<any[]>([])
const history = ref<any[]>([])
const totalRate = ref({ sent: 0, recv: 0 })
const totalSent = ref(0)
const totalRecv = ref(0)
const nodes = ref<any[]>([])
const protoFilter = ref('')
const interval = ref(10000)

const W = 1000, H = 200
const intervalLabel = computed(() => INTERVALS.find(i => i.value === interval.value)?.label ?? '')
const windowLabel = computed(() => INTERVALS.find(i => i.value === interval.value)?.window ?? '')

// 粒度聚合：把后端秒桶/分钟桶聚合到所选粒度（固定槽位，含空槽补零）
function aggregate(rows: any[], stepMs: number, points: number) {
  const step = stepMs
  const now = Date.now()
  const lastSlot = Math.floor(now / step)
  const acc = new Map<number, { sent: number; recv: number }>()
  for (const b of rows) {
    const t = typeof b.start === 'number'
      ? (b.start < 1e12 ? b.start * 1000 : b.start)
      : new Date(b.start).getTime()
    const idx = Math.floor(t / step)
    const e = acc.get(idx) ?? { sent: 0, recv: 0 }
    e.sent += b.total?.sent ?? 0
    e.recv += b.total?.recv ?? 0
    acc.set(idx, e)
  }
  const out: { t: number; sent: number; recv: number }[] = []
  for (let i = lastSlot - points + 1; i <= lastSlot; i++) {
    const e = acc.get(i)
    out.push({ t: i * step, sent: e?.sent ?? 0, recv: e?.recv ?? 0 })
  }
  return out
}

const points = computed(() => {
  const cfg = INTERVALS.find(i => i.value === interval.value)!
  return aggregate(history.value, cfg.value, cfg.points)
})
const maxY = computed(() => Math.max(1024, ...points.value.map(p => Math.max(p.sent, p.recv))))

function xy(vals: number[]) {
  const n = vals.length
  if (!n) return ''
  const step = W / Math.max(1, n - 1)
  return vals.map((v, i) => `${i === 0 ? 'M' : 'L'}${(i * step).toFixed(1)},${(H - (v / maxY.value) * (H - 4)).toFixed(1)}`).join(' ')
}
const linePath = computed(() => ({
  sent: xy(points.value.map(p => p.sent)),
  recv: xy(points.value.map(p => p.recv)),
}))
const areaPath = computed(() => ({
  sent: linePath.value.sent ? `${linePath.value.sent} L${W},${H} L0,${H} Z` : '',
  recv: linePath.value.recv ? `${linePath.value.recv} L${W},${H} L0,${H} Z` : '',
}))

const xTicks = computed(() => {
  const ps = points.value
  if (ps.length < 2) return []
  const fmt = (t: number) => {
    const d = new Date(t)
    return interval.value >= 300000
      ? d.toLocaleString([], { month: 'numeric', day: 'numeric', hour: '2-digit' })
      : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }
  const n = Math.min(6, ps.length)
  return Array.from({ length: n }, (_, i) => ({
    pct: (i / (n - 1)) * 100,
    label: fmt(ps[Math.round((i / (n - 1)) * (ps.length - 1))].t),
  }))
})

const filteredRates = computed(() =>
  protoFilter.value === '' ? rates.value : rates.value.filter((r: any) => String(r.proto) === protoFilter.value))

function fmtSize(n: number): string {
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(n >= 100 || i === 0 ? 0 : 1)}${u[i]}`
}
function fmtRate(bps: number): string { return fmtSize(bps) }
function fmtAxis(v: number): string { return fmtSize(v) }

async function loadNodes() {
  try {
    const info = await api.clusterInfo()
    if (info.enabled) nodes.value = await api.clusterNodes()
  } catch { /* 非集群 */ }
}

async function reload() {
  loading.value = true
  error.value = ''
  try {
    const cfg = INTERVALS.find(i => i.value === interval.value)!
    const winS = (cfg.value / 1000) * cfg.points
    // 后端秒桶仅 1h；窗口超 1h 时传更大 window 让后端回退分钟桶
    const win = winS > 3600 ? `${Math.round(winS / 60)}m` : `${winS}s`
    const r = await api.monitorTraffic(nodeId.value, win)
    rates.value = r.snapshot.per_sec ?? []
    history.value = r.history ?? []
    totalRate.value = {
      sent: (r.snapshot.per_sec ?? []).reduce((s: number, x: any) => s + (x.rate?.sent ?? 0), 0),
      recv: (r.snapshot.per_sec ?? []).reduce((s: number, x: any) => s + (x.rate?.recv ?? 0), 0),
    }
    totalSent.value = r.snapshot.total?.sent ?? 0
    totalRecv.value = r.snapshot.total?.recv ?? 0
  } catch (e: any) {
    error.value = e.response?.data?.detail ?? '加载失败'
  } finally { loading.value = false }
}

let timer: number | undefined
function restartTimer() {
  if (timer) clearInterval(timer)
  timer = undefined
  if (live.value) timer = window.setInterval(reload, interval.value)
  reload()
}

loadNodes().then(() => restartTimer())
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<style scoped>
.toolbar { display: flex; align-items: center; gap: 10px; margin-bottom: 14px; flex-wrap: wrap; }
.mono { font-family: 'JetBrains Mono', Menlo, monospace; }
.legend { font-size: 12px; color: var(--lh-muted); display: flex; align-items: center; gap: 6px; }
.sw { display: inline-block; width: 14px; height: 3px; border-radius: 2px; }
.sw.s { background: var(--lh-accent, #3b8bfd); }
.sw.r { background: #2ea36b; }
.chart-wrap { position: relative; }
.chart { width: 100%; height: 200px; display: block; }
.line { fill: none; stroke-width: 1.6; }
.line.s { stroke: var(--lh-accent, #3b8bfd); }
.line.r { stroke: #2ea36b; }
.area { opacity: .12; }
.area.s { fill: var(--lh-accent, #3b8bfd); }
.area.r { fill: #2ea36b; }
.tick { font-size: 10px; fill: var(--lh-muted, #8a94a0); }
.xaxis { position: relative; height: 18px; font-size: 10px; color: var(--lh-muted); }
.xaxis span { position: absolute; transform: translateX(-50%); white-space: nowrap; }
.xaxis span:first-child { transform: none; }
.xaxis span:last-child { transform: translateX(-100%); }
.empty { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; color: var(--lh-muted); }
</style>

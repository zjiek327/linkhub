<template>
  <div class="sys-pane">
    <!-- 概览卡片 -->
    <div class="cards">
      <el-card shadow="never" class="card">
        <div class="card-title">CPU</div>
        <div class="card-value" :class="pctClass(snap.cpu?.percent)">{{ (snap.cpu?.percent ?? 0).toFixed(1) }}%</div>
        <div class="card-sub">{{ snap.cpu?.counts ?? '–' }} 核 · {{ snap.cpu?.model || '' }}</div>
        <div class="sparkline" v-html="spark(hist.map(p => p.cpu), '%')"></div>
      </el-card>
      <el-card shadow="never" class="card">
        <div class="card-title">内存</div>
        <div class="card-value" :class="pctClass(snap.mem?.used_pct)">{{ (snap.mem?.used_pct ?? 0).toFixed(1) }}%</div>
        <div class="card-sub">{{ fmtSize(snap.mem?.used ?? 0) }} / {{ fmtSize(snap.mem?.total ?? 0) }}<span v-if="snap.mem?.swap_total"> · swap {{ fmtSize(snap.mem.swap_used) }}/{{ fmtSize(snap.mem.swap_total) }}</span></div>
        <div class="sparkline" v-html="spark(hist.map(p => p.mem_used), 'B')"></div>
      </el-card>
      <el-card shadow="never" class="card">
        <div class="card-title">负载</div>
        <div class="card-value">{{ (snap.load?.load1 ?? 0).toFixed(2) }}</div>
        <div class="card-sub">1/5/15m: {{ (snap.load?.load1 ?? 0).toFixed(2) }} / {{ (snap.load?.load5 ?? 0).toFixed(2) }} / {{ (snap.load?.load15 ?? 0).toFixed(2) }}</div>
      </el-card>
      <el-card shadow="never" class="card">
        <div class="card-title">网卡总速率</div>
        <div class="card-value small">↑{{ fmtRate(snap.net_rate?.sent ?? 0) }}/s</div>
        <div class="card-value small ok">↓{{ fmtRate(snap.net_rate?.recv ?? 0) }}/s</div>
        <div class="card-sub">{{ snap.host?.hostname }} · {{ snap.host?.platform }}</div>
      </el-card>
      <el-card shadow="never" class="card" v-if="snap.self">
        <div class="card-title">LinkHub 自身</div>
        <div class="card-sub" style="margin-top:6px">会话 {{ snap.self.sessions }} · goroutines {{ snap.self.go_routines }}</div>
        <div class="card-sub">RSS {{ snap.self.rss_mb }}MB · heap {{ snap.self.heap_mb }}MB · GC {{ snap.self.num_gc }}</div>
        <div class="card-sub">运行 {{ fmtUptime(snap.host?.uptime_s ?? 0) }}</div>
      </el-card>
    </div>

    <!-- CPU/内存曲线 -->
    <el-card shadow="never" style="margin-top:14px">
      <template #header>📈 CPU 使用率 / 内存用量（15s 粒度，最近 1h）</template>
      <div class="duo-chart">
        <svg :viewBox="`0 0 500 120`" preserveAspectRatio="none" class="mini-chart">
          <path v-if="cpuArea" :d="cpuArea" class="area cpu" />
          <path v-if="cpuLine" :d="cpuLine" class="line cpu" />
          <text x="4" y="12" class="lbl">CPU %（max {{ maxCPU.toFixed(0) }}）</text>
        </svg>
        <svg :viewBox="`0 0 500 120`" preserveAspectRatio="none" class="mini-chart">
          <path v-if="memArea" :d="memArea" class="area mem" />
          <path v-if="memLine" :d="memLine" class="line mem" />
          <text x="4" y="12" class="lbl">内存（max {{ fmtSize(maxMem) }}）</text>
        </svg>
      </div>
    </el-card>

    <div class="row2">
      <!-- 磁盘 -->
      <el-card shadow="never">
        <template #header>💾 磁盘挂载点</template>
        <div v-for="d in snap.disks ?? []" :key="d.path" class="disk-row">
          <span class="mono path">{{ d.path }}</span>
          <el-progress :percentage="Math.min(100, d.used_pct)" :stroke-width="10"
                       :status="d.used_pct > 90 ? 'exception' : d.used_pct > 75 ? 'warning' : 'success'"
                       style="flex:1;margin:0 10px" />
          <span class="muted">{{ fmtSize(d.used) }}/{{ fmtSize(d.total) }}</span>
        </div>
        <el-empty v-if="!(snap.disks ?? []).length" description="无数据" :image-size="40" />
      </el-card>
      <!-- TCP -->
      <el-card shadow="never">
        <template #header>🔗 TCP 连接状态</template>
        <div class="tcp-grid">
          <div class="tcp-item"><b class="ok">{{ snap.net_stat?.established ?? 0 }}</b><span>ESTABLISHED</span></div>
          <div class="tcp-item"><b>{{ snap.net_stat?.listen ?? 0 }}</b><span>LISTEN</span></div>
          <div class="tcp-item"><b class="warn">{{ snap.net_stat?.time_wait ?? 0 }}</b><span>TIME_WAIT</span></div>
          <div class="tcp-item"><b class="err">{{ snap.net_stat?.close_wait ?? 0 }}</b><span>CLOSE_WAIT</span></div>
          <div class="tcp-item"><b class="muted">{{ snap.net_stat?.other ?? 0 }}</b><span>其他</span></div>
        </div>
        <div v-if="(snap.temps ?? []).length" class="temps">
          <span v-for="t in snap.temps" :key="t.sensor" class="muted">{{ t.sensor }}: {{ t.c.toFixed(0) }}℃</span>
        </div>
      </el-card>
    </div>

    <!-- 进程 Top -->
    <el-card shadow="never" style="margin-top:14px">
      <template #header>⚙️ 进程 Top（CPU 优先）</template>
      <el-table :data="snap.procs ?? []" height="260" size="small">
        <el-table-column prop="pid" label="PID" width="80" />
        <el-table-column prop="name" label="进程" min-width="160">
          <template #default="{ row }"><span class="mono">{{ row.name }}</span></template>
        </el-table-column>
        <el-table-column prop="user" label="用户" width="100" />
        <el-table-column label="CPU%" width="90">
          <template #default="{ row }">{{ row.cpu.toFixed(1) }}</template>
        </el-table-column>
        <el-table-column label="内存" width="90">
          <template #default="{ row }">{{ row.mem.toFixed(0) }}MB</template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ snap: any; hist: any[] }>()

function fmtSize(n: number): string {
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return `${n.toFixed(n >= 100 || i === 0 ? 0 : 1)}${u[i]}`
}
function fmtRate(bps: number): string { return fmtSize(bps) }
function fmtUptime(s: number): string {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600)
  return d > 0 ? `${d}天${h}时` : `${h}时${Math.floor((s % 3600) / 60)}分`
}
function pctClass(v?: number): string {
  if (v == null) return ''
  return v > 90 ? 'err' : v > 75 ? 'warn' : 'ok'
}

// 迷你 sparkline（SVG polyline）
function spark(vals: number[], unit: 'B' | '%'): string {
  const v = vals.slice(-60)
  if (v.length < 2) return ''
  const max = Math.max(1, ...v)
  const step = 100 / (v.length - 1)
  const pts = v.map((x, i) => `${(i * step).toFixed(1)},${(28 - (x / max) * 24).toFixed(1)}`).join(' ')
  return `<svg viewBox="0 0 100 30" preserveAspectRatio="none" style="width:100%;height:30px">
    <polyline points="${pts}" fill="none" stroke="var(--lh-accent,#3b8bfd)" stroke-width="1.4"/>
  </svg>`
}

const maxCPU = computed(() => Math.max(100, ...props.hist.map(p => p.cpu ?? 0)))
const maxMem = computed(() => Math.max(1, ...props.hist.map(p => p.mem_used ?? 0)))

function path(vals: number[], max: number): string {
  const v = vals.slice(-240)
  if (v.length < 2) return ''
  const step = 500 / (v.length - 1)
  return v.map((x, i) => `${i === 0 ? 'M' : 'L'}${(i * step).toFixed(1)},${(120 - (x / max) * 112).toFixed(1)}`).join(' ')
}
const cpuLine = computed(() => path(props.hist.map(p => p.cpu ?? 0), maxCPU.value))
const cpuArea = computed(() => cpuLine.value ? `${cpuLine.value} L500,120 L0,120 Z` : '')
const memLine = computed(() => path(props.hist.map(p => p.mem_used ?? 0), maxMem.value))
const memArea = computed(() => memLine.value ? `${memLine.value} L500,120 L0,120 Z` : '')
</script>

<style scoped>
.cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 12px; }
.card-title { font-size: 12px; color: var(--lh-muted); }
.card-value { font-size: 26px; font-weight: 600; margin: 4px 0; }
.card-value.small { font-size: 16px; }
.card-value.ok { color: #2ea36b; }
.card-value.warn { color: #e6a23c; }
.card-value.err { color: #f56c6c; }
.card-sub { font-size: 12px; color: var(--lh-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sparkline { margin-top: 6px; }
.duo-chart { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.mini-chart { width: 100%; height: 120px; }
.line.cpu { stroke: var(--lh-accent, #3b8bfd); }
.area.cpu { fill: var(--lh-accent, #3b8bfd); }
.line.mem { stroke: #9b59b6; }
.area.mem { fill: #9b59b6; }
.line { fill: none; stroke-width: 1.5; }
.area { opacity: .15; }
.lbl { font-size: 10px; fill: var(--lh-muted); }
.row2 { display: grid; grid-template-columns: 1.4fr 1fr; gap: 14px; margin-top: 14px; }
.disk-row { display: flex; align-items: center; margin-bottom: 8px; }
.path { width: 130px; font-size: 12px; overflow: hidden; text-overflow: ellipsis; }
.muted { color: var(--lh-muted); font-size: 12px; }
.tcp-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
.tcp-item { text-align: center; padding: 8px 0; border-radius: 8px; background: var(--lh-panel-bg); }
.tcp-item b { display: block; font-size: 20px; }
.tcp-item span { font-size: 11px; color: var(--lh-muted); }
.ok { color: #2ea36b; } .warn { color: #e6a23c; } .err { color: #f56c6c; }
.temps { margin-top: 10px; display: flex; gap: 12px; flex-wrap: wrap; }
.mono { font-family: 'JetBrains Mono', Menlo, monospace; }
</style>

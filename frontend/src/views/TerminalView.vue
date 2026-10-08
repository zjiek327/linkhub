<template>
  <div class="terminal-page">
    <div class="tabs-bar">
      <el-tabs v-model="activeId" type="card" closable @tab-remove="closeTab" @tab-click="switchTab">
        <el-tab-pane v-for="t in tabs" :key="t.id" :name="String(t.id)">
          <template #label>
            <span class="tab-label">
              <span class="dot" :class="t.online ? 'on' : 'off'" />
              {{ t.name || `会话 #${t.id}` }}
            </span>
          </template>
        </el-tab-pane>
      </el-tabs>
    </div>
    <div class="panes">
      <TerminalPane v-for="t in tabs" :key="t.id"
                    v-show="String(t.id) === activeId"
                    :ref="el => paneRefs[t.id] = el"
                    :session-id="t.id" :conn-id="t.connId" :remote-node="t.remoteNode"
                    :display-name="t.name"
                    @status="onStatus(t.id, $event)"
                    @open-new="openTab" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import TerminalPane from '../components/TerminalPane.vue'

interface Tab { id: number; connId: number | null; remoteNode: string; name: string; online: boolean }

const route = useRoute()
const router = useRouter()
const tabs = ref<Tab[]>([])
const activeId = ref('')
const paneRefs = ref<Record<number, any>>({})

// 从 sessionStorage 恢复（刷新不丢）
const STORE = 'linkhub_terminal_tabs'
function save() {
  sessionStorage.setItem(STORE, JSON.stringify({ tabs: tabs.value, active: activeId.value }))
}
function restore() {
  try {
    const raw = sessionStorage.getItem(STORE)
    if (!raw) return
    const { tabs: t, active } = JSON.parse(raw)
    if (Array.isArray(t) && t.length) {
      tabs.value = t
      activeId.value = active || String(t[0].id)
    }
  } catch { /* 忽略坏数据 */ }
}
restore()

function openTab(payload: { id: number; connId?: number | null; remoteNode?: string; name?: string }) {
  const id = Number(payload.id)
  let t = tabs.value.find(x => x.id === id)
  if (!t) {
    t = { id, connId: payload.connId ?? null, remoteNode: payload.remoteNode ?? '',
          name: payload.name ?? '', online: true }
    tabs.value.push(t)
  }
  activeId.value = String(id)
  save()
  nextTick(() => paneRefs.value[id]?.refit?.())
}

function closeTab(name: string) {
  const id = Number(name)
  paneRefs.value[id]?.close?.()
  tabs.value = tabs.value.filter(x => x.id !== id)
  if (activeId.value === name) activeId.value = tabs.value.length ? String(tabs.value.at(-1)!.id) : ''
  save()
  if (!tabs.value.length) router.push('/devices')
}

function switchTab() { save() }

function onStatus(id: number, s: { online: boolean; name?: string }) {
  const t = tabs.value.find(x => x.id === id)
  if (t) { t.online = s.online; if (s.name) t.name = s.name }
  save()
}

// 路由驱动的打开：/terminal/{id}?conn=&node=&name=
watch(() => [route.params.sessionId, route.query], () => {
  const sid = Number(route.params.sessionId)
  if (sid) openTab({
    id: sid,
    connId: route.query.conn ? Number(route.query.conn) : null,
    remoteNode: (route.query.node as string) || '',
    name: (route.query.name as string) || '',
  })
}, { immediate: true, deep: true })
</script>

<style scoped>
.terminal-page { display: flex; flex-direction: column; height: 100%; background: #0c0c0c; }
.tabs-bar { background: #161616; padding: 4px 8px 0; }
.tabs-bar :deep(.el-tabs__header) { margin: 0; border-bottom: none; }
.tabs-bar :deep(.el-tabs__item) { color: #999; }
.tabs-bar :deep(.el-tabs__item.is-active) { color: #fff; background: #0c0c0c; }
.tab-label { display: inline-flex; align-items: center; gap: 6px; }
.dot { width: 7px; height: 7px; border-radius: 50%; }
.dot.on { background: #67c23a; }
.dot.off { background: #909399; }
.panes { flex: 1; overflow: hidden; }
</style>

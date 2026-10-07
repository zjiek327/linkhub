<template>
  <div class="page">
    <el-row :gutter="16">
      <el-col :span="6" v-for="card in cards" :key="card.label">
        <el-card shadow="hover">
          <div class="stat">
            <span class="icon">{{ card.icon }}</span>
            <div>
              <div class="num">{{ card.value }}</div>
              <div class="label">{{ card.label }}</div>
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-card style="margin-top:16px" shadow="never">
      <template #header>
        <span>活跃会话</span>
        <el-tag v-if="connected" type="success" size="small" style="margin-left:8px">事件通道已连接</el-tag>
      </template>
      <el-table :data="sessions" empty-text="暂无活跃会话，去设备详情页打开一个终端吧">
        <el-table-column prop="id" label="会话" width="80" />
        <el-table-column prop="connection_id" label="连接配置" width="100" />
        <el-table-column label="状态" width="110">
          <template #default="{ row }"><StatusBadge :status="row.status" /></template>
        </el-table-column>
        <el-table-column prop="opened_at" label="打开时间">
          <template #default="{ row }">{{ new Date(row.opened_at).toLocaleString() }}</template>
        </el-table-column>
        <el-table-column label="操作" width="180">
          <template #default="{ row }">
            <el-button size="small" type="primary" @click="$router.push(`/terminal/${row.id}?conn=${row.connection_id}`)">进入终端</el-button>
            <el-button size="small" type="danger" plain @click="close(row.id)">关闭</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, type Session, type Stats } from '../api'
import { useEvents } from '../api/events'
import StatusBadge from '../components/StatusBadge.vue'

const stats = ref<Stats>({ devices_total: 0, devices_online: 0, sessions_online: 0, templates_total: 0, nodes_total: 0, nodes_online: 0 })
const sessions = ref<Session[]>([])

const cards = computed(() => [
  { icon: '🖥️', label: '设备总数', value: stats.value.devices_total },
  { icon: '🟢', label: '在线设备', value: stats.value.devices_online },
  { icon: '🔗', label: '活跃会话', value: stats.value.sessions_online },
  { icon: '📦', label: '设备模板', value: stats.value.templates_total },
  { icon: '🕸️', label: '集群节点', value: stats.value.nodes_total ? `${stats.value.nodes_online}/${stats.value.nodes_total}` : '单机' },
])

async function refresh() {
  stats.value = await api.stats()
  sessions.value = (await api.sessions()).filter(s => ['online', 'connecting', 'error'].includes(s.status) && !s.closed_at)
}
async function close(id: number) {
  await api.closeSession(id)
  await refresh()
}

const { connected } = useEvents(() => refresh())
onMounted(refresh)
</script>

<style scoped>
.stat { display: flex; align-items: center; gap: 14px; }
.stat .icon { font-size: 32px; }
.stat .num { font-size: 26px; font-weight: 700; }
.stat .label { color: #909399; font-size: 13px; }
</style>

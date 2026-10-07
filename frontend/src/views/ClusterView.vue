<template>
  <div class="page">
    <el-alert v-if="!info.enabled" type="info" :closable="false" style="margin-bottom:14px">
      集群模式未启用。在各节点后端设置 <code>LINKHUB_CLUSTER_ENABLED=true</code> 与相同的
      <code>LINKHUB_CLUSTER_TOKEN</code> 后重启，再回到本页加入集群。
    </el-alert>

    <div class="toolbar">
      <el-button type="primary" :disabled="!info.enabled" @click="joinVisible = true">加入节点</el-button>
      <el-button @click="load">刷新</el-button>
      <el-tag v-if="info.enabled" type="success" effect="plain">
        本机：{{ info.name }}（{{ info.node_id }}）
      </el-tag>
    </div>

    <el-card shadow="never" v-if="pending.length">
      <template #header>
        <div style="display:flex;justify-content:space-between;align-items:center">
          <span>🔍 发现的节点（自动探测）</span>
          <div>
            <el-button size="small" type="primary" :disabled="!pendingSel.length"
                       @click="joinSelected">加入选中（{{ pendingSel.length }}）</el-button>
            <el-button size="small" :disabled="!pendingSel.length" @click="ignoreSelected">忽略选中</el-button>
          </div>
        </div>
      </template>
      <el-table :data="pending" @selection-change="pendingSel = $event">
        <el-table-column type="selection" width="40" />
        <el-table-column prop="name" label="名称" width="200" />
        <el-table-column prop="node_id" label="节点 ID" width="140" />
        <el-table-column prop="address" label="地址" min-width="220" />
        <el-table-column label="操作" width="180">
          <template #default="{ row }">
            <el-button size="small" type="primary" @click="approve(row)">加入</el-button>
            <el-button size="small" plain @click="ignore([row.node_id])">忽略</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-card shadow="never" style="margin-top:14px">
      <template #header>集群节点</template>
      <el-table :data="nodes" v-loading="loading">
        <el-table-column label="名称" width="220">
          <template #default="{ row }">
            {{ row.name }}
            <el-tag v-if="row.is_self" size="small" type="warning" effect="plain">本机</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="node_id" label="节点 ID" width="130" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }"><StatusBadge :status="row.status" /></template>
        </el-table-column>
        <el-table-column prop="address" label="地址" min-width="200" />
        <el-table-column label="串口资源" min-width="220">
          <template #default="{ row }">
            <el-tag v-for="p in row.resources?.serial_ports ?? []" :key="p.device"
                    size="small" effect="plain" style="margin-right:4px">{{ p.device }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="最后心跳" width="170">
          <template #default="{ row }">{{ new Date(row.last_seen).toLocaleTimeString() }}</template>
        </el-table-column>
        <el-table-column label="操作" width="110">
          <template #default="{ row }">
            <el-popconfirm v-if="!row.is_self" title="将该节点移出集群？" @confirm="leave(row.node_id)">
              <template #reference><el-button size="small" type="danger" plain>移除</el-button></template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="joinVisible" title="加入节点" width="440px">
      <el-form label-width="90px">
        <el-form-item label="节点地址" required>
          <el-input v-model="joinForm.address" placeholder="http://192.168.1.10:8000" />
        </el-form-item>
        <el-form-item label="集群令牌">
          <el-input v-model="joinForm.token" type="password" show-password
                    placeholder="留空使用本机配置的令牌" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="joinVisible = false">取消</el-button>
        <el-button type="primary" :disabled="!joinForm.address" @click="join">握手加入</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api, type ClusterInfo, type ClusterNode, type DiscoveredNode } from '../api'
import { useEvents } from '../api/events'
import StatusBadge from '../components/StatusBadge.vue'

const info = ref<ClusterInfo>({ enabled: false, node_id: '', name: '', address: '' })
const nodes = ref<ClusterNode[]>([])
const pending = ref<DiscoveredNode[]>([])
const pendingSel = ref<DiscoveredNode[]>([])
const loading = ref(false)
const joinVisible = ref(false)
const joinForm = reactive({ address: '', token: localStorage.getItem('linkhub_token') ?? '' })

async function load() {
  loading.value = true
  try {
    info.value = await api.clusterInfo()
    if (info.value.enabled) {
      nodes.value = await api.clusterNodes()
      pending.value = await api.discovered()
    }
  } finally { loading.value = false }
}

async function join() {
  try {
    const node = await api.joinCluster(joinForm.address, joinForm.token || undefined)
    ElMessage.success(`已加入节点「${node.name}」`)
    if (joinForm.token) localStorage.setItem('linkhub_token', joinForm.token)
    joinVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail ?? '加入失败')
  }
}

async function approve(row: DiscoveredNode) {
  try {
    await api.joinCluster(row.address, joinForm.token || undefined)
    ElMessage.success(`「${row.name}」已加入`)
    load()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail ?? '加入失败')
  }
}

async function joinSelected() {
  let ok = 0
  for (const row of pendingSel.value) {
    try {
      await api.joinCluster(row.address, joinForm.token || undefined)
      ok++
    } catch { /* 单个失败不阻塞其余 */ }
  }
  ElMessage.success(`已加入 ${ok}/${pendingSel.value.length} 个节点`)
  load()
}

async function ignore(ids: string[]) {
  await api.dismissNodes(ids)
  load()
}
async function ignoreSelected() {
  await ignore(pendingSel.value.map(r => r.node_id))
  ElMessage.success('已忽略，不再出现在发现列表')
}

async function leave(nodeId: string) {
  await api.leaveCluster(nodeId)
  ElMessage.success('已移除')
  load()
}

let nodeStatusTimer: number | undefined
useEvents(e => {
  if (e.event !== 'node_status') return
  if (nodeStatusTimer) return
  nodeStatusTimer = window.setTimeout(() => { nodeStatusTimer = undefined; load() }, 1500)
})
onMounted(load)
// beacon 每 3s 广播，发现列表 5s 轮询保持新鲜
import { onUnmounted } from 'vue'
const timer = window.setInterval(load, 5000)
onUnmounted(() => clearInterval(timer))
</script>

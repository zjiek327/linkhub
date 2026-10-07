<template>
  <div class="page" v-loading="!device">
    <template v-if="device">
      <el-alert v-if="isRemote" type="warning" :closable="false" style="margin-bottom:14px">
        远程设备，归属节点「{{ nodeName }}」。元数据为只读副本，终端操作经本节点中继。
      </el-alert>
      <el-card shadow="never">
        <template #header>
          <div style="display:flex;align-items:center;gap:12px">
            <span style="font-size:18px;font-weight:600">{{ device.name }}</span>
            <StatusBadge :status="device.online ? 'online' : 'offline'" />
            <el-tag v-if="isRemote" type="warning" effect="plain" size="small">@ {{ nodeName }}</el-tag>
            <el-tag v-for="t in device.tags" :key="t" size="small">{{ t }}</el-tag>
          </div>
        </template>
        <el-descriptions :column="3" border>
          <el-descriptions-item label="描述">{{ device.description || '—' }}</el-descriptions-item>
          <el-descriptions-item label="位置">{{ device.location || '—' }}</el-descriptions-item>
          <el-descriptions-item label="负责人">{{ device.owner || '—' }}</el-descriptions-item>
          <el-descriptions-item label="创建时间">{{ fmtTime(device.created_at) }}</el-descriptions-item>
          <el-descriptions-item label="更新时间">{{ fmtTime(device.updated_at) }}</el-descriptions-item>
        </el-descriptions>
      </el-card>

      <el-card shadow="never" style="margin-top:16px">
        <template #header>
          <div style="display:flex;justify-content:space-between;align-items:center">
            <span>连接配置</span>
            <el-button v-if="!isRemote" type="primary" size="small" @click="openConnDialog()">新增连接</el-button>
          </div>
        </template>
        <el-table :data="connections" empty-text="暂无连接配置，点右上角新增">
          <el-table-column label="方式" width="100">
            <template #default="{ row }">
              <el-tag effect="plain">{{ kindLabel(row.kind) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column label="参数" min-width="260">
            <template #default="{ row }">
              <code style="font-size:12px">{{ paramsSummary(row) }}</code>
            </template>
          </el-table-column>
          <el-table-column label="启用" width="80">
            <template #default="{ row }">
              <el-switch v-if="!isRemote" :model-value="row.enabled" @change="toggle(row, $event)" />
              <el-tag v-else size="small" :type="row.enabled ? 'success' : 'info'" effect="plain">
                {{ row.enabled ? '启用' : '禁用' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="240">
            <template #default="{ row }">
              <el-button size="small" type="success" :disabled="!row.enabled" @click="openTerminal(row)">打开终端</el-button>
              <template v-if="!isRemote">
                <el-button size="small" @click="openConnDialog(row)">编辑</el-button>
                <el-popconfirm title="删除该连接配置？" @confirm="removeConn(row.id)">
                  <template #reference><el-button size="small" type="danger" plain>删除</el-button></template>
                </el-popconfirm>
              </template>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <el-card v-if="!isRemote" shadow="never" style="margin-top:16px">
        <template #header>历史会话</template>
        <el-table :data="sessions" empty-text="暂无会话记录">
          <el-table-column prop="id" label="ID" width="70" />
          <el-table-column label="状态" width="100">
            <template #default="{ row }"><StatusBadge :status="row.status" /></template>
          </el-table-column>
          <el-table-column prop="opened_at" label="打开时间">
            <template #default="{ row }">{{ fmtTime(row.opened_at) }}</template>
          </el-table-column>
          <el-table-column label="关闭时间">
            <template #default="{ row }">{{ row.closed_at ? fmtTime(row.closed_at) : '—' }}</template>
          </el-table-column>
          <el-table-column prop="last_error" label="错误" min-width="160" show-overflow-tooltip />
          <el-table-column label="操作" width="120">
            <template #default="{ row }">
              <el-button v-if="!row.closed_at" size="small" type="danger" plain @click="closeSession(row.id)">关闭</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-card>
    </template>

    <el-dialog v-model="connDialog" :title="editing ? '编辑连接' : '新增连接'" width="560px">
      <el-form label-width="110px">
        <el-form-item label="连接方式">
          <el-select v-model="connForm.kind" style="width:100%" :disabled="!!editing">
            <el-option v-for="k in kinds" :key="k.kind" :label="kindLabel(k.kind)" :value="k.kind" />
          </el-select>
        </el-form-item>
        <el-form-item label="名称"><el-input v-model="connForm.name" /></el-form-item>
        <el-form-item v-if="clusterEnabled" label="端口所在节点">
          <el-select v-model="connForm.node_id" style="width:100%">
            <el-option label="本机" value="" />
            <el-option v-for="n in peerNodes" :key="n.node_id" :label="n.name" :value="n.node_id" />
          </el-select>
        </el-form-item>
        <el-form-item label="自动重连">
          <el-switch v-model="connForm.auto_reconnect" />
        </el-form-item>
      </el-form>
      <ConnectionForm v-if="currentSchema" :schema="currentSchema" v-model="connForm.params" />
      <template #footer>
        <el-button @click="connDialog = false">取消</el-button>
        <el-button type="primary" :disabled="!connForm.name" @click="saveConn">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api, type ClusterNode, type Connection, type ConnectorKind, type Device, type Session } from '../api'
import { useEvents } from '../api/events'
import StatusBadge from '../components/StatusBadge.vue'
import ConnectionForm from '../components/ConnectionForm.vue'

const route = useRoute()
const router = useRouter()
const deviceId = Number(route.params.id)
const remoteNode = (route.query.node as string) || ''
const isRemote = !!remoteNode
const nodeName = ref(remoteNode)

const device = ref<(Device & Record<string, any>) | null>(null)
const connections = ref<Connection[]>([])
const sessions = ref<Session[]>([])
const kinds = ref<ConnectorKind[]>([])
const clusterEnabled = ref(false)
const peerNodes = ref<ClusterNode[]>([])
const selfNodeId = ref('')

const connDialog = ref(false)
const editing = ref<Connection | null>(null)
const connForm = reactive({ kind: 'serial', name: '', auto_reconnect: true, node_id: '', params: {} as Record<string, any> })

const currentSchema = computed(() => kinds.value.find(k => k.kind === connForm.kind)?.schema)

const fmtTime = (t: string) => (t && !t.startsWith('1970') ? new Date(t).toLocaleString() : '—')

function kindLabel(kind: string) {
  return { serial: '串口/USB', ssh: 'SSH', ble: '蓝牙 BLE', mqtt: 'MQTT', telnet: 'Telnet' }[kind] ?? kind
}
function paramsSummary(row: Connection) {
  const p = row.params ?? {}
  if (row.kind === 'serial') return `${p.port} · ${p.baudrate} ${p.bytesize ?? 8}${p.parity ?? 'N'}${p.stopbits ?? 1}`
  return JSON.stringify(p)
}

async function load() {
  if (isRemote) {
    const r = await api.proxyDevice(remoteNode, deviceId)
    device.value = { ...r.device, connections: [] }
    connections.value = r.connections
  } else {
    device.value = await api.device(deviceId)
    connections.value = await api.connections(deviceId)
    const all = await api.sessions()
    sessions.value = all.filter(s => connections.value.some(c => c.id === s.connection_id)).slice(0, 20)
  }
}

function openConnDialog(row?: Connection) {
  editing.value = row ?? null
  if (row) {
    connForm.kind = row.kind
    connForm.name = row.name
    connForm.auto_reconnect = row.params?.auto_reconnect ?? true
    connForm.node_id = row.node_id && row.node_id !== 'local' ? row.node_id : ''
    connForm.params = { ...row.params }
  } else {
    connForm.kind = 'serial'
    connForm.name = ''
    connForm.auto_reconnect = true
    connForm.node_id = ''
    connForm.params = {}
  }
  connDialog.value = true
}

async function saveConn() {
  const params = { ...connForm.params, auto_reconnect: connForm.auto_reconnect }
  const body = {
    kind: connForm.kind, name: connForm.name, params,
    enabled: editing.value?.enabled ?? true,
    node_id: connForm.node_id || null,
  }
  if (editing.value) await api.updateConnection(editing.value.id, body)
  else await api.createConnection(deviceId, body)
  ElMessage.success('已保存')
  connDialog.value = false
  load()
}

async function toggle(row: Connection, val: boolean) {
  await api.updateConnection(row.id, { kind: row.kind, name: row.name, params: row.params, enabled: val, node_id: row.node_id })
  row.enabled = val
}

async function removeConn(id: number) {
  await api.deleteConnection(id)
  ElMessage.success('已删除')
  load()
}

async function openTerminal(row: Connection) {
  try {
    if (isRemote) {
      const r = await api.proxyOpen(remoteNode, row.id)
      router.push(`/terminal/${r.session_id}?node=${r.node_id}&conn=${row.id}`)
    } else {
      const sess = await api.openSession(row.id)
      const node = sess.node_id && sess.node_id !== 'local' && sess.node_id !== selfNodeId.value
        ? `&node=${sess.node_id}` : ''
      router.push(`/terminal/${sess.id}?conn=${row.id}${node}`)
    }
  } catch (e: any) {
    const detail = e.response?.data?.detail
    // 端口已有活跃会话 → 直接跳转到已打开的终端（可多人旁观同一会话）
    if (e.response?.status === 409 && detail?.session_id) {
      ElMessage.info('已有打开的会话，正在跳转')
      router.push(`/terminal/${detail.session_id}?conn=${row.id}`)
      return
    }
    ElMessage.error(typeof detail === 'string' ? detail : detail?.message ?? '打开会话失败')
  }
}

async function closeSession(id: number) {
  await api.closeSession(id)
  load()
}

useEvents(e => {
  if (e.event === 'session_status' && !isRemote && e.data.device_id === deviceId) load()
})
onMounted(async () => {
  kinds.value = await api.connectorKinds()
  const info = await api.clusterInfo()
  clusterEnabled.value = info.enabled
  selfNodeId.value = info.node_id
  if (info.enabled) {
    const ns = await api.clusterNodes()
    peerNodes.value = ns.filter(n => !n.is_self && n.status === 'online')
    if (isRemote) nodeName.value = ns.find(n => n.node_id === remoteNode)?.name ?? remoteNode
  }
  load()
})
</script>

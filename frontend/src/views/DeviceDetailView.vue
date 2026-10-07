<template>
  <div class="page" v-loading="!device">
    <template v-if="device">
      <el-card shadow="never">
        <template #header>
          <div style="display:flex;align-items:center;gap:12px">
            <span style="font-size:18px;font-weight:600">{{ device.name }}</span>
            <StatusBadge :status="device.online ? 'online' : 'offline'" />
            <el-tag v-for="t in device.tags" :key="t" size="small">{{ t }}</el-tag>
          </div>
        </template>
        <el-descriptions :column="3" border>
          <el-descriptions-item label="描述">{{ device.description || '—' }}</el-descriptions-item>
          <el-descriptions-item label="位置">{{ device.location || '—' }}</el-descriptions-item>
          <el-descriptions-item label="负责人">{{ device.owner || '—' }}</el-descriptions-item>
          <el-descriptions-item label="创建时间">{{ new Date(device.created_at).toLocaleString() }}</el-descriptions-item>
          <el-descriptions-item label="更新时间">{{ new Date(device.updated_at).toLocaleString() }}</el-descriptions-item>
        </el-descriptions>
      </el-card>

      <el-card shadow="never" style="margin-top:16px">
        <template #header>
          <div style="display:flex;justify-content:space-between;align-items:center">
            <span>连接配置</span>
            <el-button type="primary" size="small" @click="openConnDialog()">新增连接</el-button>
          </div>
        </template>
        <el-table :data="connections" empty-text="暂无连接配置，点右上角新增">
          <el-table-column label="方式" width="100">
            <template #default="{ row }">
              <el-tag effect="plain">{{ kindLabel(row.kind) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column label="参数" min-width="280">
            <template #default="{ row }">
              <code style="font-size:12px">{{ paramsSummary(row) }}</code>
            </template>
          </el-table-column>
          <el-table-column label="启用" width="80">
            <template #default="{ row }">
              <el-switch :model-value="row.enabled" @change="toggle(row, $event)" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="260">
            <template #default="{ row }">
              <el-button size="small" type="success" :disabled="!row.enabled" @click="openTerminal(row)">打开终端</el-button>
              <el-button size="small" @click="openConnDialog(row)">编辑</el-button>
              <el-popconfirm title="删除该连接配置？" @confirm="removeConn(row.id)">
                <template #reference><el-button size="small" type="danger" plain>删除</el-button></template>
              </el-popconfirm>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <el-card shadow="never" style="margin-top:16px">
        <template #header>历史会话</template>
        <el-table :data="sessions" empty-text="暂无会话记录">
          <el-table-column prop="id" label="ID" width="70" />
          <el-table-column label="状态" width="100">
            <template #default="{ row }"><StatusBadge :status="row.status" /></template>
          </el-table-column>
          <el-table-column prop="opened_at" label="打开时间">
            <template #default="{ row }">{{ new Date(row.opened_at).toLocaleString() }}</template>
          </el-table-column>
          <el-table-column label="关闭时间">
            <template #default="{ row }">{{ row.closed_at ? new Date(row.closed_at).toLocaleString() : '—' }}</template>
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
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api, type Connection, type ConnectorKind, type Device, type Session } from '../api'
import { useEvents } from '../api/events'
import StatusBadge from '../components/StatusBadge.vue'
import ConnectionForm from '../components/ConnectionForm.vue'

const route = useRoute()
const router = useRouter()
const deviceId = Number(route.params.id)

const device = ref<Device | null>(null)
const connections = ref<Connection[]>([])
const sessions = ref<Session[]>([])
const kinds = ref<ConnectorKind[]>([])

const connDialog = ref(false)
const editing = ref<Connection | null>(null)
const connForm = reactive({ kind: 'serial', name: '', auto_reconnect: true, params: {} as Record<string, any> })

const currentSchema = computed(() => kinds.value.find(k => k.kind === connForm.kind)?.schema)

function kindLabel(kind: string) {
  return { serial: '串口/USB', ssh: 'SSH', ble: '蓝牙 BLE', mqtt: 'MQTT', telnet: 'Telnet' }[kind] ?? kind
}
function paramsSummary(row: Connection) {
  const p = row.params ?? {}
  if (row.kind === 'serial') return `${p.port} · ${p.baudrate} ${p.bytesize ?? 8}${p.parity ?? 'N'}${p.stopbits ?? 1}`
  return JSON.stringify(p)
}

async function load() {
  device.value = await api.device(deviceId)
  connections.value = await api.connections(deviceId)
  const all = await api.sessions()
  sessions.value = all.filter(s => connections.value.some(c => c.id === s.connection_id)).slice(0, 20)
}

function openConnDialog(row?: Connection) {
  editing.value = row ?? null
  if (row) {
    connForm.kind = row.kind
    connForm.name = row.name
    connForm.auto_reconnect = row.params?.auto_reconnect ?? true
    connForm.params = { ...row.params }
  } else {
    connForm.kind = 'serial'
    connForm.name = ''
    connForm.auto_reconnect = true
    connForm.params = {}
  }
  connDialog.value = true
}

async function saveConn() {
  const params = { ...connForm.params, auto_reconnect: connForm.auto_reconnect }
  if (editing.value) {
    await api.updateConnection(editing.value.id, { kind: connForm.kind, name: connForm.name, params, enabled: editing.value.enabled })
  } else {
    await api.createConnection(deviceId, { kind: connForm.kind, name: connForm.name, params })
  }
  ElMessage.success('已保存')
  connDialog.value = false
  load()
}

async function toggle(row: Connection, val: boolean) {
  await api.updateConnection(row.id, { kind: row.kind, name: row.name, params: row.params, enabled: val })
  row.enabled = val
}

async function removeConn(id: number) {
  await api.deleteConnection(id)
  ElMessage.success('已删除')
  load()
}

async function openTerminal(row: Connection) {
  try {
    const sess = await api.openSession(row.id)
    router.push(`/terminal/${sess.id}`)
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail ?? '打开会话失败')
  }
}

async function closeSession(id: number) {
  await api.closeSession(id)
  load()
}

useEvents(e => { if (e.event === 'session_status' && e.data.device_id === deviceId) load() })
onMounted(async () => {
  kinds.value = await api.connectorKinds()
  load()
})
watch(() => route.params.id, () => location.reload())
</script>

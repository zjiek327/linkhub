<template>
  <div class="page">
    <div class="toolbar">
      <el-input v-model="keyword" placeholder="搜索名称 / 描述" clearable style="width:220px"
                @change="load" @clear="load">
        <template #prefix><el-icon><Search /></el-icon></template>
      </el-input>
      <el-select v-model="nodeFilter" placeholder="全部节点" clearable style="width:160px" @change="load">
        <el-option v-for="n in nodeOptions" :key="n" :label="n" :value="n" />
      </el-select>
      <el-button type="primary" @click="createVisible = true">新建设备</el-button>
      <el-button @click="$router.push('/templates')">从模板创建</el-button>
      <el-button type="warning" plain :disabled="!selection.length" @click="batchVisible = true">
        批量执行（{{ selection.length }}）
      </el-button>
    </div>

    <el-card shadow="never">
      <el-table :data="items" v-loading="loading" @selection-change="selection = $event">
        <el-table-column type="selection" width="40" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <StatusBadge :status="row.online ? 'online' : 'offline'" />
          </template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="130">
          <template #default="{ row }">
            <el-link type="primary" @click="goDetail(row)">{{ row.name }}</el-link>
          </template>
        </el-table-column>
        <el-table-column label="节点" width="130">
          <template #default="{ row }">
            <el-tag size="small" :type="isRemote(row) ? (row.node_online ? 'warning' : 'info') : 'primary'"
                    effect="plain">
              {{ isRemote(row) ? row.node_name : '本机' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="标签" min-width="120">
          <template #default="{ row }">
            <el-tag v-for="t in row.tags" :key="t" size="small" style="margin-right:4px">{{ t }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="location" label="位置" width="100" />
        <el-table-column prop="description" label="描述" min-width="150" show-overflow-tooltip />
        <el-table-column label="操作" width="150">
          <template #default="{ row }">
            <el-button size="small" @click="goDetail(row)">详情</el-button>
            <el-popconfirm :title="isRemote(row) ? '剔除该远程设备？（在归属节点上级联删除）' : '确定删除该设备及其全部连接配置？'"
                           @confirm="remove(row)">
              <template #reference><el-button size="small" type="danger" plain>删除</el-button></template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>
      <el-pagination style="margin-top:12px;justify-content:flex-end" layout="total, prev, pager, next"
                     :total="total" :page-size="size" v-model:current-page="page" @current-change="load" />
    </el-card>

    <el-dialog v-model="createVisible" title="新建设备" width="480px">
      <el-form label-width="80px">
        <el-form-item label="名称" required><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="form.description" type="textarea" /></el-form-item>
        <el-form-item label="位置"><el-input v-model="form.location" /></el-form-item>
        <el-form-item label="负责人"><el-input v-model="form.owner" /></el-form-item>
        <el-form-item label="标签">
          <el-select v-model="form.tags" multiple filterable allow-create placeholder="回车创建标签" style="width:100%" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :disabled="!form.name" @click="create">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="batchVisible" title="批量执行命令" width="680px">
      <el-alert type="info" :closable="false" style="margin-bottom:12px">
        向选中的 {{ selection.length }} 台设备下发命令（跨节点自动路由到归属节点），收集输出窗口。
      </el-alert>
      <el-input v-model="batchCmd" placeholder="如：uname -a" style="margin-bottom:12px">
        <template #prepend>命令</template>
      </el-input>
      <div style="margin-bottom:12px">
        输出等待
        <el-input-number v-model="batchWait" :min="100" :max="30000" :step="500" /> ms
        <el-button type="primary" style="margin-left:14px" :loading="batchRunning"
                   :disabled="!batchCmd" @click="runBatch">执行</el-button>
      </div>
      <el-table v-if="batchResults.length" :data="batchResults" max-height="400">
        <el-table-column prop="device_name" label="设备" width="150" />
        <el-table-column label="节点" width="110">
          <template #default="{ row }">
            {{ nodes[row.node_id] ?? row.node_id }}
          </template>
        </el-table-column>
        <el-table-column label="结果" width="80">
          <template #default="{ row }">
            <el-tag :type="row.ok ? 'success' : 'danger'" size="small">{{ row.ok ? '成功' : '失败' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="输出" min-width="220">
          <template #default="{ row }">
            <pre class="out">{{ row.ok ? row.output : row.error }}</pre>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import { api, type BatchResult, type ClusterInfo, type Device } from '../api'
import { useEvents } from '../api/events'
import StatusBadge from '../components/StatusBadge.vue'

const router = useRouter()
const items = ref<Device[]>([])
const total = ref(0)
const page = ref(1)
const size = 20
const keyword = ref('')
const nodeFilter = ref('')
const loading = ref(false)
const clusterInfo = ref<ClusterInfo>({ enabled: false, node_id: 'local', name: '', address: '' })

const selection = ref<Device[]>([])
const batchVisible = ref(false)
const batchCmd = ref('')
const batchWait = ref(1500)
const batchRunning = ref(false)
const batchResults = ref<BatchResult[]>([])
const nodes = ref<Record<string, string>>({})

const createVisible = ref(false)
const form = reactive({ name: '', description: '', location: '', owner: '', tags: [] as string[] })

const isRemote = (d: Device) => clusterInfo.value.enabled && d.node_id !== clusterInfo.value.node_id
const nodeOptions = computed(() =>
  [...new Set(items.value.map(d => isRemote(d) ? d.node_name : '本机'))])

async function load() {
  loading.value = true
  try {
    const r = await api.devices({ keyword: keyword.value || undefined, page: page.value, size })
    let list = r.items
    if (nodeFilter.value) {
      list = list.filter(d => (isRemote(d) ? d.node_name : '本机') === nodeFilter.value)
    }
    items.value = list
    total.value = r.total
  } finally { loading.value = false }
}

function goDetail(d: Device) {
  if (isRemote(d)) router.push(`/devices/${d.id}?node=${d.node_id}`)
  else router.push(`/devices/${d.id}`)
}

async function create() {
  await api.createDevice(form)
  ElMessage.success('创建成功')
  createVisible.value = false
  form.name = ''; form.description = ''; form.location = ''; form.owner = ''; form.tags = []
  load()
}
async function remove(row: Device) {
  if (isRemote(row)) await api.proxyDeleteDevice(row.node_id, row.id)
  else await api.deleteDevice(row.id)
  ElMessage.success('已删除')
  load()
}

async function runBatch() {
  batchRunning.value = true
  batchResults.value = []
  try {
    const targets = selection.value.map(d => ({
      node_id: isRemote(d) ? d.node_id : 'local', device_id: d.id,
    }))
    const r = await api.batchExec(targets, batchCmd.value, batchWait.value)
    batchResults.value = r.results
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail ?? '批量执行失败')
  } finally { batchRunning.value = false }
}

useEvents(e => { if (e.event === 'session_status' || e.event === 'node_status') load() })
onMounted(async () => {
  clusterInfo.value = await api.clusterInfo()
  if (clusterInfo.value.enabled) {
    for (const n of await api.clusterNodes()) nodes.value[n.node_id] = n.name
  }
  load()
})
</script>

<style scoped>
.out { margin: 0; font-size: 12px; white-space: pre-wrap; word-break: break-all; max-height: 120px; overflow: auto; }
</style>

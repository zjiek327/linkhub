<template>
  <div class="page">
    <el-alert v-if="!info.enabled" type="info" :closable="false" style="margin-bottom:14px">
      集群模式未启用。在各节点后端设置 <code>LINKHUB_CLUSTER_ENABLED=true</code> 与相同的
      <code>LINKHUB_CLUSTER_TOKEN</code> 后重启，再回到本页加入集群。
    </el-alert>

    <el-card v-if="!info.enabled && selfCap" shadow="never" style="margin-bottom:14px">
      <template #header>🖥 本机桌面</template>
      <div style="display:flex;align-items:center;gap:12px">
        <el-tag size="small" effect="plain"
                :type="selfCap.status === 'active' ? 'success' : selfCap.status === 'available' ? 'warning' : 'info'">
          {{ capText(selfCap) }}
        </el-tag>
        <span v-if="selfCap.hint" style="color:var(--lh-muted,#888);font-size:13px">{{ selfCap.hint }}</span>
        <span style="flex:1" />
        <el-button size="small" type="primary" :disabled="selfCap.status !== 'active'"
                   @click="openDesktop('', '本机')">连接桌面</el-button>
        <el-button v-if="selfCap.status === 'available'" size="small" type="warning" plain
                   @click="showCapHint('', '本机', selfCap)">帮我安装</el-button>
      </div>
    </el-card>

    <div class="toolbar">
      <el-button type="primary" :disabled="!info.enabled" @click="joinVisible = true">加入节点</el-button>
      <el-button @click="load">刷新</el-button>
      <el-radio-group v-model="statusFilter" size="small">
        <el-radio-button value="online">在线</el-radio-button>
        <el-radio-button value="offline">离线</el-radio-button>
        <el-radio-button value="all">全部</el-radio-button>
      </el-radio-group>
      <el-popconfirm title="清理所有离线节点？" @confirm="cleanOffline">
        <template #reference>
          <el-button size="small" type="danger" plain :disabled="!offlineCount">清理离线（{{ offlineCount }}）</el-button>
        </template>
      </el-popconfirm>
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
      <el-table :data="filteredNodes" v-loading="loading">
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
        <el-table-column label="桌面" width="110">
          <template #default="{ row }">
            <template v-if="row.status === 'online' || row.is_self">
              <el-button v-if="row.resources?.desktop?.status === 'active'" size="small" type="primary" plain
                         @click="openDesktop(row.node_id, row.name)">🖥 桌面</el-button>
              <el-button v-else-if="row.resources?.desktop?.status === 'available'" size="small" plain
                         @click="showCapHint(row.node_id, row.name, row.resources.desktop)">需开启</el-button>
              <el-tooltip v-else content="该节点无图形桌面环境或能力未知">
                <el-button size="small" disabled>无桌面</el-button>
              </el-tooltip>
            </template>
          </template>
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

    <el-dialog v-model="capDlg.visible" :title="`开启桌面共享 · ${capDlg.name}`" width="560px" append-to-body>
      <template v-if="capDlg.step === 'ask'">
        <p style="margin:0 0 10px">{{ capDlg.hint }}</p>
        <p style="margin:0"><b>需要我帮你安装吗？</b></p>
      </template>
      <template v-else-if="capDlg.step === 'install'">
        <el-form label-width="90px">
          <el-form-item label="VNC 密码" required>
            <el-input v-model="capDlg.password" type="password" show-password
                      placeholder="安装启动后用它连接桌面" @keyup.enter="doSetup" />
          </el-form-item>
        </el-form>
        <el-alert type="info" :closable="false">
          仅 Ubuntu X11 会话支持自动安装（需要目标节点有免密 sudo）；其他系统会返回手动安装指引
        </el-alert>
      </template>
      <template v-else>
        <el-alert :type="capDlg.ok ? 'success' : 'error'" :closable="false" :title="capDlg.message" />
        <pre v-if="capDlg.output" class="setup-output">{{ capDlg.output }}</pre>
      </template>
      <template #footer>
        <template v-if="capDlg.step === 'ask'">
          <el-button autofocus @click="capDlg.visible = false">不需要</el-button>
          <el-button type="primary" @click="capDlg.step = 'install'">需要</el-button>
        </template>
        <template v-else-if="capDlg.step === 'install'">
          <el-button @click="capDlg.step = 'ask'">返回</el-button>
          <el-button type="primary" :loading="capDlg.loading" :disabled="!capDlg.password"
                     @click="doSetup">开始安装</el-button>
        </template>
        <template v-else>
          <el-button v-if="!capDlg.ok" @click="capDlg.step = 'install'">重试</el-button>
          <el-button @click="capDlg.visible = false">关闭</el-button>
          <el-button v-if="capDlg.ok" type="primary"
                     @click="capDlg.visible = false; openDesktop(capDlg.node, capDlg.name)">连接桌面</el-button>
        </template>
      </template>
    </el-dialog>

    <el-dialog v-model="deskVisible" :title="`🖥 桌面共享 · ${deskNodeName}`"
               width="92%" top="2vh" destroy-on-close append-to-body>
      <DesktopPane v-if="deskVisible" :node="deskNode" :node-name="deskNodeName" />
    </el-dialog>

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
import { computed, defineAsyncComponent, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api, type ClusterInfo, type ClusterNode, type DiscoveredNode } from '../api'
import { useEvents } from '../api/events'
import StatusBadge from '../components/StatusBadge.vue'

// noVNC 体积大（~290KB），按需加载：打开桌面对话框时才拉取 chunk
const DesktopPane = defineAsyncComponent(() => import('../components/DesktopPane.vue'))

type DesktopCap = { status: 'active' | 'available' | 'headless'; port: number; hint?: string }

const deskVisible = ref(false)
const deskNode = ref('')
const deskNodeName = ref('')
const selfCap = ref<DesktopCap | null>(null)

function capText(c: DesktopCap) {
  return c.status === 'active' ? `VNC 服务运行中（:${c.port}）`
    : c.status === 'available' ? '有图形桌面，未开启 VNC 服务' : '无图形桌面环境'
}
function openDesktop(nodeId: string, name: string) {
  deskNode.value = nodeId
  deskNodeName.value = name
  deskVisible.value = true
}

// 「需开启」→ 安装辅助对话框：询问 → 输密码安装 → 结果（成功可直接连桌面）
const capDlg = reactive({
  visible: false, step: 'ask' as 'ask' | 'install' | 'done',
  node: '', name: '', hint: '', password: '',
  loading: false, ok: false, message: '', output: '',
})
function showCapHint(nodeId: string, name: string, c: DesktopCap) {
  Object.assign(capDlg, {
    visible: true, step: 'ask', node: nodeId, name,
    hint: c.hint || '该节点有图形桌面但未运行 VNC 服务端',
    password: '', ok: false, message: '', output: '',
  })
}
async function doSetup() {
  capDlg.loading = true
  try {
    const r = await api.desktopSetup(capDlg.node, capDlg.password)
    capDlg.ok = r.ok
    capDlg.message = r.message
    capDlg.output = r.output ?? ''
  } catch (e: any) {
    capDlg.ok = false
    capDlg.message = e.response?.data?.detail ?? '安装请求失败（节点不可达？）'
  } finally {
    capDlg.loading = false
    capDlg.step = 'done'
  }
}

const info = ref<ClusterInfo>({ enabled: false, node_id: '', name: '', address: '' })
const nodes = ref<ClusterNode[]>([])
const pending = ref<DiscoveredNode[]>([])
const pendingSel = ref<DiscoveredNode[]>([])
const loading = ref(false)
const statusFilter = ref<'online' | 'offline' | 'all'>('all')
const joinVisible = ref(false)
const joinForm = reactive({ address: '', token: localStorage.getItem('linkhub_token') ?? '' })

const offlineCount = computed(() => nodes.value.filter(n => !n.is_self && n.status !== 'online').length)
const filteredNodes = computed(() => {
  if (statusFilter.value === 'all') return nodes.value
  if (statusFilter.value === 'online') return nodes.value.filter(n => n.is_self || n.status === 'online')
  return nodes.value.filter(n => !n.is_self && n.status !== 'online')
})

async function load() {
  loading.value = true
  try {
    info.value = await api.clusterInfo()
    if (info.value.enabled) {
      nodes.value = await api.clusterNodes()
      pending.value = await api.discovered()
    } else {
      // 非集群模式：本机桌面能力独立探测
      try { selfCap.value = await api.desktopCapability() } catch { selfCap.value = null }
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

async function cleanOffline() {
  const offline = nodes.value.filter(n => !n.is_self && n.status !== 'online')
  let ok = 0
  for (const n of offline) {
    try { await api.leaveCluster(n.node_id); ok++ } catch { /* 单个失败继续 */ }
  }
  ElMessage.success(`已清理 ${ok} 个离线节点`)
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

<style scoped>
.setup-output {
  max-height: 220px; overflow: auto; margin: 10px 0 0; padding: 8px 10px;
  background: var(--lh-workbench-bg, #0c1116); color: var(--lh-muted, #8a94a0);
  border-radius: 6px; font-size: 12px; white-space: pre-wrap; word-break: break-all;
}
</style>

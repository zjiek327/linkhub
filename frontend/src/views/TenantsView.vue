<template>
  <div class="page">
    <el-card shadow="never">
      <template #header>
        <div class="head">
          <span>租户管理</span>
          <el-button type="primary" size="small" @click="openDialog()">新建租户</el-button>
        </div>
      </template>
      <el-table :data="tenants" v-loading="loading">
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="name" label="名称" min-width="120" />
        <el-table-column prop="code" label="标识" width="120" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'danger'" size="small">{{ row.status === 'active' ? '启用' : '停用' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="members" label="成员数" width="80" />
        <el-table-column prop="remark" label="备注" min-width="140" show-overflow-tooltip />
        <el-table-column label="操作" width="260">
          <template #default="{ row }">
            <el-button size="small" @click="openMembers(row)">成员</el-button>
            <el-button size="small" @click="openNodes(row)">节点分配</el-button>
            <el-button size="small" @click="openDialog(row)">编辑</el-button>
            <el-popconfirm title="删除该租户（成员关系与节点分配一并删除）？" @confirm="remove(row.id)">
              <template #reference><el-button size="small" type="danger" plain :disabled="row.id === 1">删除</el-button></template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dlg" :title="editing ? '编辑租户' : '新建租户'" width="460px">
      <el-form label-width="80px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="标识"><el-input v-model="form.code" :disabled="!!editing" placeholder="登录用，如 team-a" /></el-form-item>
        <el-form-item label="状态">
          <el-switch v-model="form.active" active-text="启用" inactive-text="停用" />
        </el-form-item>
        <el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dlg = false">取消</el-button>
        <el-button type="primary" :disabled="!form.name || !form.code" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="memberDlg" :title="`成员管理 · ${current?.name ?? ''}`" width="560px">
      <el-table :data="members" size="small">
        <el-table-column prop="user_name" label="用户" width="140" />
        <el-table-column label="角色" width="150">
          <template #default="{ row }">
            <el-select :model-value="row.role" size="small" @change="(v: string) => changeRole(row, v)">
              <el-option label="租户管理员" value="tenant_admin" />
              <el-option label="操作员" value="operator" />
              <el-option label="观察者" value="viewer" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="加入时间" />
        <el-table-column width="80">
          <template #default="{ row }">
            <el-popconfirm title="移出该成员？" @confirm="removeMember(row.user_id)">
              <template #reference><el-button size="small" type="danger" plain>移出</el-button></template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>
      <div style="display:flex;gap:8px;margin-top:12px">
        <el-input v-model="newMember" placeholder="用户名（先在系统管理创建）" size="small" />
        <el-select v-model="newRole" size="small" style="width:130px">
          <el-option label="租户管理员" value="tenant_admin" />
          <el-option label="操作员" value="operator" />
          <el-option label="观察者" value="viewer" />
        </el-select>
        <el-button size="small" type="primary" @click="addMember">添加</el-button>
      </div>
    </el-dialog>

    <el-dialog v-model="nodeDlg" :title="`节点分配 · ${current?.name ?? ''}`" width="480px">
      <el-alert type="info" :closable="false" style="margin-bottom:10px">
        分配后该租户只能将设备放置在勾选的节点上；全不选 = 可用全部节点。
      </el-alert>
      <el-checkbox-group v-model="selectedNodes">
        <el-checkbox v-for="n in nodes" :key="n.node_id" :value="n.node_id">
          {{ n.name }}（{{ n.status === 'online' ? '在线' : '离线' }}）
        </el-checkbox>
      </el-checkbox-group>
      <template #footer>
        <el-button @click="nodeDlg = false">取消</el-button>
        <el-button type="primary" @click="saveNodes">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api, type ClusterNode } from '../api'

const loading = ref(false)
const tenants = ref<any[]>([])
const nodes = ref<ClusterNode[]>([])
const dlg = ref(false)
const editing = ref<any>(null)
const form = reactive({ name: '', code: '', active: true, remark: '' })

const memberDlg = ref(false)
const members = ref<any[]>([])
const current = ref<any>(null)
const newMember = ref('')
const newRole = ref('operator')

const nodeDlg = ref(false)
const selectedNodes = ref<string[]>([])

async function load() {
  loading.value = true
  try {
    tenants.value = await api.tenants()
    nodes.value = await api.clusterNodes()
  } finally { loading.value = false }
}

function openDialog(row?: any) {
  editing.value = row ?? null
  Object.assign(form, row
    ? { name: row.name, code: row.code, active: row.status === 'active', remark: row.remark }
    : { name: '', code: '', active: true, remark: '' })
  dlg.value = true
}

async function save() {
  const body = { name: form.name, code: form.code, status: form.active ? 'active' : 'disabled', remark: form.remark }
  if (editing.value) await api.updateTenant(editing.value.id, body)
  else await api.createTenant(body)
  ElMessage.success('已保存')
  dlg.value = false
  load()
}

async function remove(id: number) {
  await api.deleteTenant(id)
  ElMessage.success('已删除')
  load()
}

async function openMembers(row: any) {
  current.value = row
  members.value = await api.tenantMembers(row.id)
  memberDlg.value = true
}

async function addMember() {
  if (!newMember.value) return
  await api.addTenantMember(current.value.id, { name: newMember.value, role: newRole.value })
  ElMessage.success('已添加')
  newMember.value = ''
  members.value = await api.tenantMembers(current.value.id)
  load()
}

async function changeRole(row: any, role: string) {
  await api.updateTenantMember(current.value.id, row.user_id, { role })
  ElMessage.success('角色已更新')
}

async function removeMember(userId: number) {
  await api.removeTenantMember(current.value.id, userId)
  members.value = await api.tenantMembers(current.value.id)
  load()
}

async function openNodes(row: any) {
  current.value = row
  const r = await api.tenantNodes(row.id)
  selectedNodes.value = r.nodes ?? []
  nodeDlg.value = true
}

async function saveNodes() {
  await api.setTenantNodes(current.value.id, selectedNodes.value)
  ElMessage.success('节点分配已保存')
  nodeDlg.value = false
}

onMounted(load)
</script>

<style scoped>
.page { padding: 16px; }
.head { display: flex; justify-content: space-between; align-items: center; }
</style>

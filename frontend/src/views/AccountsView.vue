<template>
  <div class="page">
    <el-row :gutter="16">
      <el-col :span="10">
        <el-card shadow="never">
          <template #header>
            <div class="head">
              <span>用户（当前租户）</span>
              <el-button type="primary" size="small" @click="userDlg = true">新建账号</el-button>
            </div>
          </template>
          <el-table :data="users" size="small">
            <el-table-column prop="name" label="用户名" />
            <el-table-column prop="role" label="角色" width="110">
              <template #default="{ row }">{{ roleLabel(row.role) }}</template>
            </el-table-column>
            <el-table-column prop="enabled" label="状态" width="80">
              <template #default="{ row }">
                <el-tag :type="row.enabled ? 'success' : 'danger'" size="small">{{ row.enabled ? '启用' : '禁用' }}</el-tag>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
      <el-col :span="14">
        <el-card shadow="never">
          <template #header>
            <div class="head">
              <span>设备分组（成员只能看到组内设备）</span>
              <el-button type="primary" size="small" @click="openGroupDlg()">新建分组</el-button>
            </div>
          </template>
          <el-table :data="groups" size="small">
            <el-table-column prop="name" label="分组" min-width="100" />
            <el-table-column prop="members" label="成员数" width="70" />
            <el-table-column prop="devices" label="设备数" width="70" />
            <el-table-column label="操作" width="200">
              <template #default="{ row }">
                <el-button size="small" @click="openGroup(row)">管理</el-button>
                <el-popconfirm title="删除该分组（成员与设备关联一并删除）？" @confirm="removeGroup(row.id)">
                  <template #reference><el-button size="small" type="danger" plain>删除</el-button></template>
                </el-popconfirm>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>

    <el-dialog v-model="groupDlg" :title="`分组管理 · ${current?.name ?? ''}`" width="620px">
      <el-tabs>
        <el-tab-pane label="成员">
          <div style="display:flex;gap:8px;margin-bottom:10px">
            <el-select v-model="addMemberId" filterable placeholder="选择用户" size="small" style="flex:1">
              <el-option v-for="u in users" :key="u.id" :label="u.name" :value="u.id" />
            </el-select>
            <el-button size="small" type="primary" @click="addMember">加入分组</el-button>
          </div>
          <el-table :data="members" size="small">
            <el-table-column prop="name" label="用户名" />
            <el-table-column width="90">
              <template #default="{ row }">
                <el-popconfirm title="移出该成员？" @confirm="removeMember(row.id)">
                  <template #reference><el-button size="small" type="danger" plain>移出</el-button></template>
                </el-popconfirm>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>
        <el-tab-pane label="设备">
          <el-alert type="info" :closable="false" style="margin-bottom:8px">
            勾选加入该分组的设备（一个设备可同时属于多个分组）
          </el-alert>
          <el-checkbox-group v-model="selectedDevices">
            <el-checkbox v-for="d in allDevices" :key="d.id" :value="d.id">{{ d.name }}</el-checkbox>
          </el-checkbox-group>
          <el-button style="margin-top:10px" type="primary" size="small" @click="saveDevices">保存设备</el-button>
        </el-tab-pane>
      </el-tabs>
    </el-dialog>

    <el-dialog v-model="userDlg" title="新建账号" width="420px">
      <el-form label-width="80px">
        <el-form-item label="用户名"><el-input v-model="newUser.name" /></el-form-item>
        <el-form-item label="密码"><el-input v-model="newUser.password" type="password" /></el-form-item>
        <el-form-item label="角色">
          <el-select v-model="newUser.role" style="width:100%">
            <el-option label="租户管理员" value="tenant_admin" />
            <el-option label="操作员" value="operator" />
            <el-option label="观察者" value="viewer" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="userDlg = false">取消</el-button>
        <el-button type="primary" :disabled="!newUser.name || !newUser.password" @click="createUser">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../api'

const users = ref<any[]>([])
const groups = ref<any[]>([])
const allDevices = ref<any[]>([])
const groupDlg = ref(false)
const userDlg = ref(false)
const current = ref<any>(null)
const members = ref<any[]>([])
const addMemberId = ref<number | ''>('')
const selectedDevices = ref<number[]>([])
const newUser = reactive({ name: '', password: '', role: 'operator' })

function roleLabel(role: string) {
  return { platform_admin: '平台管理员', tenant_admin: '租户管理员', operator: '操作员', viewer: '观察者' }[role] ?? role
}

async function load() {
  users.value = await api.users()
  groups.value = await api.deviceGroups()
  const d = await api.devices({ size: 500 })
  allDevices.value = d.items
}

async function openGroup(row: any) {
  current.value = row
  const g = await api.deviceGroup(row.id)
  members.value = g.members ?? []
  selectedDevices.value = (g.device_ids ?? []).map(Number)
  groupDlg.value = true
}

async function addMember() {
  if (!addMemberId.value) return
  await api.addGroupMember(current.value.id, addMemberId.value)
  ElMessage.success('已加入')
  const g = await api.deviceGroup(current.value.id)
  members.value = g.members ?? []
  addMemberId.value = ''
  load()
}

async function removeMember(uid: number) {
  await api.removeGroupMember(current.value.id, uid)
  const g = await api.deviceGroup(current.value.id)
  members.value = g.members ?? []
  load()
}

async function saveDevices() {
  await api.setGroupDevices(current.value.id, selectedDevices.value)
  ElMessage.success('设备已保存')
  load()
}

async function removeGroup(id: number) {
  await api.deleteDeviceGroup(id)
  ElMessage.success('已删除')
  load()
}

async function createUser() {
  await api.createUser({ ...newUser, enabled: true })
  ElMessage.success('已创建（记得在租户管理中把该用户加入本租户）')
  userDlg.value = false
  Object.assign(newUser, { name: '', password: '', role: 'operator' })
  load()
}

onMounted(load)
</script>

<style scoped>
.page { padding: 16px; }
.head { display: flex; justify-content: space-between; align-items: center; }
</style>

<template>
  <div class="page">
    <el-tabs v-model="tab">
      <el-tab-pane label="用户" name="users">
        <div class="toolbar">
          <el-button type="primary" size="small" @click="userDlg = true">新增用户</el-button>
        </div>
        <el-table :data="users">
          <el-table-column prop="name" label="用户名" width="180" />
          <el-table-column label="角色" width="140">
            <template #default="{ row }">
              <el-tag :type="{ admin: 'danger', operator: 'warning', viewer: 'info' }[row.role]" effect="plain">{{ row.role }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="启用" width="100">
            <template #default="{ row }">
              <el-switch :model-value="row.enabled" @change="toggleUser(row, $event)" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="200">
            <template #default="{ row }">
              <el-button size="small" @click="editUser(row)">编辑/改密</el-button>
              <el-popconfirm title="删除该用户？" @confirm="removeUser(row.id)">
                <template #reference><el-button size="small" type="danger" plain>删除</el-button></template>
              </el-popconfirm>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="凭证" name="credentials">
        <div class="toolbar">
          <el-button type="primary" size="small" @click="credDlg = true">新增凭证</el-button>
          <span class="hint">密码/私钥加密存储（AES-GCM），SSH 连接配置可引用</span>
        </div>
        <el-table :data="credentials">
          <el-table-column prop="name" label="名称" width="220" />
          <el-table-column label="类型" width="120">
            <template #default="{ row }">
              <el-tag effect="plain">{{ row.type === 'key' ? '私钥' : '密码' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="120">
            <template #default="{ row }">
              <el-popconfirm title="删除该凭证？" @confirm="removeCred(row.id)">
                <template #reference><el-button size="small" type="danger" plain>删除</el-button></template>
              </el-popconfirm>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="审计日志" name="audit">
        <el-table :data="auditLogs" max-height="600">
          <el-table-column prop="ts" label="时间" width="200">
            <template #default="{ row }">{{ new Date(row.ts).toLocaleString() }}</template>
          </el-table-column>
          <el-table-column prop="user" label="用户" width="120" />
          <el-table-column prop="action" label="动作" width="160" />
          <el-table-column prop="target" label="目标" min-width="160" />
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="userDlg" title="用户" width="420px">
      <el-form label-width="80px">
        <el-form-item label="用户名"><el-input v-model="userForm.name" :disabled="!!userForm.id" /></el-form-item>
        <el-form-item label="角色">
          <el-select v-model="userForm.role" style="width:100%">
            <el-option label="管理员 admin" value="admin" />
            <el-option label="操作员 operator" value="operator" />
            <el-option label="观察者 viewer" value="viewer" />
          </el-select>
        </el-form-item>
        <el-form-item :label="userForm.id ? '新密码' : '密码'">
          <el-input v-model="userForm.password" type="password" show-password placeholder="留空=不修改" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="userDlg = false">取消</el-button>
        <el-button type="primary" @click="saveUser">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="credDlg" title="新增凭证" width="420px">
      <el-form label-width="80px">
        <el-form-item label="名称"><el-input v-model="credForm.name" placeholder="如：树莓派-root" /></el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="credForm.type">
            <el-radio value="password">密码</el-radio>
            <el-radio value="key">私钥</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="内容">
          <el-input v-model="credForm.secret" type="textarea" :rows="4" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="credDlg = false">取消</el-button>
        <el-button type="primary" @click="saveCred">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api, type CredentialItem, type UserItem } from '../api'

const tab = ref('users')
const users = ref<UserItem[]>([])
const credentials = ref<CredentialItem[]>([])
const auditLogs = ref<any[]>([])

const userDlg = ref(false)
const userForm = reactive({ id: 0, name: '', role: 'viewer', password: '', enabled: true })
const credDlg = ref(false)
const credForm = reactive({ name: '', type: 'password', secret: '' })

async function load() {
  users.value = await api.users()
  credentials.value = await api.credentials()
  auditLogs.value = await api.audit()
}
function editUser(row: UserItem) {
  Object.assign(userForm, { ...row, password: '' })
  userDlg.value = true
}
async function saveUser() {
  try {
    if (userForm.id) await api.updateUser(userForm.id, userForm)
    else await api.createUser(userForm)
    ElMessage.success('已保存')
    userDlg.value = false
    userForm.id = 0; userForm.name = ''; userForm.password = ''
    load()
  } catch (e: any) { ElMessage.error(e.response?.data?.detail ?? '保存失败') }
}
async function toggleUser(row: UserItem, val: boolean) {
  await api.updateUser(row.id, { name: row.name, role: row.role, enabled: val })
  row.enabled = val
}
async function removeUser(id: number) { await api.deleteUser(id); load() }

async function saveCred() {
  try {
    await api.createCredential(credForm)
    ElMessage.success('已保存')
    credDlg.value = false
    credForm.name = ''; credForm.secret = ''
    load()
  } catch (e: any) { ElMessage.error(e.response?.data?.detail ?? '保存失败') }
}
async function removeCred(id: number) { await api.deleteCredential(id); load() }

onMounted(load)
</script>

<style scoped>
.hint { color: var(--el-text-color-secondary); font-size: 12px; }
</style>

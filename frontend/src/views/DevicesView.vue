<template>
  <div class="page">
    <div class="toolbar">
      <el-input v-model="keyword" placeholder="搜索名称 / 描述" clearable style="width:240px"
                @change="load" @clear="load">
        <template #prefix><el-icon><Search /></el-icon></template>
      </el-input>
      <el-button type="primary" @click="createVisible = true">新建设备</el-button>
      <el-button @click="$router.push('/templates')">从模板创建</el-button>
    </div>

    <el-card shadow="never">
      <el-table :data="items" v-loading="loading">
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <StatusBadge :status="row.online ? 'online' : 'offline'" />
          </template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="140">
          <template #default="{ row }">
            <el-link type="primary" @click="$router.push(`/devices/${row.id}`)">{{ row.name }}</el-link>
          </template>
        </el-table-column>
        <el-table-column label="标签" min-width="140">
          <template #default="{ row }">
            <el-tag v-for="t in row.tags" :key="t" size="small" style="margin-right:4px">{{ t }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="location" label="位置" width="120" />
        <el-table-column prop="description" label="描述" min-width="180" show-overflow-tooltip />
        <el-table-column label="操作" width="160">
          <template #default="{ row }">
            <el-button size="small" @click="$router.push(`/devices/${row.id}`)">详情</el-button>
            <el-popconfirm title="确定删除该设备及其全部连接配置？" @confirm="remove(row.id)">
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
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import { api, type Device } from '../api'
import { useEvents } from '../api/events'
import StatusBadge from '../components/StatusBadge.vue'

const items = ref<Device[]>([])
const total = ref(0)
const page = ref(1)
const size = 20
const keyword = ref('')
const loading = ref(false)

const createVisible = ref(false)
const form = reactive({ name: '', description: '', location: '', owner: '', tags: [] as string[] })

async function load() {
  loading.value = true
  try {
    const r = await api.devices({ keyword: keyword.value || undefined, page: page.value, size })
    items.value = r.items
    total.value = r.total
  } finally { loading.value = false }
}
async function create() {
  await api.createDevice(form)
  ElMessage.success('创建成功')
  createVisible.value = false
  form.name = ''; form.description = ''; form.location = ''; form.owner = ''; form.tags = []
  load()
}
async function remove(id: number) {
  await api.deleteDevice(id)
  ElMessage.success('已删除')
  load()
}

useEvents(e => { if (e.event === 'session_status') load() })  // 在线徽标实时刷新
onMounted(load)
</script>

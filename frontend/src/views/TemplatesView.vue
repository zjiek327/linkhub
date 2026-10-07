<template>
  <div class="page">
    <el-row :gutter="16">
      <el-col :span="8" v-for="tpl in templates" :key="tpl.key" style="margin-bottom:16px">
        <el-card shadow="hover" class="tpl-card">
          <div class="head">
            <span class="icon">{{ tpl.icon }}</span>
            <div>
              <div class="name">{{ tpl.name }}</div>
              <el-tag size="small" effect="plain">{{ tpl.category }}</el-tag>
              <el-tag v-if="tpl.builtin" size="small" type="warning" effect="plain" style="margin-left:4px">内置</el-tag>
            </div>
          </div>
          <div class="desc">{{ tpl.description }}</div>
          <div class="conns">
            <el-tag v-for="(c, i) in tpl.default_connections" :key="i" size="small" type="info" effect="plain">
              {{ c.name || c.kind }} · {{ c.params?.baudrate ?? '' }}
            </el-tag>
          </div>
          <el-button type="primary" style="width:100%" @click="useTemplate(tpl)">用模板创建设备</el-button>
        </el-card>
      </el-col>
    </el-row>

    <el-dialog v-model="dialogVisible" :title="`用模板创建：${current?.name ?? ''}`" width="520px">
      <el-alert v-if="notes.length" type="warning" :closable="false" style="margin-bottom:14px">
        <ul style="margin:0;padding-left:18px">
          <li v-for="(n, i) in notes" :key="i">{{ n }}</li>
        </ul>
      </el-alert>
      <el-form label-width="110px">
        <el-form-item label="设备名称" required><el-input v-model="createName" placeholder="如：机柜A-树莓派01" /></el-form-item>
        <template v-for="(c, i) in current?.default_connections ?? []" :key="i">
          <el-form-item v-if="c.kind === 'serial'" :label="`${c.name} 端口`">
            <el-select v-model="portOverrides[i]" filterable allow-create style="width:100%">
              <el-option v-for="p in ports" :key="p.device" :label="`${p.device} · ${p.description}`" :value="p.device" />
            </el-select>
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :disabled="!createName" @click="create">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api, type SerialPort, type Template } from '../api'

const router = useRouter()
const templates = ref<Template[]>([])
const ports = ref<SerialPort[]>([])
const dialogVisible = ref(false)
const current = ref<Template | null>(null)
const createName = ref('')
const portOverrides = ref<Record<number, string>>({})

const notes = computed<string[]>(() => current.value?.spec?.notes ?? [])

function useTemplate(tpl: Template) {
  current.value = tpl
  createName.value = ''
  portOverrides.value = {}
  dialogVisible.value = true
}

async function create() {
  if (!current.value) return
  const overrides: Record<number, any> = {}
  for (const [i, port] of Object.entries(portOverrides.value)) {
    if (port) overrides[Number(i)] = { port }
  }
  const dev = await api.fromTemplate(current.value.key, { name: createName.value, param_overrides: overrides })
  ElMessage.success(`设备「${dev.name}」已创建`)
  dialogVisible.value = false
  router.push(`/devices/${dev.id}`)
}

onMounted(async () => {
  templates.value = await api.templates()
  try { ports.value = await api.serialPorts() } catch { /* 无串口环境 */ }
})
</script>

<style scoped>
.tpl-card .head { display: flex; gap: 10px; align-items: center; margin-bottom: 10px; }
.tpl-card .icon { font-size: 34px; }
.tpl-card .name { font-weight: 600; font-size: 15px; margin-bottom: 4px; }
.tpl-card .desc { color: #606266; font-size: 12px; height: 72px; overflow: hidden; white-space: pre-line; margin-bottom: 10px; }
.tpl-card .conns { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 12px; }
</style>

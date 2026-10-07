<template>
  <!-- 按连接器 JSON Schema 动态渲染参数表单 -->
  <el-form label-width="110px" size="default">
    <el-form-item v-for="(prop, key) in properties" :key="key" :label="prop.title || key">
      <el-select v-if="prop.enum" v-model="model[key]" style="width: 100%">
        <el-option v-for="opt in prop.enum" :key="String(opt)" :label="String(opt)" :value="opt" />
      </el-select>
      <el-switch v-else-if="prop.type === 'boolean'" v-model="model[key]" />
      <el-input-number v-else-if="prop.type === 'integer'" v-model="model[key]" :min="0" style="width: 100%" />
      <el-select v-else-if="key === 'port'" v-model="model[key]" filterable allow-create
                 placeholder="选择或输入串口" style="width: 100%">
        <el-option v-for="p in ports" :key="(p.node_id || 'local') + p.device"
                   :label="p.node ? `${p.node} · ${p.device} · ${p.description}` : `${p.device} · ${p.description}`"
                   :value="p.device" />
      </el-select>
      <el-input v-else v-model="model[key]" :placeholder="prop.description || ''" />
      <div v-if="prop.description && key !== 'port'" class="desc">{{ prop.description }}</div>
    </el-form-item>
  </el-form>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, watch } from 'vue'
import { api, type SerialPort } from '../api'

const props = defineProps<{ schema: any; modelValue: Record<string, any> }>()
const emit = defineEmits<{ 'update:modelValue': [v: Record<string, any>] }>()

const model = reactive<Record<string, any>>({})
const ports = ref<SerialPort[]>([])

const properties = computed(() => props.schema?.properties ?? {})

// 初始化：用 schema 默认值 + 传入值
watch(() => props.schema, (s) => {
  for (const [k, p] of Object.entries<any>(s?.properties ?? {})) {
    if (!(k in model)) model[k] = props.modelValue?.[k] ?? p.default ?? (p.type === 'integer' ? 115200 : p.type === 'boolean' ? false : '')
  }
}, { immediate: true })
watch(() => props.modelValue, v => Object.assign(model, v ?? {}))
watch(model, v => emit('update:modelValue', { ...v }), { deep: true })

onMounted(async () => {
  try { ports.value = await api.serialPorts() } catch { /* 无串口环境 */ }
})
</script>

<style scoped>
.desc { font-size: 12px; color: #909399; line-height: 1.4; }
</style>

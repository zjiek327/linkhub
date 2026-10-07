<template>
  <el-tag :type="tagType" effect="dark" size="small" round>
    <span class="dot" />{{ label }}
  </el-tag>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{ status: string }>(), { status: 'offline' })

const label = computed(() => ({
  online: '在线', connecting: '连接中', closed: '已关闭', error: '异常', offline: '离线',
}[props.status] ?? props.status))

const tagType = computed(() => ({
  online: 'success', connecting: 'warning', closed: 'info', error: 'danger', offline: 'info',
}[props.status] ?? 'info'))
</script>

<style scoped>
.dot { display: inline-block; width: 6px; height: 6px; border-radius: 50%; background: currentColor; margin-right: 4px; }
</style>

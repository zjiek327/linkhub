<template>
  <div class="page">
    <el-row :gutter="16">
      <!-- 定时任务 -->
      <el-col :span="12">
        <el-card shadow="never">
          <template #header>
            <div style="display:flex;justify-content:space-between;align-items:center">
              <span>⏰ 定时任务</span>
              <el-button type="primary" size="small" @click="taskDlg = true">新建任务</el-button>
            </div>
          </template>
          <el-table :data="tasks" empty-text="暂无任务">
            <el-table-column prop="name" label="名称" width="140" />
            <el-table-column prop="command" label="命令" min-width="140" show-overflow-tooltip />
            <el-table-column label="间隔" width="100">
              <template #default="{ row }">{{ row.interval_s >= 3600 ? (row.interval_s/3600)+'h' : (row.interval_s/60)+'min' }}</template>
            </el-table-column>
            <el-table-column label="上次" width="100">
              <template #default="{ row }">
                <el-tag v-if="row.history?.length" size="small"
                        :type="row.history.at(-1).ok ? 'success' : 'danger'" effect="plain">
                  {{ row.history.at(-1).ok ? '成功' : '失败' }}
                </el-tag>
                <span v-else>—</span>
              </template>
            </el-table-column>
            <el-table-column label="启用" width="80">
              <template #default="{ row }">
                <el-switch :model-value="row.enabled" @change="toggleTask(row, $event)" />
              </template>
            </el-table-column>
            <el-table-column label="操作" width="120">
              <template #default="{ row }">
                <el-button size="small" @click="viewHistory(row)">历史</el-button>
                <el-popconfirm title="删除该任务？" @confirm="removeTask(row.id)">
                  <template #reference><el-button size="small" type="danger" plain>删</el-button></template>
                </el-popconfirm>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>

      <!-- Playbook -->
      <el-col :span="12">
        <el-card shadow="never">
          <template #header>📜 脚本编排（多步骤）</template>
          <el-form label-width="90px">
            <el-form-item label="名称"><el-input v-model="pb.name" placeholder="如：上线巡检" /></el-form-item>
          </el-form>
          <div v-for="(s, i) in pb.steps" :key="i" class="step">
            <div class="step-head">步骤 {{ i+1 }}
              <el-button text size="small" type="danger" @click="pb.steps.splice(i,1)">删除</el-button>
            </div>
            <el-input v-model="s.command" placeholder="命令，如 uname -a" style="margin-bottom:8px" />
            <div style="display:flex;gap:10px;align-items:center">
              等待 <el-input-number v-model="s.wait_ms" :min="100" :max="60000" :step="500" size="small" /> ms
              <el-switch v-model="s.parallel" active-text="并行" inactive-text="逐台" size="small" />
              <span class="targets">{{ s.targets.length }} 台设备</span>
              <el-button size="small" @click="pickTargets(s)">选设备</el-button>
            </div>
          </div>
          <el-button style="width:100%" @click="pb.steps.push({ command: '', wait_ms: 1500, parallel: true, targets: [] })">+ 添加步骤</el-button>
          <el-button type="primary" style="width:100%;margin:10px 0 0" :loading="pbRunning"
                     :disabled="!pb.steps.length" @click="runPb">▶ 执行</el-button>

          <div v-if="pbResult" class="pb-result">
            <el-alert :type="pbResult.ok ? 'success' : 'error'" :closable="false">
              {{ pbResult.name }}：{{ pbResult.ok ? '全部成功' : '存在失败（后续步骤已跳过）' }}
            </el-alert>
            <div v-for="(st, i) in pbResult.steps" :key="i" class="step">
              <b>步骤 {{ st.step + 1 }}</b>
              <pre class="out" v-for="(r, j) in st.results" :key="j">{{ r.device_name || r.device_id }}: {{ r.ok ? r.output : r.error }}</pre>
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-dialog v-model="taskDlg" title="新建定时任务" width="480px">
      <el-form label-width="90px">
        <el-form-item label="名称"><el-input v-model="taskForm.name" /></el-form-item>
        <el-form-item label="命令"><el-input v-model="taskForm.command" placeholder="uname -a" /></el-form-item>
        <el-form-item label="间隔(分钟)">
          <el-input-number v-model="taskForm.interval_min" :min="1" />
        </el-form-item>
        <el-form-item label="目标设备">
          <el-button size="small" @click="pickTargets(taskForm)">{{ taskForm.targets.length }} 台已选，点击修改</el-button>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="taskDlg = false">取消</el-button>
        <el-button type="primary" @click="saveTask">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="pickerDlg" title="选择目标设备" width="560px">
      <el-table :data="allDevices" @selection-change="pickerSel = $event" max-height="400">
        <el-table-column type="selection" width="40" />
        <el-table-column prop="name" label="设备" />
        <el-table-column label="节点" width="120">
          <template #default="{ row }">{{ row.node_name || '本机' }}</template>
        </el-table-column>
      </el-table>
      <template #footer>
        <el-button @click="pickerDlg = false">取消</el-button>
        <el-button type="primary" @click="confirmPick">确定（{{ pickerSel.length }}）</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="historyDlg" :title="`任务历史：${historyTask?.name ?? ''}`" width="640px">
      <el-table :data="historyTask?.history ?? []" max-height="420">
        <el-table-column label="时间" width="180">
          <template #default="{ row }">{{ new Date(row.ts).toLocaleString() }}</template>
        </el-table-column>
        <el-table-column label="结果" width="80">
          <template #default="{ row }">
            <el-tag size="small" :type="row.ok ? 'success' : 'danger'">{{ row.ok ? '成功' : '失败' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="输出" min-width="240">
          <template #default="{ row }">
            <pre class="out">{{ (row.results ?? []).map((r: any) => `${r.device_name}: ${r.ok ? r.output : r.error}`).join('\n') }}</pre>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api, type Device } from '../api'

const tasks = ref<any[]>([])
const taskDlg = ref(false)
const taskForm = reactive<any>({ name: '', command: '', interval_min: 5, targets: [] })
const historyDlg = ref(false)
const historyTask = ref<any>(null)

const pb = reactive<any>({ name: '', steps: [] })
const pbRunning = ref(false)
const pbResult = ref<any>(null)

const pickerDlg = ref(false)
const pickerSel = ref<Device[]>([])
const allDevices = ref<Device[]>([])
let pickerCb: ((t: any[]) => void) | null = null

function pickTargets(target: any) {
  pickerCb = (sel) => { target.targets = sel.map(d => ({ node_id: d.node_id === 'local' ? 'local' : d.node_id, device_id: d.id })) }
  pickerDlg.value = true
}
function confirmPick() { pickerCb?.(pickerSel.value); pickerDlg.value = false }

async function load() {
  tasks.value = await api.tasks()
  const r = await api.devices({ size: 200 })
  allDevices.value = r.items
}
async function saveTask() {
  try {
    await api.createTask({ ...taskForm, interval_s: taskForm.interval_min * 60 })
    ElMessage.success('任务已创建')
    taskDlg.value = false
    taskForm.name = ''; taskForm.command = ''; taskForm.targets = []
    load()
  } catch (e: any) { ElMessage.error(e.response?.data?.detail ?? '创建失败') }
}
async function toggleTask(row: any, val: boolean) {
  await api.updateTask(row.id, { enabled: val }); row.enabled = val; load()
}
async function removeTask(id: number) { await api.deleteTask(id); load() }
function viewHistory(row: any) { historyTask.value = row; historyDlg.value = true }

async function runPb() {
  pbRunning.value = true; pbResult.value = null
  try {
    pbResult.value = await api.playbookRun(pb)
  } catch (e: any) { ElMessage.error(e.response?.data?.detail ?? '执行失败') }
  finally { pbRunning.value = false }
}

onMounted(load)
</script>

<style scoped>
.step { border: 1px solid var(--el-border-color); border-radius: 6px; padding: 10px; margin-bottom: 10px; }
.step-head { display: flex; justify-content: space-between; font-weight: 600; margin-bottom: 8px; }
.targets { color: var(--el-text-color-secondary); font-size: 12px; }
.out { margin: 4px 0; font-size: 12px; white-space: pre-wrap; word-break: break-all; background: var(--el-fill-color-light); padding: 6px; border-radius: 4px; }
.pb-result { margin-top: 14px; }
</style>

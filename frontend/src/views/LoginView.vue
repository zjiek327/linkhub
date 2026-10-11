<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="oct">🐙</div>
      <h2>灵枢 LinkHub</h2>
      <p class="sub">连连 · 设备连接平台</p>
      <el-form @submit.prevent="login">
        <el-form-item>
          <el-input v-model="name" :placeholder="$t('login.username')" size="large" autofocus>
            <template #prefix><el-icon><User /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-form-item>
          <el-input v-model="password" type="password" :placeholder="$t('login.password')" size="large" show-password
                    @keyup.enter="login">
            <template #prefix><el-icon><Lock /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-form-item v-if="tenants.length > 1">
          <el-select v-model="tenantCode" size="large" style="width:100%">
            <template #prefix><el-icon><OfficeBuilding /></el-icon></template>
            <el-option v-for="t in tenants" :key="t.code" :label="`${t.name}（${roleLabel(t.role)}）`" :value="t.code" />
          </el-select>
        </el-form-item>
        <el-button type="primary" size="large" style="width:100%" :loading="loading" @click="login">
          登 录
        </el-button>
      </el-form>
      <el-alert v-if="firstRun" type="warning" :closable="false" style="margin-top:12px">
        {{ $t('login.firstRunTip') }}
      </el-alert>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock, OfficeBuilding } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { api, setToken } from '../api'

const { t: $t } = useI18n()
const router = useRouter()
const name = ref('')
const password = ref('')
const loading = ref(false)
// 多租户：账号密码验证后拉取可用租户；单租户/平台管理员无选择框直接进
const tenants = ref<{ tenant_id: number; name: string; code: string; role: string }[]>([])
const tenantCode = ref('')
const firstRun = !localStorage.getItem('linkhub_token')
// 进入登录页即清掉可能失效的旧 token（比如升级前残留的非 JWT 值）
setToken('')

async function login() {
  if (!name.value || !password.value) return
  loading.value = true
  try {
    // 第一步（首次）：验证账号密码并拉取可用租户
    if (!tenants.value.length) {
      const tr = await api.loginTenants(name.value, password.value)
      tenants.value = tr.tenants
      if (tenants.value.length > 1) {
        tenantCode.value = tenants.value[0].code
        loading.value = false
        return // 展示租户选择，用户二次点击登录
      }
    }
    // 第二步：带租户登录（单租户默认直进）
    const code = tenantCode.value || tenants.value[0]?.code || ''
    const r = await api.login(name.value, password.value, code)
    setToken(r.token)
    localStorage.setItem('linkhub_tenant', code)
    ElMessage.success(`${$t('login.welcome')}，${r.user.name}`)
    router.push('/')
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail ?? '登录失败')
  } finally {
    loading.value = false
  }
}

function roleLabel(role: string) {
  return { platform_admin: '平台管理员', tenant_admin: '管理员', operator: '操作员', viewer: '观察者' }[role] ?? role
}
</script>

<style scoped>
.login-wrap { height: 100vh; display: flex; align-items: center; justify-content: center;
  background: linear-gradient(135deg, #0a1929 0%, #1d3a2a 100%); }
.login-card { width: 360px; padding: 36px 32px; background: #fff; border-radius: 14px;
  box-shadow: 0 12px 40px rgba(0,0,0,.35); text-align: center; }
html.dark .login-card { background: #1d1d1d; }
.oct { font-size: 52px; }
.sub { color: #909399; margin: 4px 0 22px; }
</style>

<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="oct">🐙</div>
      <h2>灵枢 LinkHub</h2>
      <p class="sub">连连 · 设备连接平台</p>
      <el-form @submit.prevent="login">
        <el-form-item>
          <el-input v-model="name" placeholder="用户名" size="large" autofocus>
            <template #prefix><el-icon><User /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-form-item>
          <el-input v-model="password" type="password" placeholder="密码" size="large" show-password
                    @keyup.enter="login">
            <template #prefix><el-icon><Lock /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-button type="primary" size="large" style="width:100%" :loading="loading" @click="login">
          登 录
        </el-button>
      </el-form>
      <el-alert v-if="firstRun" type="warning" :closable="false" style="margin-top:12px">
        初始账号 admin / admin，登录后请立即修改密码
      </el-alert>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock } from '@element-plus/icons-vue'
import { api, setToken } from '../api'

const router = useRouter()
const name = ref('')
const password = ref('')
const loading = ref(false)
const firstRun = !localStorage.getItem('linkhub_token')
// 进入登录页即清掉可能失效的旧 token（比如升级前残留的非 JWT 值）
setToken('')

async function login() {
  if (!name.value || !password.value) return
  loading.value = true
  try {
    const r = await api.login(name.value, password.value)
    setToken(r.token)
    ElMessage.success(`欢迎，${r.user.name}`)
    router.push('/')
  } catch (e: any) {
    ElMessage.error(e.response?.data?.detail ?? '登录失败')
  } finally {
    loading.value = false
  }
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

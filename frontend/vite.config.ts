import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 后端地址：默认本机 8000；linkhub.sh 会通过 LINKHUB_PORT/LINKHUB_BACKEND 注入
const backend = process.env.LINKHUB_BACKEND
  ?? `http://127.0.0.1:${process.env.LINKHUB_PORT ?? 8000}`

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': { target: backend, changeOrigin: true },
      '/ws': { target: backend.replace(/^http/, 'ws'), ws: true },
    },
  },
})

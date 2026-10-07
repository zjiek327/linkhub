import { onUnmounted, ref } from 'vue'
import { wsUrl } from '../api'

export interface HubEvent { event: string; data: any }

/** 订阅全局事件总线（session_status 等），组件卸载自动断开。 */
export function useEvents(onEvent: (e: HubEvent) => void) {
  const connected = ref(false)
  let ws: WebSocket | null = null
  let timer: number | undefined

  function connect() {
    ws = new WebSocket(wsUrl('/ws/events'))
    ws.onopen = () => (connected.value = true)
    ws.onmessage = ev => {
      try { onEvent(JSON.parse(ev.data)) } catch { /* 忽略坏帧 */ }
    }
    ws.onclose = () => {
      connected.value = false
      timer = window.setTimeout(connect, 2000)  // 断线重连
    }
  }
  connect()
  onUnmounted(() => { clearTimeout(timer); ws?.close() })
  return { connected }
}

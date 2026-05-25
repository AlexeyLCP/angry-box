import { ref, watch, onUnmounted } from 'vue'

export interface LogEntry {
  step?: string
  server?: string
  detail?: string
  status?: string
  error?: string
}

export function useWebSocket() {
  const connected = ref(false)
  const lastMessage = ref<LogEntry | null>(null)
  const messages = ref<LogEntry[]>([])
  let ws: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null

  const connect = (chainId: string) => {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${proto}//${location.host}/api/v1/ws?chain_id=${chainId}`

    const doConnect = () => {
      ws = new WebSocket(url)

      ws.onopen = () => {
        connected.value = true
      }

      ws.onmessage = (e) => {
        try {
          const entry: LogEntry = JSON.parse(e.data)
          lastMessage.value = entry
          messages.value = [...messages.value.slice(-99), entry]
        } catch {
          // ignore
        }
      }

      ws.onclose = () => {
        connected.value = false
        // Reconnect after 3s
        reconnectTimer = setTimeout(doConnect, 3000)
      }

      ws.onerror = () => {
        ws?.close()
      }
    }

    doConnect()
  }

  const disconnect = () => {
    if (reconnectTimer) clearTimeout(reconnectTimer)
    ws?.close()
    ws = null
    connected.value = false
  }

  onUnmounted(disconnect)

  return { connected, lastMessage, messages, connect, disconnect }
}

import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useApi } from '@/composables/useApi'
import type { StatusInfo, LogEntry } from '@/types/server'

export const useStatusStore = defineStore('status', () => {
  const info = ref<StatusInfo | null>(null)
  const logs = ref<LogEntry[]>([])
  const loading = ref(false)

  const fetchStatus = async () => {
    const api = useApi()
    try {
      info.value = await api.get<StatusInfo>('/api/v1/status')
    } catch {
      info.value = null
    }
  }

  const fetchLogs = async () => {
    loading.value = true
    const api = useApi()
    try {
      logs.value = await api.get<LogEntry[]>('/api/v1/logs')
    } finally {
      loading.value = false
    }
  }

  return { info, logs, loading, fetchStatus, fetchLogs }
})

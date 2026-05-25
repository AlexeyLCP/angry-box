import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { serversApi } from '@/api/client'
import type { Server, HealthStatus } from '@/types/server'

export const useServersStore = defineStore('servers', () => {
  const servers = ref<Server[]>([])
  const health = ref<Record<string, HealthStatus>>({})
  const loading = ref(false)

  const onlineCount = computed(
    () => servers.value.filter((s) => s.status === 'online').length,
  )

  const fetchAll = async () => {
    loading.value = true
    try {
      servers.value = await serversApi.list()
    } finally {
      loading.value = false
    }
  }

  const fetchHealth = async (id: string) => {
    try {
      const h = await serversApi.health(id)
      health.value[id] = h
      return h
    } catch {
      health.value[id] = {
        online: false,
        latency_ms: 0,
        xray_running: false,
        xray_version: '',
        error: 'unreachable',
      }
      return health.value[id]
    }
  }

  const addServer = async (data: Partial<Server>) => {
    await serversApi.create(data)
    await fetchAll()
  }

  const removeServer = async (id: string) => {
    await serversApi.delete(id)
    servers.value = servers.value.filter((s) => s.id !== id)
  }

  const getServer = (id: string) =>
    servers.value.find((s) => s.id === id) ?? null

  return {
    servers,
    health,
    loading,
    onlineCount,
    fetchAll,
    fetchHealth,
    addServer,
    removeServer,
    getServer,
  }
})

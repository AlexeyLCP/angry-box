<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useServersStore } from '@/stores/servers'
import { useChainsStore } from '@/stores/chains'
import { useToast } from 'primevue/usetoast'
import type { Server, HealthStatus } from '@/types/server'
import type { Chain } from '@/types/chain'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Skeleton from 'primevue/skeleton'

const route = useRoute()
const serverStore = useServersStore()
const chainsStore = useChainsStore()
const toast = useToast()

const id = route.params.id as string
const server = ref<Server | null>(null)
const health = ref<HealthStatus | null>(null)
const healthLoading = ref(false)

onMounted(async () => {
  await serverStore.fetchAll()
  server.value = serverStore.getServer(id)
  await checkHealth()
  await chainsStore.fetchAll()
})

const checkHealth = async () => {
  healthLoading.value = true
  try {
    health.value = await serverStore.fetchHealth(id)
  } catch {
    health.value = null
  } finally {
    healthLoading.value = false
  }
}

const chainsForServer = () =>
  chainsStore.chains.filter(
    (c) => c.nodes?.some((n) => n.server_id === id),
  )

const scan = async () => {
  try {
    const result = await (await import('@/api/client')).serversApi.scan(id)
    toast.add({ severity: 'info', summary: 'Scan', detail: JSON.stringify(result), life: 5000 })
  } catch (e) {
    toast.add({ severity: 'error', summary: 'Scan failed', detail: String(e), life: 5000 })
  }
}

const install = async () => {
  try {
    const result = await (await import('@/api/client')).serversApi.install(id)
    toast.add({ severity: 'success', summary: 'Installed', detail: result.status, life: 3000 })
  } catch (e) {
    toast.add({ severity: 'error', summary: 'Install failed', detail: String(e), life: 5000 })
  }
}
</script>

<template>
  <div v-if="server">
    <div class="page-header">
      <div>
        <h2>{{ server.name }}</h2>
        <p class="text-muted">{{ server.host }}:{{ server.port }} · {{ server.username }}</p>
      </div>
      <div :class="['status-dot', server.status === 'online' ? 'online' : 'offline']">
        {{ server.status.toUpperCase() }}
      </div>
    </div>

    <div class="grid-2">
      <!-- Connection info -->
      <Card>
        <template #content>
          <h4>Connection</h4>
          <div class="kv"><span>Host</span><strong>{{ server.host }}</strong></div>
          <div class="kv"><span>SSH Port</span><strong>{{ server.port }}</strong></div>
          <div class="kv"><span>User</span><strong>{{ server.username }}</strong></div>
          <div class="kv"><span>Auth</span><strong>{{ server.auth_method }}</strong></div>
        </template>
      </Card>

      <!-- Health -->
      <Card>
        <template #content>
          <div class="card-header-row">
            <h4>Health</h4>
            <Button icon="pi pi-refresh" text rounded size="small" :loading="healthLoading" @click="checkHealth" />
          </div>
          <div v-if="healthLoading" class="skeleton-stack">
            <Skeleton height="18px" />
            <Skeleton height="18px" width="80%" />
            <Skeleton height="18px" width="60%" />
          </div>
          <div v-else-if="health">
            <div class="kv">
              <span>SSH</span>
              <strong :style="{ color: health.online ? 'var(--p-green-500)' : 'var(--p-red-500)' }">
                {{ health.online ? `Connected (${health.latency_ms}ms)` : 'Unreachable' }}
              </strong>
            </div>
            <div class="kv">
              <span>Xray</span>
              <strong :style="{ color: health.xray_running ? 'var(--p-green-500)' : 'var(--p-red-500)' }">
                {{ health.xray_running ? `Running ${health.xray_version}` : 'Stopped' }}
              </strong>
            </div>
            <div v-if="health.error" class="kv">
              <span>Error</span><strong style="color:var(--p-red-400)">{{ health.error }}</strong>
            </div>
          </div>
          <p v-else class="text-muted">No health data</p>
        </template>
      </Card>
    </div>

    <!-- System info -->
    <Card style="margin-top:16px">
      <template #content>
        <h4>System</h4>
        <div class="kv-row">
          <div class="kv"><span>OS</span><strong>{{ server.os || 'Unknown' }}</strong></div>
          <div class="kv"><span>Arch</span><strong>{{ server.arch || 'Unknown' }}</strong></div>
          <div class="kv"><span>Source</span><strong>{{ server.source }}</strong></div>
          <div class="kv"><span>Created</span><strong>{{ new Date(server.created_at).toLocaleString() }}</strong></div>
          <div class="kv" v-if="server.last_seen"><span>Last Seen</span><strong>{{ new Date(server.last_seen).toLocaleString() }}</strong></div>
          <div class="kv" v-if="server.tags?.length"><span>Tags</span><strong>{{ server.tags.join(', ') }}</strong></div>
        </div>
      </template>
    </Card>

    <!-- Actions -->
    <Card style="margin-top:16px">
      <template #content>
        <h4>Actions</h4>
        <div class="action-btns">
          <Button label="Scan" icon="pi pi-radar" outlined @click="scan" />
          <Button label="Install Xray" icon="pi pi-download" @click="install" />
          <Button label="Import Config" icon="pi pi-file-import" outlined />
          <Button label="Test Connection" icon="pi pi-plug" outlined @click="checkHealth" />
        </div>
      </template>
    </Card>

    <!-- Related chains -->
    <Card style="margin-top:16px" v-if="chainsForServer().length > 0">
      <template #content>
        <h4>Chains on this server ({{ chainsForServer().length }})</h4>
        <div class="chain-chips">
          <span
            v-for="c in chainsForServer()"
            :key="c.id"
            class="chain-chip"
          >
            <Tag :value="c.status" :severity="c.status === 'active' ? 'success' : 'info'" :rounded="true" />
            <span>{{ c.name }}</span>
          </span>
        </div>
      </template>
    </Card>
  </div>

  <div v-else class="text-muted" style="text-align:center;padding:60px">
    Loading...
  </div>
</template>

<style scoped>
.page-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 22px; }
.muted { color: var(--p-text-muted-color); font-size: 12px; }

.status-dot {
  padding: 4px 14px;
  border-radius: 20px;
  font-size: 11px;
  font-weight: 800;
}
.status-dot.online { background: rgba(34,197,94,0.15); color: var(--p-green-400); }
.status-dot.offline { background: rgba(239,68,68,0.15); color: var(--p-red-400); }

.grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }

h4 { margin: 0 0 10px; font-size: 14px; }
.card-header-row { display: flex; align-items: center; justify-content: space-between; }
.card-header-row h4 { margin: 0; }

.kv { display: flex; justify-content: space-between; align-items: center; padding: 4px 0; font-size: 13px; }
.kv span { color: var(--p-text-muted-color); }

.kv-row { display: flex; flex-direction: column; gap: 2px; }

.skeleton-stack { display: flex; flex-direction: column; gap: 8px; }

.action-btns { display: flex; gap: 8px; flex-wrap: wrap; }

.chain-chips { display: flex; gap: 8px; flex-wrap: wrap; }
.chain-chip { display: flex; align-items: center; gap: 6px; font-size: 13px; }
</style>

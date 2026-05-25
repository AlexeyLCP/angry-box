<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useServersStore } from '@/stores/servers'
import { useChainsStore } from '@/stores/chains'
import { useBuilderStore } from '@/stores/builder'
import { serversApi } from '@/api/client'
import TopologyMap from '@/components/topology/TopologyMap.vue'
import Button from 'primevue/button'
import Select from 'primevue/select'
import InputText from 'primevue/inputtext'
import InputNumber from 'primevue/inputnumber'

const router = useRouter()
const servers = useServersStore()
const chainsStore = useChainsStore()
const builder = useBuilderStore()
const selectedServerId = ref<string | null>(null)

// Quick Client Connection form state
const quickPort = ref(443)
const quickSecurity = ref('reality')
const quickSNI = ref('discord.com')
const quickFingerprint = ref('chrome')
const quickTransport = ref('xhttp')
const quickHost = ref('discord.com')
const quickPath = ref('/download')
const quickMode = ref('packet-up')
const quickPrivKey = ref('')
const quickPubKey = ref('')
const quickGenerating = ref(false)

let interval: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
  await Promise.all([servers.fetchAll(), chainsStore.fetchAll()])
  interval = setInterval(() => {
    servers.servers.forEach((s) => servers.fetchHealth(s.id))
  }, 30_000)
  servers.servers.forEach((s) => servers.fetchHealth(s.id))
})

onUnmounted(() => {
  if (interval) clearInterval(interval)
})

const onlineCount = computed(() =>
  Object.values(servers.health).filter((h) => h.online).length
)

const activeChainsCount = computed(() =>
  chainsStore.chains.filter((c) => c.status === 'active').length
)

const selectedServer = computed(() =>
  selectedServerId.value
    ? servers.servers.find((s) => s.id === selectedServerId.value) ?? null
    : null
)

const selectedHealth = computed(() =>
  selectedServerId.value ? servers.health[selectedServerId.value] : null
)

const onSelectServer = (serverId: string) => {
  selectedServerId.value = serverId
}

const closePanel = () => {
  selectedServerId.value = null
}

const generateQuickKeys = async () => {
  if (!selectedServerId.value) return
  quickGenerating.value = true
  try {
    const res = await serversApi.generateKeys(selectedServerId.value)
    quickPrivKey.value = res.private_key
    quickPubKey.value = res.public_key
  } catch { /* toast in parent */ }
  finally { quickGenerating.value = false }
}

const startBuildingChain = () => {
  if (!selectedServer.value) return
  builder.reset()
  builder.addNode(selectedServer.value)
  // Apply quick settings to the new node
  builder.updateClientInbound(0, {
    port: quickPort.value,
    security: quickSecurity.value,
    server_name: quickSNI.value,
    fingerprint: quickFingerprint.value,
    transport: quickTransport.value,
    xhttp_host: quickHost.value,
    xhttp_path: quickPath.value,
    xhttp_mode: quickMode.value,
    reality_key: quickPrivKey.value,
    reality_pub: quickPubKey.value,
  } as any)
  router.push('/chains/new')
}
</script>

<template>
  <div class="dashboard">
    <!-- Stats bar -->
    <div class="stats-bar">
      <div class="stat">
        <i class="pi pi-server" />
        <div>
          <span class="stat-value">{{ servers.servers.length }}</span>
          <span class="stat-label">Servers</span>
        </div>
      </div>
      <div class="stat">
        <i class="pi pi-check-circle" style="color: var(--p-green-400)" />
        <div>
          <span class="stat-value">{{ onlineCount }}</span>
          <span class="stat-label">Online</span>
        </div>
      </div>
      <div class="stat">
        <i class="pi pi-link" />
        <div>
          <span class="stat-value">{{ activeChainsCount }}</span>
          <span class="stat-label">Active Chains</span>
        </div>
      </div>
      <div class="stat-actions">
        <Button
          label="New Chain"
          icon="pi pi-plus"
          size="small"
          @click="router.push('/chains/new')"
        />
      </div>
    </div>

    <!-- Topology + Side panel -->
    <div class="topo-area">
      <div class="topo-main">
        <TopologyMap
          :servers="servers.servers"
          :chains="chainsStore.chains"
          :health="servers.health"
          @select-server="onSelectServer"
        />
      </div>

      <!-- Slide-in server panel -->
      <Transition name="slide">
        <div v-if="selectedServer" class="server-panel">
          <div class="panel-head">
            <h3>{{ selectedServer.name }}</h3>
            <button class="close-btn" @click="closePanel">
              <i class="pi pi-times" />
            </button>
          </div>

          <div class="panel-body">
            <!-- Server status line -->
            <div class="server-status-line">
              <span :class="['status-dot-sm', selectedHealth?.online ? 'online' : 'offline']" />
              <span class="server-host-sm">{{ selectedServer.host }}:{{ selectedServer.port }}</span>
              <span v-if="selectedHealth?.latency_ms" class="latency">{{ selectedHealth.latency_ms }}ms</span>
            </div>

            <!-- Client Connection mini-editor -->
            <div class="quick-connect">
              <div class="qc-header">
                <i class="pi pi-globe" />
                <span>Client Connection</span>
              </div>
              <p class="qc-hint">Configure how users will reach this server.</p>

              <div class="qc-field">
                <label>Security</label>
                <Select v-model="quickSecurity" :options="['reality','tls']" size="small" fluid />
              </div>
              <div class="qc-field">
                <label>Port</label>
                <InputNumber v-model="quickPort" :min="1" :max="65535" size="small" fluid />
              </div>
              <div class="qc-field">
                <label>SNI</label>
                <InputText v-model="quickSNI" size="small" fluid />
              </div>
              <div class="qc-field">
                <label>Fingerprint</label>
                <Select v-model="quickFingerprint" :options="['chrome','firefox','safari','random','none']" size="small" fluid />
              </div>
              <div class="qc-field">
                <label>Transport</label>
                <Select v-model="quickTransport" :options="['xhttp','ws','grpc','tcp']" size="small" fluid />
              </div>

              <!-- Reality keys -->
              <template v-if="quickSecurity === 'reality'">
                <div class="qc-field">
                  <label>Private Key</label>
                  <InputText v-model="quickPrivKey" type="password" placeholder="Click generate" size="small" fluid />
                </div>
                <Button
                  :label="quickGenerating ? 'Generating...' : quickPrivKey ? 'Regenerate Keys' : 'Generate Reality Keys'"
                  icon="pi pi-key"
                  size="small"
                  outlined
                  fluid
                  :loading="quickGenerating"
                  @click="generateQuickKeys"
                />
              </template>
            </div>

            <div class="panel-actions">
              <Button
                label="Build Chain"
                icon="pi pi-link"
                size="small"
                severity="success"
                fluid
                @click="startBuildingChain"
              />
              <Button
                label="Server Details"
                icon="pi pi-arrow-right"
                size="small"
                text
                fluid
                @click="router.push(`/servers/${selectedServer.id}`)"
              />
            </div>
          </div>
        </div>
      </Transition>
    </div>
  </div>
</template>

<style scoped>
.dashboard {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.stats-bar {
  display: flex;
  align-items: center;
  gap: 24px;
  padding: 14px 20px;
  background: var(--p-surface-card);
  border-radius: 12px;
  margin-bottom: 16px;
  flex-shrink: 0;
}

.stat {
  display: flex;
  align-items: center;
  gap: 8px;
}
.stat i { font-size: 18px; color: var(--p-primary-color); }
.stat > div { display: flex; flex-direction: column; }
.stat-value { font-size: 18px; font-weight: 800; }
.stat-label { font-size: 10px; color: var(--p-text-muted-color); text-transform: uppercase; letter-spacing: 0.5px; }

.stat-actions { margin-left: auto; }

.topo-area {
  flex: 1;
  display: flex;
  overflow: hidden;
  position: relative;
  min-height: 0;
}

.topo-main {
  flex: 1;
  background: var(--p-surface-card);
  border-radius: 12px;
  overflow: hidden;
}

/* Slide-in panel */
.server-panel {
  position: absolute;
  right: 0;
  top: 0;
  bottom: 0;
  width: 280px;
  background: var(--p-surface-card);
  border-left: 1px solid var(--p-surface-border);
  box-shadow: -4px 0 24px rgba(0, 0, 0, 0.3);
  border-radius: 0 12px 12px 0;
  display: flex;
  flex-direction: column;
  z-index: 10;
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px;
  border-bottom: 1px solid var(--p-surface-border);
}
.panel-head h3 { margin: 0; font-size: 16px; }

.close-btn {
  background: none;
  border: none;
  color: var(--p-text-muted-color);
  cursor: pointer;
  padding: 4px;
  border-radius: 6px;
}
.close-btn:hover { background: var(--p-surface-hover); color: var(--p-text-color); }

.panel-body { flex: 1; padding: 16px; overflow-y: auto; display: flex; flex-direction: column; gap: 16px; }

.server-status-line { display: flex; align-items: center; gap: 8px; font-size: 12px; }
.status-dot-sm { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.status-dot-sm.online { background: var(--p-green-400); }
.status-dot-sm.offline { background: var(--p-red-400); }
.server-host-sm { color: var(--p-text-muted-color); }
.latency { margin-left: auto; color: var(--p-green-400); font-weight: 600; }

.quick-connect {
  background: var(--p-surface-ground);
  border-radius: 10px;
  padding: 14px;
}
.qc-header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 700;
  color: var(--p-primary-color);
  margin-bottom: 2px;
}
.qc-header i { font-size: 16px; }
.qc-hint { font-size: 11px; color: var(--p-text-muted-color); margin: 0 0 12px; }

.qc-field { display: flex; flex-direction: column; gap: 3px; margin-bottom: 10px; }
.qc-field label { font-size: 10px; color: var(--p-text-muted-color); font-weight: 600; text-transform: uppercase; letter-spacing: 0.5px; }

.panel-actions { display: flex; flex-direction: column; gap: 6px; margin-top: auto; }

/* Transitions */
.slide-enter-active { transition: transform 0.25s ease; }
.slide-leave-active { transition: transform 0.2s ease; }
.slide-enter-from { transform: translateX(100%); }
.slide-leave-to { transform: translateX(100%); }
</style>

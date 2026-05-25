<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useBuilderStore } from '@/stores/builder'
import { useServersStore } from '@/stores/servers'
import { useChainsStore } from '@/stores/chains'
import { useWebSocket, type LogEntry } from '@/composables/useWebSocket'
import type { Server } from '@/types/server'
import ChainCanvas from '@/components/chains/ChainCanvas.vue'
import NodeInspector from '@/components/chains/NodeInspector.vue'
import LogStream from '@/components/chains/LogStream.vue'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Tag from 'primevue/tag'

const router = useRouter()
const builder = useBuilderStore()
const servers = useServersStore()
const chains = useChainsStore()
const ws = useWebSocket()

const chainName = ref('')
const applying = ref(false)
const connectionLink = ref('')
const showLogs = ref(false)
const showConfirm = ref(false)

onMounted(() => servers.fetchAll())
onUnmounted(() => { builder.reset(); ws.disconnect() })

const onDragStart = (e: DragEvent, srv: Server) => {
  if (e.dataTransfer) {
    e.dataTransfer.setData('application/lucx-server', JSON.stringify(srv))
    e.dataTransfer.effectAllowed = 'copy'
  }
}

const preview = computed(() => builder.buildPreview())

const requestApply = () => {
  if (!builder.canApply) return
  showConfirm.value = true
}

const apply = async () => {
  showConfirm.value = false
  if (!builder.canApply) return
  applying.value = true
  showLogs.value = true
  connectionLink.value = ''

  const chainNameInner = chainName.value || `Chain ${new Date().toISOString().slice(0, 19)}`
  const payload = { name: chainNameInner, nodes: builder.buildApiPayload() }

  try {
    const chain = await chains.create(payload.name, payload.nodes)
    ws.connect(chain.id)
    await chains.apply(chain.id)
    try {
      const cfg = await chains.getConnectionLink(chain.id)
      connectionLink.value = cfg.config
    } catch { /* best-effort */ }
  } catch {
    // Error shown via WS log stream
  } finally {
    applying.value = false
  }
}

const copyConnectionLink = () => {
  if (connectionLink.value) {
    navigator.clipboard.writeText(connectionLink.value).catch(() => {})
  }
}
</script>

<template>
  <div class="builder-root">
    <!-- LEFT: Server palette -->
    <div class="panel-servers">
      <div class="panel-header">
        <span>Servers</span>
        <span class="count">{{ servers.servers.length }}</span>
      </div>
      <div class="server-list">
        <div
          v-for="srv in servers.servers"
          :key="srv.id"
          class="server-card"
          draggable="true"
          @dragstart="(e: DragEvent) => onDragStart(e, srv)"
        >
          <div class="sc-top">
            <i
              :class="['pi', srv.status === 'online' ? 'pi-check-circle' : 'pi-circle']"
              :style="{ color: srv.status === 'online' ? 'var(--p-green-400)' : 'var(--p-text-muted-color)' }"
            />
            <span class="sc-name">{{ srv.name }}</span>
          </div>
          <div class="sc-host">{{ srv.host }}:{{ srv.port }}</div>
          <div class="sc-actions">
            <span class="sc-os">{{ srv.os || 'Unknown OS' }}</span>
            <button class="sc-add-btn" @click="builder.addNode(srv); builder.editNode(builder.nodes.length - 1)">
              <i class="pi pi-plus" /> Add
            </button>
          </div>
        </div>
        <div v-if="servers.servers.length === 0" class="no-servers">
          No servers. Add servers on the Dashboard first.
        </div>
      </div>
    </div>

    <!-- CENTER: Canvas -->
    <div class="panel-canvas">
      <div class="canvas-header">
        <InputText
          v-model="chainName"
          placeholder="Chain name (e.g. FI → NL → DE)"
          fluid
        />
        <div class="canvas-actions">
          <Button
            label="Apply"
            icon="pi pi-play"
            :disabled="!builder.canApply"
            :loading="applying"
            @click="requestApply"
          />
        </div>
      </div>
      <ChainCanvas />

      <!-- Log stream (bottom) -->
      <div v-if="showLogs" class="log-panel">
        <div v-if="connectionLink" class="config-bar">
          <code>{{ connectionLink.slice(0, 80) }}...</code>
          <Button label="Copy Connection Link" icon="pi pi-copy" size="small" text @click="copyConnectionLink" />
        </div>
        <LogStream :messages="ws.messages.value" :connected="ws.connected.value" />
      </div>
    </div>

    <!-- RIGHT: Inspector -->
    <NodeInspector />

    <!-- Confirm Dialog -->
    <Dialog v-model:visible="showConfirm" header="Confirm Apply" :style="{ width: '480px' }" modal>
      <div class="confirm-body">
        <p class="confirm-name">{{ chainName || 'Untitled Chain' }}</p>
        <div class="confirm-steps">
          <div v-for="(p, i) in preview" :key="i" class="confirm-row">
            <Tag :value="p.role.toUpperCase()" :severity="p.role === 'entry' ? 'info' : p.role === 'exit' ? 'success' : 'warn'" :rounded="true" />
            <span class="confirm-server">{{ p.server }}</span>
            <span class="confirm-detail">:{{ p.port }} · {{ p.security }} · {{ p.transport }}</span>
            <span v-if="p.nextOverride" class="confirm-override">→ {{ p.nextOverride }}</span>
          </div>
        </div>
        <p class="confirm-warning">
          <i class="pi pi-exclamation-triangle" />
          This will modify Xray config on {{ preview.length }} server{{ preview.length > 1 ? 's' : '' }}.
        </p>
      </div>
      <template #footer>
        <Button label="Cancel" text @click="showConfirm = false" />
        <Button label="Apply Chain" icon="pi pi-play" severity="success" @click="apply" />
      </template>
    </Dialog>
  </div>
</template>

<style scoped>
.builder-root {
  display: flex;
  height: calc(100vh - 24px - 24px); /* minus AppLayout padding */
  margin: -24px;
  overflow: hidden;
}

/* Left panel */
.panel-servers {
  width: 260px;
  min-width: 260px;
  border-right: 1px solid var(--p-surface-border);
  display: flex;
  flex-direction: column;
  background: var(--p-surface-card);
}

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 14px;
  font-weight: 700;
  font-size: 13px;
  border-bottom: 1px solid var(--p-surface-border);
}

.count {
  font-size: 11px;
  padding: 1px 7px;
  border-radius: 10px;
  background: var(--p-surface-hover);
}

.server-list {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.server-card {
  padding: 12px 14px;
  border-radius: 10px;
  background: var(--p-surface-ground);
  cursor: grab;
  transition: all 0.15s;
  border: 1px solid transparent;
}
.server-card:hover { background: var(--p-surface-hover); border-color: var(--p-surface-border); }
.server-card:active { cursor: grabbing; }

.sc-top {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}
.sc-name { font-size: 14px; font-weight: 700; }
.sc-host { font-size: 11px; color: var(--p-text-muted-color); margin-bottom: 8px; }

.sc-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.sc-os { font-size: 11px; color: var(--p-text-muted-color); }
.sc-add-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  border-radius: 6px;
  border: 1px solid var(--p-primary-color);
  background: transparent;
  color: var(--p-primary-color);
  cursor: pointer;
  font-size: 11px;
  font-family: inherit;
  transition: all 0.15s;
}
.sc-add-btn:hover { background: var(--p-primary-color); color: var(--p-primary-contrast-color); }

.no-servers { font-size: 12px; color: var(--p-text-muted-color); padding: 12px; text-align: center; }

/* Center */
.panel-canvas {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.canvas-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--p-surface-border);
}

.canvas-actions {
  display: flex;
  gap: 8px;
}

/* Config bar */
.config-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 14px;
  background: var(--p-surface-card);
  border-top: 1px solid var(--p-surface-border);
  font-size: 12px;
}

.config-bar code {
  flex: 1;
  color: var(--p-primary-color);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.log-panel {
  display: flex;
  flex-direction: column;
}

/* Responsive: stack on narrow screens */
@media (max-width: 900px) {
  .builder-root { flex-direction: column; }
  .panel-servers { width: 100%; min-width: 0; max-height: 200px; border-right: none; border-bottom: 1px solid var(--p-surface-border); }
  .panel-canvas { flex: 1; min-height: 0; }
}
@media (max-width: 768px) {
  .builder-root { height: auto; margin: -16px; }
  .canvas-header { flex-direction: column; gap: 8px; }
}

.confirm-body { display: flex; flex-direction: column; gap: 12px; }
.confirm-name { font-size: 16px; font-weight: 700; margin: 0; }
.confirm-steps { display: flex; flex-direction: column; gap: 6px; }
.confirm-row { display: flex; align-items: center; gap: 8px; font-size: 13px; }
.confirm-server { font-weight: 600; }
.confirm-detail { color: var(--p-text-muted-color); font-size: 12px; }
.confirm-override { color: var(--p-primary-color); font-size: 12px; margin-left: auto; }
.confirm-warning { font-size: 12px; color: var(--p-amber-500); display: flex; align-items: center; gap: 6px; margin: 0; }
</style>

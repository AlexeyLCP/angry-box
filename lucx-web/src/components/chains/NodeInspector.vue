<script setup lang="ts">
import { computed } from 'vue'
import { useBuilderStore, type ClientInboundSpec, type HopInboundSpec, type OutboundSpec } from '@/stores/builder'
import { serversApi } from '@/api/client'
import InputText from 'primevue/inputtext'
import InputNumber from 'primevue/inputnumber'
import Select from 'primevue/select'
import Button from 'primevue/button'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import TabPanels from 'primevue/tabpanels'
import TabPanel from 'primevue/tabpanel'

const store = useBuilderStore()
const node = computed(() => store.editingNode)
const idx = computed(() => store.editingIndex)

const transports = ['xhttp', 'ws', 'grpc', 'tcp']
const securities = ['reality', 'tls']
const fingerprints = ['chrome', 'firefox', 'safari', 'random', 'none']
const xhttpModes = ['packet-up', 'stream-one', 'stream-up']

const updateClient = (key: keyof ClientInboundSpec, value: unknown) => {
  if (idx.value === null) return
  store.updateClientInbound(idx.value, { [key]: value } as Partial<ClientInboundSpec>)
}
const updateHop = (key: keyof HopInboundSpec, value: unknown) => {
  if (idx.value === null) return
  store.updateHopInbound(idx.value, { [key]: value } as Partial<HopInboundSpec>)
}
const updateOut = (key: keyof OutboundSpec, value: unknown) => {
  if (idx.value === null) return
  store.updateOutbound(idx.value, { [key]: value } as Partial<OutboundSpec>)
}

const generateKeys = async () => {
  if (!node.value) return
  try {
    const res = await serversApi.generateKeys(node.value.server.id)
    updateClient('reality_key', res.private_key)
    updateClient('reality_pub', res.public_key)
  } catch { /* toast in parent */ }
}
</script>

<template>
  <div v-if="node && idx !== null" class="inspector">
    <div class="inspector-header">
      <div>
        <span class="inspector-title">Node #{{ idx + 1 }} — {{ node.role.toUpperCase() }}</span>
        <div class="inspector-server"><i class="pi pi-server" /> {{ node.server.name }}</div>
      </div>
      <Button icon="pi pi-times" text rounded size="small" @click="store.closeEditor()" />
    </div>

    <div class="inspector-body">
      <!-- Server info -->
      <div class="field-group">
        <label>Server</label>
        <div class="server-info">
          <div>{{ node.server.host }}:{{ node.server.port }}</div>
          <div class="hint">{{ node.server.os || 'Unknown OS' }}</div>
        </div>
      </div>

      <Tabs value="0">
        <TabList>
          <Tab value="0" class="tab-primary"><i class="pi pi-globe" /> <span>For Users</span></Tab>
          <Tab value="1"><i class="pi pi-server" /> <span>For Servers</span></Tab>
        </TabList>
        <TabPanels>
          <!-- ====== TAB 1: FOR USERS (Client Connection) ====== -->
          <TabPanel value="0">
            <p class="section-hint">How external clients reach this node.</p>

            <div class="field-group">
              <label>Security</label>
              <Select :model-value="node.clientInbound.security" :options="securities" fluid
                @update:model-value="(v: string | undefined) => v && updateClient('security', v)" />
            </div>
            <div class="field-group">
              <label>Port</label>
              <InputNumber :model-value="node.clientInbound.port" :min="1" :max="65535" fluid
                @update:model-value="(v: number) => updateClient('port', v)" />
            </div>
            <div class="field-group">
              <label>SNI (camouflage host)</label>
              <InputText :model-value="node.clientInbound.server_name" fluid
                @update:model-value="(v: string | undefined) => v && updateClient('server_name', v)" />
            </div>
            <div class="field-group">
              <label>Fingerprint</label>
              <Select :model-value="node.clientInbound.fingerprint" :options="fingerprints" fluid
                @update:model-value="(v: string | undefined) => v && updateClient('fingerprint', v)" />
            </div>

            <template v-if="node.clientInbound.security === 'reality'">
              <div class="field-group"><label>Private Key</label>
                <InputText :model-value="node.clientInbound.reality_key" type="password" placeholder="Click Generate" fluid
                  @update:model-value="(v: string | undefined) => v && updateClient('reality_key', v)" />
              </div>
              <div class="field-group"><label>Public Key</label>
                <InputText :model-value="node.clientInbound.reality_pub" readonly placeholder="Auto-filled" fluid />
              </div>
              <Button label="Generate Reality Keys" icon="pi pi-key" size="small" outlined fluid @click="generateKeys" />
            </template>

            <div class="field-group">
              <label>Transport</label>
              <Select :model-value="node.clientInbound.transport" :options="transports" fluid
                @update:model-value="(v: string | undefined) => v && updateClient('transport', v)" />
            </div>
            <template v-if="node.clientInbound.transport === 'xhttp'">
              <div class="field-group"><label>Host</label>
                <InputText :model-value="node.clientInbound.xhttp_host" fluid
                  @update:model-value="(v: string | undefined) => v && updateClient('xhttp_host', v)" />
              </div>
              <div class="field-group"><label>Path</label>
                <InputText :model-value="node.clientInbound.xhttp_path" fluid
                  @update:model-value="(v: string | undefined) => v && updateClient('xhttp_path', v)" />
              </div>
              <div class="field-group"><label>Mode</label>
                <Select :model-value="node.clientInbound.xhttp_mode" :options="xhttpModes" fluid
                  @update:model-value="(v: string | undefined) => v && updateClient('xhttp_mode', v)" />
              </div>
            </template>
          </TabPanel>

          <!-- ====== TAB 2: FOR SERVERS (Hop Connection + Outbound) ====== -->
          <TabPanel value="1">
            <p class="section-hint">How this node connects to the next hop.</p>

            <div class="field-group">
              <label>Hop Port</label>
              <InputNumber :model-value="node.hopInbound.port" :min="1" :max="65535" fluid
                @update:model-value="(v: number) => updateHop('port', v)" />
            </div>
            <div class="field-group">
              <label>Hop Transport</label>
              <Select :model-value="node.hopInbound.transport" :options="transports" fluid
                @update:model-value="(v: string | undefined) => v && updateHop('transport', v)" />
            </div>
            <div v-if="node.hopInbound.transport === 'xhttp'" class="field-group">
              <label>Hop Mode</label>
              <Select :model-value="node.hopInbound.xhttp_mode" :options="xhttpModes" fluid
                @update:model-value="(v: string | undefined) => v && updateHop('xhttp_mode', v)" />
            </div>

            <div v-if="node.role !== 'exit'" class="outbound-section">
              <div class="section-divider" />
              <p class="section-hint">Override how this node reaches the next one.</p>

              <div class="field-group">
                <label>Address Override</label>
                <InputText :model-value="node.outbound.address" placeholder="Auto (next server host)" fluid
                  @update:model-value="(v: string | undefined) => v && updateOut('address', v)" />
              </div>
              <div class="field-group">
                <label>Port Override</label>
                <InputNumber :model-value="node.outbound.port" :min="1" :max="65535" placeholder="Auto" fluid
                  @update:model-value="(v: number) => updateOut('port', v)" />
              </div>
            </div>
          </TabPanel>
        </TabPanels>
      </Tabs>
    </div>
  </div>
</template>

<style scoped>
.inspector { width: 300px; min-width: 300px; border-left: 1px solid var(--p-surface-border); display: flex; flex-direction: column; background: var(--p-surface-card); }
.inspector-header { display: flex; align-items: flex-start; justify-content: space-between; padding: 14px 16px; border-bottom: 1px solid var(--p-surface-border); }
.inspector-title { font-weight: 700; font-size: 14px; }
.inspector-server { font-size: 12px; color: var(--p-text-muted-color); margin-top: 2px; display: flex; align-items: center; gap: 4px; }
.inspector-body { flex: 1; overflow-y: auto; padding: 14px 16px; display: flex; flex-direction: column; gap: 10px; }

.field-group { display: flex; flex-direction: column; gap: 4px; margin-bottom: 12px; }
.field-group label { font-size: 11px; color: var(--p-text-muted-color); font-weight: 600; text-transform: uppercase; letter-spacing: 0.5px; }

.server-info { padding: 8px 10px; background: var(--p-surface-ground); border-radius: 6px; font-size: 13px; }
.hint { color: var(--p-text-muted-color); font-size: 11px; }

.section-hint { font-size: 11px; color: var(--p-text-muted-color); margin: 0 0 8px; }
.section-divider { border-top: 1px solid var(--p-surface-border); margin: 12px 0 8px; }
.outbound-section { margin-top: 8px; }

/* Primary tab (For Users) visual emphasis */
:deep(.tab-primary) {
  font-weight: 700 !important;
}
:deep(.tab-primary i) {
  color: var(--p-primary-color);
  font-size: 16px;
}
</style>

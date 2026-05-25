<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useChainsStore } from '@/stores/chains'
import { chainsApi } from '@/api/client'
import { useToast } from 'primevue/usetoast'
import type { Chain, ChainNode } from '@/types/chain'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Divider from 'primevue/divider'
import CopyButton from '@/components/shared/CopyButton.vue'

const route = useRoute()
const router = useRouter()
const store = useChainsStore()
const toast = useToast()

const id = route.params.id as string
const chain = ref<Chain | null>(null)
const connectionLink = ref('')
const loadingLink = ref(false)

onMounted(async () => {
  try {
    // Fetch individual chain to get nodes (list endpoint omits them)
    chain.value = await chainsApi.get(id)
  } catch {
    chain.value = null
  }
})

const roleColor = (role: string) =>
  ({ entry: '#2563eb', hop: '#d97706', exit: '#059669' })[role] ?? '#666'

const roleIcon = (role: string) =>
  ({ entry: 'pi pi-sign-in', hop: 'pi pi-arrow-right', exit: 'pi pi-sign-out' })[role] ?? 'pi pi-circle'

const fmtDate = (d: string | null) => {
  if (!d) return '—'
  return new Date(d).toLocaleString()
}

const severity = (status: string) =>
  status === 'active' ? 'success' : status === 'draft' ? 'info' : 'warn'

const apply = async () => {
  if (!chain.value) return
  try {
    await store.apply(id)
    toast.add({ severity: 'success', summary: 'Applied', detail: 'Chain is active', life: 3000 })
    await store.fetchAll()
    chain.value = store.getChain(id) ?? null
  } catch (e) {
    toast.add({ severity: 'error', summary: 'Apply failed', detail: String(e), life: 5000 })
  }
}

const rollback = async () => {
  if (!chain.value) return
  try {
    await store.rollback(id)
    toast.add({ severity: 'info', summary: 'Rolled back', detail: 'Chain is draft', life: 3000 })
    await store.fetchAll()
    chain.value = store.getChain(id) ?? null
  } catch (e) {
    toast.add({ severity: 'error', summary: 'Rollback failed', detail: String(e), life: 5000 })
  }
}

const remove = async () => {
  if (!chain.value) return
  try {
    await store.remove(id)
    toast.add({ severity: 'success', summary: 'Deleted', detail: 'Chain removed', life: 3000 })
    router.replace('/chains')
  } catch (e) {
    toast.add({ severity: 'error', summary: 'Delete failed', detail: String(e), life: 5000 })
  }
}

const fetchConnectionLink = async () => {
  loadingLink.value = true
  try {
    const cfg = await store.getConnectionLink(id)
    connectionLink.value = cfg.config
  } catch {
    connectionLink.value = ''
  } finally {
    loadingLink.value = false
  }
}
</script>

<template>
  <div v-if="chain">
    <div class="page-header">
      <div>
        <h2>{{ chain.name }}</h2>
        <p class="text-muted">{{ chain.id }}</p>
      </div>
      <div class="header-actions">
        <Button
          v-if="chain.status !== 'active'"
          label="Apply"
          icon="pi pi-play"
          severity="success"
          @click="apply"
        />
        <Button
          v-if="chain.status === 'active'"
          label="Rollback"
          icon="pi pi-undo"
          severity="info"
          @click="rollback"
        />
        <Button
          icon="pi pi-pencil"
          label="Edit"
          outlined
          @click="router.push('/chains/new')"
        />
        <Button
          icon="pi pi-trash"
          label="Delete"
          severity="danger"
          outlined
          @click="remove"
        />
      </div>
    </div>

    <!-- Status bar -->
    <div class="meta-bar">
      <div class="meta-item">
        <span class="meta-label">Status</span>
        <Tag :value="chain.status" :severity="severity(chain.status)" :rounded="true" />
      </div>
      <div class="meta-item">
        <span class="meta-label">Nodes</span>
        <strong>{{ chain.nodes?.length ?? 0 }}</strong>
      </div>
      <div class="meta-item">
        <span class="meta-label">Created</span>
        <span>{{ fmtDate(chain.created_at) }}</span>
      </div>
      <div class="meta-item">
        <span class="meta-label">Applied</span>
        <span>{{ fmtDate(chain.applied_at) }}</span>
      </div>
    </div>

    <Divider />

    <!-- Topology visualization -->
    <h3>Topology</h3>
    <div class="topology">
      <template v-for="(node, i) in chain.nodes" :key="i">
        <div class="topo-node">
          <div class="topo-box" :style="{ borderColor: roleColor(node.role) }">
            <div class="topo-role" :style="{ background: roleColor(node.role) }">
              {{ node.role.toUpperCase() }}
            </div>
            <div class="topo-body">
              <span class="topo-protocol">{{ node.protocol.toUpperCase() }}</span>
              <span class="topo-backend">{{ node.backend_type }}</span>
              <span class="topo-server-id" :title="node.server_id">
                <i class="pi pi-server" style="font-size:11px" />
                {{ node.server_id.slice(0, 8) }}...
              </span>
            </div>
          </div>
        </div>
        <div v-if="i < (chain.nodes?.length ?? 0) - 1" class="topo-arrow">
          <i class="pi pi-arrow-down" />
          <span class="arrow-label">hop {{ i }}→{{ i + 1 }}</span>
        </div>
      </template>
    </div>

    <!-- Client Config -->
    <Divider />
    <div class="config-section">
      <div class="config-header">
        <h3>Client Config</h3>
        <Button
          v-if="!connectionLink"
          :label="loadingLink ? 'Loading...' : 'Copy Connection Link'"
          icon="pi pi-download"
          size="small"
          :loading="loadingLink"
          @click="fetchConnectionLink"
        />
      </div>
      <div v-if="connectionLink" class="config-box">
        <code>{{ connectionLink }}</code>
        <div class="config-actions">
          <CopyButton :text="connectionLink" label="Copy Connection Link" />
        </div>
      </div>
      <p v-if="!connectionLink && !loadingLink" class="text-muted" style="margin-top:8px">
        Config available only for active chains.
      </p>
    </div>
  </div>

  <div v-else class="text-muted" style="text-align:center;padding:60px">
    Loading...
  </div>
</template>

<style scoped>
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 16px;
}
.page-header h2 { margin: 0; font-size: 22px; }
.muted { color: var(--p-text-muted-color); font-size: 12px; }

.header-actions { display: flex; gap: 6px; }

.meta-bar {
  display: flex;
  gap: 24px;
  flex-wrap: wrap;
}
.meta-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 13px;
}
.meta-label { color: var(--p-text-muted-color); font-size: 11px; font-weight: 600; }

h3 { font-size: 15px; margin: 0 0 12px; }

.topology {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 20px 0;
}
.topo-node { width: 300px; }
.topo-box {
  border: 2px solid;
  border-radius: 10px;
  overflow: hidden;
}
.topo-role {
  color: white;
  font-size: 10px;
  font-weight: 800;
  padding: 4px 10px;
  text-align: center;
}
.topo-body {
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  background: var(--p-surface-card);
}
.topo-protocol { font-weight: 700; font-size: 14px; }
.topo-backend { font-size: 12px; color: var(--p-primary-color); }
.topo-server-id {
  font-size: 11px;
  color: var(--p-text-muted-color);
  display: flex;
  align-items: center;
  gap: 4px;
}
.topo-arrow {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 6px 0;
  color: var(--p-primary-color);
  opacity: 0.6;
}
.arrow-label { font-size: 10px; color: var(--p-text-muted-color); }

.config-section { margin-top: 8px; }
.config-header { display: flex; align-items: center; justify-content: space-between; }
.config-header h3 { margin: 0; }
.config-box {
  margin-top: 10px;
  padding: 12px;
  background: var(--p-surface-card);
  border: 1px solid var(--p-surface-border);
  border-radius: 8px;
}
.config-box code {
  font-size: 11px;
  word-break: break-all;
  color: var(--p-primary-color);
  line-height: 1.5;
}
.config-actions { margin-top: 10px; display: flex; gap: 8px; }
</style>

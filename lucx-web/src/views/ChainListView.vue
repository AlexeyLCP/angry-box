<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useChainsStore } from '@/stores/chains'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Tag from 'primevue/tag'
import ConfirmDialog from 'primevue/confirmdialog'
import type { Chain } from '@/types/chain'

const router = useRouter()
const store = useChainsStore()
const toast = useToast()

const filterStatus = ref<'all' | 'active' | 'draft' | 'broken'>('all')

const filtered = computed(() => {
  if (filterStatus.value === 'all') return store.chains
  return store.chains.filter((c) => c.status === filterStatus.value)
})

const statuses = [
  { label: 'All', value: 'all' },
  { label: 'Active', value: 'active' },
  { label: 'Draft', value: 'draft' },
]

onMounted(() => store.fetchAll())

const severity = (status: string) =>
  status === 'active' ? 'success' : status === 'draft' ? 'info' : 'warn'

const fmtDate = (d: string) => {
  if (!d) return '—'
  return new Date(d).toLocaleString()
}

const apply = async (chain: Chain) => {
  try {
    await store.apply(chain.id)
    toast.add({ severity: 'success', summary: 'Applied', detail: `${chain.name} is active`, life: 3000 })
  } catch (e) {
    toast.add({ severity: 'error', summary: 'Apply failed', detail: String(e), life: 5000 })
  }
  await store.fetchAll()
}

const rollback = async (chain: Chain) => {
  try {
    await store.rollback(chain.id)
    toast.add({ severity: 'info', summary: 'Rolled back', detail: `${chain.name} is draft`, life: 3000 })
  } catch (e) {
    toast.add({ severity: 'error', summary: 'Rollback failed', detail: String(e), life: 5000 })
  }
  await store.fetchAll()
}

const remove = async (chain: Chain) => {
  try {
    await store.remove(chain.id)
    toast.add({ severity: 'success', summary: 'Deleted', detail: `${chain.name} removed`, life: 3000 })
  } catch (e) {
    toast.add({ severity: 'error', summary: 'Delete failed', detail: String(e), life: 5000 })
  }
}

const copyConnectionLink = async (chain: Chain) => {
  try {
    const cfg = await store.getConnectionLink(chain.id)
    await navigator.clipboard.writeText(cfg.config)
    toast.add({ severity: 'success', summary: 'Copied', detail: 'Client config copied', life: 2000 })
  } catch (e) {
    toast.add({ severity: 'error', summary: 'Failed', detail: 'Cannot get config (is chain applied?)', life: 3000 })
  }
}
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <h2>Chains</h2>
        <p class="text-muted">{{ store.chains.length }} chains</p>
      </div>
      <Button icon="pi pi-plus" label="New Chain" @click="router.push('/chains/new')" />
    </div>

    <!-- Filters -->
    <div class="filters">
      <button
        v-for="s in statuses"
        :key="s.value"
        :class="['filter-chip', { active: filterStatus === s.value }]"
        @click="filterStatus = s.value as typeof filterStatus"
      >
        {{ s.label }}
      </button>
    </div>

    <!-- Table -->
    <DataTable
      :value="filtered"
      :loading="store.loading"
      striped-rows
      paginator
      :rows="15"
      :rows-per-page-options="[10, 15, 25]"
      sort-field="created_at"
      :sort-order="-1"
      current-page-report-template="{first}-{last} of {totalRecords}"
      empty-message="No chains yet. Create your first chain!"
    >
      <Column field="name" header="Name" sortable>
        <template #body="{ data }: { data: Chain }">
          <span class="chain-name" @click="router.push(`/chains/${data.id}`)">{{ data.name }}</span>
        </template>
      </Column>

      <Column header="Hops" class="col-narrow">
        <template #body="{ data }: { data: Chain }">
          <span class="hop-count">{{ data.nodes?.length ?? '—' }}</span>
        </template>
      </Column>

      <Column field="status" header="Status" sortable class="col-narrow">
        <template #body="{ data }: { data: Chain }">
          <Tag :value="data.status" :severity="severity(data.status)" :rounded="true" />
        </template>
      </Column>

      <Column field="created_at" header="Created" sortable class="col-date">
        <template #body="{ data }: { data: Chain }">
          <span class="date-text">{{ fmtDate(data.created_at) }}</span>
        </template>
      </Column>

      <Column header="Actions" class="col-actions" :style="{ textAlign: 'right' }">
        <template #body="{ data }: { data: Chain }">
          <div class="actions">
            <Button
              v-if="data.status !== 'active'"
              icon="pi pi-play"
              text
              rounded
              size="small"
              severity="success"
              title="Apply"
              @click.stop="apply(data)"
            />
            <Button
              v-if="data.status === 'active'"
              icon="pi pi-undo"
              text
              rounded
              size="small"
              severity="info"
              title="Rollback"
              @click.stop="rollback(data)"
            />
            <Button
              v-if="data.status === 'active'"
              icon="pi pi-copy"
              text
              rounded
              size="small"
              title="Copy Connection Link"
              @click.stop="copyConnectionLink(data)"
            />
            <Button
              icon="pi pi-trash"
              text
              rounded
              size="small"
              severity="danger"
              title="Delete"
              @click.stop="remove(data)"
            />
            <Button
              icon="pi pi-arrow-right"
              text
              rounded
              size="small"
              title="Details"
              @click.stop="router.push(`/chains/${data.id}`)"
            />
          </div>
        </template>
      </Column>
    </DataTable>
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
.muted { color: var(--p-text-muted-color); font-size: 13px; margin: 2px 0 0; }

.filters {
  display: flex;
  gap: 6px;
  margin-bottom: 16px;
}

.filter-chip {
  padding: 4px 14px;
  border: 1px solid var(--p-surface-border);
  border-radius: 20px;
  background: transparent;
  color: var(--p-text-muted-color);
  cursor: pointer;
  font-size: 12px;
  transition: all 0.15s;
}
.filter-chip:hover { border-color: var(--p-primary-color); color: var(--p-text-color); }
.filter-chip.active { background: var(--p-primary-color); border-color: var(--p-primary-color); color: var(--p-primary-contrast-color); }

.chain-name {
  font-weight: 600;
  cursor: pointer;
}
.chain-name:hover { color: var(--p-primary-color); }

.hop-count { font-variant-numeric: tabular-nums; }
.date-text { font-size: 12px; color: var(--p-text-muted-color); white-space: nowrap; }
.actions { display: flex; justify-content: flex-end; gap: 2px; }

.col-narrow { width: 80px; }
.col-date { width: 180px; }
.col-actions { width: 190px; }
</style>

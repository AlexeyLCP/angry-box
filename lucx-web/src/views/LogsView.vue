<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useStatusStore } from '@/stores/status'
import { useRouter } from 'vue-router'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Tag from 'primevue/tag'
import Button from 'primevue/button'
import type { LogEntry } from '@/types/server'

const status = useStatusStore()
const router = useRouter()

const filterEvent = ref<'all' | 'apply' | 'create' | 'rollback'>('all')

const filtered = computed(() => {
  if (filterEvent.value === 'all') return status.logs
  return status.logs.filter((l) => l.event === filterEvent.value)
})

onMounted(() => status.fetchLogs())

const eventSeverity = (event: string) =>
  ({ apply: 'success', create: 'info', rollback: 'warn', delete: 'danger' })[event] ?? 'secondary'

const fmtDate = (d: string) => new Date(d).toLocaleString()
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <h2>Logs</h2>
        <p class="text-muted">{{ status.logs.length }} events</p>
      </div>
      <Button icon="pi pi-refresh" text rounded @click="status.fetchLogs()" :loading="status.loading" />
    </div>

    <div class="filters">
      <button
        v-for="ev in ['all', 'apply', 'create', 'rollback']"
        :key="ev"
        :class="['filter-chip', { active: filterEvent === ev }]"
        @click="filterEvent = ev as typeof filterEvent"
      >
        {{ ev }}
      </button>
    </div>

    <DataTable
      :value="filtered"
      :loading="status.loading"
      striped-rows
      paginator
      :rows="20"
      sort-field="timestamp"
      :sort-order="-1"
      empty-message="No log entries yet. Apply or create a chain."
    >
      <Column field="timestamp" header="Time" sortable class="col-date">
        <template #body="{ data }: { data: LogEntry }">
          <span class="date-text">{{ fmtDate(data.timestamp) }}</span>
        </template>
      </Column>

      <Column field="event" header="Event" sortable class="col-narrow">
        <template #body="{ data }: { data: LogEntry }">
          <Tag :value="data.event" :severity="eventSeverity(data.event)" :rounded="true" />
        </template>
      </Column>

      <Column field="chain_name" header="Chain" sortable>
        <template #body="{ data }: { data: LogEntry }">
          <span class="chain-link" @click="router.push(`/chains/${data.chain_id}`)">
            {{ data.chain_name }}
          </span>
        </template>
      </Column>

      <Column field="status" header="Status" class="col-narrow">
        <template #body="{ data }: { data: LogEntry }">
          <Tag :value="data.status" :severity="data.status === 'success' ? 'success' : 'danger'" :rounded="true" />
        </template>
      </Column>
    </DataTable>
  </div>
</template>

<style scoped>
.page-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 16px; }
.page-header h2 { margin: 0; font-size: 22px; }
.muted { color: var(--p-text-muted-color); font-size: 13px; margin: 2px 0 0; }

.filters { display: flex; gap: 6px; margin-bottom: 16px; }
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

.date-text { font-size: 12px; color: var(--p-text-muted-color); white-space: nowrap; }
.chain-link { font-weight: 600; cursor: pointer; }
.chain-link:hover { color: var(--p-primary-color); }

.col-date { width: 180px; }
.col-narrow { width: 100px; }
</style>

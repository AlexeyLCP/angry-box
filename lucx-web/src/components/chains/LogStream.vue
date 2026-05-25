<script setup lang="ts">
import type { LogEntry } from '@/composables/useWebSocket'
import { computed } from 'vue'
import type { ComputedRef } from 'vue'

const props = defineProps<{
  messages: LogEntry[]
  connected: boolean
}>()

const color = (entry: LogEntry): string => {
  if (entry.error) return 'var(--p-red-400)'
  if (entry.status === 'ok' || entry.status === 'active') return 'var(--p-green-400)'
  if (entry.step === 'complete') return 'var(--p-green-400)'
  return 'var(--p-text-muted-color)'
}

const icon = (entry: LogEntry): string => {
  if (entry.error) return 'pi pi-times-circle'
  if (entry.status === 'ok' || entry.status === 'active') return 'pi pi-check-circle'
  if (entry.step === 'complete') return 'pi pi-verified'
  if (entry.status === 'started') return 'pi pi-spin pi-spinner'
  return 'pi pi-circle'
}
</script>

<template>
  <div class="log-stream">
    <div class="log-header">
      <i class="pi pi-terminal" />
      <span>Apply Progress</span>
      <span class="spacer" />
      <span :class="['status', connected ? 'live' : 'dead']">
        {{ connected ? 'LIVE' : 'OFFLINE' }}
      </span>
    </div>

    <div class="log-body">
      <div v-if="messages.length === 0" class="empty">
        No messages yet...
      </div>
      <div
        v-for="(m, i) in messages"
        :key="i"
        class="log-line"
        :style="{ color: color(m) }"
      >
        <i :class="icon(m)" />
        <span class="step" v-if="m.step">[{{ m.step.toUpperCase() }}]</span>
        <span class="server" v-if="m.server">{{ m.server }}</span>
        <span class="detail">{{ m.detail || m.error }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.log-stream {
  border-top: 1px solid var(--p-surface-border);
  max-height: 220px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.log-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  font-size: 12px;
  font-weight: 700;
  color: var(--p-text-muted-color);
  background: var(--p-surface-card);
  border-bottom: 1px solid var(--p-surface-border);
}

.spacer { flex: 1; }

.status {
  font-size: 10px;
  padding: 2px 6px;
  border-radius: 4px;
}

.live { background: rgba(34, 197, 94, 0.15); color: var(--p-green-400); }
.dead { background: rgba(239, 68, 68, 0.15); color: var(--p-red-400); }

.log-body {
  flex: 1;
  overflow-y: auto;
  padding: 8px 14px;
  background: var(--p-surface-ground);
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
  font-size: 12px;
  line-height: 1.6;
}

.empty {
  color: var(--p-text-muted-color);
  opacity: 0.5;
}

.log-line {
  display: flex;
  align-items: flex-start;
  gap: 6px;
}

.log-line i { margin-top: 3px; font-size: 10px; flex-shrink: 0; }

.step { color: var(--p-primary-color); font-weight: 600; }
.server { color: var(--p-amber-400); margin-right: 4px; }
</style>

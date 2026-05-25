<script setup lang="ts">
import { onMounted } from 'vue'
import { useStatusStore } from '@/stores/status'
import Card from 'primevue/card'
import Tag from 'primevue/tag'
import Divider from 'primevue/divider'

const status = useStatusStore()

onMounted(() => status.fetchStatus())
</script>

<template>
  <div>
    <div class="page-header">
      <h2>Settings</h2>
      <span class="version">LucX Core v{{ status.info?.version ?? '...' }}</span>
    </div>

    <div class="grid-2">
      <!-- System info -->
      <Card>
        <template #content>
          <h4>System</h4>
          <div class="kv">
            <span>Version</span>
            <Tag :value="status.info?.version ?? 'dev'" severity="info" :rounded="true" />
          </div>
          <div class="kv">
            <span>Go</span>
            <strong>{{ status.info?.go_version ?? '—' }}</strong>
          </div>
          <div class="kv"><span>OS</span><strong>{{ status.info?.os ?? '—' }}</strong></div>
          <div class="kv"><span>Arch</span><strong>{{ status.info?.arch ?? '—' }}</strong></div>
          <div class="kv"><span>CPU Cores</span><strong>{{ status.info?.num_cpu ?? '—' }}</strong></div>
          <div class="kv"><span>PID</span><strong>{{ status.info?.pid ?? '—' }}</strong></div>
        </template>
      </Card>

      <!-- Runtime -->
      <Card>
        <template #content>
          <h4>Runtime</h4>
          <div class="kv"><span>Uptime</span><strong>{{ status.info?.uptime ?? '—' }}</strong></div>
          <div class="kv"><span>Database</span><code class="db-path">{{ status.info?.db_path ?? '—' }}</code></div>
          <div class="kv"><span>API</span><code>localhost:8744</code></div>
        </template>
      </Card>
    </div>

    <Divider />

    <!-- Config info -->
    <Card>
      <template #content>
        <h4>CLI Flags Reference</h4>
        <div class="cli-table">
          <div class="cli-row head">
            <span>Flag</span><span>Default</span><span>Description</span>
          </div>
          <div class="cli-row"><code>-listen</code><code>:8744</code><span>API listen address</span></div>
          <div class="cli-row"><code>-db</code><code>./lucx.db</code><span>SQLite database path</span></div>
          <div class="cli-row"><code>-jwt-secret</code><code>(auto)</code><span>JWT signing secret</span></div>
          <div class="cli-row"><code>-apply-chain</code><code>(empty)</code><span>Apply chain by ID (CLI mode)</span></div>
          <div class="cli-row"><code>-add-server</code><code>false</code><span>Add server (CLI mode)</span></div>
          <div class="cli-row"><code>-server-host</code><code>(empty)</code><span>Server host for -add-server</span></div>
          <div class="cli-row"><code>-server-user</code><code>root</code><span>SSH user for -add-server</span></div>
          <div class="cli-row"><code>-server-port</code><code>22</code><span>SSH port for -add-server</span></div>
        </div>
      </template>
    </Card>
  </div>
</template>

<style scoped>
.page-header { display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 20px; }
.page-header h2 { margin: 0; font-size: 22px; }
.version { font-size: 13px; color: var(--p-primary-color); font-weight: 600; }

.grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }

h4 { margin: 0 0 10px; font-size: 14px; }

.kv { display: flex; justify-content: space-between; align-items: center; padding: 4px 0; font-size: 13px; }
.kv span { color: var(--p-text-muted-color); }

.db-path { font-size: 11px; color: var(--p-primary-color); max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.cli-table { display: flex; flex-direction: column; font-size: 13px; }
.cli-row { display: grid; grid-template-columns: 180px 140px 1fr; gap: 12px; padding: 5px 0; border-bottom: 1px solid var(--p-surface-border); align-items: center; }
.cli-row.head { color: var(--p-text-muted-color); font-weight: 700; font-size: 11px; border-bottom: 2px solid var(--p-surface-border); }
.cli-row code { background: var(--p-surface-hover); padding: 1px 5px; border-radius: 4px; font-size: 12px; }
</style>

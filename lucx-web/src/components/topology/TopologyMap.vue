<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import type { Server, HealthStatus } from '@/types/server'
import type { Chain } from '@/types/chain'
import { useGeoGuess } from '@/composables/useGeoGuess'
import { computeLayout, getConnections, curvedPath, chainCountForServer } from '@/composables/useTopology'

const props = defineProps<{
  servers: Server[]
  chains: Chain[]
  health: Record<string, HealthStatus>
}>()

const emit = defineEmits<{
  'select-server': [serverId: string]
}>()

const svgWidth = 800
const svgHeight = 600
const containerRef = ref<HTMLElement | null>(null)

const onlineServers = computed(() =>
  props.servers.filter((s) => props.health[s.id]?.online)
)

const activeChains = computed(() =>
  props.chains.filter((c) => c.status === 'active')
)

const layout = computed(() => {
  const nodes = computeLayout(props.servers, svgWidth, svgHeight)
  // Add chain counts
  return nodes.map((n) => ({
    ...n,
    chainCount: chainCountForServer(n.server.id, props.chains),
  }))
})

const connections = computed(() => {
  const pairs = getConnections(props.servers, props.chains)
  const layoutMap = new Map(layout.value.map((n) => [n.server.id, n]))
  const result: { from: string; to: string; chainIds: string[]; path: string }[] = []
  for (const [key, chainIds] of pairs) {
    const [a, b] = key.split('::')
    const na = layoutMap.get(a)
    const nb = layoutMap.get(b)
    if (na && nb) {
      result.push({ from: a, to: b, chainIds, path: curvedPath(na.x, na.y, nb.x, nb.y) })
    }
  }
  return result
})

const statusClass = (server: Server) => {
  const h = props.health[server.id]
  if (!h) return 'unknown'
  return h.online ? 'online' : 'offline'
}

const flagForHost = (host: string) => useGeoGuess(host)
</script>

<template>
  <div ref="containerRef" class="topology-root">
    <svg
      :viewBox="`0 0 ${svgWidth} ${svgHeight}`"
      preserveAspectRatio="xMidYMid meet"
      class="topo-svg"
    >
      <!-- Decorative rings -->
      <circle :cx="svgWidth/2" :cy="svgHeight/2" r="200" class="deco-ring" />
      <circle :cx="svgWidth/2" :cy="svgHeight/2" r="280" class="deco-ring deco-ring-2" />

      <!-- Connections -->
      <g v-for="(conn, i) in connections" :key="'c'+i">
        <path
          :d="conn.path"
          class="connection-line"
          :class="{ active: conn.chainIds.length > 0 }"
        />
        <!-- Animated pulse dot -->
        <circle r="3" class="pulse-dot" v-if="conn.chainIds.length > 0">
          <animateMotion
            :dur="`${1.5 + i * 0.3}s`"
            repeatCount="indefinite"
            :path="conn.path"
          />
        </circle>
      </g>

      <!-- Server nodes -->
      <g
        v-for="node in layout"
        :key="node.server.id"
        :class="['topo-node', statusClass(node.server)]"
        @click="emit('select-server', node.server.id)"
      >
        <!-- Hover glow -->
        <circle :cx="node.x" :cy="node.y" r="36" class="node-glow" />
        <!-- Outer ring -->
        <circle :cx="node.x" :cy="node.y" r="28" class="node-outer" />
        <!-- Inner circle -->
        <circle :cx="node.x" :cy="node.y" r="24" class="node-inner" />
        <!-- Status dot -->
        <circle
          :cx="node.x - 16"
          :cy="node.y - 16"
          r="5"
          :class="['status-dot', statusClass(node.server)]"
        />
        <!-- Chain count badge -->
        <template v-if="node.chainCount > 0">
          <circle :cx="node.x + 16" :cy="node.y - 16" r="9" class="badge-bg" />
          <text :x="node.x + 16" :y="node.y - 13" text-anchor="middle" class="badge-text">
            {{ node.chainCount }}
          </text>
        </template>
        <!-- Flag -->
        <text :x="node.x" :y="node.y - 4" text-anchor="middle" class="node-flag">
          {{ flagForHost(node.server.host) }}
        </text>
        <!-- Name -->
        <text :x="node.x" :y="node.y + 8" text-anchor="middle" class="node-name">
          {{ node.server.name }}
        </text>
        <!-- Host -->
        <text :x="node.x" :y="node.y + 20" text-anchor="middle" class="node-host">
          {{ node.server.host }}
        </text>
      </g>
    </svg>

    <!-- Empty state -->
    <div v-if="servers.length === 0" class="empty-overlay">
      <i class="pi pi-server" />
      <p>No servers yet</p>
      <span>Add a server to see the network map</span>
    </div>
  </div>
</template>

<style scoped>
.topology-root {
  position: relative;
  width: 100%;
  height: 100%;
  min-height: 400px;
}

.topo-svg {
  width: 100%;
  height: 100%;
}

/* Decorative rings */
.deco-ring {
  fill: none;
  stroke: var(--p-surface-border);
  stroke-width: 0.5;
  opacity: 0.4;
}
.deco-ring-2 {
  opacity: 0.2;
}

/* Connection lines */
.connection-line {
  fill: none;
  stroke: var(--p-surface-border);
  stroke-width: 1.5;
  opacity: 0.4;
}
.connection-line.active {
  stroke: var(--p-primary-color);
  opacity: 0.5;
  stroke-dasharray: 6 4;
  animation: dash-flow 3s linear infinite;
}
@keyframes dash-flow {
  to { stroke-dashoffset: -20; }
}

.pulse-dot {
  fill: var(--p-primary-color);
  opacity: 0.8;
}

/* Node styles */
.topo-node { cursor: pointer; }
.topo-node :deep(.node-glow) {
  fill: transparent;
  transition: all 0.3s ease;
}
.topo-node:hover :deep(.node-glow) {
  fill: var(--p-primary-color);
  opacity: 0.08;
}
.node-outer {
  fill: var(--p-surface-card);
  stroke: var(--p-surface-border);
  stroke-width: 2;
  transition: stroke 0.3s;
}
.topo-node.online .node-outer { stroke: var(--p-green-400); opacity: 0.6; }
.topo-node.offline .node-outer { stroke: var(--p-red-400); opacity: 0.4; }
.topo-node.unknown .node-outer { stroke: var(--p-text-muted-color); opacity: 0.4; }

.node-inner {
  fill: var(--p-surface-ground);
}

.status-dot { stroke: var(--p-surface-card); stroke-width: 1.5; }
.status-dot.online { fill: var(--p-green-400); animation: pulse 2s ease-in-out infinite; }
.status-dot.offline { fill: var(--p-red-400); }
.status-dot.unknown { fill: var(--p-text-muted-color); }

@keyframes pulse {
  0%, 100% { r: 5; opacity: 1; }
  50% { r: 8; opacity: 0.5; }
}

.badge-bg { fill: var(--p-primary-color); opacity: 0.8; }
.badge-text { fill: white; font-size: 9px; font-weight: 700; }

.node-flag { font-size: 14px; }
.node-name {
  font-size: 10px;
  font-weight: 700;
  fill: var(--p-text-color);
}
.node-host {
  font-size: 8px;
  fill: var(--p-text-muted-color);
}

.empty-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  color: var(--p-text-muted-color);
  gap: 8px;
  pointer-events: none;
}
.empty-overlay i { font-size: 48px; opacity: 0.3; }
.empty-overlay p { font-size: 16px; }
.empty-overlay span { font-size: 12px; opacity: 0.6; }
</style>

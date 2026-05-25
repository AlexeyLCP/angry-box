<script setup lang="ts">
import { ref, nextTick } from 'vue'
import { useBuilderStore } from '@/stores/builder'
import type { Server } from '@/types/server'

const store = useBuilderStore()

const roleColor = (role: string) =>
  ({ entry: '#3b82f6', hop: '#f59e0b', exit: '#10b981' })[role] ?? '#666'

const isDragOver = ref(false)
const dropIndex = ref<number | null>(null)

const onDragOver = (e: DragEvent) => {
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'
  isDragOver.value = true
}

const onDragLeave = () => {
  isDragOver.value = false
}

const onDrop = (e: DragEvent) => {
  e.preventDefault()
  isDragOver.value = false
  if (!e.dataTransfer) return
  try {
    const data = JSON.parse(e.dataTransfer.getData('application/lucx-server'))
    const server = data as Server
    store.addNode(server)
    nextTick(() => store.editNode(store.nodes.length - 1))
  } catch {
    // invalid drop
  }
}

const onDragOverReorder = (e: DragEvent, index: number) => {
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  dropIndex.value = index
}

const onReorderLeave = () => {
  dropIndex.value = null
}

const onReorderDrop = (e: DragEvent, toIndex: number) => {
  e.preventDefault()
  dropIndex.value = null
  const from = parseInt(e.dataTransfer!.getData('application/lucx-reorder'))
  if (!isNaN(from)) store.reorder(from, toIndex)
}
</script>

<template>
  <div
    class="canvas"
    :class="{ 'drag-over': isDragOver && store.nodes.length === 0 }"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  >
    <div v-if="store.nodes.length === 0" class="empty">
      <i class="pi pi-arrow-down-left" :class="{ 'pulse-icon': isDragOver }" />
      <p>{{ isDragOver ? 'Drop to add!' : 'Drag servers here or click "Add to chain"' }}</p>
      <span>Entry  →  Hop(s)  →  Exit</span>
    </div>

    <div v-else class="chain-horizontal">
      <div
        v-for="(node, i) in store.nodes"
        :key="i"
        class="node-wrapper"
        :class="{ 'drop-target': dropIndex === i }"
        draggable="true"
        @dragstart="(e) => {
          e.dataTransfer!.setData('application/lucx-reorder', String(i))
          e.dataTransfer!.effectAllowed = 'move'
        }"
        @dragover="(e) => onDragOverReorder(e, i)"
        @dragleave="onReorderLeave"
        @drop="(e) => onReorderDrop(e, i)"
        @click="store.editNode(i)"
      >
        <!-- Insert indicator line -->
        <div v-if="dropIndex === i" class="insert-indicator" />

        <div
          :class="['node-card', `node-${node.role}`, { selected: store.editingIndex === i }]"
          :style="{ borderTopColor: roleColor(node.role) }"
        >
          <div class="node-header">
            <span class="role-badge" :style="{ background: roleColor(node.role) }">
              {{ node.role.toUpperCase() }}
            </span>
            <span class="node-num">#{{ i + 1 }}</span>
          </div>
          <div class="node-body">
            <i class="pi pi-server" />
            <span class="node-name">{{ node.server.name }}</span>
          </div>
          <div class="node-footer">
            <span class="node-host">{{ node.server.host }}</span>
            <span class="node-port">:{{ node.role === 'entry' ? node.clientInbound.port : node.hopInbound.port }}</span>
          </div>
          <button class="btn-remove" @click.stop="store.removeNode(i)">
            <i class="pi pi-times" />
          </button>
        </div>

        <!-- SVG Arrow -->
        <div v-if="i < store.nodes.length - 1" class="arrow-connector">
          <svg width="64" height="24" viewBox="0 0 64 24">
            <defs>
              <marker id="arrowhead" markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto">
                <polygon points="0 0, 8 3, 0 6" :fill="roleColor(node.role)" />
              </marker>
            </defs>
            <line x1="0" y1="12" x2="54" y2="12"
              :stroke="roleColor(node.role)"
              stroke-width="2"
              stroke-dasharray="100"
              marker-end="url(#arrowhead)"
              class="flow-line" />
          </svg>
        </div>
      </div>

      <!-- End drop zone -->
      <div
        class="drop-zone"
        :class="{ 'drop-active': dropIndex === store.nodes.length }"
        @dragover="(e) => onDragOverReorder(e, store.nodes.length)"
        @dragleave="onReorderLeave"
        @drop="onDrop"
      >
        <i class="pi pi-plus" />
        <span>Add server</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.canvas {
  flex: 1;
  overflow: auto;
  padding: 24px;
}

.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--p-text-muted-color);
  gap: 8px;
}
.empty i { font-size: 40px; opacity: 0.5; }
.empty p { font-size: 15px; }
.empty span { font-size: 13px; opacity: 0.6; }

.chain-horizontal {
  display: flex;
  align-items: center;
  gap: 0;
  min-height: 200px;
  padding: 12px 0;
}

.node-wrapper {
  display: flex;
  align-items: center;
  cursor: pointer;
  flex-shrink: 0;
}

.node-card {
  position: relative;
  width: 160px;
  padding: 16px 14px;
  border-radius: 12px;
  background: var(--p-surface-card);
  border: 1px solid var(--p-surface-border);
  border-top: 3px solid;
  transition: all 0.2s ease;
}
.node-card:hover {
  transform: translateY(-3px);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.4);
}
.node-card.selected {
  box-shadow: 0 0 0 2px var(--p-primary-color);
}

.node-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}
.role-badge {
  color: white;
  font-size: 10px;
  font-weight: 800;
  padding: 2px 8px;
  border-radius: 4px;
  letter-spacing: 0.5px;
}
.node-num { font-size: 11px; color: var(--p-text-muted-color); }

.node-body {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 6px;
}
.node-body i { color: var(--p-text-muted-color); font-size: 14px; }
.node-name { font-weight: 700; font-size: 14px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

.node-footer {
  font-size: 11px;
  color: var(--p-text-muted-color);
  display: flex;
  gap: 2px;
}
.node-host { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.node-port { flex-shrink: 0; }

.btn-remove {
  position: absolute;
  top: -6px;
  right: -6px;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--p-surface-border);
  border: none;
  color: var(--p-text-muted-color);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 10px;
  opacity: 0;
  transition: opacity 0.15s;
}
.node-card:hover .btn-remove { opacity: 1; }
.btn-remove:hover { background: var(--p-red-400); color: white; }

.arrow-connector {
  flex-shrink: 0;
  padding: 0 2px;
}
.flow-line {
  animation: flow-dash 1.5s ease forwards;
}
@keyframes flow-dash {
  from { stroke-dashoffset: 100; }
  to { stroke-dashoffset: 0; }
}

.drop-zone {
  flex-shrink: 0;
  width: 120px;
  height: 100px;
  border: 2px dashed var(--p-surface-border);
  border-radius: 12px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  color: var(--p-text-muted-color);
  font-size: 12px;
  transition: all 0.2s;
  margin-left: 8px;
}
.drop-zone:hover, .drop-zone.drop-active {
  border-color: var(--p-primary-color);
  background: color-mix(in srgb, var(--p-primary-color) 8%, transparent);
  transform: scale(1.03);
}
.drop-zone i { font-size: 18px; }

/* Drag-over canvas glow */
.canvas.drag-over {
  background: color-mix(in srgb, var(--p-primary-color) 3%, transparent);
  transition: background 0.2s;
}
.pulse-icon {
  animation: iconPulse 0.8s ease-in-out infinite;
  color: var(--p-primary-color) !important;
}
@keyframes iconPulse {
  0%, 100% { transform: scale(1); opacity: 1; }
  50% { transform: scale(1.2); opacity: 0.6; }
}

/* Insert indicator between nodes */
.node-wrapper.drop-target {
  position: relative;
}
.insert-indicator {
  position: absolute;
  left: -8px;
  top: 0;
  bottom: 0;
  width: 3px;
  background: var(--p-primary-color);
  border-radius: 2px;
  animation: glowPulse 0.8s ease-in-out infinite;
  z-index: 5;
}
@keyframes glowPulse {
  0%, 100% { box-shadow: 0 0 4px var(--p-primary-color); }
  50% { box-shadow: 0 0 12px var(--p-primary-color); }
}
</style>

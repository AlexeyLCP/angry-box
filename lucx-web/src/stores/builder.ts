import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Server } from '@/types/server'

export interface ClientInboundSpec {
  client_id: string
  security: string
  reality_key: string
  reality_pub: string
  server_name: string
  password: string
  port: number
  transport: string
  xhttp_host: string
  xhttp_path: string
  xhttp_mode: string
  fingerprint: string
}

const defaultClientInbound = (): ClientInboundSpec => ({
  client_id: '',
  security: 'reality',
  reality_key: '',
  reality_pub: '',
  server_name: 'discord.com',
  password: '',
  port: 443,
  transport: 'xhttp',
  xhttp_host: 'discord.com',
  xhttp_path: '/download',
  xhttp_mode: 'packet-up',
  fingerprint: 'chrome',
})

export interface HopInboundSpec {
  client_id: string
  port: number
  transport: string
  xhttp_mode: string
}

const defaultHopInbound = (port = 443): HopInboundSpec => ({
  client_id: '',
  port,
  transport: 'xhttp',
  xhttp_mode: 'stream-one',
})

export interface OutboundSpec {
  address: string
  port: number
}

export interface ChainNodeDraft {
  server: Server
  role: 'entry' | 'hop' | 'exit'
  clientInbound: ClientInboundSpec
  hopInbound: HopInboundSpec
  outbound: OutboundSpec
}

export const useBuilderStore = defineStore('builder', () => {
  const name = ref('')
  const nodes = ref<ChainNodeDraft[]>([])
  const editingIndex = ref<number | null>(null)

  const canApply = computed(() => nodes.value.length >= 2)
  const editingNode = computed(() => {
    if (editingIndex.value === null) return null
    return nodes.value[editingIndex.value] ?? null
  })

  const addNode = (server: Server) => {
    nodes.value.push({
      server,
      role: nodes.value.length === 0 ? 'entry' : 'hop',
      clientInbound: defaultClientInbound(),
      hopInbound: defaultHopInbound(),
      outbound: { address: '', port: 0 },
    })
    reassignRoles()
  }

  const removeNode = (index: number) => {
    nodes.value.splice(index, 1)
    if (editingIndex.value === index) editingIndex.value = null
    else if (editingIndex.value !== null && editingIndex.value > index) editingIndex.value--
    reassignRoles()
  }

  const reorder = (from: number, to: number) => {
    const [item] = nodes.value.splice(from, 1)
    nodes.value.splice(to, 0, item)
    editingIndex.value = to
    reassignRoles()
  }

  const editNode = (index: number) => { editingIndex.value = index }
  const closeEditor = () => { editingIndex.value = null }

  const updateClientInbound = (index: number, spec: Partial<ClientInboundSpec>) => {
    const node = nodes.value[index]
    if (node) Object.assign(node.clientInbound, spec)
  }

  const updateHopInbound = (index: number, spec: Partial<HopInboundSpec>) => {
    const node = nodes.value[index]
    if (node) Object.assign(node.hopInbound, spec)
  }

  const updateOutbound = (index: number, spec: Partial<OutboundSpec>) => {
    const node = nodes.value[index]
    if (node) Object.assign(node.outbound, spec)
  }

  const reassignRoles = () => {
    nodes.value.forEach((n, i) => {
      if (i === 0) n.role = 'entry'
      else if (i === nodes.value.length - 1) n.role = 'exit'
      else n.role = 'hop'
    })
  }

  const buildApiPayload = () => {
    return nodes.value.map((n, i) => {
      const role = i === 0 ? 'entry' : i === nodes.value.length - 1 ? 'exit' : 'hop'
      return {
        server_id: n.server.id,
        backend_type: 'xray',
        protocol: 'vless',
        position: i,
        role,
        inbound_spec: JSON.stringify(n.clientInbound),
        hop_inbound_spec: JSON.stringify(n.hopInbound),
        outbound_spec: JSON.stringify(n.outbound),
      }
    })
  }

  const buildPreview = () => {
    return nodes.value.map((n, i) => {
      const role = i === 0 ? 'entry' : i === nodes.value.length - 1 ? 'exit' : 'hop'
      const port = role === 'entry' ? n.clientInbound.port : n.hopInbound.port
      const security = role === 'entry' ? n.clientInbound.security : 'none'
      return {
        server: n.server.name,
        role,
        port,
        security,
        transport: role === 'entry' ? n.clientInbound.transport : n.hopInbound.transport,
        nextOverride: n.outbound.address
          ? `${n.outbound.address}:${n.outbound.port || 'auto'}`
          : null,
      }
    })
  }

  const reset = () => {
    name.value = ''
    nodes.value = []
    editingIndex.value = null
  }

  return {
    name, nodes, editingIndex, canApply, editingNode,
    addNode, removeNode, reorder, editNode, closeEditor,
    updateClientInbound, updateHopInbound, updateOutbound,
    buildApiPayload, buildPreview, reset,
  }
})

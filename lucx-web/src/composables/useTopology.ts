import type { Server } from '@/types/server'
import type { Chain } from '@/types/chain'

export interface LayoutNode {
  server: Server
  x: number
  y: number
  angle: number
  chainCount: number
}

export interface Connection {
  from: string
  to: string
  chainIds: string[]
  path: string
}

/**
 * Compute circle layout positions for servers.
 */
export function computeLayout(servers: Server[], width: number, height: number): LayoutNode[] {
  const count = servers.length
  if (count === 0) return []

  const cx = width / 2
  const cy = height / 2
  const radius = Math.min(cx, cy) * 0.65

  return servers.map((server, i) => {
    const angle = (2 * Math.PI * i) / count - Math.PI / 2
    return {
      server,
      x: cx + radius * Math.cos(angle),
      y: cy + radius * Math.sin(angle),
      angle,
      chainCount: 0,
    }
  })
}

/**
 * Find connections between servers that share chains.
 */
export function getConnections(servers: Server[], chains: Chain[]): Map<string, string[]> {
  const pairs = new Map<string, string[]>()
  for (const chain of chains) {
    if (chain.status !== 'active') continue
    const ids = chain.nodes?.map((n) => n.server_id) ?? []
    for (let i = 0; i < ids.length - 1; i++) {
      const key = [ids[i], ids[i + 1]].sort().join('::')
      if (!pairs.has(key)) pairs.set(key, [])
      pairs.get(key)!.push(chain.id)
    }
  }
  return pairs
}

/**
 * Generate a curved SVG path between two points.
 */
export function curvedPath(x1: number, y1: number, x2: number, y2: number): string {
  const dx = x2 - x1
  const dy = y2 - y1
  const dist = Math.sqrt(dx * dx + dy * dy)
  if (dist < 1) return ''
  const offsetX = (-dy / dist) * (dist * 0.3)
  const offsetY = (dx / dist) * (dist * 0.3)
  const mx = (x1 + x2) / 2 + offsetX
  const my = (y1 + y2) / 2 + offsetY
  return `M ${x1} ${y1} Q ${mx} ${my} ${x2} ${y2}`
}

/**
 * Count how many chains a server participates in.
 */
export function chainCountForServer(serverId: string, chains: Chain[]): number {
  return chains.filter((c) => c.nodes?.some((n) => n.server_id === serverId)).length
}

import { useApi } from '@/composables/useApi'
import type { Server, HealthStatus, KeyPair } from '@/types/server'
import type { Chain } from '@/types/chain'

const api = useApi()

export const authApi = {
  login: (password: string) =>
    api.post<{ token: string }>('/api/v1/auth/login', { password }),
}

export const serversApi = {
  list: () => api.get<Server[]>('/api/v1/servers'),
  get: (id: string) => api.get<Server>(`/api/v1/servers/${id}`),
  create: (data: Partial<Server>) => api.post<Server>('/api/v1/servers', data),
  delete: (id: string) => api.del(`/api/v1/servers/${id}`),
  health: (id: string) =>
    api.get<HealthStatus>(`/api/v1/servers/${id}/health`),
  scan: (id: string) => api.post(`/api/v1/servers/${id}/scan`),
  install: (id: string) =>
    api.post<{ status: string; version: string; path: string }>(
      `/api/v1/servers/${id}/install`,
    ),
  generateKeys: (id: string) =>
    api.post<KeyPair>(`/api/v1/servers/${id}/x25519`),
}

export const chainsApi = {
  list: () => api.get<Chain[]>('/api/v1/chains'),
  get: (id: string) => api.get<Chain>(`/api/v1/chains/${id}`),
  create: (data: { name: string; nodes: unknown[] }) =>
    api.post<Chain>('/api/v1/chains', data),
  delete: (id: string) => api.del(`/api/v1/chains/${id}`),
  updateNode: (chainId: string, pos: number, inboundSpec: string, hopInboundSpec?: string) =>
    api.put(`/api/v1/chains/${chainId}/nodes/${pos}`, {
      inbound_spec: inboundSpec,
      hop_inbound_spec: hopInboundSpec || '',
    }),
  validate: (id: string) =>
    api.post<{ valid: boolean; error?: string }>(
      `/api/v1/chains/${id}/validate`,
    ),
  apply: (id: string) =>
    api.post<{ status: string }>(`/api/v1/chains/${id}/apply`),
  rollback: (id: string) =>
    api.post<{ status: string }>(`/api/v1/chains/${id}/rollback`),
  getConnectionLink: (id: string) =>
    api.get<{ config: string }>(`/api/v1/chains/${id}/config`),
}

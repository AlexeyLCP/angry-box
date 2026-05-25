export interface Chain {
  id: string
  name: string
  status: string
  applied_at: string | null
  created_at: string
  nodes: ChainNode[]
}

export interface ChainNode {
  chain_id: string
  server_id: string
  backend_type: string
  protocol: string
  position: number
  role: 'entry' | 'hop' | 'exit'
  inbound_spec: string
  hop_inbound_spec: string
  outbound_spec: string
  inbound_result: string
  outbound_result: string
}

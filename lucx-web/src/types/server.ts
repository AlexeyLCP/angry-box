export interface Server {
  id: string
  name: string
  host: string
  port: number
  username: string
  auth_method: string
  os: string
  arch: string
  status: string
  source: string
  tags: string[]
  last_seen: string | null
  created_at: string
}

export interface HealthStatus {
  online: boolean
  latency_ms: number
  xray_running: boolean
  xray_version: string
  error?: string
}

export interface KeyPair {
  private_key: string
  public_key: string
}

export interface StatusInfo {
  version: string
  go_version: string
  uptime: string
  os: string
  arch: string
  num_cpu: number
  pid: number
  db_path: string
}

export interface LogEntry {
  timestamp: string
  chain_id: string
  chain_name: string
  event: string
  status: string
}

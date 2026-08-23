export type Protocol = "tcp" | "udp"

export interface HealthCheck {
  enabled: boolean
  interval_seconds: number
  timeout_millis: number
  failure_threshold: number
  recovery_threshold: number
}

export interface Backend {
  id: string
  address: string
  port: number
  weight: number
  enabled: boolean
}

export interface Listener {
  id: string
  name: string
  enabled: boolean
  listen_address: string
  listen_port: number
  protocols: Protocol[]
  scheduler: "wrr" | "rr"
  affinity_seconds: number
  backends: Backend[]
}

export interface ProfileConfig {
  schema_version: number
  health_check: HealthCheck
  listeners: Listener[]
}

export interface Profile {
  id: string
  name: string
  description: string
  current_revision: number
  created_at: string
  updated_at: string
}

export interface Revision {
  id: number
  profile_id: string
  number: number
  config: ProfileConfig
  created_at: string
}

export interface NodeInfo {
  id: string
  name: string
  ingress_address: string
  profile_id: string
  desired_revision: number
  applied_revision: number
  agent_version: string
  status: "online" | "offline" | "error"
  last_seen_at?: string
  last_error?: string
}

export interface Status {
  version: string
  profiles: number
  nodes: number
  online_nodes: number
  listeners: number
}

export interface BackendHealth {
  node_id: string
  address: string
  state: "unknown" | "reachable" | "unreachable"
  consecutive_successes: number
  consecutive_failures: number
  latency_millis: number
  checked_at: string
}

export interface ServiceStat {
  node_id: string
  protocol: Protocol
  listen_address: string
  listen_port: number
  backend_address?: string
  backend_port?: number
  connections: number
  incoming_packets: number
  outgoing_packets: number
  incoming_bytes: number
  outgoing_bytes: number
  collected_at: string
}

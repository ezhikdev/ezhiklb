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
  auto_version: boolean
  version: string
  created_at: string
  updated_at: string
}

export interface Revision {
  id: number
  profile_id: string
  number: number
  version: string
  config: ProfileConfig
  created_at: string
}

export interface AuditEvent {
  id: number
  action: string
  target_type: string
  target_id: string
  details: string
  created_at: string
}

export interface NodeInfo {
  id: string
  name: string
  ingress_address: string
  observed_address: string
  profile_id: string
  desired_revision: number
  applied_revision: number
  agent_version: string
  status: "connecting" | "online" | "offline" | "error" | "disabled" | "deleting"
  apply_state: "waiting" | "applying" | "applied" | "error" | "disabled" | "decommissioning"
  last_seen_at?: string
  online_since?: string
  last_error?: string
  metrics?: NodeMetrics
  diagnostics?: NodeDiagnostics
  update_target?: string
  update_state?: "idle" | "requested" | "updating" | "completed" | "error"
  update_error?: string
}

export interface NodeDiagnostics {
  ipvs_available: boolean
  firewall_ready: boolean
  service_count: number
  destination_count: number
  error?: string
  checked_at: string
}

export interface NodeMetrics {
  ram_used_percent: number
  cpu_used_percent: number
  load_1: number
  cpu_cores: number
  network_rx_bps: number
  network_tx_bps: number
  active_ips: number
  collected_at: string
}

export interface NodeMetricPoint {
  node_id: string
  ram_used_percent: number
  cpu_used_percent: number
  load_1: number
  network_rx_bps: number
  network_tx_bps: number
  active_ips: number
  collected_at: string
}

export interface SystemSettings {
  panel_port: number
  agent_port: number
  legacy_panel_port?: number
  legacy_agent_port?: number
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

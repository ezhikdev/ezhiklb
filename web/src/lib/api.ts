import type { AuditEvent, BackendHealth, NodeInfo, NodeMetricPoint, Profile, ProfileConfig, Revision, ServiceStat, Status, SystemSettings } from "../types"

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...options,
    headers: { "Content-Type": "application/json", ...options?.headers },
  })
  if (!response.ok) {
    const body = await response.json().catch(() => null)
    throw new ApiError(response.status, body?.error?.message ?? `HTTP ${response.status}`)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

// The panel API always intends to return `[]` for an empty list, but a nil
// Go slice marshals to JSON `null`, and any future regression (server or
// network) could resend something malformed. Every list endpoint goes
// through this so a bad payload degrades to an empty list instead of
// crashing the whole render tree (`null.filter is not a function`).
async function requestArray<T>(path: string, options?: RequestInit): Promise<T[]> {
  const result = await request<T[] | null | undefined>(path, options)
  return Array.isArray(result) ? result : []
}

export const api = {
  login: (token: string) => request<{ authenticated: boolean }>("/api/v1/auth/login", { method: "POST", body: JSON.stringify({ token }) }),
  logout: () => request<void>("/api/v1/auth/logout", { method: "POST" }),
  status: () => request<Status>("/api/v1/status"),
  profiles: () => requestArray<Profile>("/api/v1/profiles"),
  profile: (id: string) => request<{ profile: Profile; revision: Revision }>(`/api/v1/profiles/${id}`),
  createProfile: (name: string, description: string, config: ProfileConfig, autoVersion = true, version = "") =>
    request<{ profile: Profile; revision: Revision }>("/api/v1/profiles", { method: "POST", body: JSON.stringify({ name, description, config, auto_version: autoVersion, version }) }),
  publishProfile: (id: string, name: string, description: string, config: ProfileConfig, autoVersion = true, version = "", resetConnections = false) =>
    request<{ profile: Profile; revision: Revision }>(`/api/v1/profiles/${id}`, { method: "PUT", body: JSON.stringify({ name, description, config, auto_version: autoVersion, version, reset_connections: resetConnections }) }),
  revisions: (id: string) => requestArray<Revision>(`/api/v1/profiles/${id}/revisions`),
  rollbackProfile: (id: string, number: number) => request<{ profile: Profile; revision: Revision }>(`/api/v1/profiles/${id}/rollback/${number}`, { method: "POST" }),
  cloneProfile: (id: string, name: string) => request<{ profile: Profile; revision: Revision }>(`/api/v1/profiles/${id}/clone`, { method: "POST", body: JSON.stringify({ name }) }),
  deleteProfile: (id: string) => request<void>(`/api/v1/profiles/${id}`, { method: "DELETE" }),
  nodes: () => requestArray<NodeInfo>("/api/v1/nodes"),
  createNode: (name: string, ingressAddress: string, profileID: string) => request<{ node: NodeInfo; agent_token: string }>("/api/v1/nodes", { method: "POST", body: JSON.stringify({ name, ingress_address: ingressAddress, profile_id: profileID }) }),
  updateNode: (id: string, name: string, ingressAddress: string) => request<void>(`/api/v1/nodes/${id}`, { method: "PUT", body: JSON.stringify({ name, ingress_address: ingressAddress }) }),
  deleteNode: (id: string) => request<void>(`/api/v1/nodes/${id}`, { method: "DELETE" }),
  forceDeleteNode: (id: string) => request<void>(`/api/v1/nodes/${id}/force-delete`, { method: "POST" }),
  setNodeEnabled: (id: string, enabled: boolean) => request<void>(`/api/v1/nodes/${id}/enabled`, { method: "PUT", body: JSON.stringify({ enabled }) }),
  requestHealthProbe: (id: string) => request<{ health_probe: number }>(`/api/v1/nodes/${id}/health-probe`, { method: "POST" }),
  requestNodeUpdate: (id: string) => request<{ version: string }>(`/api/v1/nodes/${id}/update`, { method: "POST" }),
  health: () => requestArray<BackendHealth>("/api/v1/health"),
  stats: () => requestArray<ServiceStat>("/api/v1/stats"),
  metricHistory: (nodeID = "all") => requestArray<NodeMetricPoint>(`/api/v1/metrics/history?node_id=${encodeURIComponent(nodeID)}`),
  events: (filter = "all") => requestArray<AuditEvent>(`/api/v1/events?filter=${encodeURIComponent(filter)}`),
  settings: () => request<SystemSettings>("/api/v1/settings"),
  updateSettings: (settings: SystemSettings) => request<{ settings: SystemSettings; restarting: boolean }>("/api/v1/settings", { method: "PUT", body: JSON.stringify(settings) }),
  assignProfile: (nodeID: string, profileID: string) => request<void>(`/api/v1/nodes/${nodeID}/profile`, { method: "PUT", body: JSON.stringify({ profile_id: profileID }) }),
}

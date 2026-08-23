import type { BackendHealth, NodeInfo, Profile, ProfileConfig, Revision, ServiceStat, Status } from "../types"

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

export const api = {
  login: (token: string) => request<{ authenticated: boolean }>("/api/v1/auth/login", { method: "POST", body: JSON.stringify({ token }) }),
  logout: () => request<void>("/api/v1/auth/logout", { method: "POST" }),
  status: () => request<Status>("/api/v1/status"),
  profiles: () => request<Profile[]>("/api/v1/profiles"),
  profile: (id: string) => request<{ profile: Profile; revision: Revision }>(`/api/v1/profiles/${id}`),
  createProfile: (name: string, description: string, config: ProfileConfig) =>
    request<{ profile: Profile; revision: Revision }>("/api/v1/profiles", { method: "POST", body: JSON.stringify({ name, description, config }) }),
  publishProfile: (id: string, name: string, description: string, config: ProfileConfig) =>
    request<{ profile: Profile; revision: Revision }>(`/api/v1/profiles/${id}`, { method: "PUT", body: JSON.stringify({ name, description, config }) }),
  nodes: () => request<NodeInfo[]>("/api/v1/nodes"),
  health: () => request<BackendHealth[]>("/api/v1/health"),
  stats: () => request<ServiceStat[]>("/api/v1/stats"),
  assignProfile: (nodeID: string, profileID: string) => request<void>(`/api/v1/nodes/${nodeID}/profile`, { method: "PUT", body: JSON.stringify({ profile_id: profileID }) }),
}

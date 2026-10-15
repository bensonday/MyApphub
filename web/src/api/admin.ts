import type { AppVersion, Platform, PublicApp } from '../types'

export interface ManagedApp extends PublicApp { versions: AppVersion[] }
export interface TrashApp { slug: string; name: string; deleted_at: string }
export interface TrashVersion { id: string; slug: string; platform: Platform; version_name: string; deleted_at: string }

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: 'same-origin',
    headers: { Accept: 'application/json', ...(init.body ? { 'Content-Type': 'application/json' } : {}), ...init.headers },
  })
  const value = await response.json().catch(() => ({})) as { code?: string; message?: string }
  if (!response.ok) throw new Error(value.message || `请求失败（${response.status}）`)
  return value as T
}

export const adminAPI = {
  session: () => request<{ authenticated: boolean }>('/api/admin/session'),
  login: (token: string) => request<{ ok: boolean }>('/api/admin/login', { method: 'POST', body: JSON.stringify({ token }) }),
  logout: () => request<{ ok: boolean }>('/api/admin/logout', { method: 'POST' }),
  apps: () => request<{ apps: ManagedApp[] }>('/api/admin/apps'),
  trash: () => request<{ apps: TrashApp[]; versions: TrashVersion[] }>('/api/admin/trash'),
  promote: (slug: string, versionId: string) => request(`/api/admin/apps/${slug}/versions/${versionId}/promote`, { method: 'POST' }),
  deleteVersion: (slug: string, versionId: string) => request(`/api/admin/apps/${slug}/versions/${versionId}`, { method: 'DELETE' }),
  restoreVersion: (slug: string, versionId: string) => request(`/api/admin/apps/${slug}/versions/${versionId}/restore`, { method: 'POST' }),
  deleteApp: (slug: string) => request(`/api/admin/apps/${slug}`, { method: 'DELETE' }),
  restoreApp: (slug: string) => request(`/api/admin/apps/${slug}/restore`, { method: 'POST' }),
  password: (slug: string, password: string) => request(`/api/admin/apps/${slug}/password`, { method: 'PUT', body: JSON.stringify({ password }) }),
}

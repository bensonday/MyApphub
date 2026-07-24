import type { AccessResponse, Platform, ProjectConfigResponse, PublicApp, PublicAppResponse, UdidResultResponse, UdidSessionResponse } from '../types'
import { demoAccess, demoApps } from './demo'

const apiBase = (import.meta.env.VITE_API_BASE_URL ?? '').trim().replace(/\/$/, '')
const demoEnabled = import.meta.env.MODE === 'demo'

export function apiUrl(path: string, base = apiBase): string {
  if (/^https?:\/\//i.test(path)) return path
  const normalizedBase = base.trim().replace(/\/$/, '')
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  return `${normalizedBase}${normalizedPath}`
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public app?: PublicApp,
    public retryAfterSeconds?: number,
  ) { super(message) }
}

async function request<T>(path: string, init: RequestInit = {}, timeoutMs = 12_000): Promise<T> {
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs)
  try {
    const response = await fetch(apiUrl(path), {
      ...init,
      headers: { Accept: 'application/json', ...init.headers },
      signal: controller.signal,
    })
    const value = await response.json().catch(() => ({})) as { code?: string; message?: string; app?: PublicApp }
    if (!response.ok) {
      const retryAfter = Number.parseInt(response.headers.get('Retry-After') ?? '', 10)
      throw new ApiError(
        response.status,
        value.code ?? 'request_failed',
        value.message ?? 'Request failed',
        value.app,
        Number.isFinite(retryAfter) && retryAfter > 0 ? retryAfter : undefined,
      )
    }
    return value as T
  } catch (error) {
    if (error instanceof ApiError) throw error
    if (error instanceof DOMException && error.name === 'AbortError') throw new ApiError(408, 'timeout', 'Request timed out')
    throw new ApiError(0, 'network_error', 'Network unavailable')
  } finally {
    window.clearTimeout(timeout)
  }
}

export const api = {
  async getConfig(): Promise<ProjectConfigResponse> {
    if (demoEnabled) return { projectName: 'MyApphub' }
    return request('/api/public/config')
  },
  async getApp(slug: string, platform?: Platform): Promise<PublicAppResponse> {
	if (demoEnabled && demoApps[slug]) return structuredClone(demoApps[slug])
	const query = platform ? `?platform=${encodeURIComponent(platform)}` : ''
	return request(`/api/public/apps/${encodeURIComponent(slug)}${query}`)
  },
  async access(slug: string, password = '', platform?: Platform): Promise<AccessResponse> {
    if (demoEnabled && demoApps[slug]) {
      if (demoApps[slug].app.passwordProtected && password !== 'demo2026') throw new ApiError(401, 'invalid_password', 'Invalid password')
	  return demoAccess(slug, platform)
    }
    return request(`/api/public/apps/${encodeURIComponent(slug)}/access`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
	  body: JSON.stringify({ password, platform }),
    })
  },
  createUdidSession: () => request<UdidSessionResponse>('/api/udid/sessions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }),
  getUdidResult: (challenge: string) => request<UdidResultResponse>(`/api/udid/result/${encodeURIComponent(challenge)}`, { cache: 'no-store' }),
  deleteUdidResult: (challenge: string) => request<{ deleted: boolean }>(`/api/udid/result/${encodeURIComponent(challenge)}`, { method: 'DELETE' }),
}

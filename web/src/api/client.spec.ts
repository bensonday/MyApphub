import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, apiUrl } from './client'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
  vi.resetModules()
})

describe('API error metadata', () => {
  it('joins a separately deployed API origin without corrupting absolute URLs', () => {
    expect(apiUrl('/api/public/apps/demo', 'https://api.example.com/')).toBe('https://api.example.com/api/public/apps/demo')
    expect(apiUrl('assets/demo/icon.png', 'https://api.example.com')).toBe('https://api.example.com/assets/demo/icon.png')
    expect(apiUrl('https://downloads.example.com/app.apk', 'https://api.example.com')).toBe('https://downloads.example.com/app.apk')
  })

  it('uses VITE_API_BASE_URL for production API requests', async () => {
    vi.stubEnv('VITE_API_BASE_URL', 'https://api.example.com/')
    vi.resetModules()
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const { api: configuredApi } = await import('./client')

    await configuredApi.getApp('demo app')

    expect(fetchMock).toHaveBeenCalledWith('https://api.example.com/api/public/apps/demo%20app', expect.any(Object))
  })

  it('loads public project configuration from the API origin', async () => {
    vi.stubEnv('VITE_API_BASE_URL', 'https://api.example.com/')
    vi.resetModules()
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"projectName":"Community Hub"}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)
    const { api: configuredApi } = await import('./client')

    await expect(configuredApi.getConfig()).resolves.toEqual({ projectName: 'Community Hub' })
    expect(fetchMock).toHaveBeenCalledWith('https://api.example.com/api/public/config', expect.any(Object))
  })

  it('keeps public app details and Retry-After from an error response', async () => {
    const app = {
      schemaVersion: 1,
      slug: 'waiting-app',
      platform: 'ios' as const,
      identifier: 'com.example.waiting',
      name: 'Waiting App',
      passwordProtected: false,
      createdAt: '2026-01-01T00:00:00Z',
      updatedAt: '2026-01-01T00:00:00Z',
    }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: 'no_current_version',
      message: 'No installable version',
      app,
    }), { status: 503, headers: { 'Content-Type': 'application/json', 'Retry-After': '527' } })))

    const error = await api.getApp('waiting-app').catch((value: unknown) => value)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 503, code: 'no_current_version', app, retryAfterSeconds: 527 })
  })
})

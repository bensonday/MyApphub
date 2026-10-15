import type { Env } from './types'

export function json(value: unknown, status = 200, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store', ...headers } })
}

export function fail(status: number, code: string, message: string, headers: HeadersInit = {}): Response {
  return json({ code, message }, status, headers)
}

export async function bodyJSON(request: Request, maxBytes = 16_384): Promise<Record<string, unknown> | Response> {
  if (Number(request.headers.get('content-length') || 0) > maxBytes) return fail(413, 'body_too_large', 'Request body is too large')
  const raw = await request.text()
  if (raw.length > maxBytes) return fail(413, 'body_too_large', 'Request body is too large')
  try {
    const value: unknown = JSON.parse(raw)
    if (value && typeof value === 'object' && !Array.isArray(value)) return value as Record<string, unknown>
  } catch { /* handled below */ }
  return fail(400, 'invalid_json', 'Invalid JSON object')
}

export const nowISO = () => new Date().toISOString()
export const id = () => crypto.randomUUID()
export const text = (value: unknown, max: number) => typeof value === 'string' ? value.trim().slice(0, max) : ''
export const validSlug = (value: string) => /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(value) && value.length <= 100
export const tokenSecret = (env: Env) => env.APPHUB_HMAC_SECRET || ''
export function publicOrigin(request: Request, env: Env): string {
  return env.APPHUB_CANONICAL_ORIGIN || new URL(request.url).origin
}

export function securityHeaders(response: Response): Response {
  const headers = new Headers(response.headers)
  headers.set('X-Content-Type-Options', 'nosniff')
  headers.set('Referrer-Policy', 'no-referrer')
  headers.set('X-Frame-Options', 'DENY')
  headers.set('Content-Security-Policy', "default-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers })
}

export function clientIP(request: Request): string {
  return request.headers.get('CF-Connecting-IP') || 'unknown'
}

export function sameOrigin(request: Request): boolean {
  const origin = request.headers.get('Origin')
  return !origin || origin === new URL(request.url).origin
}

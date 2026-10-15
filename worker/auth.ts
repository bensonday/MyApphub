import { fail, json, sameOrigin, tokenSecret } from './common'
import type { Env } from './types'

const encoder = new TextEncoder()
const b64 = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes)).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '')
const unb64 = (value: string) => Uint8Array.from(atob(value.replaceAll('-', '+').replaceAll('_', '/')), c => c.charCodeAt(0))

function equal(a: string, b: string): boolean {
  const left = encoder.encode(a), right = encoder.encode(b)
  let diff = left.length ^ right.length
  for (let i = 0; i < Math.max(left.length, right.length); i++) diff |= (left[i] || 0) ^ (right[i] || 0)
  return diff === 0
}

async function hmac(secret: string, content: string): Promise<string> {
  const key = await crypto.subtle.importKey('raw', encoder.encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign'])
  return b64(new Uint8Array(await crypto.subtle.sign('HMAC', key, encoder.encode(content))))
}

export async function signClaims(secret: string, claims: Record<string, unknown>, ttlSeconds: number): Promise<string> {
  const payload = b64(encoder.encode(JSON.stringify({ ...claims, exp: Math.floor(Date.now() / 1000) + ttlSeconds })))
  return `${payload}.${await hmac(secret, payload)}`
}

export async function verifyClaims(secret: string, value: string): Promise<Record<string, unknown> | null> {
  if (!secret || value.length > 4096) return null
  const parts = value.split('.')
  if (parts.length !== 2 || !equal(parts[1], await hmac(secret, parts[0]))) return null
  try {
    const claims = JSON.parse(new TextDecoder().decode(unb64(parts[0]))) as Record<string, unknown>
    if (typeof claims.exp !== 'number' || claims.exp < Math.floor(Date.now() / 1000)) return null
    return claims
  } catch { return null }
}

export async function hashPassword(password: string): Promise<string> {
  if (password.length < 8 || password.length > 256) throw new Error('password must contain 8 to 256 characters')
  const salt = crypto.getRandomValues(new Uint8Array(16))
  const key = await crypto.subtle.importKey('raw', encoder.encode(password), 'PBKDF2', false, ['deriveBits'])
  const bits = await crypto.subtle.deriveBits({ name: 'PBKDF2', hash: 'SHA-256', salt, iterations: 600_000 }, key, 256)
  return `pbkdf2-sha256$600000$${b64(salt)}$${b64(new Uint8Array(bits))}`
}

export async function verifyPassword(password: string, encoded: string | null): Promise<boolean> {
  if (!encoded) return false
  const parts = encoded.split('$')
  if (parts.length !== 4 || parts[0] !== 'pbkdf2-sha256') return false
  const iterations = Number(parts[1])
  if (!Number.isInteger(iterations) || iterations < 100_000 || iterations > 1_000_000) return false
  try {
    const salt = unb64(parts[2])
    const key = await crypto.subtle.importKey('raw', encoder.encode(password), 'PBKDF2', false, ['deriveBits'])
    const actual = b64(new Uint8Array(await crypto.subtle.deriveBits({ name: 'PBKDF2', hash: 'SHA-256', salt, iterations }, key, 256)))
    return equal(actual, parts[3])
  } catch { return false }
}

const cookieName = 'apphub_admin'
export async function login(request: Request, env: Env): Promise<Response> {
  if (!sameOrigin(request)) return fail(403, 'invalid_origin', 'Invalid origin')
  const body = await request.json().catch(() => null) as { token?: string } | null
  if (!env.APPHUB_TOKEN || !body?.token || !equal(body.token, env.APPHUB_TOKEN)) return fail(401, 'unauthorized', 'Invalid administrator token')
  const token = await signClaims(tokenSecret(env), { purpose: 'admin' }, 8 * 3600)
  const secure = new URL(request.url).protocol === 'https:' ? ' Secure;' : ''
  return json({ ok: true }, 200, { 'Set-Cookie': `${cookieName}=${token}; HttpOnly;${secure} SameSite=Strict; Path=/; Max-Age=28800` })
}

export function logout(): Response {
  return json({ ok: true }, 200, { 'Set-Cookie': `${cookieName}=; HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age=0` })
}

export async function isAdmin(request: Request, env: Env): Promise<boolean> {
  const bearer = request.headers.get('Authorization')?.match(/^Bearer (.+)$/)?.[1]
  if (bearer && env.APPHUB_TOKEN && equal(bearer, env.APPHUB_TOKEN)) return true
  const cookie = request.headers.get('Cookie')?.split(';').map(x => x.trim()).find(x => x.startsWith(`${cookieName}=`))?.slice(cookieName.length + 1)
  if (!cookie) return false
  const claims = await verifyClaims(tokenSecret(env), cookie)
  return claims?.purpose === 'admin'
}

export async function requireAdmin(request: Request, env: Env): Promise<Response | null> {
  if (!sameOrigin(request)) return fail(403, 'invalid_origin', 'Invalid origin')
  return await isAdmin(request, env) ? null : fail(401, 'unauthorized', 'Administrator login required')
}

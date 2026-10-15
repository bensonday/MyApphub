import { signClaims, verifyClaims, verifyPassword } from './auth'
import { bodyJSON, clientIP, fail, json, publicOrigin, text, tokenSecret } from './common'
import { appBySlug, publicAppResponse, resolveRelease, versionById, variants } from './data'
import type { Env, Platform, VariantRow, VersionRow } from './types'

const escapeXML = (value: string) => value.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;').replaceAll("'", '&apos;')

export async function rateLimit(env: Env, key: string, limit: number, seconds: number): Promise<number> {
  const now = Date.now()
  const row = await env.DB.prepare('SELECT attempts, resets_at FROM rate_limits WHERE key = ?').bind(key).first<{ attempts: number; resets_at: string }>()
  if (!row || Date.parse(row.resets_at) <= now) {
    await env.DB.prepare('INSERT INTO rate_limits (key, attempts, resets_at) VALUES (?, 1, ?) ON CONFLICT(key) DO UPDATE SET attempts = 1, resets_at = excluded.resets_at')
      .bind(key, new Date(now + seconds * 1000).toISOString()).run()
    return 0
  }
  await env.DB.prepare('UPDATE rate_limits SET attempts = attempts + 1 WHERE key = ?').bind(key).run()
  return row.attempts >= limit ? Math.max(1, Math.ceil((Date.parse(row.resets_at) - now) / 1000)) : 0
}

export async function publicApp(request: Request, env: Env, slug: string): Promise<Response> {
  return publicAppResponse(env, slug, new URL(request.url).searchParams.get('platform'))
}

export async function access(request: Request, env: Env, slug: string): Promise<Response> {
  const body = await bodyJSON(request)
  if (body instanceof Response) return body
  const platform = text(body.platform, 16) as Platform | ''
  const release = await resolveRelease(env, slug, platform || undefined)
  if (!release) return fail(404, 'not_found', 'App or current version not found')
  const { app, variant, version } = release
  if (app.password_hash) {
    const retry = await rateLimit(env, `password:${clientIP(request)}:${slug}`, 5, 600)
    if (retry) return fail(429, 'rate_limited', 'Try again later', { 'Retry-After': String(retry) })
    if (!await verifyPassword(typeof body.password === 'string' ? body.password : '', app.password_hash)) return fail(401, 'invalid_password', 'Invalid password')
    await env.DB.prepare('DELETE FROM rate_limits WHERE key = ?').bind(`password:${clientIP(request)}:${slug}`).run()
  }
  const origin = publicOrigin(request, env)
  const claims = { slug, platform: variant.platform, versionId: version.id, passwordVersion: app.password_version }
  const downloadToken = await signClaims(tokenSecret(env), { ...claims, purpose: 'download' }, 7200)
  const downloadUrl = `${origin}/download/${encodeURIComponent(slug)}/${variant.platform}/${encodeURIComponent(version.id)}?token=${encodeURIComponent(downloadToken)}`
  let installUrl: string | undefined
  if (variant.platform === 'ios') {
    const manifestToken = await signClaims(tokenSecret(env), { ...claims, purpose: 'manifest' }, 7200)
    const manifestUrl = `${origin}/manifest/${encodeURIComponent(slug)}/ios/${encodeURIComponent(version.id)}?token=${encodeURIComponent(manifestToken)}`
    installUrl = `itms-services://?action=download-manifest&url=${encodeURIComponent(manifestUrl)}`
  }
  return json({ downloadUrl, installUrl, releaseNotes: version.release_notes || undefined, expiresAt: new Date(Date.now() + 7200_000).toISOString() })
}

async function releaseFromPath(env: Env, slug: string, platform: string, versionId: string): Promise<{ variant: VariantRow; version: VersionRow; passwordVersion: number; name: string } | null> {
  const app = await appBySlug(env, slug)
  if (!app) return null
  const variant = (await variants(env, slug)).find(row => row.platform === platform)
  const version = await versionById(env, slug, platform, versionId)
  return variant && version ? { variant, version, passwordVersion: app.password_version, name: app.name } : null
}

async function validInstallToken(request: Request, env: Env, purpose: string, slug: string, platform: string, versionId: string, passwordVersion: number): Promise<boolean> {
  const token = new URL(request.url).searchParams.get('token') || ''
  const claims = await verifyClaims(tokenSecret(env), token)
  return claims?.purpose === purpose && claims.slug === slug && claims.platform === platform && claims.versionId === versionId && claims.passwordVersion === passwordVersion
}

export async function download(request: Request, env: Env, slug: string, platform: string, versionId: string): Promise<Response> {
  const release = await releaseFromPath(env, slug, platform, versionId)
  if (!release) return fail(404, 'not_found', 'Version not found')
  if (!await validInstallToken(request, env, 'download', slug, platform, versionId, release.passwordVersion)) return fail(401, 'invalid_token', 'Invalid or expired token')
  const { version } = release
  const baseHeaders = new Headers({ 'Content-Type': platform === 'ios' ? 'application/octet-stream' : 'application/vnd.android.package-archive',
    'Content-Disposition': `attachment; filename="${slug}-${version.version_name.replace(/[^a-zA-Z0-9._-]/g, '_')}.${platform === 'ios' ? 'ipa' : 'apk'}"`,
    'Cache-Control': 'private, no-store', 'Accept-Ranges': 'bytes', 'ETag': `"${version.sha256}"` })
  const range = request.headers.get('Range')
  let offset = 0, length = version.file_size, status = 200
  if (range) {
    const match = /^bytes=(\d*)-(\d*)$/.exec(range)
    if (!match || (!match[1] && !match[2])) return new Response(null, { status: 416, headers: { 'Content-Range': `bytes */${version.file_size}` } })
    if (match[1]) {
      offset = Number(match[1])
      const end = match[2] ? Number(match[2]) : version.file_size - 1
      length = Math.min(end, version.file_size - 1) - offset + 1
    } else {
      length = Math.min(Number(match[2]), version.file_size)
      offset = version.file_size - length
    }
    if (!Number.isSafeInteger(offset) || !Number.isSafeInteger(length) || length <= 0 || offset >= version.file_size)
      return new Response(null, { status: 416, headers: { 'Content-Range': `bytes */${version.file_size}` } })
    status = 206
    baseHeaders.set('Content-Range', `bytes ${offset}-${offset + length - 1}/${version.file_size}`)
  }
  baseHeaders.set('Content-Length', String(length))
  if (request.method === 'HEAD') return new Response(null, { status, headers: baseHeaders })
  const object = await env.BUCKET.get(version.package_key, { range: { offset, length } })
  if (!object) return fail(503, 'package_unavailable', 'Package is temporarily unavailable')
  return new Response(object.body, { status, headers: baseHeaders })
}

export async function manifest(request: Request, env: Env, slug: string, platform: string, versionId: string): Promise<Response> {
  const release = await releaseFromPath(env, slug, platform, versionId)
  if (!release || platform !== 'ios') return fail(404, 'not_found', 'Version not found')
  if (!await validInstallToken(request, env, 'manifest', slug, platform, versionId, release.passwordVersion)) return fail(401, 'invalid_token', 'Invalid or expired token')
  const token = await signClaims(tokenSecret(env), { purpose: 'download', slug, platform, versionId, passwordVersion: release.passwordVersion }, 7200)
  const downloadURL = `${new URL(request.url).origin}/download/${encodeURIComponent(slug)}/ios/${encodeURIComponent(versionId)}?token=${encodeURIComponent(token)}`
  const xml = `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>items</key><array><dict><key>assets</key><array><dict><key>kind</key><string>software-package</string><key>url</key><string>${escapeXML(downloadURL)}</string></dict></array><key>metadata</key><dict><key>bundle-identifier</key><string>${escapeXML(release.variant.identifier)}</string><key>bundle-version</key><string>${escapeXML(release.version.version_name)}</string><key>kind</key><string>software</string><key>title</key><string>${escapeXML(release.name)}</string></dict></dict></array></dict></plist>`
  return new Response(xml, { headers: { 'Content-Type': 'application/xml; charset=utf-8', 'Cache-Control': 'private, no-store' } })
}

export async function icon(env: Env, slug: string, platform: string, name: string): Promise<Response> {
  const app = await appBySlug(env, slug)
  if (!app) return fail(404, 'not_found', 'Icon not found')
  const variant = (await variants(env, slug)).find(row => row.platform === platform)
  if (!variant?.icon_key || name !== `icon-${variant.icon_key.split('/').at(-1)}`) return fail(404, 'not_found', 'Icon not found')
  const object = await env.BUCKET.get(variant.icon_key)
  if (!object) return fail(404, 'not_found', 'Icon not found')
  const type = variant.icon_key.endsWith('.png') ? 'image/png' : variant.icon_key.endsWith('.webp') ? 'image/webp' : 'image/jpeg'
  return new Response(object.body, { headers: { 'Content-Type': type, 'Cache-Control': 'public, max-age=31536000, immutable' } })
}

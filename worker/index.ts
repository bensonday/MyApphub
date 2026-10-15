import { isAdmin, login, logout, requireAdmin } from './auth'
import { bodyJSON, fail, json, securityHeaders, text, tokenSecret, validSlug } from './common'
import { appBySlug, deleteApp, deleteVersion, listApps, promote, restoreApp, restoreVersion, trash, updatePassword } from './data'
import { access, download, icon, manifest, publicApp } from './public'
import { abortUpload, beginUpload, finishUpload, setVariantIcon, uploadIcon, uploadPart } from './upload'
import { callback, createSession, deleteResult, getProfile, getResult } from './udid'
import type { Env, UploadRow, VersionRow } from './types'

async function route(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url)
  const path = url.pathname
  const method = request.method
  if (path === '/api/public/config' && method === 'GET') return json({ projectName: env.APPHUB_PROJECT_NAME || 'MyApphub' }, 200, { 'Cache-Control': 'public, max-age=300' })
  if (path === '/healthz' && method === 'GET') return json({ ok: true })
  if (!env.APPHUB_TOKEN || new TextEncoder().encode(env.APPHUB_TOKEN).length < 32 ||
      new TextEncoder().encode(tokenSecret(env)).length < 32 || env.APPHUB_TOKEN === tokenSecret(env))
    return fail(503, 'not_configured', 'Service secrets are not configured')

  if (path === '/api/admin/login' && method === 'POST') return login(request, env)
  if (path === '/api/admin/logout' && method === 'POST') return logout()
  if (path === '/api/admin/session' && method === 'GET') return json({ authenticated: await isAdmin(request, env) })

  const publicMatch = /^\/api\/public\/apps\/([a-z0-9-]+)(?:\/(access))?$/.exec(path)
  if (publicMatch && validSlug(publicMatch[1])) {
    if (!publicMatch[2] && method === 'GET') return publicApp(request, env, publicMatch[1])
    if (publicMatch[2] && method === 'POST') return access(request, env, publicMatch[1])
  }

  const fileMatch = /^\/(download|manifest)\/([a-z0-9-]+)\/(ios|android)\/([a-zA-Z0-9-]+)$/.exec(path)
  if (fileMatch && validSlug(fileMatch[2])) {
    if (fileMatch[1] === 'download' && (method === 'GET' || method === 'HEAD')) return download(request, env, fileMatch[2], fileMatch[3], fileMatch[4])
    if (fileMatch[1] === 'manifest' && method === 'GET') return manifest(request, env, fileMatch[2], fileMatch[3], fileMatch[4])
  }
  const iconMatch = /^\/assets\/([a-z0-9-]+)\/(ios|android)\/([^/]+)$/.exec(path)
  if (iconMatch && method === 'GET') return icon(env, iconMatch[1], iconMatch[2], iconMatch[3])
  if (path.startsWith('/assets/')) return env.ASSETS.fetch(request)

  if (path === '/api/udid/sessions' && method === 'POST') return createSession(request, env)
  if (path === '/api/udid/callback' && (method === 'PUT' || method === 'POST')) return callback(request, env)
  const udidMatch = /^\/api\/udid\/(profile|result)\/([a-f0-9]{64})$/.exec(path)
  if (udidMatch) {
    if (udidMatch[1] === 'profile' && method === 'GET') return getProfile(request, env, udidMatch[2])
    if (udidMatch[1] === 'result' && method === 'GET') return getResult(env, udidMatch[2])
    if (udidMatch[1] === 'result' && method === 'DELETE') return deleteResult(env, udidMatch[2])
  }

  if (path.startsWith('/api/admin/') || path.startsWith('/api/v1/')) {
    const denied = await requireAdmin(request, env)
    if (denied) return denied
    if ((path === '/api/admin/apps' || path === '/api/v1/apps') && method === 'GET') return json({ apps: await listApps(env) })
    if (path === '/api/admin/trash' && method === 'GET') return trash(env)
    if (path === '/api/v1/uploads' && method === 'POST') return beginUpload(request, env)
    const uploadMatch = /^\/api\/v1\/uploads\/([a-f0-9-]+)(?:\/(parts\/([0-9]+)|icon|complete))?$/.exec(path)
    if (uploadMatch) {
      if (uploadMatch[2]?.startsWith('parts/') && method === 'PUT') return uploadPart(request, env, uploadMatch[1], uploadMatch[3])
      if (uploadMatch[2] === 'icon' && method === 'PUT') return uploadIcon(request, env, uploadMatch[1])
      if (uploadMatch[2] === 'complete' && method === 'POST') return finishUpload(request, env, uploadMatch[1])
      if (!uploadMatch[2] && method === 'DELETE') return abortUpload(env, uploadMatch[1])
    }
    const appMatch = /^\/api\/(?:admin|v1)\/apps\/([a-z0-9-]+)(?:\/(.*))?$/.exec(path)
    if (appMatch && validSlug(appMatch[1])) {
      const slug = appMatch[1], suffix = appMatch[2] || ''
      if (suffix === 'password' && method === 'PUT') {
        const body = await bodyJSON(request)
        if (body instanceof Response) return body
        return updatePassword(env, slug, typeof body.password === 'string' ? body.password : '')
      }
      if (!suffix && method === 'DELETE') return deleteApp(env, slug)
      if (suffix === 'restore' && method === 'POST') return restoreApp(env, slug)
      const variantIcon = /^(ios|android)\/icon$/.exec(suffix)
      if (variantIcon && method === 'PUT') return setVariantIcon(request, env, slug, variantIcon[1] as 'ios' | 'android')
      const versionMatch = /^versions\/([a-f0-9-]+)(?:\/(promote|restore))?$/.exec(suffix)
      if (versionMatch) {
        if (versionMatch[2] === 'promote' && method === 'POST') return promote(env, slug, versionMatch[1])
        if (versionMatch[2] === 'restore' && method === 'POST') return restoreVersion(env, slug, versionMatch[1])
        if (!versionMatch[2] && method === 'DELETE') return deleteVersion(env, slug, versionMatch[1])
      }
    }
  }
  if (/^\/(api|download|manifest)(\/|$)/.test(path)) return fail(404, 'not_found', 'Not found')
  return env.ASSETS.fetch(request)
}

async function cleanup(env: Env): Promise<void> {
  const now = new Date().toISOString()
  const expired = (await env.DB.prepare('SELECT * FROM uploads WHERE expires_at < ? LIMIT 100').bind(now).all<UploadRow>()).results
  for (const row of expired) {
    try { await env.BUCKET.resumeMultipartUpload(row.object_key, row.r2_upload_id).abort() } catch { /* upload may already be gone */ }
    if (row.icon_key) await env.BUCKET.delete(row.icon_key)
    await env.DB.prepare('DELETE FROM uploads WHERE id = ?').bind(row.id).run()
  }
  await env.DB.prepare('DELETE FROM udid_sessions WHERE expires_at < ?').bind(now).run()
  await env.DB.prepare('DELETE FROM udid_callback_diagnostics WHERE created_at < ?').bind(new Date(Date.now() - 86400_000).toISOString()).run()
  await env.DB.prepare('DELETE FROM rate_limits WHERE resets_at < ?').bind(now).run()
  const cutoff = new Date(Date.now() - 7 * 86400_000).toISOString()
  const oldVersions = (await env.DB.prepare('SELECT * FROM versions WHERE deleted_at < ? LIMIT 100').bind(cutoff).all<VersionRow>()).results
  for (const version of oldVersions) {
    await env.BUCKET.delete(version.package_key)
    await env.DB.prepare('DELETE FROM versions WHERE id = ?').bind(version.id).run()
  }
  const oldApps = (await env.DB.prepare('SELECT slug FROM apps WHERE deleted_at < ? LIMIT 100').bind(cutoff).all<{ slug: string }>()).results
  for (const app of oldApps) {
    const versions = (await env.DB.prepare('SELECT * FROM versions WHERE slug = ?').bind(app.slug).all<VersionRow>()).results
    for (const version of versions) await env.BUCKET.delete(version.package_key)
    const icons = (await env.DB.prepare('SELECT icon_key FROM variants WHERE slug = ?').bind(app.slug).all<{ icon_key: string | null }>()).results
    for (const icon of icons) if (icon.icon_key) await env.BUCKET.delete(icon.icon_key)
    await env.DB.batch([
      env.DB.prepare('DELETE FROM versions WHERE slug = ?').bind(app.slug),
      env.DB.prepare('DELETE FROM variants WHERE slug = ?').bind(app.slug),
      env.DB.prepare('DELETE FROM apps WHERE slug = ?').bind(app.slug),
    ])
  }
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url)
    if ((request.method === 'GET' || request.method === 'HEAD') &&
        url.hostname === 'myapphub.bensonday.workers.dev' &&
        !url.pathname.startsWith('/api/') && !url.pathname.startsWith('/download/') &&
        !url.pathname.startsWith('/manifest/') && !url.pathname.startsWith('/assets/')) {
      return Response.redirect(`${env.APPHUB_CANONICAL_ORIGIN || 'https://apphub.bensonday.cc'}${url.pathname}${url.search}`, 308)
    }
    try { return securityHeaders(await route(request, env)) }
    catch (error) {
      console.error('request failed', error instanceof Error ? error.message : 'unknown error')
      return securityHeaders(fail(500, 'internal_error', 'Internal server error'))
    }
  },
  async scheduled(_event: ScheduledEvent, env: Env): Promise<void> {
    try { await cleanup(env) } catch (error) { console.error('cleanup failed', error instanceof Error ? error.message : 'unknown error') }
  },
}

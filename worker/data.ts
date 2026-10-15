import { fail, json, nowISO, validSlug } from './common'
import { hashPassword } from './auth'
import type { AppRow, Env, Platform, VariantRow, VersionRow } from './types'

const platformOk = (value: string): value is Platform => value === 'ios' || value === 'android'
const slugify = (value: string) => value.toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 80) || 'app'

export function publicVersion(row: VersionRow) {
  return { schemaVersion: 1, id: row.id, versionName: row.version_name, buildNumber: row.build_number,
    packageFile: row.platform === 'ios' ? 'package.ipa' : 'package.apk', sha256: row.sha256, fileSize: row.file_size,
    minimumOsVersion: row.minimum_os_version || undefined, distributionType: row.distribution_type || undefined,
    provisioningExpiresAt: row.provisioning_expires_at || undefined, releaseNotes: row.release_notes || undefined,
    uploadedAt: row.uploaded_at }
}

export function publicApp(app: AppRow, variant: VariantRow) {
  return { schemaVersion: 1, slug: app.slug, name: app.name, platform: variant.platform, identifier: variant.identifier,
    icon: variant.icon_key ? `icon-${encodeURIComponent(variant.icon_key.split('/').at(-1) || '')}` : undefined,
    currentVersionId: variant.current_version_id || undefined, passwordProtected: Boolean(app.password_hash),
    createdAt: app.created_at, updatedAt: app.updated_at }
}

export async function appBySlug(env: Env, slug: string, includeDeleted = false): Promise<AppRow | null> {
  return env.DB.prepare(`SELECT * FROM apps WHERE slug = ? ${includeDeleted ? '' : 'AND deleted_at IS NULL'}`).bind(slug).first<AppRow>()
}

export async function variants(env: Env, slug: string): Promise<VariantRow[]> {
  return (await env.DB.prepare('SELECT * FROM variants WHERE slug = ? ORDER BY CASE platform WHEN \'ios\' THEN 0 ELSE 1 END').bind(slug).all<VariantRow>()).results
}

export async function versionById(env: Env, slug: string, platform: string, versionId: string, includeDeleted = false): Promise<VersionRow | null> {
  if (!platformOk(platform)) return null
  return env.DB.prepare(`SELECT * FROM versions WHERE id = ? AND slug = ? AND platform = ? ${includeDeleted ? '' : 'AND deleted_at IS NULL'}`)
    .bind(versionId, slug, platform).first<VersionRow>()
}

export async function listApps(env: Env): Promise<unknown[]> {
  const rows = (await env.DB.prepare('SELECT * FROM apps WHERE deleted_at IS NULL ORDER BY updated_at DESC').all<AppRow>()).results
  const result: unknown[] = []
  for (const app of rows) {
    for (const variant of await variants(env, app.slug)) {
      const versions = (await env.DB.prepare('SELECT * FROM versions WHERE slug = ? AND platform = ? AND deleted_at IS NULL ORDER BY uploaded_at DESC')
        .bind(app.slug, variant.platform).all<VersionRow>()).results
      result.push({ ...publicApp(app, variant), versions: versions.map(publicVersion) })
    }
  }
  return result
}

export async function publicAppResponse(env: Env, slug: string, requested: string | null): Promise<Response> {
  const app = await appBySlug(env, slug)
  if (!app) return fail(404, 'not_found', 'App not found')
  const variantRows = await variants(env, slug)
  const releases = []
  for (const variant of variantRows) {
    if (!variant.current_version_id) continue
    const version = await versionById(env, slug, variant.platform, variant.current_version_id)
    if (!version) continue
    const value = publicVersion(version)
    if (app.password_hash) value.releaseNotes = undefined
    releases.push({ app: publicApp(app, variant), version: value })
  }
  if (!releases.length) return json({ code: 'no_current_version', message: 'No installable version', app: publicApp(app, variantRows[0]) }, 503)
  const selected = releases.find(item => item.app.platform === requested) || releases[0]
  return json({ ...selected, variants: releases })
}

export async function resolveRelease(env: Env, slug: string, platform: string | undefined): Promise<{ app: AppRow; variant: VariantRow; version: VersionRow } | null> {
  const app = await appBySlug(env, slug)
  if (!app) return null
  const choices = await variants(env, slug)
  const selected = choices.find(v => v.platform === platform) || (!platform ? choices.find(v => v.current_version_id) : undefined)
  if (!selected?.current_version_id) return null
  const version = await versionById(env, slug, selected.platform, selected.current_version_id)
  return version ? { app, variant: selected, version } : null
}

export async function updatePassword(env: Env, slug: string, password: string): Promise<Response> {
  const app = await appBySlug(env, slug)
  if (!app) return fail(404, 'not_found', 'App not found')
  let hash: string | null = null
  try { if (password) hash = await hashPassword(password) }
  catch { return fail(400, 'invalid_password', 'Password must contain 8 to 256 characters') }
  await env.DB.prepare('UPDATE apps SET password_hash = ?, password_version = password_version + 1, updated_at = ? WHERE slug = ?')
    .bind(hash, nowISO(), slug).run()
  return json({ app: publicApp({ ...app, password_hash: hash }, (await variants(env, slug))[0]) })
}

export async function promote(env: Env, slug: string, versionId: string): Promise<Response> {
  const version = await env.DB.prepare('SELECT * FROM versions WHERE id = ? AND slug = ? AND deleted_at IS NULL').bind(versionId, slug).first<VersionRow>()
  const app = await appBySlug(env, slug)
  if (!version || !app) return fail(404, 'not_found', 'Version not found')
  const timestamp = nowISO()
  await env.DB.prepare('UPDATE variants SET current_version_id = ?, updated_at = ? WHERE slug = ? AND platform = ?')
    .bind(versionId, timestamp, slug, version.platform).run()
  await env.DB.prepare('UPDATE apps SET updated_at = ? WHERE slug = ?').bind(timestamp, slug).run()
  const variant = (await variants(env, slug)).find(v => v.platform === version.platform)!
  return json({ app: publicApp(app, { ...variant, current_version_id: versionId }) })
}

export async function deleteVersion(env: Env, slug: string, versionId: string): Promise<Response> {
  const version = await env.DB.prepare('SELECT * FROM versions WHERE id = ? AND slug = ? AND deleted_at IS NULL').bind(versionId, slug).first<VersionRow>()
  if (!version) return fail(404, 'not_found', 'Version not found')
  const variant = (await variants(env, slug)).find(v => v.platform === version.platform)
  if (variant?.current_version_id === versionId) return fail(409, 'current_version', 'Current version cannot be deleted')
  const deletedAt = nowISO()
  await env.DB.prepare('UPDATE versions SET deleted_at = ? WHERE id = ?').bind(deletedAt, versionId).run()
  return json({ deleted: true, recoverableUntil: new Date(Date.now() + 7 * 86400_000).toISOString() })
}

export async function restoreVersion(env: Env, slug: string, versionId: string): Promise<Response> {
  const version = await env.DB.prepare('SELECT * FROM versions WHERE id = ? AND slug = ? AND deleted_at IS NOT NULL').bind(versionId, slug).first<VersionRow>()
  if (!version) return fail(404, 'not_found', 'Deleted version not found')
  if (Date.now() - Date.parse(version.deleted_at!) > 7 * 86400_000) return fail(410, 'expired', 'Recovery period expired')
  await env.DB.prepare('UPDATE versions SET deleted_at = NULL WHERE id = ?').bind(versionId).run()
  const app = await appBySlug(env, slug, true)
  const variant = (await variants(env, slug)).find(v => v.platform === version.platform)
  return json({ app: app && variant ? publicApp(app, variant) : null })
}

export async function deleteApp(env: Env, slug: string): Promise<Response> {
  const app = await appBySlug(env, slug)
  if (!app) return fail(404, 'not_found', 'App not found')
  await env.DB.prepare('UPDATE apps SET deleted_at = ? WHERE slug = ?').bind(nowISO(), slug).run()
  return json({ deleted: true, recoverableUntil: new Date(Date.now() + 7 * 86400_000).toISOString() })
}

export async function restoreApp(env: Env, slug: string): Promise<Response> {
  const app = await appBySlug(env, slug, true)
  if (!app?.deleted_at) return fail(404, 'not_found', 'Deleted app not found')
  if (Date.now() - Date.parse(app.deleted_at) > 7 * 86400_000) return fail(410, 'expired', 'Recovery period expired')
  await env.DB.prepare('UPDATE apps SET deleted_at = NULL WHERE slug = ?').bind(slug).run()
  return json({ restored: true })
}

export async function trash(env: Env): Promise<Response> {
  const apps = (await env.DB.prepare('SELECT slug, name, deleted_at FROM apps WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC').all()).results
  const versions = (await env.DB.prepare('SELECT id, slug, platform, version_name, deleted_at FROM versions WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC').all()).results
  return json({ apps, versions })
}

export { slugify, platformOk, validSlug }

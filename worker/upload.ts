import { bodyJSON, fail, id, json, nowISO, text, validSlug } from './common'
import { appBySlug, platformOk, publicApp, publicVersion, slugify, variants } from './data'
import type { AppRow, Env, Platform, UploadMetadata, UploadRow, VariantRow, VersionRow } from './types'

export const PART_SIZE = 8 * 1024 * 1024
const MAX_PACKAGE = 1024 * 1024 * 1024
const SHA256 = /^[0-9a-f]{64}$/i

function metadataFrom(value: Record<string, unknown>): UploadMetadata | null {
  const platform = text(value.platform, 16)
  const name = text(value.name, 100)
  const identifier = text(value.identifier, 200)
  const versionName = text(value.versionName, 100)
  const buildNumber = text(value.buildNumber, 100)
  const slug = text(value.slug, 100)
  const sha256 = text(value.sha256, 64)
  const fileSize = Number(value.fileSize)
  if (!platformOk(platform) || !name || !/^[A-Za-z0-9._-]+$/.test(identifier) || !versionName || !buildNumber ||
      (slug && !validSlug(slug)) || !SHA256.test(sha256) || !Number.isSafeInteger(fileSize) || fileSize < 4 || fileSize > MAX_PACKAGE) return null
  if (platform === 'ios' && value.distributionType !== 'enterprise' && value.distributionType !== 'ad-hoc' && value.distributionType !== 'development') return null
  return { platform, name, identifier, versionName, buildNumber, slug, sha256: sha256.toLowerCase(), fileSize,
    minimumOsVersion: text(value.minimumOsVersion, 50), distributionType: text(value.distributionType, 30),
    provisioningExpiresAt: text(value.provisioningExpiresAt, 50), signingCertificateSha256: text(value.signingCertificateSha256, 64),
    releaseNotes: text(value.releaseNotes, 10_000), promote: value.promote === true, iconMime: text(value.iconMime, 30) }
}

async function upload(env: Env, uploadId: string): Promise<UploadRow | null> {
  const row = await env.DB.prepare('SELECT * FROM uploads WHERE id = ?').bind(uploadId).first<UploadRow>()
  return row && Date.parse(row.expires_at) > Date.now() ? row : null
}

export async function beginUpload(request: Request, env: Env): Promise<Response> {
  const body = await bodyJSON(request, 16_384)
  if (body instanceof Response) return body
  const metadata = metadataFrom(body)
  if (!metadata) return fail(400, 'invalid_metadata', 'Invalid package metadata')
  const identity = await env.DB.prepare('SELECT slug FROM variants WHERE platform = ? AND identifier = ?').bind(metadata.platform, metadata.identifier).first<{ slug: string }>()
  if (identity && metadata.slug && identity.slug !== metadata.slug) return fail(409, 'slug_conflict', 'Identifier already belongs to another app slug')
  let slug = identity?.slug || metadata.slug || slugify(metadata.name)
  if (!identity && !metadata.slug) {
    const base = slug
    for (let suffix = 2; suffix < 10_000; suffix++) {
      const variant = (await variants(env, slug)).find(item => item.platform === metadata.platform)
      if (!variant || variant.identifier === metadata.identifier) break
      slug = `${base.slice(0, 95 - String(suffix).length)}-${suffix}`
    }
  }
  const existing = await appBySlug(env, slug, true)
  if (existing?.deleted_at) return fail(409, 'app_deleted', 'Restore the app before uploading')
  const selected = (await variants(env, slug)).find(item => item.platform === metadata.platform)
  if (selected && selected.identifier !== metadata.identifier) return fail(409, 'identifier_conflict', 'Slug already belongs to another identifier')
  const versions = (await env.DB.prepare('SELECT * FROM versions WHERE slug = ? AND platform = ?').bind(slug, metadata.platform).all<VersionRow>()).results
  const duplicate = versions.find(version => version.sha256 === metadata.sha256)
  if (duplicate && existing && selected) return json({ duplicate: true, app: publicApp(existing, selected), version: publicVersion(duplicate) })
  if (versions.some(version => version.version_name === metadata.versionName && version.build_number === metadata.buildNumber))
    return fail(409, 'version_conflict', 'Version name and build number already exist')
  metadata.slug = slug
  const uploadId = id()
  const objectKey = `packages/${slug}/${metadata.platform}/${uploadId}.${metadata.platform === 'ios' ? 'ipa' : 'apk'}`
  const multipart = await env.BUCKET.createMultipartUpload(objectKey)
  try {
    await env.DB.prepare('INSERT INTO uploads (id, object_key, r2_upload_id, metadata_json, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)')
      .bind(uploadId, objectKey, multipart.uploadId, JSON.stringify(metadata), nowISO(), new Date(Date.now() + 24 * 3600_000).toISOString()).run()
  } catch (error) {
    await multipart.abort()
    throw error
  }
  return json({ id: uploadId, partSize: PART_SIZE }, 201)
}

export async function uploadPart(request: Request, env: Env, uploadId: string, rawPart: string): Promise<Response> {
  const row = await upload(env, uploadId)
  if (!row) return fail(404, 'upload_not_found', 'Upload not found or expired')
  const partNumber = Number(rawPart)
  if (!Number.isInteger(partNumber) || partNumber < 1 || partNumber > 10_000) return fail(400, 'invalid_part', 'Invalid part number')
  if (Number(request.headers.get('content-length') || 0) > PART_SIZE) return fail(413, 'part_too_large', 'Part exceeds 8 MiB')
  const bytes = new Uint8Array(await request.arrayBuffer())
  if (!bytes.length || bytes.length > PART_SIZE) return fail(413, 'part_too_large', 'Part exceeds 8 MiB')
  const metadata = JSON.parse(row.metadata_json) as UploadMetadata
  const expectedParts = Math.ceil(metadata.fileSize / PART_SIZE)
  if (partNumber > expectedParts || (partNumber < expectedParts && bytes.length !== PART_SIZE) ||
      (partNumber === expectedParts && bytes.length !== metadata.fileSize - (partNumber - 1) * PART_SIZE))
    return fail(400, 'invalid_part_size', 'Part size does not match upload metadata')
  if (partNumber === 1 && (bytes[0] !== 0x50 || bytes[1] !== 0x4b)) return fail(400, 'invalid_package', 'Package must be ZIP based')
  const expectedHash = request.headers.get('X-Part-SHA256')
  if (!expectedHash || !SHA256.test(expectedHash)) return fail(400, 'missing_checksum', 'Part SHA-256 is required')
  const actualHash = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(value => value.toString(16).padStart(2, '0')).join('')
  if (actualHash !== expectedHash.toLowerCase()) return fail(400, 'checksum_mismatch', 'Part SHA-256 does not match')
  const multipart = env.BUCKET.resumeMultipartUpload(row.object_key, row.r2_upload_id)
  const part = await multipart.uploadPart(partNumber, bytes)
  return json(part)
}

export function iconFormat(bytes: Uint8Array): { mime: string; extension: string } | null {
  const png = bytes.length >= 8 && [137, 80, 78, 71, 13, 10, 26, 10].every((value, index) => bytes[index] === value)
  const jpeg = bytes.length >= 3 && bytes[0] === 255 && bytes[1] === 216 && bytes[2] === 255
  const webp = bytes.length >= 12 && String.fromCharCode(...bytes.slice(0, 4)) === 'RIFF' && String.fromCharCode(...bytes.slice(8, 12)) === 'WEBP'
  if (png) return { mime: 'image/png', extension: 'png' }
  if (jpeg) return { mime: 'image/jpeg', extension: 'jpg' }
  if (webp) return { mime: 'image/webp', extension: 'webp' }
  return null
}

async function storeIcon(env: Env, bytes: Uint8Array): Promise<{ key: string; mime: string } | null> {
  if (!bytes.length || bytes.length > 2 * 1024 * 1024) return null
  const format = iconFormat(bytes)
  if (!format) return null
  const key = `icons/${id()}.${format.extension}`
  await env.BUCKET.put(key, bytes, { httpMetadata: { contentType: format.mime } })
  return { key, mime: format.mime }
}

/*
 * Replacing the icon of an app that is already published: the CLI reaches this after the server reports
 * the package itself as a duplicate, so fixing an icon does not require re-uploading the whole build.
 */
export async function setVariantIcon(request: Request, env: Env, slug: string, platform: Platform): Promise<Response> {
  const variant = (await variants(env, slug)).find(row => row.platform === platform)
  if (!variant) return fail(404, 'not_found', 'Variant not found')
  const icon = await storeIcon(env, new Uint8Array(await request.arrayBuffer()))
  if (!icon) return fail(400, 'invalid_icon', 'Icon must be a PNG, JPEG or WebP image under 2 MiB')
  const previous = variant.icon_key
  await env.DB.prepare('UPDATE variants SET icon_key = ?, updated_at = ? WHERE slug = ? AND platform = ?')
    .bind(icon.key, nowISO(), slug, platform).run()
  if (previous && previous !== icon.key) {
    const shared = await env.DB.prepare('SELECT COUNT(*) AS count FROM variants WHERE icon_key = ?').bind(previous).first<{ count: number }>()
    if (!shared?.count) await env.BUCKET.delete(previous)
  }
  return json({ icon: `icon-${icon.key.split('/').at(-1)}`, mime: icon.mime })
}

export async function uploadIcon(request: Request, env: Env, uploadId: string): Promise<Response> {
  const row = await upload(env, uploadId)
  if (!row) return fail(404, 'upload_not_found', 'Upload not found or expired')
  const metadata = JSON.parse(row.metadata_json) as UploadMetadata
  if (metadata.iconMime !== 'image/png' && metadata.iconMime !== 'image/jpeg' && metadata.iconMime !== 'image/webp') return fail(400, 'invalid_icon', 'Icon MIME type is invalid')
  const bytes = new Uint8Array(await request.arrayBuffer())
  if (!bytes.length || bytes.length > 2 * 1024 * 1024) return fail(413, 'icon_too_large', 'Icon exceeds 2 MiB')
  const format = iconFormat(bytes)
  if (!format || format.mime !== metadata.iconMime) return fail(400, 'invalid_icon', 'Icon data does not match MIME type')
  const key = `icons/${uploadId}.${format.extension}`
  await env.BUCKET.put(key, bytes, { httpMetadata: { contentType: metadata.iconMime } })
  await env.DB.prepare('UPDATE uploads SET icon_key = ? WHERE id = ?').bind(key, uploadId).run()
  return json({ uploaded: true })
}

export async function finishUpload(request: Request, env: Env, uploadId: string): Promise<Response> {
  const row = await upload(env, uploadId)
  if (!row) return fail(404, 'upload_not_found', 'Upload not found or expired')
  const body = await bodyJSON(request, 256_000)
  if (body instanceof Response) return body
  const metadata = JSON.parse(row.metadata_json) as UploadMetadata
  const parts = body.parts
  const expected = Math.ceil(metadata.fileSize / PART_SIZE)
  if (!Array.isArray(parts) || parts.length !== expected || !parts.every((part, index) =>
    part && typeof part === 'object' && (part as { partNumber?: number }).partNumber === index + 1 &&
    typeof (part as { etag?: string }).etag === 'string')) return fail(400, 'invalid_parts', 'Incomplete multipart upload')
  const multipart = env.BUCKET.resumeMultipartUpload(row.object_key, row.r2_upload_id)
  await multipart.complete(parts as R2UploadedPart[])
  const object = await env.BUCKET.head(row.object_key)
  if (!object || object.size !== metadata.fileSize) {
    await env.BUCKET.delete(row.object_key)
    return fail(400, 'size_mismatch', 'Uploaded package size does not match metadata')
  }
  const slug = metadata.slug!
  const timestamp = nowISO()
  const versionId = id()
  const current = await appBySlug(env, slug, true)
  const selected = (await variants(env, slug)).find(v => v.platform === metadata.platform)
  const statements = []
  if (!current) statements.push(env.DB.prepare('INSERT INTO apps (slug, name, created_at, updated_at) VALUES (?, ?, ?, ?)').bind(slug, metadata.name, timestamp, timestamp))
  else statements.push(env.DB.prepare('UPDATE apps SET name = ?, updated_at = ? WHERE slug = ? AND deleted_at IS NULL').bind(metadata.name, timestamp, slug))
  if (!selected) statements.push(env.DB.prepare('INSERT INTO variants (slug, platform, identifier, icon_key, current_version_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)')
    .bind(slug, metadata.platform, metadata.identifier, row.icon_key, metadata.promote ? versionId : null, timestamp, timestamp))
  else statements.push(env.DB.prepare('UPDATE variants SET icon_key = COALESCE(?, icon_key), current_version_id = CASE WHEN ? THEN ? ELSE current_version_id END, updated_at = ? WHERE slug = ? AND platform = ?')
    .bind(row.icon_key, metadata.promote ? 1 : 0, versionId, timestamp, slug, metadata.platform))
  statements.push(env.DB.prepare(`INSERT INTO versions (id, slug, platform, version_name, build_number, package_key, sha256, file_size,
    minimum_os_version, distribution_type, provisioning_expires_at, signing_certificate_sha256, release_notes, uploaded_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`).bind(versionId, slug, metadata.platform, metadata.versionName, metadata.buildNumber,
    row.object_key, metadata.sha256, metadata.fileSize, metadata.minimumOsVersion || null, metadata.distributionType || null,
    metadata.provisioningExpiresAt || null, metadata.signingCertificateSha256 || null, metadata.releaseNotes || null, timestamp))
  statements.push(env.DB.prepare('DELETE FROM uploads WHERE id = ?').bind(uploadId))
  try { await env.DB.batch(statements) }
  catch (error) {
    await env.BUCKET.delete(row.object_key)
    if (row.icon_key) await env.BUCKET.delete(row.icon_key)
    throw error
  }
  const app = await appBySlug(env, slug)
  const variant = (await variants(env, slug)).find(v => v.platform === metadata.platform)
  const version = await env.DB.prepare('SELECT * FROM versions WHERE id = ?').bind(versionId).first<VersionRow>()
  return json({ app: publicApp(app as AppRow, variant as VariantRow), version: publicVersion(version as VersionRow), duplicate: false }, 201)
}

export async function abortUpload(env: Env, uploadId: string): Promise<Response> {
  const row = await env.DB.prepare('SELECT * FROM uploads WHERE id = ?').bind(uploadId).first<UploadRow>()
  if (!row) return json({ deleted: true })
  await env.BUCKET.resumeMultipartUpload(row.object_key, row.r2_upload_id).abort()
  if (row.icon_key) await env.BUCKET.delete(row.icon_key)
  await env.DB.prepare('DELETE FROM uploads WHERE id = ?').bind(uploadId).run()
  return json({ deleted: true })
}

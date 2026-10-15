import { SignedPlistError, signProfile, verifySignedPlist } from './cms'
import { clientIP, fail, id, json, publicOrigin } from './common'
import { rateLimit } from './public'
import type { Env } from './types'

interface Session { challenge: string; payload_uuid: string; udid: string | null; created_at: string; expires_at: string; used: number }
const xml = (value: string) => value.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;')
const validChallenge = (value: string) => /^[a-f0-9]{64}$/.test(value)
const validUDID = (value: string) => /^[A-Fa-f0-9-]{20,64}$/.test(value)
const pem = (value: string | undefined) => value ? atob(value) : ''
const configured = (env: Env) => Boolean(env.APPHUB_PROFILE_CERT_B64 && env.APPHUB_PROFILE_KEY_B64 && env.APPHUB_DEVICE_CA_B64)
// The callback has to answer with a profile iOS will install; an empty PayloadContent is rejected as "profile is empty".
const noRestrictions = ['allowCamera', 'allowAppInstallation', 'allowSafari', 'allowiTunes', 'allowExplicitContent',
  'allowAssistant', 'allowScreenShot', 'allowCloudBackup', 'allowCloudDocumentSync', 'allowAppCellularDataModification']
  .map(key => `<key>${key}</key><true/>`).join('')

async function session(env: Env, challenge: string): Promise<Session | null> {
  if (!validChallenge(challenge)) return null
  const row = await env.DB.prepare('SELECT * FROM udid_sessions WHERE challenge = ?').bind(challenge).first<Session>()
  return row && Date.parse(row.expires_at) > Date.now() ? row : null
}

function profile(first: boolean, challenge: string, uuid: string, callback: string, projectName: string, payloadUuid = uuid): string {
  const common = `<key>PayloadOrganization</key><string>${xml(projectName)}</string><key>PayloadVersion</key><integer>1</integer><key>PayloadUUID</key><string>${uuid}</string>`
  if (first) return `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>PayloadContent</key><dict><key>URL</key><string>${xml(callback)}</string><key>DeviceAttributes</key><array><string>UDID</string><string>VERSION</string><string>PRODUCT</string></array><key>Challenge</key><string>${challenge}</string></dict>${common}<key>PayloadDisplayName</key><string>获取设备 UDID</string><key>PayloadDescription</key><string>仅用于显示并复制本设备 UDID，结果将在 30 分钟后删除。</string><key>PayloadIdentifier</key><string>cc.bensonday.apphub.udid</string><key>PayloadType</key><string>Profile Service</string></dict></plist>`
  const completion = `<dict><key>PayloadType</key><string>com.apple.applicationaccess</string><key>PayloadVersion</key><integer>1</integer><key>PayloadIdentifier</key><string>cc.bensonday.apphub.udid.complete</string><key>PayloadUUID</key><string>${payloadUuid}</string><key>PayloadDisplayName</key><string>${xml(projectName)} 未启用任何限制</string><key>PayloadDescription</key><string>仅用于结束获取设备 UDID 的流程，未启用任何限制，可随时删除。</string>${noRestrictions}</dict>`
  return `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>PayloadContent</key><array>${completion}</array>${common}<key>PayloadDisplayName</key><string>UDID 获取完成</string><key>PayloadDescription</key><string>UDID 已获取，可随时删除本描述文件。</string><key>PayloadIdentifier</key><string>com.myapphub.complete.${uuid}</string><key>PayloadType</key><string>Configuration</string></dict></plist>`
}

async function signedResponse(env: Env, content: string, filename?: string): Promise<Response> {
  if (!configured(env)) return fail(503, 'udid_not_configured', 'UDID profile signing is not configured')
  const signed = await signProfile(content, pem(env.APPHUB_PROFILE_CERT_B64), pem(env.APPHUB_PROFILE_KEY_B64))
  const headers = new Headers({ 'Content-Type': 'application/x-apple-aspen-config', 'Cache-Control': 'no-store' })
  if (filename) headers.set('Content-Disposition', `attachment; filename="${filename}"`)
  return new Response(signed, { headers })
}

export async function createSession(request: Request, env: Env): Promise<Response> {
  if (!configured(env)) return fail(503, 'udid_not_configured', 'UDID profile signing is not configured')
  const retry = await rateLimit(env, `udid:${clientIP(request)}`, 10, 600)
  if (retry) return fail(429, 'rate_limited', 'Try again later', { 'Retry-After': String(retry) })
  const challenge = [...crypto.getRandomValues(new Uint8Array(32))].map(byte => byte.toString(16).padStart(2, '0')).join('')
  await env.DB.prepare('INSERT INTO udid_sessions (challenge, payload_uuid, created_at, expires_at) VALUES (?, ?, ?, ?)')
    .bind(challenge, id(), new Date().toISOString(), new Date(Date.now() + 30 * 60_000).toISOString()).run()
  return json({ challenge, profileUrl: `/api/udid/profile/${challenge}`, resultUrl: `/api/udid/result/${challenge}` }, 201)
}

export async function getProfile(request: Request, env: Env, challenge: string): Promise<Response> {
  const row = await session(env, challenge)
  if (!row) return fail(404, 'session_expired', 'Session expired')
  const callback = `${publicOrigin(request, env)}/api/udid/callback`
  return signedResponse(env, profile(true, challenge, row.payload_uuid, callback, env.APPHUB_PROJECT_NAME || 'MyApphub'), 'apphub-udid.mobileconfig')
}

function plistValues(content: string): Record<string, string> {
  const values: Record<string, string> = {}
  const pattern = /<key>([^<]{1,100})<\/key>\s*<string>([^<]{0,1000})<\/string>/g
  for (const match of content.matchAll(pattern)) values[match[1]] = match[2]
  return values
}

/*
 * Device responses are rejected with a single 400 for the device; the reason is kept server-side so a real
 * iPhone can be diagnosed from D1. Only structure and certificate names are recorded, never UDID or challenge.
 */
async function recordDiagnostic(env: Env, request: Request, reason: string, detail: Record<string, string>): Promise<void> {
  const summary = Object.entries(detail).map(([key, value]) => `${key}=${value}`).join(' ').slice(0, 900) || '-'
  console.error('udid callback', reason, summary)
  try {
    await env.DB.prepare('INSERT INTO udid_callback_diagnostics (id, created_at, reason, detail, client) VALUES (?, ?, ?, ?, ?)')
      .bind(id(), new Date().toISOString(), reason.slice(0, 60), summary, (request.headers.get('user-agent') || '').slice(0, 80)).run()
  } catch (error) {
    console.error('udid callback diagnostic failed', error instanceof Error ? error.message : 'unknown')
  }
}

export async function callback(request: Request, env: Env): Promise<Response> {
  if (!configured(env)) return fail(503, 'udid_not_configured', 'UDID trust certificate is not configured')
  if (Number(request.headers.get('content-length') || 0) > 65_536) return fail(413, 'invalid_profile', 'Profile response too large')
  const data = new Uint8Array(await request.arrayBuffer())
  if (data.length > 65_536) return fail(413, 'invalid_profile', 'Profile response too large')
  const notes: Record<string, string> = { contentType: request.headers.get('content-type') || '-' }
  let content: string
  try { content = await verifySignedPlist(data, pem(env.APPHUB_DEVICE_CA_B64), notes) }
  catch (error) {
    const detail = error instanceof SignedPlistError
      ? { ...notes, ...error.diagnostics, code: error.code }
      : { ...notes, code: 'error', exception: error instanceof Error ? error.message : 'unknown' }
    await recordDiagnostic(env, request, 'reject:verify', detail)
    return fail(400, 'invalid_profile', 'Invalid signed profile response')
  }
  const values = plistValues(content)
  const challenge = values.CHALLENGE || ''
  const udid = values.UDID || ''
  const base = { ...notes, keys: Object.keys(values).sort().join(',') || '-', challengeLength: String(challenge.length), udidLength: String(udid.length) }
  if (!validChallenge(challenge) || !validUDID(udid)) {
    await recordDiagnostic(env, request, 'reject:values', base)
    return fail(400, 'invalid_profile', 'Invalid profile response')
  }
  const row = await session(env, challenge)
  if (!row || row.used) {
    await recordDiagnostic(env, request, 'reject:session', { ...base, session: !row ? 'missing' : 'used' })
    return fail(409, 'challenge_used', 'Session expired or already used')
  }
  const result = await env.DB.prepare('UPDATE udid_sessions SET udid = ?, used = 1 WHERE challenge = ? AND used = 0 AND expires_at > ?')
    .bind(udid, challenge, new Date().toISOString()).run()
  if (!result.meta.changes) {
    await recordDiagnostic(env, request, 'reject:race', base)
    return fail(409, 'challenge_used', 'Session expired or already used')
  }
  await recordDiagnostic(env, request, 'ok', { ...base, completion: 'applicationaccess' })
  return signedResponse(env, profile(false, challenge, id(), '', env.APPHUB_PROJECT_NAME || 'MyApphub', id()))
}

export async function getResult(env: Env, challenge: string): Promise<Response> {
  const row = await session(env, challenge)
  if (!row) return fail(404, 'session_expired', 'Session expired')
  return json({ pending: !row.udid, udid: row.udid })
}

export async function deleteResult(env: Env, challenge: string): Promise<Response> {
  if (!validChallenge(challenge)) return fail(400, 'invalid_challenge', 'Invalid challenge')
  await env.DB.prepare('DELETE FROM udid_sessions WHERE challenge = ?').bind(challenge).run()
  return json({ deleted: true })
}

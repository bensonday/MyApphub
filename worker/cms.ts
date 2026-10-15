// Minimal DER/CMS support for the RSA profile-signing identity used by the UDID tool.
// It keeps the signing key in a Worker secret; the key never reaches the browser or CLI.
const utf8 = new TextEncoder()
const bytes = (...parts: Uint8Array[]) => {
  const result = new Uint8Array(parts.reduce((n, part) => n + part.length, 0))
  let offset = 0
  for (const part of parts) { result.set(part, offset); offset += part.length }
  return result
}
const der = (tag: number, content: Uint8Array) => {
  const length = content.length < 128 ? new Uint8Array([content.length]) : (() => {
    const out: number[] = []
    for (let n = content.length; n; n >>>= 8) out.unshift(n & 255)
    return new Uint8Array([0x80 | out.length, ...out])
  })()
  return bytes(new Uint8Array([tag]), length, content)
}
const seq = (...items: Uint8Array[]) => der(0x30, bytes(...items))
const set = (...items: Uint8Array[]) => der(0x31, bytes(...items.sort((a, b) => {
  for (let i = 0; i < Math.min(a.length, b.length); i++) if (a[i] !== b[i]) return a[i] - b[i]
  return a.length - b.length
})))
const integer = (value: number) => der(0x02, new Uint8Array([value]))
const oid = (value: number[]) => {
  const result = [value[0] * 40 + value[1]]
  for (const part of value.slice(2)) {
    const stack = [part & 127]
    for (let n = part >>> 7; n; n >>>= 7) stack.unshift((n & 127) | 128)
    result.push(...stack)
  }
  return der(0x06, new Uint8Array(result))
}
const nullDER = der(0x05, new Uint8Array())
const dataOID = oid([1, 2, 840, 113549, 1, 7, 1])
const signedOID = oid([1, 2, 840, 113549, 1, 7, 2])
const shaOID = oid([2, 16, 840, 1, 101, 3, 4, 2, 1])
const sha1OID = oid([1, 3, 14, 3, 2, 26])
const sha384OID = oid([2, 16, 840, 1, 101, 3, 4, 2, 2])
const skiOID = oid([2, 5, 29, 14])
const rsaOID = oid([1, 2, 840, 113549, 1, 1, 11])
const digestAlgorithm = seq(shaOID, nullDER)

interface TLV { tag: number; start: number; content: number; end: number; raw: Uint8Array; value: Uint8Array }
function tlv(source: Uint8Array, start: number): TLV {
  if (start + 2 > source.length) throw new Error('truncated DER')
  let position = start + 1
  let length = source[position++]
  if (length & 128) {
    const count = length & 127
    if (!count || count > 4 || position + count > source.length) throw new Error('invalid DER length')
    length = 0
    for (let i = 0; i < count; i++) length = length * 256 + source[position++]
  }
  if (position + length > source.length) throw new Error('truncated DER content')
  return { tag: source[start], start, content: position, end: position + length,
    raw: source.slice(start, position + length), value: source.slice(position, position + length) }
}
function children(source: Uint8Array): TLV[] {
  const values: TLV[] = []
  for (let offset = 0; offset < source.length;) { const item = tlv(source, offset); values.push(item); offset = item.end }
  return values
}
function pem(raw: string, label: string): Uint8Array {
  const match = raw.match(new RegExp(`-----BEGIN ${label}-----([\\s\\S]*?)-----END ${label}-----`))
  if (!match) throw new Error(`missing ${label} PEM`)
  return Uint8Array.from(atob(match[1].replace(/\s/g, '')), char => char.charCodeAt(0))
}

function certificateFields(certificate: Uint8Array): { serial: Uint8Array; issuer: Uint8Array; subject: Uint8Array; spki: Uint8Array; tbs: Uint8Array; signature: Uint8Array; signatureOID: Uint8Array } {
  const root = tlv(certificate, 0)
  if (root.tag !== 0x30) throw new Error('invalid certificate')
  const cert = children(root.value)
  const tbs = children(cert[0].value)
  const shift = tbs[0].tag === 0xa0 ? 1 : 0
  return { serial: tbs[shift].raw, issuer: tbs[shift + 2].raw, subject: tbs[shift + 4].raw, spki: tbs[shift + 5].raw,
    tbs: cert[0].raw, signature: cert[2].value.slice(1), signatureOID: children(cert[1].value)[0].raw }
}

const same = (a: Uint8Array, b: Uint8Array) => a.length === b.length && a.every((value, index) => value === b[index])
const digestHash = (algorithmOID: Uint8Array): 'SHA-1' | 'SHA-256' | 'SHA-384' => {
  if (same(algorithmOID, sha1OID)) return 'SHA-1'
  if (same(algorithmOID, shaOID)) return 'SHA-256'
  if (same(algorithmOID, sha384OID)) return 'SHA-384'
  throw new Error('unsupported CMS digest algorithm')
}
const hashForOID = (value: Uint8Array) => {
  if (same(value, rsaOID)) return 'SHA-256'
  if (same(value, oid([1, 2, 840, 113549, 1, 1, 5]))) return 'SHA-1'
  if (same(value, oid([1, 2, 840, 113549, 1, 1, 12]))) return 'SHA-384'
  throw new Error('unsupported certificate signature algorithm')
}

/*
 * Diagnostics for rejected device responses. They describe structure and certificate names only:
 * never the UDID, the challenge, or any payload content.
 */
export interface SignedPlistDiagnostics { [key: string]: string }

export class SignedPlistError extends Error {
  constructor(readonly code: string, readonly diagnostics: SignedPlistDiagnostics) {
    super(code)
    this.name = 'SignedPlistError'
  }
}

const nameLabels: Record<string, string> = {
  '2.5.4.3': 'CN', '2.5.4.6': 'C', '2.5.4.7': 'L', '2.5.4.8': 'ST', '2.5.4.10': 'O', '2.5.4.11': 'OU',
  '0.9.2342.19200300.100.1.25': 'DC', '1.2.840.113549.1.9.1': 'email',
}
const hex = (value: Uint8Array) => [...value].map(byte => byte.toString(16).padStart(2, '0')).join('')
function oidText(value: Uint8Array): string {
  if (!value.length) return ''
  const parts = [Math.floor(value[0] / 40), value[0] % 40]
  let current = 0
  for (let index = 1; index < value.length; index++) {
    current = current * 128 + (value[index] & 127)
    if (!(value[index] & 128)) { parts.push(current); current = 0 }
  }
  return parts.join('.')
}
function stringText(item: TLV): string {
  if (item.tag === 0x1e) return [...item.value].filter((_, index) => index % 2 === 0)
    .map((byte, index) => String.fromCharCode(byte * 256 + (item.value[index * 2 + 1] || 0))).join('')
  if (item.tag === 0x0c || item.tag === 0x13 || item.tag === 0x14 || item.tag === 0x16 || item.tag === 0x1a)
    return new TextDecoder('utf-8', { fatal: false }).decode(item.value)
  return ''
}
function describeName(name: Uint8Array): string {
  const parts: string[] = []
  try {
    const sequence = name[0] === 0x30 ? tlv(name, 0).value : name
    for (const rdn of children(sequence)) for (const attribute of children(rdn.value)) {
      const pair = children(attribute.value)
      if (pair.length < 2 || pair[0].tag !== 0x06) continue
      const label = nameLabels[oidText(pair[0].value)], text = stringText(pair[1])
      if (label && text) parts.push(`${label}=${text}`)
    }
  } catch { /* names are diagnostic only */ }
  return parts.join(',') || 'unknown'
}
function describeCertificate(certificate: Uint8Array): string {
  try {
    const fields = certificateFields(certificate)
    return `${describeName(fields.subject)} <- ${describeName(fields.issuer)} serial ${hex(fields.serial).slice(0, 26)}`
  } catch { return 'unreadable' }
}
const payloadKind = (payload: Uint8Array) =>
  payload[0] === 0x3c ? 'xml-plist' : payload[0] === 0x62 && payload[1] === 0x70 ? 'binary-plist' : `head:${hex(payload.slice(0, 4))}`

async function verifiesCertificate(child: Uint8Array, parent: Uint8Array): Promise<boolean> {
  const leaf = certificateFields(child), authority = certificateFields(parent)
  if (!same(leaf.issuer, authority.subject)) return false
  const hash = hashForOID(leaf.signatureOID)
  const key = await crypto.subtle.importKey('spki', authority.spki, { name: 'RSASSA-PKCS1-v1_5', hash }, false, ['verify'])
  return crypto.subtle.verify('RSASSA-PKCS1-v1_5', key, leaf.signature, leaf.tbs)
}

export async function signProfile(content: string, certificatePEM: string, keyPEM: string): Promise<Uint8Array> {
  const certificate = pem(certificatePEM, 'CERTIFICATE')
  const { serial, issuer, spki } = certificateFields(certificate)
  const key = await crypto.subtle.importKey('pkcs8', pem(keyPEM, 'PRIVATE KEY'), { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' }, false, ['sign'])
  const publicKey = await crypto.subtle.importKey('spki', spki, { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' }, false, ['verify'])
  const data = utf8.encode(content)
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', data))
  const attrs = set(
    seq(oid([1, 2, 840, 113549, 1, 9, 3]), set(dataOID)),
    seq(oid([1, 2, 840, 113549, 1, 9, 4]), set(der(0x04, digest))),
  )
  const signature = new Uint8Array(await crypto.subtle.sign('RSASSA-PKCS1-v1_5', key, attrs))
  if (!await crypto.subtle.verify('RSASSA-PKCS1-v1_5', publicKey, signature, attrs)) throw new Error('profile certificate and key do not match')
  const signedAttrs = attrs.slice(); signedAttrs[0] = 0xa0
  const signerInfo = seq(integer(1), seq(issuer, serial), digestAlgorithm, signedAttrs, seq(rsaOID, nullDER), der(0x04, signature))
  const signedData = seq(integer(1), set(digestAlgorithm), seq(dataOID, der(0xa0, der(0x04, data))), der(0xa0, certificate), set(signerInfo))
  return seq(signedOID, der(0xa0, signedData))
}

/*
 * iOS posts BER, not strict DER: Apple's CMS encoder writes indefinite lengths, which the original parser
 * rejected outright. The readers below accept both forms and re-encode canonically when DER is required.
 */
interface Element { tag: number; constructed: boolean; indefinite: boolean; contentStart: number; contentEnd: number; end: number }
interface Node { tag: number; constructed: boolean; indefinite: boolean; content: Uint8Array; children: Node[] }

function element(source: Uint8Array, start: number, limit: number): Element {
  if (start + 2 > limit) throw new Error('truncated DER')
  const tag = source[start]
  const constructed = (tag & 0x20) !== 0
  let position = start + 1
  const first = source[position++]
  let indefinite = false, length = 0
  if (first === 0x80) {
    if (!constructed) throw new Error('invalid DER length')
    indefinite = true
  } else if (first & 0x80) {
    const count = first & 0x7f
    if (!count || count > 4 || position + count > limit) throw new Error('invalid DER length')
    for (let index = 0; index < count; index++) length = length * 256 + source[position++]
    if (position + length > limit) throw new Error('truncated DER content')
  } else length = first
  if (!indefinite) return { tag, constructed, indefinite, contentStart: position, contentEnd: position + length, end: position + length }
  let cursor = position
  while (cursor + 2 <= limit) {
    if (source[cursor] === 0 && source[cursor + 1] === 0) return { tag, constructed, indefinite, contentStart: position, contentEnd: cursor, end: cursor + 2 }
    cursor = element(source, cursor, limit).end
  }
  throw new Error('missing end-of-contents')
}

function parseNode(source: Uint8Array): Node {
  const item = element(source, 0, source.length)
  const content = source.slice(item.contentStart, item.contentEnd)
  return { tag: item.tag, constructed: item.constructed, indefinite: item.indefinite, content, children: item.constructed ? nodes(content) : [] }
}

function nodes(source: Uint8Array): Node[] {
  const list: Node[] = []
  let cursor = 0
  while (cursor < source.length) {
    const item = element(source, cursor, source.length)
    list.push(parseNode(source.slice(cursor, item.end)))
    cursor = item.end
  }
  return list
}

const encodeNode = (node: Node): Uint8Array => der(node.tag, node.constructed ? bytes(...node.children.map(encodeNode)) : node.content)

function sketch(source: Uint8Array): string {
  const parts: string[] = []
  let cursor = 0
  try {
    while (cursor < source.length && parts.length < 8) {
      const item = element(source, cursor, source.length)
      parts.push(`${item.tag.toString(16)}${item.indefinite ? 'i' : 'd'}:${item.contentEnd - item.contentStart}@${cursor}`)
      cursor = item.end
    }
  } catch { parts.push(`!${hex(source.slice(cursor, cursor + 6))}@${cursor}`) }
  return parts.join(' ')
}

function certificateSubjectKeyIdentifier(certificate: Uint8Array): Uint8Array | null {
  try {
    const tbs = children(tlv(certificate, 0).value)[0]
    const extension = children(tbs.value).find(item => item.tag === 0xa3)
    if (!extension) return null
    for (const entry of children(children(extension.value)[0].value)) {
      const pair = children(entry.value)
      if (pair.length < 2 || pair[0].tag !== 0x06 || !same(pair[0].raw, skiOID)) continue
      const value = pair[pair.length - 1]
      if (value.tag !== 0x04) continue
      const inner = tlv(value.value, 0)
      return inner.tag === 0x04 ? inner.value : value.value
    }
  } catch { /* extension is optional */ }
  return null
}

function distinct(parts: Uint8Array[]): Uint8Array[] {
  return parts.filter((part, index) => parts.findIndex(other => same(other, part)) === index)
}

export async function verifySignedPlist(input: Uint8Array, trustedCAPEM: string, notes: SignedPlistDiagnostics = {}): Promise<string> {
  notes.bytes = String(input.length)
  notes.head = hex(input.slice(0, 24))
  try {
    let trust: Uint8Array
    try { trust = pem(trustedCAPEM, 'CERTIFICATE') } catch { throw new SignedPlistError('trust_anchor_unreadable', notes) }
    notes.anchor = describeCertificate(trust)
    const root = parseNode(input)
    if (root.indefinite) notes.encoding = 'ber-indefinite'
    if (root.tag !== 0x30 || root.children.length < 2 || !same(encodeNode(root.children[0]), signedOID)) throw new SignedPlistError('not_cms_signed_data', notes)
    const signedData = root.children[1].children[0]
    if (!signedData || signedData.tag !== 0x30) throw new SignedPlistError('not_cms_signed_data', notes)
    const parts = signedData.children
    notes.signedData = parts.map(node => node.tag.toString(16)).join(' ')
    const encapsulated = parts[2]?.children || []
    const content = encapsulated[1]?.children?.[0]
    const payload = content?.tag === 0x04 ? content.content : content?.constructed ? bytes(...content.children.map(child => child.content)) : null
    if (!payload || payload.length > 65_536) throw new SignedPlistError('invalid_payload', notes)
    notes.payload = `${payload.length}B ${payloadKind(payload)}`
    const certificateSet = parts.find(node => node.tag === 0xa0)
    const signerSet = [...parts].reverse().find(node => node.tag === 0x31)
    if (!certificateSet?.children.length || !signerSet?.children.length) throw new SignedPlistError('missing_certificates_or_signer', notes)
    const certificates = certificateSet.children.filter(node => node.tag === 0x30).map(encodeNode)
    notes.certificates = `${certificates.length} ${certificates.map(describeCertificate).join(' | ')}`.slice(0, 400)
    const signer = signerSet.children[0].children
    notes.signerInfo = signer.map(node => node.tag.toString(16)).join(' ')
    const hash = digestHash(encodeNode(signer[2].children[0]))
    notes.digest = hash
    const sid = signer[1]
    notes.signerId = sid.tag === 0x30 ? 'issuerAndSerial' : `tag:${sid.tag.toString(16)}`
    const signerCertificate = sid.tag === 0x30 && sid.children.length === 2
      ? certificates.find(cert => {
        const fields = certificateFields(cert)
        return same(fields.issuer, encodeNode(sid.children[0])) && same(fields.serial, encodeNode(sid.children[1]))
      })
      : sid.tag === 0x80
        ? certificates.find(cert => { const ski = certificateSubjectKeyIdentifier(cert); return Boolean(ski && same(ski, sid.content)) })
        : undefined
    if (!signerCertificate || signer[3]?.tag !== 0xa0 || !signer[5] || signer[5].tag !== 0x04) throw new SignedPlistError('invalid_signer', notes)
    notes.signer = describeCertificate(signerCertificate)
    const attributes = signer[3]
    const digestAttr = attributes.children.find(node => node.children[0] && same(encodeNode(node.children[0]), oid([1, 2, 840, 113549, 1, 9, 4])))
    const contentAttr = attributes.children.find(node => node.children[0] && same(encodeNode(node.children[0]), oid([1, 2, 840, 113549, 1, 9, 3])))
    if (!digestAttr?.children[1]?.children[0] || !contentAttr?.children[1]?.children[0]) throw new SignedPlistError('missing_signed_attributes', notes)
    const digest = new Uint8Array(await crypto.subtle.digest(hash, payload))
    if (!same(digestAttr.children[1].children[0].content, digest)) throw new SignedPlistError('content_digest_mismatch', notes)
    if (!same(encodeNode(contentAttr.children[1].children[0]), dataOID)) throw new SignedPlistError('content_type_mismatch', notes)
    const key = await crypto.subtle.importKey('spki', certificateFields(signerCertificate).spki, { name: 'RSASSA-PKCS1-v1_5', hash }, false, ['verify'])
    const candidates = distinct([
      encodeNode({ ...attributes, tag: 0x31 }),
      der(0x31, attributes.content),
      bytes(new Uint8Array([0x31, 0x80]), attributes.content, new Uint8Array([0, 0])),
    ])
    notes.signatureCandidates = String(candidates.length)
    let encoding = 'none'
    for (const candidate of candidates) {
      if (!await crypto.subtle.verify('RSASSA-PKCS1-v1_5', key, signer[5].content, candidate)) continue
      encoding = same(candidate, candidates[0]) ? 'canonical-der' : candidate.length === 4 + attributes.content.length && candidate[1] === 0x80 ? 'ber-indefinite' : 'raw-content'
      break
    }
    notes.signatureEncoding = encoding
    if (encoding === 'none') throw new SignedPlistError('signature_invalid', notes)
    const chain: string[] = []
    let current = signerCertificate
    for (let depth = 0; depth < 5; depth++) {
      if (same(current, trust)) break
      const issuer = certificateFields(current).issuer
      chain.push(describeName(certificateFields(current).subject))
      const parent = [...certificates, trust].find(cert => same(certificateFields(cert).subject, issuer) && !same(cert, current))
      if (!parent || !await verifiesCertificate(current, parent)) {
        notes.chain = `${chain.join(' -> ')} -> ${describeName(issuer)}`
        notes.nextCertificates = certificates.filter(cert => same(certificateFields(cert).subject, issuer) && !same(cert, current)).map(describeCertificate).join(' | ').slice(0, 320) || 'none'
        throw new SignedPlistError('untrusted_device_certificate', notes)
      }
      current = parent
    }
    notes.chain = chain.join(' -> ')
    if (!same(current, trust)) throw new SignedPlistError('chain_too_long', notes)
    notes.chainStatus = 'verified'
    return new TextDecoder('utf-8', { fatal: true }).decode(payload)
  } catch (error) {
    if (error instanceof SignedPlistError) throw error
    notes.exception = error instanceof Error ? error.message : 'unknown'
    notes.sketch = sketch(input)
    throw new SignedPlistError('verify_failed', notes)
  }
}

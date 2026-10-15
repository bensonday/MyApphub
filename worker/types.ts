export type Platform = 'ios' | 'android'

export interface Env {
  DB: D1Database
  BUCKET: R2Bucket
  ASSETS: Fetcher
  APPHUB_PROJECT_NAME?: string
  APPHUB_CANONICAL_ORIGIN?: string
  APPHUB_TOKEN?: string
  APPHUB_HMAC_SECRET?: string
  APPHUB_PROFILE_CERT_B64?: string
  APPHUB_PROFILE_KEY_B64?: string
  APPHUB_DEVICE_CA_B64?: string
}

export interface UploadMetadata {
  platform: Platform
  identifier: string
  name: string
  versionName: string
  buildNumber: string
  minimumOsVersion?: string
  distributionType?: string
  provisioningExpiresAt?: string
  signingCertificateSha256?: string
  releaseNotes?: string
  slug?: string
  promote: boolean
  sha256: string
  fileSize: number
  iconMime?: string
}

export interface AppRow {
  slug: string
  name: string
  password_hash: string | null
  password_version: number
  created_at: string
  updated_at: string
  deleted_at: string | null
}
export interface VariantRow {
  slug: string
  platform: Platform
  identifier: string
  icon_key: string | null
  current_version_id: string | null
  created_at: string
  updated_at: string
}
export interface VersionRow {
  id: string
  slug: string
  platform: Platform
  version_name: string
  build_number: string
  package_key: string
  sha256: string
  file_size: number
  minimum_os_version: string | null
  distribution_type: string | null
  provisioning_expires_at: string | null
  signing_certificate_sha256: string | null
  release_notes: string | null
  uploaded_at: string
  deleted_at: string | null
}

export interface UploadRow {
  id: string
  object_key: string
  r2_upload_id: string
  metadata_json: string
  icon_key: string | null
  created_at: string
  expires_at: string
}

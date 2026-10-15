export type Platform = 'ios' | 'android'

export interface PublicApp {
  schemaVersion: number
  slug: string
  platform: Platform
  identifier: string
  name: string
  icon?: string
  currentVersionId?: string
  passwordProtected: boolean
  passwordVersion?: number
  createdAt: string
  updatedAt: string
}

export interface AppVersion {
  schemaVersion: number
  id: string
  versionName: string
  buildNumber: string
  packageFile: string
  sha256: string
  fileSize: number
  minimumOsVersion?: string
  distributionType?: string
  provisioningExpiresAt?: string
  releaseNotes?: string
  uploadedAt: string
}

export interface PublicRelease { app: PublicApp; version: AppVersion }
export interface PublicAppResponse extends PublicRelease { variants?: PublicRelease[] }
export interface AccessResponse { downloadUrl?: string; installUrl?: string; releaseNotes?: string; expiresAt: string }
export interface UdidSessionResponse { challenge: string; profileUrl: string; resultUrl: string }
export interface UdidResultResponse { pending: boolean; udid?: string | null }
export interface ProjectConfigResponse { projectName: string }

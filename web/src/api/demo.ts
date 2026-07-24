import type { AccessResponse, Platform, PublicAppResponse } from '../types'

const now = '2026-07-22T08:00:00Z'
const version = (platform: 'ios' | 'android', overrides: Partial<PublicAppResponse['version']>): PublicAppResponse['version'] => ({
  schemaVersion: 1, id: 'current', versionName: '1.0.0', buildNumber: '100', packageFile: platform === 'ios' ? 'app.ipa' : 'app.apk', sha256: 'demo', fileSize: 120_000_000, uploadedAt: now, ...overrides,
})

export const demoApps: Record<string, PublicAppResponse> = {
  whatsapp: { app: { schemaVersion: 1, slug: 'whatsapp', platform: 'ios', identifier: 'com.whatsapp', name: 'WhatsApp', icon: 'demo/whatsapp.svg', currentVersionId: 'current', passwordProtected: false, createdAt: now, updatedAt: now }, version: version('ios', { versionName: '2.26.14', buildNumber: '261402', fileSize: 118_600_000, minimumOsVersion: '15.0', distributionType: 'enterprise', releaseNotes: '优化消息同步速度与通话稳定性，修复部分设备无法预览图片的问题。' }) },
  instagram: { app: { schemaVersion: 1, slug: 'instagram', platform: 'android', identifier: 'com.instagram.android', name: 'Instagram', icon: 'demo/instagram.svg', currentVersionId: 'current', passwordProtected: true, createdAt: now, updatedAt: now }, version: version('android', { versionName: '392.1.0', buildNumber: '728431', fileSize: 96_400_000, minimumOsVersion: '10', releaseNotes: '优化动态加载体验，修复深色模式下部分界面显示异常的问题。' }) },
  'example-learning': { app: { schemaVersion: 1, slug: 'example-learning', platform: 'ios', identifier: 'com.example.learning', name: 'Example Learning', currentVersionId: 'current', passwordProtected: false, createdAt: now, updatedAt: now }, version: version('ios', { versionName: '3.8.2', buildNumber: '308020', fileSize: 142_800_000, minimumOsVersion: '15.0', distributionType: 'ad-hoc', releaseNotes: '优化课程加载速度，修复离线资源更新后的显示问题。' }) },
}

export function demoAccess(slug: string, platform?: Platform): AccessResponse {
  const ios = (platform ?? demoApps[slug]?.app.platform) === 'ios'
  return { downloadUrl: '#demo-download', installUrl: ios ? '#demo-install' : undefined, expiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString() }
}

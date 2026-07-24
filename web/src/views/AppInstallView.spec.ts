import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import { loadProjectBrand } from '../composables/useProjectBrand'
import type { PublicApp, PublicAppResponse } from '../types'
import AppInstallView from './AppInstallView.vue'

const mocks = vi.hoisted(() => ({
  getConfig: vi.fn(),
  getApp: vi.fn(),
  access: vi.fn(),
}))

vi.mock('../api/client', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api/client')>(),
  api: {
    getConfig: mocks.getConfig,
    getApp: mocks.getApp,
    access: mocks.access,
  },
}))

const app: PublicApp = {
  schemaVersion: 1,
  slug: 'whatsapp',
  platform: 'android',
  identifier: 'com.example.whatsapp',
  name: 'WhatsApp',
  passwordProtected: false,
  createdAt: '2026-07-01T00:00:00Z',
  updatedAt: '2026-07-22T00:00:00Z',
}

const response: PublicAppResponse = {
  app,
  version: {
    schemaVersion: 1,
    id: 'v1',
    versionName: '1.0.0',
    buildNumber: '1',
    packageFile: 'package.apk',
    sha256: 'abc',
    fileSize: 1024,
    uploadedAt: '2026-07-22T00:00:00Z',
  },
}

async function render() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/app/:slug', component: AppInstallView },
      { path: '/404', component: { template: '<div>404</div>' } },
    ],
  })
  await router.push('/app/whatsapp')
  await router.isReady()
  return mount(AppInstallView, {
    global: {
      plugins: [router],
      stubs: { QrPass: true, AppLogo: true },
    },
  })
}

beforeEach(async () => {
  mocks.getConfig.mockReset()
  mocks.getApp.mockReset()
  mocks.access.mockReset()
  mocks.getConfig.mockRejectedValue(new Error('offline'))
  await loadProjectBrand()
})

describe('App install page API states', () => {
  it('requests a fresh access token for every install click', async () => {
    mocks.getApp.mockResolvedValue(response)
    mocks.access.mockResolvedValue({ downloadUrl: '#demo-download', expiresAt: '2099-01-01T00:00:00Z' })
    const wrapper = await render()
    await flushPromises()
    expect(mocks.access).toHaveBeenCalledTimes(1)

    await wrapper.get('.mobile-priority').trigger('click')
    await flushPromises()
    expect(mocks.access).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('updates the page title when the configured project name loads', async () => {
    mocks.getConfig.mockResolvedValue({ projectName: '团队应用中心' })
    await loadProjectBrand()
    mocks.getApp.mockResolvedValue(response)
    mocks.access.mockResolvedValue({ downloadUrl: '#demo-download', expiresAt: '2099-01-01T00:00:00Z' })

    const wrapper = await render()
    await flushPromises()
    expect(document.title).toBe('WhatsApp · 团队应用中心')
    wrapper.unmount()
  })

  it('shows public app identity while disabling install when no version exists', async () => {
    mocks.getApp.mockRejectedValue(new ApiError(503, 'no_current_version', 'No installable version', app))
    const wrapper = await render()
    await flushPromises()

    expect(wrapper.text()).toContain('WhatsApp')
    expect(wrapper.text()).toContain('暂无可安装版本')
    expect(wrapper.get('.mobile-priority').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('switches platforms without changing the shared product URL', async () => {
    const iosRelease = {
      app: { ...app, platform: 'ios' as const, identifier: 'com.example.whatsapp.ios' },
      version: { ...response.version, id: 'ios-v1', packageFile: 'package.ipa', distributionType: 'enterprise' },
    }
    mocks.getApp.mockResolvedValue({ ...response, variants: [response, iosRelease] })
    mocks.access.mockResolvedValue({ downloadUrl: '#demo-download', installUrl: '#demo-install', expiresAt: '2099-01-01T00:00:00Z' })
    const wrapper = await render()
    await flushPromises()

    const iosButton = wrapper.findAll('.platform-switcher button').find((button) => button.text() === 'iOS')
    expect(iosButton).toBeDefined()
    await iosButton!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('iOS')
    expect(mocks.access).toHaveBeenLastCalledWith('whatsapp', '', 'ios')
    expect(mocks.getApp).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})

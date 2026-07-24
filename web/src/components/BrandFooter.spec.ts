import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loadProjectBrand } from '../composables/useProjectBrand'
import BrandFooter from './BrandFooter.vue'

const mocks = vi.hoisted(() => ({ getConfig: vi.fn() }))

vi.mock('../api/client', () => ({
  api: { getConfig: mocks.getConfig },
}))

beforeEach(() => {
  mocks.getConfig.mockReset()
})

describe('BrandFooter', () => {
  it('reacts to a long configured project name', async () => {
    mocks.getConfig.mockResolvedValue({ projectName: 'Community Mobile Distribution Hub' })
    const wrapper = mount(BrandFooter)
    await loadProjectBrand()
    await flushPromises()

    expect(wrapper.text()).toContain('Community Mobile Distribution Hub')
    expect(wrapper.get('.footer-mark').text()).toBe('C')
  })
})

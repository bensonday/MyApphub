import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loadProjectBrand, useProjectBrand } from './useProjectBrand'

const mocks = vi.hoisted(() => ({ getConfig: vi.fn() }))

vi.mock('../api/client', () => ({
  api: { getConfig: mocks.getConfig },
}))

beforeEach(async () => {
  mocks.getConfig.mockReset()
  mocks.getConfig.mockRejectedValue(new Error('reset'))
  await loadProjectBrand()
})

describe('project brand', () => {
  it('loads a custom Unicode project name and derives its mark', async () => {
    mocks.getConfig.mockResolvedValue({ projectName: '  团队应用中心  ' })
    await loadProjectBrand()

    const brand = useProjectBrand()
    expect(brand.projectName.value).toBe('团队应用中心')
    expect(brand.projectMark.value).toBe('团')
  })

  it('falls back without blocking when configuration fails or is blank', async () => {
    mocks.getConfig.mockRejectedValueOnce(new Error('offline'))
    await expect(loadProjectBrand()).resolves.toBeUndefined()
    expect(useProjectBrand().projectName.value).toBe('MyApphub')

    mocks.getConfig.mockResolvedValueOnce({ projectName: '   ' })
    await loadProjectBrand()
    expect(useProjectBrand().projectName.value).toBe('MyApphub')
    expect(useProjectBrand().projectMark.value).toBe('M')
  })
})

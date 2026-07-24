import { describe, expect, it } from 'vitest'
import { useAppAccent } from './useAppAccent'

describe('useAppAccent', () => {
  it('creates a deterministic fallback accent and readable foreground', () => {
    const first = useAppAccent('example-learning')
    const second = useAppAccent('example-learning')
    expect(first.accent.value).toBe(second.accent.value)
    expect(first.rgb.value.split(',')).toHaveLength(3)
    expect(['#111214', '#FFFFFF']).toContain(first.foreground.value)
  })
})

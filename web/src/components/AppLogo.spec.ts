import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import AppLogo from './AppLogo.vue'

describe('AppLogo', () => {
  it('uses a stable two-character fallback when no icon exists', () => {
    const wrapper = mount(AppLogo, { props: { name: 'Example Learning' } })
    expect(wrapper.get('.fallback-mark').text()).toBe('EL')
    expect(wrapper.find('img').exists()).toBe(false)
  })

  it('keeps uploaded artwork as an image asset', () => {
    const wrapper = mount(AppLogo, { props: { name: 'WhatsApp', src: '/assets/whatsapp/icon.png' } })
    const image = wrapper.get('img')
    expect(image.attributes('src')).toBe('/assets/whatsapp/icon.png')
    expect(image.attributes('alt')).toContain('WhatsApp')
  })
})

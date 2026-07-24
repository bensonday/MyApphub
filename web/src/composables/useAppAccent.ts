import { computed, ref } from 'vue'

const fallbackPalette = ['#986CFF', '#EE6C4D', '#2A9D8F', '#E39A35', '#4B7BEC', '#C95B8E']

function hash(value: string): number {
  let result = 0
  for (const char of value) result = ((result << 5) - result + char.charCodeAt(0)) | 0
  return Math.abs(result)
}

function hexToRgb(hex: string): [number, number, number] {
  const value = hex.replace('#', '')
  return [0, 2, 4].map((index) => Number.parseInt(value.slice(index, index + 2), 16)) as [number, number, number]
}

function rgbToHex(r: number, g: number, b: number): string {
  return `#${[r, g, b].map((v) => Math.round(v).toString(16).padStart(2, '0')).join('')}`
}

function normalize(r: number, g: number, b: number): string {
  const max = Math.max(r, g, b), min = Math.min(r, g, b)
  if (max - min < 24) return '#986CFF'
  const target = 132
  const average = (r + g + b) / 3 || 1
  const scale = Math.min(1.35, Math.max(.68, target / average))
  return rgbToHex(Math.min(232, Math.max(24, r * scale)), Math.min(232, Math.max(24, g * scale)), Math.min(232, Math.max(24, b * scale)))
}

export function useAppAccent(seed: string) {
  const accent = ref(fallbackPalette[hash(seed) % fallbackPalette.length])
  const rgb = computed(() => hexToRgb(accent.value).join(', '))
  const foreground = computed(() => {
    const [r, g, b] = hexToRgb(accent.value).map((v) => v / 255)
    const luminance = .2126 * r + .7152 * g + .0722 * b
    return luminance > .58 ? '#111214' : '#FFFFFF'
  })

  function readFromImage(image: HTMLImageElement) {
    try {
      const canvas = document.createElement('canvas')
      canvas.width = canvas.height = 24
      const context = canvas.getContext('2d', { willReadFrequently: true })
      if (!context) return
      context.drawImage(image, 0, 0, 24, 24)
      const pixels = context.getImageData(0, 0, 24, 24).data
      let red = 0, green = 0, blue = 0, weight = 0
      for (let index = 0; index < pixels.length; index += 16) {
        const alpha = pixels[index + 3] / 255
        const spread = Math.max(pixels[index], pixels[index + 1], pixels[index + 2]) - Math.min(pixels[index], pixels[index + 1], pixels[index + 2])
        const sampleWeight = alpha * Math.max(.15, spread / 255)
        red += pixels[index] * sampleWeight
        green += pixels[index + 1] * sampleWeight
        blue += pixels[index + 2] * sampleWeight
        weight += sampleWeight
      }
      if (weight > 2) accent.value = normalize(red / weight, green / weight, blue / weight)
    } catch {
      // Canvas extraction can fail for cross-origin images; deterministic fallback remains active.
    }
  }

  return { accent, rgb, foreground, readFromImage }
}

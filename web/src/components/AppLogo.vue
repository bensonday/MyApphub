<script setup lang="ts">
import { computed, ref } from 'vue'

const props = defineProps<{ name: string; src?: string }>()
const emit = defineEmits<{ loaded: [image: HTMLImageElement] }>()
const failed = ref(false)
const initials = computed(() => {
  const words = props.name.trim().split(/\s+/).filter(Boolean)
  const characters = words.length > 1 ? words.slice(0, 2).map((word) => Array.from(word)[0]) : Array.from(words[0] ?? '').slice(0, 2)
  return characters.filter((char) => /[\p{L}\p{N}]/u.test(char)).join('').toUpperCase() || 'APP'
})
</script>

<template>
  <div class="logo-chamber" :aria-label="`${name} 图标`">
    <div class="logo-asset" :class="{ 'logo-fallback': !src || failed }">
      <img v-if="src && !failed" :src="src" :alt="`${name} App 图标`" @load="emit('loaded', $event.target as HTMLImageElement)" @error="failed = true" />
      <template v-else><span class="fallback-orbit" aria-hidden="true"></span><span class="fallback-mark">{{ initials }}</span></template>
    </div>
    <span class="asset-label" aria-hidden="true">ORIGINAL ASSET</span>
  </div>
</template>

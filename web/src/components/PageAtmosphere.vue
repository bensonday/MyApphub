<script setup lang="ts">
import BrandFooter from './BrandFooter.vue'
defineProps<{ accentRgb?: string; accentForeground?: string }>()
</script>
<template>
  <div class="page-atmosphere" :style="{ '--app-accent-rgb': accentRgb ?? '152, 108, 255', '--app-accent-foreground': accentForeground ?? '#ffffff' }">
    <span class="ambient-orb" aria-hidden="true"></span><slot /><BrandFooter />
  </div>
</template>

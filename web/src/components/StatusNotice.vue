<script setup lang="ts">
defineProps<{ tone: 'warning' | 'error' | 'info'; title: string }>()
</script>
<template><div class="status-notice" :class="`status-${tone}`" role="status"><span class="notice-symbol" aria-hidden="true">{{ tone === 'warning' ? '!' : tone === 'error' ? '×' : 'i' }}</span><div><strong>{{ title }}</strong><div class="notice-body"><slot /></div></div></div></template>

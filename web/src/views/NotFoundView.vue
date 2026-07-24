<script setup lang="ts">
import { watchEffect } from 'vue'
import PageAtmosphere from '../components/PageAtmosphere.vue'
import { useProjectBrand } from '../composables/useProjectBrand'

const { projectName } = useProjectBrand()
watchEffect(() => { document.title = `分享地址不存在 · ${projectName.value}` })
</script>
<template><PageAtmosphere accent-rgb="230, 117, 81" accent-foreground="#111214"><main class="public-page narrow-page"><section class="single-panel not-found"><p class="giant-code">404</p><h1>这个分享地址已离开。</h1><p>应用可能已停止分享，或链接地址有误。请联系应用提供者获取新的安装地址。</p></section></main></PageAtmosphere></template>

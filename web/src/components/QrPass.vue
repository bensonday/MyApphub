<script setup lang="ts">
import QRCode from 'qrcode'
import { onMounted, ref, watch } from 'vue'
const props = defineProps<{ value: string; platform: 'ios' | 'android' }>()
const source = ref('')
async function render() { source.value = await QRCode.toDataURL(props.value, { width: 280, margin: 1, color: { dark: '#101114', light: '#ffffff' }, errorCorrectionLevel: 'M' }) }
onMounted(render)
watch(() => props.value, render)
</script>

<template>
  <aside class="qr-pass" aria-label="手机扫码安装">
    <div class="pass-heading"><span>ACCESS PASS / 01</span><span class="pass-star">✦</span></div>
    <div class="qr-shell"><img v-if="source" :src="source" alt="当前安装页面二维码" /></div>
    <h2>扫描，继续安装</h2>
    <p>使用{{ platform === 'ios' ? 'iPhone 或 iPad' : 'Android 手机' }}相机打开当前页面</p>
  </aside>
</template>

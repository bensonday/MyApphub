<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watchEffect } from 'vue'
import { ApiError, api, apiUrl } from '../api/client'
import PageAtmosphere from '../components/PageAtmosphere.vue'
import StatusNotice from '../components/StatusNotice.vue'
import { useRetryCountdown } from '../composables/useRetryCountdown'
import { useProjectBrand } from '../composables/useProjectBrand'

type State = 'ready' | 'creating' | 'waiting' | 'success' | 'error'
const state = ref<State>('ready')
const challenge = ref('')
const udid = ref('')
const message = ref('')
const copied = ref(false)
let timer: number | undefined
const retry = useRetryCountdown()
const { projectName } = useProjectBrand()
watchEffect(() => { document.title = `获取设备 UDID · ${projectName.value}` })

const environmentOk = computed(() => {
  const ua = navigator.userAgent
  const ios = /iPhone|iPad|iPod/.test(ua) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)
  const safari = /Safari/.test(ua) && !/CriOS|FxiOS|EdgiOS/.test(ua)
  return ios && safari
})
const activeStep = computed(() => state.value === 'ready' || state.value === 'creating' ? 1 : state.value === 'waiting' ? 2 : 3)

async function start() {
  if (retry.active.value) return
  state.value = 'creating'; message.value = ''
  try {
    const session = await api.createUdidSession()
    challenge.value = session.challenge
    localStorage.setItem('apphub_udid_challenge', session.challenge)
    state.value = 'waiting'
    window.location.assign(apiUrl(session.profileUrl))
    schedulePoll()
  } catch (error) {
    state.value = 'error'
    if (error instanceof ApiError && error.code === 'rate_limited') {
      retry.start(error.retryAfterSeconds)
      message.value = `请求过于频繁，请在 ${retry.seconds.value} 秒后重试。`
    } else message.value = error instanceof ApiError && error.code === 'udid_not_configured' ? 'UDID 服务暂未配置，请联系管理员。' : '无法创建临时描述文件，请稍后重试。'
  }
}

function schedulePoll() { window.clearTimeout(timer); timer = window.setTimeout(poll, 2200) }
async function poll() {
  if (!challenge.value || state.value !== 'waiting') return
  try {
    const result = await api.getUdidResult(challenge.value)
    if (result.udid) { udid.value = result.udid; state.value = 'success'; return }
    schedulePoll()
  } catch { state.value = 'error'; message.value = '临时会话已过期，请重新开始。'; localStorage.removeItem('apphub_udid_challenge') }
}

async function copy() {
  await navigator.clipboard.writeText(udid.value)
  copied.value = true
  if (challenge.value) await api.deleteUdidResult(challenge.value).catch(() => undefined)
  localStorage.removeItem('apphub_udid_challenge')
  window.setTimeout(() => { copied.value = false }, 1800)
}

function reset() { window.clearTimeout(timer); localStorage.removeItem('apphub_udid_challenge'); challenge.value = ''; udid.value = ''; message.value = ''; state.value = 'ready' }
async function restart() { reset(); await start() }
function resumePolling() { if (document.visibilityState === 'visible' && state.value === 'waiting') poll() }
onMounted(() => { const saved = localStorage.getItem('apphub_udid_challenge'); if (saved) { challenge.value = saved; state.value = 'waiting'; poll() } document.addEventListener('visibilitychange', resumePolling) })
onBeforeUnmount(() => { window.clearTimeout(timer); document.removeEventListener('visibilitychange', resumePolling) })
</script>

<template>
  <PageAtmosphere accent-rgb="152, 108, 255" accent-foreground="#ffffff"><main class="public-page narrow-page"><div class="release-status"><span class="verified-dot"></span><span>PRIVATE DEVICE TOOL</span><span class="release-serial">30 MINUTE SESSION</span></div>
    <StatusNotice v-if="!environmentOk" tone="warning" title="请使用 iPhone 或 iPad 的 Safari">当前浏览器不能安装 Apple 描述文件。你仍可查看流程，但无法开始。</StatusNotice>
    <section class="single-panel udid-panel"><span class="panel-cross" aria-hidden="true">+</span><h1>获取设备<br />UDID.</h1><p class="page-lede">只为把当前 iPhone 或 iPad 加入测试设备。临时结果会在 30 分钟后自动删除。</p>
      <div class="step-grid"><div v-for="step in 3" :key="step" class="udid-step" :class="{ active: activeStep === step, complete: activeStep > step }"><span>0{{ step }}</span><strong>{{ ['下载描述文件', '前往设置安装', '返回并复制'][step - 1] }}</strong><small>{{ ['使用 Safari 开始', '按系统提示完成', '发送给开发者'][step - 1] }}</small></div></div>
      <div v-if="state === 'waiting'" class="waiting-panel" role="status"><span class="waiting-orbit" aria-hidden="true"></span><div><strong>仍在等待安装结果</strong><p>请前往“设置”安装描述文件，完成后返回 Safari。</p></div></div>
      <div v-else-if="state === 'success'" class="result-panel"><span>YOUR DEVICE UDID</span><code>{{ udid }}</code></div>
      <StatusNotice v-else-if="state === 'error'" tone="error" title="本次获取未完成">{{ retry.active.value ? `请求过于频繁，请在 ${retry.seconds.value} 秒后重试。` : message }}</StatusNotice>
      <div v-else class="privacy-strip">不会建立长期设备库，也不会将 UDID 用于其他用途。</div>
      <button v-if="state === 'ready' || state === 'creating'" class="primary-action" type="button" :disabled="!environmentOk || state === 'creating'" @click="start"><span>{{ state === 'creating' ? '正在创建…' : '开始获取 UDID' }}</span><span class="action-arrow">↗</span></button>
      <button v-else-if="state === 'success'" class="primary-action" type="button" @click="copy"><span>{{ copied ? '已复制' : '复制 UDID' }}</span><span class="action-arrow">{{ copied ? '✓' : '↗' }}</span></button>
      <button v-else-if="state === 'error'" class="primary-action" type="button" :disabled="retry.active.value" @click="restart"><span>{{ retry.active.value ? `${retry.seconds.value} 秒后重试` : '重新开始' }}</span><span class="action-arrow">↻</span></button>
    </section>
  </main></PageAtmosphere>
</template>

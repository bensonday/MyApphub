<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, api, apiUrl } from '../api/client'
import AppLogo from '../components/AppLogo.vue'
import PageAtmosphere from '../components/PageAtmosphere.vue'
import QrPass from '../components/QrPass.vue'
import StatusNotice from '../components/StatusNotice.vue'
import { useAppAccent } from '../composables/useAppAccent'
import { useRetryCountdown } from '../composables/useRetryCountdown'
import { useProjectBrand } from '../composables/useProjectBrand'
import type { AccessResponse, Platform, PublicApp, PublicAppResponse } from '../types'

const route = useRoute()
const router = useRouter()
const slug = computed(() => String(route.params.slug ?? ''))
const { accent, rgb, foreground, readFromImage } = useAppAccent(slug.value)
const data = ref<PublicAppResponse | null>(null)
const unavailableApp = ref<PublicApp | null>(null)
const access = ref<AccessResponse | null>(null)
const loading = ref(true)
const submitting = ref(false)
const pageError = ref('')
const password = ref('')
const passwordVisible = ref(false)
const passwordError = ref('')
const notesExpanded = ref(false)
const pageUrl = window.location.href
const retry = useRetryCountdown()
const { projectName } = useProjectBrand()

function devicePlatform(): Platform | undefined {
  const ua = navigator.userAgent
  if (/Android/.test(ua)) return 'android'
  if (/iPhone|iPad|iPod/.test(ua) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)) return 'ios'
  return undefined
}

const app = computed(() => data.value?.app ?? unavailableApp.value ?? undefined)
const version = computed(() => data.value?.version)
const iconUrl = computed(() => {
  if (!app.value?.icon) return undefined
  if (app.value.icon.startsWith('demo/')) return `/${app.value.icon}`
  return apiUrl(`/assets/${encodeURIComponent(app.value.slug)}/${app.value.platform}/${encodeURIComponent(app.value.icon)}`)
})
const variants = computed(() => data.value?.variants ?? (data.value ? [{ app: data.value.app, version: data.value.version }] : []))
const needsPassword = computed(() => Boolean(app.value?.passwordProtected && !access.value))
const isIos = computed(() => app.value?.platform === 'ios')
const actionLabel = computed(() => isIos.value ? '直接安装' : '下载 APK')
const installUrl = computed(() => isIos.value ? access.value?.installUrl : access.value?.downloadUrl)
const notesLong = computed(() => (version.value?.releaseNotes?.length ?? 0) > 240)
const isAdHoc = computed(() => version.value?.distributionType?.toLowerCase().includes('ad') ?? false)
const expiresSoon = computed(() => {
  if (!version.value?.provisioningExpiresAt) return false
  const remaining = new Date(version.value.provisioningExpiresAt).getTime() - Date.now()
  return remaining > 0 && remaining <= 30 * 86_400_000
})
const formattedSize = computed(() => {
  const bytes = version.value?.fileSize ?? 0
  if (!bytes) return '—'
  return `${(bytes / 1_000_000).toFixed(bytes >= 100_000_000 ? 1 : 2)} MB`
})
const formattedDate = computed(() => version.value ? new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(version.value.uploadedAt)).replaceAll('/', '.') : '—')
const mismatchMessage = computed(() => {
  if (!app.value) return ''
  const ua = navigator.userAgent
  const mobileApple = /iPhone|iPad|iPod/.test(ua)
  const android = /Android/.test(ua)
  if (app.value.platform === 'ios' && android) return '这是 iOS 安装包，请使用 iPhone 或 iPad 打开。'
  if (app.value.platform === 'android' && mobileApple) return '这是 Android 安装包，请使用 Android 手机打开。'
  return ''
})
watch([app, projectName], ([currentApp, currentProjectName]) => {
  if (currentApp) document.title = `${currentApp.name} · ${currentProjectName}`
})

async function load() {
  loading.value = true
  pageError.value = ''
  data.value = null
  unavailableApp.value = null
  access.value = null
  try {
    data.value = await api.getApp(slug.value, devicePlatform())
    if (!data.value.app.passwordProtected) access.value = await api.access(slug.value, '', data.value.app.platform)
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return router.replace('/404')
    if (error instanceof ApiError && error.code === 'no_current_version' && error.app) {
      unavailableApp.value = error.app
      pageError.value = '当前暂无可安装版本。'
    } else {
      pageError.value = '页面暂时无法加载，请检查网络后重试。'
    }
  } finally { loading.value = false }
}

async function unlock() {
  if (!password.value || submitting.value || retry.active.value) return
  submitting.value = true
  passwordError.value = ''
  try { access.value = await api.access(slug.value, password.value, app.value?.platform) }
  catch (error) {
    if (error instanceof ApiError && error.code === 'invalid_password') passwordError.value = '密码不正确，请重试。'
    else if (error instanceof ApiError && error.code === 'rate_limited') {
      retry.start(error.retryAfterSeconds)
      passwordError.value = `尝试次数过多，请在 ${retry.seconds.value} 秒后重试。`
    }
    else passwordError.value = '验证失败，请检查网络后重试。'
  } finally { submitting.value = false }
}

async function install() {
  if (!data.value || submitting.value) return
  submitting.value = true
  pageError.value = ''
  try {
    access.value = await api.access(slug.value, password.value, app.value?.platform)
  } catch (error) {
    access.value = null
    if (error instanceof ApiError && error.code === 'invalid_password') passwordError.value = '安装密码已变更，请重新验证。'
    else if (error instanceof ApiError && error.code === 'rate_limited') {
      retry.start(error.retryAfterSeconds)
      passwordError.value = `尝试次数过多，请在 ${retry.seconds.value} 秒后重试。`
    } else pageError.value = '暂时无法生成新的安装链接，请稍后重试。'
    return
  } finally {
    submitting.value = false
  }
  if (installUrl.value?.startsWith('#demo')) return
  if (installUrl.value) window.location.assign(installUrl.value)
}

async function selectPlatform(platform: Platform) {
  if (app.value?.platform === platform || submitting.value) return
  const release = variants.value.find((item) => item.app.platform === platform)
  if (!release || !data.value) return
  data.value = { app: release.app, version: release.version, variants: data.value.variants }
  access.value = null
  pageError.value = ''
  passwordError.value = ''
  if (!release.app.passwordProtected) {
    try { access.value = await api.access(slug.value, '', platform) }
    catch { pageError.value = '暂时无法生成安装链接，请稍后重试。' }
  }
}

onMounted(load)
watch(slug, load)
</script>

<template>
  <PageAtmosphere :accent-rgb="rgb" :accent-foreground="foreground">
    <main class="public-page app-page">
      <div class="release-status"><span class="verified-dot"></span><span>PRIVATE RELEASE · VERIFIED</span><span class="release-serial">SECURE CHANNEL / {{ formattedDate }}</span></div>

      <div v-if="loading" class="install-grid" aria-label="正在加载应用信息" aria-busy="true">
        <section class="release-panel skeleton-panel"><div class="skeleton skeleton-icon"></div><div class="skeleton skeleton-title"></div><div class="skeleton skeleton-line"></div><div class="skeleton skeleton-button"></div></section>
        <aside class="qr-pass skeleton-pass"><div class="skeleton skeleton-qr"></div></aside>
      </div>

      <section v-else-if="pageError && !app" class="single-panel error-state"><span class="error-code">OFFLINE</span><h1>暂时无法打开</h1><p>{{ pageError }}</p><button class="primary-action compact-action" type="button" @click="load">重新加载 <span class="action-arrow">↗</span></button></section>

      <div v-else-if="app && !version" class="install-grid unavailable-grid">
        <section class="release-panel">
          <span class="panel-cross" aria-hidden="true">+</span>
          <div class="app-hero"><AppLogo :name="app.name" :src="iconUrl" @loaded="readFromImage" /><div class="app-identity"><h1>{{ app.name }}</h1><div class="version-row"><span class="platform-pill">{{ isIos ? 'iOS' : 'Android' }}</span><span>PRIVATE RELEASE</span></div></div></div>
          <div class="metadata-grid"><div><span>PACKAGE</span><strong>—</strong></div><div><span>STATUS</span><strong>等待发布</strong></div><div><span>CHANNEL</span><strong>安全分发</strong></div></div>
          <StatusNotice tone="warning" title="暂无可安装版本">应用信息已经发布，但管理员还没有设置当前安装版本。</StatusNotice>
          <button class="primary-action mobile-priority" type="button" disabled><span>暂无可安装版本</span><span class="action-arrow">—</span></button>
        </section>
        <aside class="qr-pass locked-pass"><span class="lock-icon">⌁</span><h2>安装通道尚未开放</h2><p>版本发布后可在此获取安装链接</p></aside>
      </div>

      <template v-else-if="app && version">
        <StatusNotice v-if="mismatchMessage" tone="warning" title="设备类型不匹配">{{ mismatchMessage }}</StatusNotice>
        <StatusNotice v-if="expiresSoon" tone="warning" title="描述文件即将到期">请联系应用提供者更新签名，避免之后无法安装。</StatusNotice>
        <StatusNotice v-if="pageError" tone="error" title="安装链接不可用">{{ pageError }}</StatusNotice>

        <div class="install-grid">
          <section class="release-panel">
            <span class="panel-cross" aria-hidden="true">+</span>
            <div class="app-hero">
              <AppLogo :name="app.name" :src="iconUrl" @loaded="readFromImage" />
              <div class="app-identity"><h1>{{ app.name }}</h1><div class="version-row"><span class="platform-pill">{{ isIos ? 'iOS' : 'Android' }}</span><span>v{{ version.versionName }} · build {{ version.buildNumber }}</span></div></div>
            </div>

            <div v-if="variants.length > 1" class="platform-switcher" aria-label="选择安装平台">
              <button v-for="item in variants" :key="item.app.platform" type="button" :class="{ active: item.app.platform === app.platform }" @click="selectPlatform(item.app.platform)">{{ item.app.platform === 'ios' ? 'iOS' : 'Android' }}</button>
            </div>

            <div class="metadata-grid"><div><span>PACKAGE</span><strong>{{ formattedSize }}</strong></div><div><span>REQUIRES</span><strong>{{ version.minimumOsVersion ? `${isIos ? 'iOS' : 'Android'} ${version.minimumOsVersion}+` : '—' }}</strong></div><div><span>UPDATED</span><strong>{{ formattedDate }}</strong></div></div>

            <form v-if="needsPassword" class="password-panel" @submit.prevent="unlock">
              <span class="password-index" aria-hidden="true">02</span><h2>验证安装密码</h2><p>输入同事提供的密码，解锁本次安装。</p>
              <label for="share-password">安装密码</label><div class="password-field"><input id="share-password" v-model="password" :type="passwordVisible ? 'text' : 'password'" autocomplete="current-password" required maxlength="256" placeholder="请输入安装密码" :aria-invalid="Boolean(passwordError)" :aria-describedby="passwordError ? 'password-error' : undefined" /><button type="button" @click="passwordVisible = !passwordVisible">{{ passwordVisible ? '隐藏' : '显示' }}</button></div>
              <p v-if="passwordError" id="password-error" class="field-error" aria-live="polite">{{ retry.active.value ? `尝试次数过多，请在 ${retry.seconds.value} 秒后重试。` : passwordError }}</p>
              <button class="primary-action" type="submit" :disabled="submitting || !password || retry.active.value"><span>{{ retry.active.value ? `${retry.seconds.value} 秒后重试` : submitting ? '正在验证…' : '验证并继续' }}</span><span class="action-arrow">→</span></button>
            </form>

            <template v-else>
              <section v-if="version.releaseNotes" class="release-notes"><h2>WHAT'S NEW</h2><p :class="{ 'notes-collapsed': notesLong && !notesExpanded }">{{ version.releaseNotes }}</p><button v-if="notesLong" class="text-action" type="button" @click="notesExpanded = !notesExpanded">{{ notesExpanded ? '收起' : '展开全部' }}</button></section>
              <button class="primary-action mobile-priority" type="button" :disabled="!installUrl || submitting || retry.active.value" @click="install"><span>{{ retry.active.value ? `${retry.seconds.value} 秒后重试` : submitting ? '正在生成链接…' : installUrl ? actionLabel : '暂无可安装版本' }}</span><span class="action-arrow">↗</span></button>
              <p v-if="isAdHoc" class="ad-hoc-note">Ad Hoc 包仅能安装在已加入描述文件的设备上。获取 UDID 不代表当前包可以立即安装。</p>
              <details class="install-guide"><summary>{{ isIos ? '首次安装如何信任开发者' : 'Android 安装说明' }}<span>查看步骤 →</span></summary><ol v-if="isIos"><li>点击“直接安装”并确认系统安装提示。</li><li>首次打开失败时，前往系统设置中的设备管理页面信任企业开发者。</li><li>iOS/iPadOS 18 及以上若系统要求，重启设备后再次打开。</li></ol><ol v-else><li>下载完成后打开 APK。</li><li>按系统提示，仅允许浏览器本次安装应用。</li><li>安装完成后可关闭该来源权限。</li></ol></details>
              <RouterLink v-if="isAdHoc" class="secondary-action" to="/udid">获取设备 UDID</RouterLink>
            </template>
          </section>
          <QrPass v-if="!needsPassword" :value="pageUrl" :platform="app.platform" />
          <aside v-else class="qr-pass locked-pass"><span class="lock-icon">⌁</span><h2>验证后显示二维码</h2><p>安装地址暂未公开</p></aside>
        </div>
      </template>
    </main>
  </PageAtmosphere>
</template>

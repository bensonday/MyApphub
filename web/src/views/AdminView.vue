<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { adminAPI, type ManagedApp, type TrashApp, type TrashVersion } from '../api/admin'

const checking = ref(true)
const authenticated = ref(false)
const token = ref('')
const apps = ref<ManagedApp[]>([])
const deletedApps = ref<TrashApp[]>([])
const deletedVersions = ref<TrashVersion[]>([])
const busy = ref(false)
const error = ref('')
const notice = ref('')
const passwordDrafts = ref<Record<string, string>>({})

async function refresh() {
  const [appResult, trashResult] = await Promise.all([adminAPI.apps(), adminAPI.trash()])
  apps.value = appResult.apps
  deletedApps.value = trashResult.apps
  deletedVersions.value = trashResult.versions
}

async function initialize() {
  try {
    authenticated.value = (await adminAPI.session()).authenticated
    if (authenticated.value) await refresh()
  } catch { error.value = '无法连接管理服务，请稍后重试。' }
  finally { checking.value = false }
}

async function signIn() {
  if (!token.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    await adminAPI.login(token.value)
    token.value = ''
    authenticated.value = true
    await refresh()
  } catch { error.value = '登录失败，请检查管理令牌。' }
  finally { busy.value = false }
}

async function signOut() {
  await adminAPI.logout().catch(() => undefined)
  authenticated.value = false
  apps.value = []
}

async function action(label: string, run: () => Promise<unknown>, confirmMessage?: string): Promise<boolean> {
  if (busy.value || (confirmMessage && !window.confirm(confirmMessage))) return false
  busy.value = true; error.value = ''; notice.value = ''
  try { await run(); await refresh(); notice.value = `${label}成功。`; return true }
  catch (reason) { error.value = reason instanceof Error ? reason.message : `${label}失败。`; return false }
  finally { busy.value = false }
}

async function updatePassword(slug: string) {
  const password = passwordDrafts.value[slug] || ''
  if (await action('修改密码', () => adminAPI.password(slug, password), password ? '确定更新分享密码？旧安装链接会立即失效。' : '确定清除分享密码？'))
    passwordDrafts.value[slug] = ''
}

const size = (bytes: number) => `${(bytes / 1_000_000).toFixed(1)} MB`
const date = (value: string) => new Date(value).toLocaleString('zh-CN')
const shareUrl = (slug: string) => `https://apphub.bensonday.cc/app/${encodeURIComponent(slug)}`
onMounted(initialize)
</script>

<template>
  <div class="admin-root">
    <header class="admin-topbar"><div><span class="admin-mark">MYAPPHUB / CONTROL</span><h1>应用管理</h1></div><button v-if="authenticated" class="admin-ghost" type="button" @click="signOut">退出登录</button></header>
    <main class="admin-content">
      <p v-if="checking" class="admin-muted">正在连接管理服务…</p>
      <form v-else-if="!authenticated" class="admin-card admin-login" @submit.prevent="signIn">
        <span class="admin-eyebrow">ADMIN ACCESS</span><h2>进入管理端</h2><p>输入部署时配置的管理令牌。令牌只用于本次登录，不保存在浏览器本地存储中。</p>
        <label for="admin-token">管理令牌</label><input id="admin-token" v-model="token" type="password" autocomplete="off" required />
        <button class="admin-primary" type="submit" :disabled="busy">{{ busy ? '正在验证…' : '登录' }}</button>
      </form>
      <template v-else>
        <div class="admin-summary"><div><strong>{{ new Set(apps.map(app => app.slug)).size }}</strong><span>应用</span></div><div><strong>{{ apps.length }}</strong><span>平台版本</span></div><div><strong>{{ apps.reduce((count, app) => count + app.versions.length, 0) }}</strong><span>安装包</span></div></div>
        <section class="admin-card admin-help"><div><span class="admin-eyebrow">UPLOAD WORKFLOW</span><h2>通过 CLI 上传安装包</h2><p>CLI 在本机校验 IPA/APK 签名与元数据，并将文件分块上传到 R2。上传后在这里管理发布状态。</p></div><code>apphub upload ./App.ipa</code></section>
        <section class="admin-section"><div class="admin-section-head"><div><span class="admin-eyebrow">RELEASES</span><h2>应用与版本</h2></div><button class="admin-ghost" type="button" @click="refresh">刷新</button></div>
          <p v-if="!apps.length" class="admin-empty">还没有应用。上传第一个 IPA 或 APK 后会显示在这里。</p>
          <article v-for="app in apps" :key="`${app.slug}-${app.platform}`" class="admin-card admin-app">
            <div class="admin-app-head"><div><span class="admin-pill">{{ app.platform === 'ios' ? 'iOS' : 'Android' }}</span><h3>{{ app.name }}</h3><p>{{ app.slug }} · {{ app.identifier }}</p></div><a :href="shareUrl(app.slug)" target="_blank" rel="noopener">查看分享页 ↗</a></div>
            <div class="admin-version" v-for="version in app.versions" :key="version.id"><div><strong>{{ version.versionName }} <small>({{ version.buildNumber }})</small></strong><p>{{ size(version.fileSize) }} · {{ date(version.uploadedAt) }} <span v-if="version.id === app.currentVersionId" class="admin-current">当前版本</span></p></div><div class="admin-actions"><button v-if="version.id !== app.currentVersionId" type="button" :disabled="busy" @click="action('发布版本', () => adminAPI.promote(app.slug, version.id), `确定将 ${version.versionName} 设为当前版本？`)">发布</button><button v-if="version.id !== app.currentVersionId" type="button" class="danger" :disabled="busy" @click="action('删除版本', () => adminAPI.deleteVersion(app.slug, version.id), `确定删除 ${version.versionName}？可在 7 天内恢复。`)">删除</button></div></div>
            <div class="admin-app-foot"><div><label :for="`password-${app.slug}-${app.platform}`">分享密码 {{ app.passwordProtected ? '（已设置）' : '（未设置）' }}</label><input :id="`password-${app.slug}-${app.platform}`" v-model="passwordDrafts[app.slug]" type="password" placeholder="新密码；留空表示清除" autocomplete="new-password" /><button type="button" :disabled="busy" @click="updatePassword(app.slug)">保存密码</button></div><button class="danger" type="button" :disabled="busy" @click="action('删除应用', () => adminAPI.deleteApp(app.slug), `确定删除 ${app.name} 的所有平台？可在 7 天内恢复。`)">删除应用</button></div>
          </article>
        </section>
        <section v-if="deletedApps.length || deletedVersions.length" class="admin-section"><div class="admin-section-head"><div><span class="admin-eyebrow">RECYCLE BIN</span><h2>回收站</h2></div></div><div class="admin-card"><div v-for="app in deletedApps" :key="app.slug" class="admin-trash"><span>应用：{{ app.name }} · {{ date(app.deleted_at) }}</span><button type="button" :disabled="busy" @click="action('恢复应用', () => adminAPI.restoreApp(app.slug))">恢复</button></div><div v-for="version in deletedVersions" :key="version.id" class="admin-trash"><span>版本：{{ version.slug }} / {{ version.platform }} / {{ version.version_name }}</span><button type="button" :disabled="busy" @click="action('恢复版本', () => adminAPI.restoreVersion(version.slug, version.id))">恢复</button></div></div></section>
      </template>
      <p v-if="error" class="admin-message admin-error" role="alert">{{ error }}</p><p v-if="notice" class="admin-message" role="status">{{ notice }}</p>
    </main>
  </div>
</template>

<style scoped>
.admin-root{min-height:100vh;background:#f3f1eb;color:#1d2425;font-family:var(--font-sans)}.admin-topbar{display:flex;justify-content:space-between;align-items:center;gap:20px;padding:30px max(24px,calc((100vw - 1120px)/2));background:#202829;color:#fff}.admin-mark,.admin-eyebrow{font:700 11px/1.5 var(--font-mono);letter-spacing:.13em;color:#8daba1}.admin-topbar h1{font-size:30px;margin:4px 0 0}.admin-content{width:min(1120px,calc(100% - 32px));margin:30px auto 80px}.admin-card{background:#fff;border:1px solid #dce0dc;border-radius:18px;box-shadow:0 9px 30px #1d292310;padding:24px;margin-bottom:16px}.admin-login{max-width:430px;margin:9vh auto;padding:34px}.admin-login h2,.admin-help h2,.admin-section h2{margin:5px 0 8px}.admin-login p,.admin-help p,.admin-muted,.admin-app p,.admin-version p{color:#67726e}.admin-login label,.admin-app-foot label{display:block;font-weight:700;margin:20px 0 6px}.admin-login input,.admin-app-foot input{width:100%;height:44px;border:1px solid #bdc8c2;border-radius:10px;padding:0 13px;background:#fff;color:#172321}.admin-primary,.admin-app button,.admin-trash button,.admin-ghost{min-height:39px;border:1px solid #b7c5bc;border-radius:9px;padding:7px 14px;background:#fff;color:#193b31;font-weight:700;cursor:pointer}.admin-primary{width:100%;margin-top:20px;border-color:#203b32;background:#203b32;color:#fff}.admin-topbar .admin-ghost{background:transparent;color:#fff}.admin-summary{display:grid;grid-template-columns:repeat(3,1fr);gap:14px;margin-bottom:16px}.admin-summary>div{display:flex;align-items:baseline;gap:12px;padding:20px 25px;background:#fff;border:1px solid #dce0dc;border-radius:16px}.admin-summary strong{font-size:30px}.admin-summary span{color:#718078}.admin-help{display:flex;justify-content:space-between;align-items:center;gap:20px}.admin-help code{padding:11px 14px;border-radius:8px;background:#eff4f0;white-space:nowrap}.admin-section{margin-top:30px}.admin-section-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px}.admin-section-head h2{margin:0}.admin-empty{padding:35px;text-align:center;color:#6b776f}.admin-app-head{display:flex;justify-content:space-between;align-items:start;gap:16px;border-bottom:1px solid #e4e9e5;padding-bottom:16px}.admin-app-head h3{font-size:22px;margin:5px 0 1px}.admin-app-head p{margin:0}.admin-app-head a{color:#1f6b4e;text-decoration:none;white-space:nowrap}.admin-pill,.admin-current{display:inline-block;border-radius:999px;padding:2px 8px;background:#e7f1eb;color:#236046;font-size:11px;font-weight:800}.admin-current{margin-left:6px}.admin-version{display:flex;justify-content:space-between;gap:20px;align-items:center;padding:14px 0;border-bottom:1px solid #edf0ec}.admin-version p{margin:2px 0 0;font-size:12px}.admin-version small{color:#65716c}.admin-actions{display:flex;gap:8px}.admin-app .danger{color:#ab392d;border-color:#e9c5bf}.admin-app-foot{display:flex;align-items:end;justify-content:space-between;gap:20px;padding-top:16px}.admin-app-foot>div{display:flex;align-items:end;gap:8px;flex-wrap:wrap}.admin-app-foot label{width:100%;margin:0}.admin-app-foot input{width:220px}.admin-trash{display:flex;justify-content:space-between;align-items:center;padding:12px 0;border-bottom:1px solid #edf0ec}.admin-trash:last-child{border:0}.admin-message{position:fixed;right:22px;bottom:22px;padding:13px 18px;background:#e3f4e9;border:1px solid #abcdb4;border-radius:10px;box-shadow:0 8px 30px #0002}.admin-error{background:#fff1ef;border-color:#e5a9a1;color:#9c2e25}button:disabled{opacity:.5;cursor:not-allowed}@media(max-width:720px){.admin-topbar{padding:23px 17px}.admin-content{margin-top:17px}.admin-summary{grid-template-columns:repeat(3,1fr);gap:7px}.admin-summary>div{display:block;padding:12px}.admin-summary strong{display:block;font-size:24px}.admin-help,.admin-app-head,.admin-version,.admin-app-foot{display:block}.admin-help code{display:block;margin-top:12px;overflow:auto}.admin-app-head a{display:block;margin-top:10px}.admin-actions{margin-top:10px}.admin-app-foot>div{margin-bottom:15px}.admin-app-foot input{width:min(220px,100%)}}
</style>

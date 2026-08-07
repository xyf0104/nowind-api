<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

interface ReleaseAsset {
  name: string
  browser_download_url: string
}

interface GitHubRelease {
  tag_name: string
  name: string | null
  draft: boolean
  prerelease: boolean
  published_at: string | null
  assets: ReleaseAsset[]
}

const releasesURL = 'https://api.github.com/repos/xyf0104/xiass-api/releases?per_page=30'

const release = ref<GitHubRelease | null>(null)
const loading = ref(true)
const error = ref('')
const isIOS = ref(false)
const installStarted = ref(false)

const manifest = computed(() => release.value?.assets.find((asset) => {
  const name = asset.name.toLowerCase()
  return name.startsWith('xiassadmin-') && name.endsWith('.plist')
}) ?? null)

const otaInstallURL = computed(() => {
  if (!manifest.value) return ''
  return `itms-services://?action=download-manifest&url=${encodeURIComponent(manifest.value.browser_download_url)}`
})

const version = computed(() => release.value?.tag_name.replace(/^ios-v/i, '') || '')

function isInstallableRelease(candidate: GitHubRelease): boolean {
  if (candidate.draft || candidate.prerelease) return false
  const assetNames = candidate.assets.map((asset) => asset.name.toLowerCase())
  return assetNames.some((name) => name.startsWith('xiassadmin-') && name.endsWith('.ipa')) &&
    assetNames.some((name) => name.startsWith('xiassadmin-') && name.endsWith('.plist'))
}

async function loadRelease() {
  loading.value = true
  error.value = ''
  installStarted.value = false

  try {
    const response = await fetch(releasesURL, {
      headers: { Accept: 'application/vnd.github+json' },
    })
    if (!response.ok) throw new Error('GitHub release request failed')

    const releases = await response.json() as GitHubRelease[]
    const latest = releases.find(isInstallableRelease)
    if (!latest) throw new Error('No installable iOS release found')
    release.value = latest
  } catch {
    release.value = null
    error.value = '暂时无法读取 iPhone 管理端的最新安装包，请稍后重试。'
  } finally {
    loading.value = false
  }
}

function startInstall() {
  installStarted.value = true
}

onMounted(() => {
  isIOS.value = /iPad|iPhone|iPod/.test(navigator.userAgent) ||
    (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)
  void loadRelease()
})
</script>

<template>
  <main class="min-h-screen bg-slate-100 px-5 py-8 text-slate-950 dark:bg-[#08131c] dark:text-white sm:px-8 sm:py-12">
    <div class="mx-auto flex min-h-[calc(100svh-4rem)] max-w-xl flex-col justify-center">
      <header class="mb-8 flex items-center gap-3">
        <img
          src="/brand/xiass-mark-light.png"
          alt="XIASS API"
          class="h-11 w-11 rounded-lg object-contain dark:hidden"
        >
        <img
          src="/brand/xiass-mark-dark.png"
          alt="XIASS API"
          class="hidden h-11 w-11 rounded-lg object-contain dark:block"
        >
        <div>
          <p class="text-sm font-semibold text-sky-700 dark:text-cyan-300">XIASS API</p>
          <h1 class="text-xl font-bold">iPhone 管理端</h1>
        </div>
      </header>

      <section class="rounded-lg border border-slate-200 bg-white p-6 shadow-sm dark:border-white/15 dark:bg-[#10202a] sm:p-8">
        <div class="flex items-start gap-4">
          <div class="flex h-12 w-12 shrink-0 items-center justify-center rounded-lg bg-sky-600 text-white shadow-sm">
            <Icon name="download" size="lg" />
          </div>
          <div class="min-w-0">
            <p class="text-sm font-medium text-sky-700 dark:text-cyan-300">原生 iOS 管理端</p>
            <h2 class="mt-1 text-2xl font-bold">直接安装最新版本</h2>
            <p class="mt-2 text-sm leading-6 text-slate-600 dark:text-slate-300">
              点击后由 iOS 系统完成安装或覆盖更新，不会下载普通 IPA 文件。
            </p>
          </div>
        </div>

        <div v-if="loading" class="mt-8 flex items-center gap-3 text-sm text-slate-600 dark:text-slate-300" aria-live="polite">
          <Icon name="refresh" size="sm" class="animate-spin" />
          正在读取最新版本…
        </div>

        <div v-else-if="error" class="mt-8" role="alert">
          <p class="text-sm leading-6 text-rose-700 dark:text-rose-300">{{ error }}</p>
          <button
            type="button"
            class="mt-4 inline-flex items-center gap-2 rounded-lg border border-slate-300 px-4 py-2.5 text-sm font-semibold text-slate-800 transition-colors hover:bg-slate-100 dark:border-white/20 dark:text-white dark:hover:bg-white/10"
            title="重新读取最新版本"
            @click="loadRelease"
          >
            <Icon name="refresh" size="sm" />
            重新读取
          </button>
        </div>

        <template v-else-if="release && otaInstallURL">
          <div class="mt-8 flex items-end justify-between gap-4 border-y border-slate-200 py-4 dark:border-white/15">
            <div>
              <p class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400">最新版本</p>
              <p class="mt-1 text-lg font-bold tabular-nums">v{{ version }}</p>
            </div>
            <button
              type="button"
              class="flex h-10 w-10 items-center justify-center rounded-lg text-slate-600 transition-colors hover:bg-slate-100 hover:text-sky-700 dark:text-slate-300 dark:hover:bg-white/10 dark:hover:text-cyan-200"
              title="检查最新版本"
              @click="loadRelease"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
          </div>

          <a
            v-if="isIOS"
            :href="otaInstallURL"
            class="mt-6 flex w-full items-center justify-center gap-2 rounded-lg bg-sky-600 px-5 py-3.5 text-base font-bold text-white transition-colors hover:bg-sky-700 active:bg-sky-800"
            @click="startInstall"
          >
            <Icon name="download" size="md" />
            安装或更新 iPhone 管理端
          </a>
          <p v-else class="mt-6 text-sm leading-6 text-amber-700 dark:text-amber-300">
            请使用已加入分发设备的 iPhone 或 iPad 在 Safari 中打开本页。
          </p>

          <p v-if="installStarted" class="mt-4 text-sm leading-6 text-emerald-700 dark:text-emerald-300" aria-live="polite">
            已交给 iOS 系统安装服务，请在弹出的确认框中继续安装。
          </p>
          <p class="mt-5 text-xs leading-5 text-slate-500 dark:text-slate-400">
            仅限已加入 XIASS 管理端 Ad Hoc 分发描述文件的设备安装。
          </p>
        </template>
      </section>
    </div>
  </main>
</template>

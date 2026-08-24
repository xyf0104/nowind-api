<template>
  <section class="overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
    <header class="flex flex-col gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
      <div class="flex min-w-0 items-center gap-3">
        <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-primary-50 text-primary-600 dark:bg-primary-950/40 dark:text-primary-400">
          <Icon name="server" size="md" :stroke-width="2" />
        </span>
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">服务器浏览器</h2>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">仅在需要人工处理登录或未知页面时打开，平时请使用成员自动化工作区。</p>
        </div>
      </div>
      <div class="flex items-center gap-2 self-start sm:self-auto">
        <span class="inline-flex items-center gap-1.5 text-xs font-medium" :class="browserStateClass">
          <span class="h-1.5 w-1.5 rounded-full" :class="browserDotClass"></span>
          {{ browserStateLabel }}
        </span>
        <button
          type="button"
          class="btn btn-secondary flex items-center gap-2 whitespace-nowrap"
          title="返回成员模块"
          @click="$emit('open-modular')"
        >
          <Icon name="grid" size="sm" :stroke-width="2" />
          <span>返回自动化工作区</span>
        </button>
        <button
          type="button"
          class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0"
          :disabled="loading || !configured"
          title="刷新服务器浏览器"
          aria-label="刷新服务器浏览器"
          @click="$emit('reload')"
        >
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" :stroke-width="2" />
        </button>
      </div>
    </header>

    <div class="flex flex-col gap-3 border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-700 dark:bg-dark-900/35 sm:flex-row sm:items-center sm:justify-between">
      <div class="flex min-w-0 items-center gap-2">
        <Icon name="mail" size="sm" class="flex-shrink-0 text-gray-500 dark:text-gray-400" :stroke-width="2" />
        <div class="min-w-0">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">邀请成员邮箱</p>
          <code v-if="mailboxEmail" class="block truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ mailboxEmail }}</code>
          <p v-else class="text-sm text-gray-400 dark:text-gray-500">开始创建后显示</p>
        </div>
      </div>
      <button
        v-if="mailboxEmail"
        type="button"
        class="btn btn-secondary flex h-9 w-9 flex-shrink-0 items-center justify-center p-0"
        title="复制邀请成员邮箱"
        aria-label="复制邀请成员邮箱"
        @click="$emit('copyMailbox')"
      >
        <Icon name="copy" size="sm" :stroke-width="2" />
      </button>
    </div>

    <div data-testid="team-browser-frame" class="relative aspect-video min-h-[320px] w-full overflow-hidden bg-gray-100 dark:bg-dark-900 sm:min-h-[400px] xl:min-h-[500px]">
      <iframe
        v-if="embedUrl"
        :key="embedUrl"
        :src="embedUrl"
        title="服务器 Chromium 浏览器"
        class="absolute inset-0 h-full w-full border-0"
        referrerpolicy="no-referrer"
        @load="frameLoading = false"
      ></iframe>

      <div v-if="frameLoading && embedUrl" class="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-gray-100/90 text-center dark:bg-dark-900/90">
        <Icon name="refresh" size="md" class="animate-spin text-primary-600 dark:text-primary-400" :stroke-width="2" />
        <p class="text-sm font-medium text-gray-700 dark:text-gray-200">正在连接服务器浏览器</p>
      </div>

      <div v-if="!configured" class="absolute inset-0 flex flex-col items-center justify-center px-8 text-center">
        <span class="mb-3 flex h-11 w-11 items-center justify-center rounded-md bg-gray-200 text-gray-500 dark:bg-dark-700 dark:text-gray-400">
          <Icon name="server" size="md" :stroke-width="2" />
        </span>
        <p class="text-sm font-semibold text-gray-800 dark:text-gray-100">服务器浏览器尚未启用</p>
        <p class="mt-1 max-w-sm text-xs leading-5 text-gray-500 dark:text-gray-400">部署 Chromium 服务并启用配置后，这里会载入持久化的浏览器工作区。</p>
      </div>

      <div v-else-if="error" class="absolute inset-0 flex flex-col items-center justify-center px-8 text-center">
        <span class="mb-3 flex h-11 w-11 items-center justify-center rounded-md bg-red-50 text-red-600 dark:bg-red-950/30 dark:text-red-400">
          <Icon name="exclamationTriangle" size="md" :stroke-width="2" />
        </span>
        <p class="text-sm font-semibold text-gray-800 dark:text-gray-100">浏览器工作区不可用</p>
        <p class="mt-1 max-w-sm text-xs leading-5 text-gray-500 dark:text-gray-400">{{ error }}</p>
        <button type="button" class="btn btn-secondary mt-4 flex items-center gap-2 whitespace-nowrap" :disabled="loading" @click="$emit('reload')">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" :stroke-width="2" />
          <span>重新连接</span>
        </button>
        <button v-if="controlConflict" type="button" class="btn btn-primary mt-3 flex items-center gap-2 whitespace-nowrap" :disabled="loading" @click="$emit('force-take-over')">
          <Icon name="arrowRight" size="sm" :stroke-width="2" />
          <span>接管浏览器</span>
        </button>
      </div>
    </div>

    <footer class="flex items-start gap-2 border-t border-gray-200 px-4 py-3 text-xs leading-5 text-gray-500 dark:border-dark-700 dark:text-gray-400">
      <Icon name="shield" size="sm" class="mt-0.5 flex-shrink-0" :stroke-width="2" />
      <p>首次在此手动登录后，浏览器配置目录会保留登录状态。为减少多设备同时打开图形浏览器造成的中断，平时请返回自动化工作区；验证码、短信、身份验证及工作区确认仍由你在页面中完成。</p>
    </footer>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Icon } from '@/components/icons'

const props = withDefaults(defineProps<{
  configured: boolean
  embedUrl: string
  loading?: boolean
  error?: string
  mailboxEmail?: string
  membersReady?: boolean
  controlConflict?: boolean
}>(), {
  loading: false,
  error: '',
  mailboxEmail: '',
  membersReady: false,
  controlConflict: false
})

defineEmits<{
  reload: []
  copyMailbox: []
  'open-modular': []
  'force-take-over': []
}>()

const frameLoading = ref(Boolean(props.embedUrl))

watch(() => props.embedUrl, (embedUrl) => {
  frameLoading.value = Boolean(embedUrl)
})

const browserStateLabel = computed(() => {
  if (!props.configured) return '未配置'
  if (props.loading || frameLoading.value) return '连接中'
  if (props.error) return '需处理'
  return props.embedUrl ? '已连接' : '待连接'
})

const browserStateClass = computed(() => {
  if (!props.configured || props.error) return 'text-amber-600 dark:text-amber-400'
  if (props.loading || frameLoading.value) return 'text-primary-600 dark:text-primary-400'
  return 'text-green-600 dark:text-green-400'
})

const browserDotClass = computed(() => {
  if (!props.configured || props.error) return 'bg-amber-500'
  if (props.loading || frameLoading.value) return 'bg-primary-500'
  return 'bg-green-500'
})
</script>

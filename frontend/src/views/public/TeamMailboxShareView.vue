<template>
  <main class="min-h-[100dvh] bg-gray-100 px-3 py-4 dark:bg-dark-950 sm:px-6 sm:py-8">
    <section class="mx-auto flex min-h-[calc(100dvh-2rem)] w-full max-w-5xl items-center sm:min-h-[calc(100dvh-4rem)]">
      <div class="w-full overflow-hidden rounded-[28px] border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <header class="flex min-w-0 items-center justify-between gap-3 border-b border-gray-200 px-4 py-3.5 dark:border-dark-700 sm:px-6 sm:py-4">
          <div class="flex min-w-0 items-center gap-3">
            <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl bg-primary-50 text-primary-700 dark:bg-primary-950/45 dark:text-primary-300">
              <Icon name="mail" size="md" :stroke-width="2" />
            </span>
            <div class="min-w-0">
              <h1 class="truncate text-base font-semibold text-gray-900 dark:text-gray-100">邮箱收件箱</h1>
              <code v-if="email" class="mt-0.5 block truncate font-mono text-xs text-gray-500 dark:text-gray-400" :title="email">{{ email }}</code>
            </div>
          </div>
          <button
            type="button"
            class="btn btn-secondary flex h-9 w-9 shrink-0 items-center justify-center p-0"
            title="刷新收件箱"
            aria-label="刷新收件箱"
            :disabled="!shareToken || checking || linkState === 'invalid'"
            @click="void fetchInbox()"
          >
            <Icon name="refresh" size="sm" :class="checking ? 'animate-spin' : ''" :stroke-width="2" />
          </button>
        </header>

        <div v-if="linkState === 'invalid'" class="flex min-h-64 flex-col items-center justify-center px-5 py-10 text-center sm:min-h-80">
          <span class="flex h-12 w-12 items-center justify-center rounded-2xl bg-red-50 text-red-600 dark:bg-red-950/35 dark:text-red-300">
            <Icon name="xCircle" size="lg" :stroke-width="2" />
          </span>
          <p class="mt-4 text-sm font-semibold text-gray-800 dark:text-gray-100">接码链接不可用</p>
        </div>

        <div v-else-if="!shareToken" class="flex min-h-64 flex-col items-center justify-center px-5 py-10 text-center sm:min-h-80">
          <span class="flex h-12 w-12 items-center justify-center rounded-2xl bg-amber-50 text-amber-700 dark:bg-amber-950/35 dark:text-amber-300">
            <Icon name="link" size="lg" :stroke-width="2" />
          </span>
          <p class="mt-4 text-sm font-semibold text-gray-800 dark:text-gray-100">缺少接码链接</p>
        </div>

        <template v-else>
          <p v-if="linkState === 'error'" class="border-b border-red-100 bg-red-50 px-4 py-2.5 text-center text-sm leading-5 text-red-700 dark:border-red-950/60 dark:bg-red-950/20 dark:text-red-300" role="alert">
            {{ errorMessage }}
          </p>

          <div class="grid min-h-[31rem] min-w-0 grid-cols-1 md:grid-cols-[minmax(15rem,19rem)_minmax(0,1fr)]">
            <aside class="min-w-0 border-b border-gray-200 bg-gray-50/80 dark:border-dark-700 dark:bg-dark-900/35 md:border-b-0 md:border-r">
              <div v-if="latestCode" class="border-b border-gray-200 px-3 py-3 dark:border-dark-700">
                <button
                  type="button"
                  class="group flex w-full min-w-0 items-center justify-between gap-3 rounded-2xl border border-green-200 bg-green-50 px-3 py-2.5 text-left transition-colors hover:bg-green-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-green-500 dark:border-green-900/60 dark:bg-green-950/25 dark:hover:bg-green-950/40"
                  data-testid="team-mailbox-share-code"
                  title="点击复制验证码"
                  @click="copyCode(latestCode)"
                >
                  <span class="min-w-0">
                    <span class="flex items-center gap-1.5 text-xs font-semibold text-green-800 dark:text-green-200">
                      <Icon :name="copied ? 'check' : 'copy'" size="xs" :stroke-width="2.5" />
                      <span>{{ copied ? '已复制验证码' : '验证码' }}</span>
                    </span>
                    <code class="mt-1 block truncate font-mono text-xl font-semibold tabular-nums text-green-900 dark:text-green-100">{{ latestCode }}</code>
                  </span>
                  <Icon name="chevronRight" size="sm" class="shrink-0 text-green-700/70 dark:text-green-300/70" :stroke-width="2" />
                </button>
              </div>

              <div class="max-h-[20rem] overflow-y-auto overscroll-contain md:max-h-[34rem]">
                <button
                  v-for="message in messages"
                  :key="message.id"
                  type="button"
                  class="w-full min-w-0 border-b border-gray-200 px-3 py-3 text-left transition-colors last:border-b-0 hover:bg-white focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500 dark:border-dark-700 dark:hover:bg-dark-800"
                  :class="selectedMessageID === message.id ? 'bg-white dark:bg-dark-800' : ''"
                  :data-testid="`team-mailbox-message-${message.id}`"
                  @click="void selectMessage(message.id)"
                >
                  <div class="flex min-w-0 items-center justify-between gap-2">
                    <span class="truncate text-xs font-medium text-gray-600 dark:text-gray-300">{{ message.from || '未知发件人' }}</span>
                    <time v-if="message.received_at" class="shrink-0 text-[11px] text-gray-400 dark:text-gray-500">{{ formatReceivedAt(message.received_at) }}</time>
                  </div>
                  <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ message.subject || '无主题邮件' }}</p>
                  <p v-if="message.preview" class="mt-1 line-clamp-2 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ message.preview }}</p>
                </button>

                <div v-if="!messages.length && !checking" class="flex min-h-52 flex-col items-center justify-center px-5 py-8 text-center">
                  <span class="flex h-10 w-10 items-center justify-center rounded-2xl bg-white text-gray-400 shadow-sm dark:bg-dark-800 dark:text-gray-500">
                    <Icon name="mail" size="md" :stroke-width="2" />
                  </span>
                  <p class="mt-3 text-sm font-medium text-gray-600 dark:text-gray-300">暂无邮件</p>
                </div>
              </div>
            </aside>

            <section class="flex min-w-0 flex-col bg-white dark:bg-dark-800">
              <div v-if="messageLoading" class="flex min-h-64 flex-1 items-center justify-center px-5 py-10 text-center text-sm text-gray-500 dark:text-gray-400">
                正在打开邮件
              </div>

              <article v-else-if="selectedMessage" class="flex min-h-0 flex-1 flex-col">
                <header class="min-w-0 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:px-6">
                  <h2 class="break-words text-base font-semibold leading-6 text-gray-900 dark:text-gray-100">{{ selectedMessage.subject || '无主题邮件' }}</h2>
                  <div class="mt-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                    <span class="max-w-full break-all">{{ selectedMessage.from || '未知发件人' }}</span>
                    <time v-if="selectedMessage.received_at">{{ formatReceivedAt(selectedMessage.received_at, true) }}</time>
                  </div>
                  <button
                    v-if="selectedMessage.code"
                    type="button"
                    class="btn btn-secondary mt-3 flex items-center gap-1.5 whitespace-nowrap"
                    title="复制验证码"
                    @click="copyCode(selectedMessage.code)"
                  >
                    <Icon :name="copied ? 'check' : 'copy'" size="xs" :stroke-width="2.5" />
                    <code class="font-mono text-xs tabular-nums">{{ selectedMessage.code }}</code>
                  </button>
                </header>
                <div class="min-h-0 flex-1 overflow-y-auto px-4 py-5 sm:px-6">
                  <pre class="m-0 whitespace-pre-wrap break-words font-sans text-sm leading-6 text-gray-700 dark:text-gray-200">{{ selectedMessage.body || selectedMessage.preview || '邮件内容为空' }}</pre>
                </div>
              </article>

              <div v-else class="flex min-h-64 flex-1 flex-col items-center justify-center px-5 py-10 text-center">
                <span class="flex h-12 w-12 items-center justify-center rounded-2xl bg-gray-50 text-gray-400 dark:bg-dark-900/50 dark:text-gray-500">
                  <Icon name="mail" size="lg" :stroke-width="2" />
                </span>
                <p class="mt-4 text-sm font-medium text-gray-600 dark:text-gray-300">选择一封邮件查看内容</p>
              </div>
            </section>
          </div>

          <p class="border-t border-gray-200 px-4 py-2.5 text-center text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">{{ checkedAtLabel }}</p>
        </template>
      </div>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Icon } from '@/components/icons'
import { buildApiUrl } from '@/api/client'

interface TeamMailboxMessage {
  id: string
  from?: string
  subject?: string
  preview?: string
  received_at?: string
  code?: string
}

interface TeamMailboxMessageDetail extends TeamMailboxMessage {
  body: string
}

interface TeamMailboxMessagesResponse {
  email: string
  messages: TeamMailboxMessage[]
  checked_at: string
  poll_after_ms?: number
}

interface APIEnvelope<T> {
  code?: number
  message?: string
  data?: T
}

type LinkState = 'ready' | 'error' | 'invalid'

const shareToken = ref(readShareToken())
const email = ref('')
const messages = ref<TeamMailboxMessage[]>([])
const selectedMessageID = ref('')
const selectedMessage = ref<TeamMailboxMessageDetail | null>(null)
const checking = ref(false)
const messageLoading = ref(false)
const copied = ref(false)
const checkedAt = ref('')
const errorMessage = ref('')
const linkState = ref<LinkState>('ready')
let pollTimer: ReturnType<typeof setTimeout> | null = null
let disposed = false
let inboxRequestVersion = 0
let messageRequestVersion = 0

const latestCode = computed(() => messages.value.find((message) => String(message.code || '').trim())?.code?.trim() || '')
const checkedAtLabel = computed(() => {
  if (!checkedAt.value) return '正在连接邮箱'
  const parsed = new Date(checkedAt.value)
  if (Number.isNaN(parsed.getTime())) return '已检查邮箱'
  return `最近检查 ${parsed.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}`
})

onMounted(() => {
  if (!shareToken.value) return
  void fetchInbox()
})

onBeforeUnmount(() => {
  disposed = true
  if (pollTimer) clearTimeout(pollTimer)
})

function readShareToken(): string {
  const params = new URLSearchParams(window.location.hash.replace(/^#/, ''))
  return String(params.get('t') || '').trim()
}

async function fetchInbox(): Promise<void> {
  if (!shareToken.value || checking.value || disposed || linkState.value === 'invalid') return
  if (pollTimer) clearTimeout(pollTimer)
  const requestVersion = ++inboxRequestVersion
  checking.value = true
  errorMessage.value = ''
  try {
    const response = await fetch(buildApiUrl('/public/team-mailbox/messages'), requestOptions())
    const payload = await response.json().catch(() => ({})) as APIEnvelope<TeamMailboxMessagesResponse>
    if (response.status === 404 || response.status === 401) {
      linkState.value = 'invalid'
      return
    }
    if (!response.ok || payload.code !== 0 || !payload.data) {
      throw new Error(payload.message || '邮箱暂时无法读取，请稍后重试')
    }
    if (disposed || requestVersion !== inboxRequestVersion) return
    const inbox = payload.data
    email.value = inbox.email || ''
    messages.value = Array.isArray(inbox.messages) ? inbox.messages.filter((message) => Boolean(message?.id)) : []
    checkedAt.value = inbox.checked_at || ''
    copied.value = false
    linkState.value = 'ready'

    const selectedStillExists = messages.value.some((message) => message.id === selectedMessageID.value)
    if (!selectedStillExists) {
      selectedMessageID.value = ''
      selectedMessage.value = null
    }
    if (!selectedMessageID.value && messages.value[0]?.id) {
      void selectMessage(messages.value[0].id)
    }
    scheduleNextPoll(inbox.poll_after_ms)
  } catch (error) {
    if (disposed || requestVersion !== inboxRequestVersion) return
    linkState.value = 'error'
    errorMessage.value = error instanceof Error && error.message ? error.message : '邮箱暂时无法读取，请稍后重试'
    scheduleNextPoll()
  } finally {
    if (requestVersion === inboxRequestVersion) checking.value = false
  }
}

async function selectMessage(messageID: string): Promise<void> {
  const selected = messages.value.find((message) => message.id === messageID)
  if (!selected || messageLoading.value || disposed) return
  selectedMessageID.value = messageID
  selectedMessage.value = null
  const requestVersion = ++messageRequestVersion
  messageLoading.value = true
  try {
    const response = await fetch(buildApiUrl(`/public/team-mailbox/messages/${encodeURIComponent(messageID)}`), requestOptions())
    const payload = await response.json().catch(() => ({})) as APIEnvelope<TeamMailboxMessageDetail>
    if (response.status === 404 || response.status === 401) {
      linkState.value = 'invalid'
      return
    }
    if (!response.ok || payload.code !== 0 || !payload.data) {
      throw new Error(payload.message || '邮件暂时无法读取，请稍后重试')
    }
    if (disposed || requestVersion !== messageRequestVersion || selectedMessageID.value !== messageID) return
    selectedMessage.value = payload.data
  } catch (error) {
    if (disposed || requestVersion !== messageRequestVersion) return
    errorMessage.value = error instanceof Error && error.message ? error.message : '邮件暂时无法读取，请稍后重试'
    linkState.value = 'error'
  } finally {
    if (requestVersion === messageRequestVersion) messageLoading.value = false
  }
}

function requestOptions(): NonNullable<Parameters<typeof window.fetch>[1]> {
  return {
    method: 'GET',
    credentials: 'omit',
    cache: 'no-store',
    referrerPolicy: 'no-referrer',
    headers: {
      Authorization: `Bearer ${shareToken.value}`,
      Accept: 'application/json',
    },
  }
}

function scheduleNextPoll(requestedDelay?: number): void {
  if (disposed || !shareToken.value || linkState.value === 'invalid') return
  const delay = Math.max(5000, Number(requestedDelay) || 5000)
  pollTimer = setTimeout(() => void fetchInbox(), delay)
}

async function copyCode(value: string): Promise<void> {
  const code = String(value || '').trim()
  if (!code) return
  try {
    await copyText(code)
    copied.value = true
  } catch {
    errorMessage.value = '复制失败，请手动选择验证码'
    linkState.value = 'error'
  }
}

async function copyText(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = value
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  const copied = document.execCommand('copy')
  textarea.remove()
  if (!copied) throw new Error('clipboard unavailable')
}

function formatReceivedAt(value: string, includeDate = false): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  if (includeDate) {
    return date.toLocaleString([], { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  }
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
</script>

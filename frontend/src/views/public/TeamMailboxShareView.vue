<template>
  <main class="min-h-[100dvh] bg-[#eef1f5] text-gray-900 dark:bg-[#101214] dark:text-gray-100 sm:px-6 sm:py-8">
    <section class="mx-auto flex min-h-[100dvh] w-full max-w-[86rem] sm:min-h-[calc(100dvh-4rem)]">
      <div class="w-full overflow-hidden bg-white shadow-xl shadow-slate-950/5 dark:bg-[#1f2227] dark:shadow-black/20 sm:rounded-[28px] sm:border sm:border-gray-200 sm:dark:border-[#33373e]">
        <header class="flex min-w-0 items-center justify-between gap-3 border-b border-gray-200 bg-white px-4 py-3.5 dark:border-[#33373e] dark:bg-[#1f2227] sm:px-6 sm:py-4">
          <div class="flex min-w-0 items-center gap-3">
            <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl bg-primary-50 text-primary-700 dark:bg-primary-500/15 dark:text-primary-200">
              <Icon name="mail" size="md" :stroke-width="2" />
            </span>
            <div class="min-w-0">
              <h1 class="truncate text-base font-semibold text-gray-950 dark:text-gray-50">邮箱收件箱</h1>
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

          <div class="grid min-h-[calc(100dvh-4.5rem)] min-w-0 grid-cols-1 md:min-h-[40rem] md:grid-cols-[minmax(18rem,23rem)_minmax(0,1fr)]">
            <aside class="min-w-0 border-b border-gray-200 bg-[#f7f8fa] dark:border-[#33373e] dark:bg-[#191c20] md:border-b-0 md:border-r">
              <div class="flex items-center justify-between gap-3 border-b border-gray-200 px-4 py-3.5 dark:border-[#33373e]">
                <p class="text-sm font-semibold text-gray-800 dark:text-gray-100">收件箱</p>
                <span class="rounded-full bg-gray-200 px-2 py-0.5 text-xs font-medium tabular-nums text-gray-600 dark:bg-[#2b3037] dark:text-gray-300">{{ messages.length }}</span>
              </div>

              <div class="max-h-[19rem] overflow-y-auto overscroll-contain md:max-h-[calc(100dvh-12.5rem)] md:min-h-[34rem]">
                <button
                  v-for="message in messages"
                  :key="message.id"
                  type="button"
                  class="w-full min-w-0 border-b border-gray-200 px-4 py-3.5 text-left transition-colors last:border-b-0 hover:bg-white focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500 dark:border-[#33373e] dark:hover:bg-[#22262c]"
                  :class="selectedMessageID === message.id ? 'bg-white shadow-[inset_3px_0_0] shadow-primary-500 dark:bg-[#22262c]' : ''"
                  :data-testid="`team-mailbox-message-${message.id}`"
                  @click="void selectMessage(message.id)"
                >
                  <div class="flex min-w-0 items-start gap-2.5">
                    <span class="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary-100 text-[11px] font-bold text-primary-700 dark:bg-primary-500/20 dark:text-primary-200">{{ senderInitials(message.from) }}</span>
                    <span class="min-w-0 flex-1">
                      <span class="flex min-w-0 items-center justify-between gap-2">
                        <span class="truncate text-xs font-semibold text-gray-700 dark:text-gray-200">{{ senderName(message.from) }}</span>
                        <time v-if="message.received_at" class="shrink-0 text-[11px] text-gray-400 dark:text-gray-500">{{ formatReceivedAt(message.received_at) }}</time>
                      </span>
                      <span class="mt-1 block truncate text-sm font-semibold text-gray-950 dark:text-gray-50">{{ message.subject || '无主题邮件' }}</span>
                      <span v-if="message.preview" class="mt-1 block line-clamp-2 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ message.preview }}</span>
                    </span>
                  </div>
                </button>

                <div v-if="!messages.length && !checking" class="flex min-h-52 flex-col items-center justify-center px-5 py-8 text-center">
                  <span class="flex h-10 w-10 items-center justify-center rounded-2xl bg-white text-gray-400 shadow-sm dark:bg-dark-800 dark:text-gray-500">
                    <Icon name="mail" size="md" :stroke-width="2" />
                  </span>
                  <p class="mt-3 text-sm font-medium text-gray-600 dark:text-gray-300">暂无邮件</p>
                </div>
              </div>
            </aside>

            <section class="flex min-w-0 flex-col bg-white dark:bg-[#1f2227]">
              <div v-if="messageLoading" class="flex min-h-64 flex-1 items-center justify-center px-5 py-10 text-center text-sm text-gray-500 dark:text-gray-400">
                正在打开邮件
              </div>

              <article v-else-if="selectedMessage" class="flex min-h-0 flex-1 flex-col">
                <header class="min-w-0 border-b border-gray-200 px-5 py-5 dark:border-[#33373e] sm:px-8 sm:py-6">
                  <h2 class="break-words text-xl font-semibold leading-8 text-gray-950 dark:text-gray-50">{{ selectedMessage.subject || '无主题邮件' }}</h2>
                  <div class="mt-5 flex min-w-0 items-start gap-3">
                    <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-primary-100 text-sm font-bold text-primary-700 dark:bg-primary-500/20 dark:text-primary-200">{{ senderInitials(selectedMessage.from) }}</span>
                    <div class="min-w-0 flex-1">
                      <p class="truncate text-sm font-semibold text-gray-800 dark:text-gray-100">{{ senderName(selectedMessage.from) }}</p>
                      <p class="mt-0.5 break-all text-xs leading-5 text-gray-500 dark:text-gray-400">{{ senderAddress(selectedMessage.from) || '未知发件人' }}</p>
                      <p v-if="email" class="mt-0.5 truncate text-xs leading-5 text-gray-400 dark:text-gray-500">发送至 {{ email }}</p>
                    </div>
                    <time v-if="selectedMessage.received_at" class="shrink-0 pt-0.5 text-right text-xs leading-5 text-gray-500 dark:text-gray-400">{{ formatReceivedAt(selectedMessage.received_at, true) }}</time>
                  </div>
                </header>
                <div class="min-h-0 flex-1 overflow-y-auto bg-[#fbfcfd] px-4 py-6 dark:bg-[#181a1e] sm:px-8 sm:py-8">
                  <div class="mx-auto w-full max-w-3xl rounded-2xl border border-gray-200 bg-white px-5 py-6 shadow-sm dark:border-[#33373e] dark:bg-[#20242a] sm:px-8 sm:py-8">
                    <div v-if="selectedMessageHtml" class="team-mailbox-email-html" v-html="selectedMessageHtml"></div>
                    <pre v-else class="m-0 whitespace-pre-wrap break-words font-sans text-[15px] leading-7 text-gray-700 dark:text-gray-200">{{ selectedMessage.body || selectedMessage.preview || '邮件内容为空' }}</pre>
                  </div>
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
import DOMPurify from 'dompurify'
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
  html?: string
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
const checkedAt = ref('')
const errorMessage = ref('')
const linkState = ref<LinkState>('ready')
let pollTimer: ReturnType<typeof setTimeout> | null = null
let disposed = false
let inboxRequestVersion = 0
let messageRequestVersion = 0

const selectedMessageHtml = computed(() => sanitizeEmailHTML(selectedMessage.value?.html || ''))
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

const mailboxHTMLTags = [
  'a', 'b', 'blockquote', 'br', 'code', 'div', 'em',
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'i',
  'li', 'ol', 'p', 'pre', 's', 'span', 'strong', 'table',
  'tbody', 'td', 'tfoot', 'th', 'thead', 'tr', 'u', 'ul',
]

const mailboxHTMLAttributes = [
  'align', 'bgcolor', 'border', 'cellpadding', 'cellspacing', 'colspan',
  'dir', 'height', 'href', 'rel', 'rowspan', 'style', 'target', 'title',
  'valign', 'width',
]

function sanitizeEmailHTML(value: string): string {
  const raw = String(value || '').trim()
  if (!raw) return ''
  return DOMPurify.sanitize(raw, {
    ALLOWED_TAGS: mailboxHTMLTags,
    ALLOWED_ATTR: mailboxHTMLAttributes,
    ALLOW_DATA_ATTR: false,
    FORBID_TAGS: ['audio', 'base', 'button', 'canvas', 'embed', 'form', 'iframe', 'img', 'input', 'link', 'math', 'object', 'picture', 'script', 'select', 'source', 'style', 'svg', 'template', 'textarea', 'track', 'video'],
    FORBID_ATTR: ['action', 'formaction', 'src', 'srcset'],
  })
}

function senderName(value?: string): string {
  const sender = String(value || '').trim()
  const match = sender.match(/^\s*([^<]+?)\s*<[^>]+>\s*$/)
  if (match?.[1]) return match[1].trim()
  if (sender.includes('@')) return sender.split('@')[0] || sender
  return sender || '未知发件人'
}

function senderAddress(value?: string): string {
  const sender = String(value || '').trim()
  const match = sender.match(/<\s*([^<>\s]+@[^<>\s]+)\s*>/)
  if (match?.[1]) return match[1]
  return sender.includes('@') ? sender : ''
}

function senderInitials(value?: string): string {
  const name = senderName(value).replace(/[^\p{L}\p{N}]/gu, '').trim()
  if (!name) return 'M'
  return Array.from(name).slice(0, 2).join('').toLocaleUpperCase()
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

<style scoped>
.team-mailbox-email-html {
  color: inherit;
  font-size: 15px;
  line-height: 1.75;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.team-mailbox-email-html :deep(*) {
  box-sizing: border-box;
  max-width: 100% !important;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.team-mailbox-email-html :deep(p) {
  margin: 0 0 1.25rem;
}

.team-mailbox-email-html :deep(p:last-child) {
  margin-bottom: 0;
}

.team-mailbox-email-html :deep(h1),
.team-mailbox-email-html :deep(h2),
.team-mailbox-email-html :deep(h3),
.team-mailbox-email-html :deep(h4),
.team-mailbox-email-html :deep(h5),
.team-mailbox-email-html :deep(h6) {
  margin: 0 0 1rem;
  color: inherit;
  font-weight: 700;
  line-height: 1.35;
}

.team-mailbox-email-html :deep(h1) {
  font-size: 1.625rem;
}

.team-mailbox-email-html :deep(h2) {
  font-size: 1.375rem;
}

.team-mailbox-email-html :deep(h3) {
  font-size: 1.125rem;
}

.team-mailbox-email-html :deep(a) {
  color: rgb(37 99 235);
  text-decoration: underline;
  text-underline-offset: 3px;
}

.team-mailbox-email-html :deep(ul),
.team-mailbox-email-html :deep(ol) {
  margin: 0 0 1.25rem;
  padding-left: 1.5rem;
}

.team-mailbox-email-html :deep(li + li) {
  margin-top: 0.4rem;
}

.team-mailbox-email-html :deep(blockquote) {
  margin: 1.25rem 0;
  border-left: 3px solid rgb(148 163 184);
  padding-left: 1rem;
  color: rgb(100 116 139);
}

.team-mailbox-email-html :deep(table) {
  width: 100% !important;
  table-layout: fixed;
  border-collapse: collapse;
}

.team-mailbox-email-html :deep(td),
.team-mailbox-email-html :deep(th) {
  min-width: 0;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.team-mailbox-email-html :deep(pre) {
  margin: 1.25rem 0;
  overflow: auto;
  white-space: pre-wrap;
  border-radius: 0.75rem;
  background: rgb(241 245 249);
  padding: 1rem;
  color: rgb(30 41 59);
}

:global(.dark) .team-mailbox-email-html :deep(a) {
  color: rgb(147 197 253);
}

:global(.dark) .team-mailbox-email-html :deep(blockquote) {
  border-color: rgb(100 116 139);
  color: rgb(203 213 225);
}

:global(.dark) .team-mailbox-email-html :deep(pre) {
  background: rgb(15 23 42);
  color: rgb(226 232 240);
}
</style>

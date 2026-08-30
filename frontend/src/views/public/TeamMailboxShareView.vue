<template>
  <main class="min-h-[100dvh] bg-[#08090b] text-gray-100 sm:px-6 sm:py-8">
    <section class="mx-auto flex min-h-[100dvh] w-full max-w-[86rem] sm:min-h-[calc(100dvh-4rem)]">
      <div class="w-full overflow-hidden bg-[#15171b] shadow-xl shadow-black/40 sm:rounded-[28px] sm:border sm:border-[#31353c]">
        <header class="flex min-w-0 items-center justify-between gap-3 border-b border-[#31353c] bg-[#15171b] px-4 py-3.5 sm:px-6 sm:py-4">
          <div class="flex min-w-0 items-center gap-3">
            <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl bg-primary-50 text-primary-700 dark:bg-primary-500/15 dark:text-primary-200">
              <Icon name="mail" size="md" :stroke-width="2" />
            </span>
            <div class="min-w-0">
              <h1 class="truncate text-base font-semibold text-gray-50">邮箱收件箱</h1>
              <code v-if="email" class="mt-0.5 block truncate font-mono text-xs text-gray-400" :title="email">{{ email }}</code>
            </div>
          </div>
          <button
            type="button"
            class="btn flex h-9 w-9 shrink-0 items-center justify-center border border-[#424850] bg-[#24282e] p-0 text-gray-200 hover:bg-[#30353c]"
            title="刷新收件箱"
            aria-label="刷新收件箱"
            :disabled="!shareToken || checking || linkState === 'invalid'"
            @click="void fetchInbox()"
          >
            <Icon name="refresh" size="sm" :class="checking ? 'animate-spin' : ''" :stroke-width="2" />
          </button>
        </header>

        <div v-if="linkState === 'invalid'" class="flex min-h-64 flex-col items-center justify-center px-5 py-10 text-center sm:min-h-80">
          <span class="flex h-12 w-12 items-center justify-center rounded-2xl bg-red-950/35 text-red-300">
            <Icon name="xCircle" size="lg" :stroke-width="2" />
          </span>
          <p class="mt-4 text-sm font-semibold text-gray-100">接码链接不可用</p>
        </div>

        <div v-else-if="!shareToken" class="flex min-h-64 flex-col items-center justify-center px-5 py-10 text-center sm:min-h-80">
          <span class="flex h-12 w-12 items-center justify-center rounded-2xl bg-amber-950/35 text-amber-300">
            <Icon name="link" size="lg" :stroke-width="2" />
          </span>
          <p class="mt-4 text-sm font-semibold text-gray-100">缺少接码链接</p>
        </div>

        <template v-else>
          <p v-if="linkState === 'error'" class="border-b border-red-950/60 bg-red-950/20 px-4 py-2.5 text-center text-sm leading-5 text-red-300" role="alert">
            {{ errorMessage }}
          </p>

          <div class="grid min-h-[calc(100dvh-4.5rem)] min-w-0 grid-cols-1 md:min-h-[40rem] md:grid-cols-[minmax(18rem,23rem)_minmax(0,1fr)]">
            <aside class="min-w-0 border-b border-[#31353c] bg-[#101215] md:border-b-0 md:border-r">
              <div class="flex items-center justify-between gap-3 border-b border-[#31353c] px-4 py-3.5">
                <p class="text-sm font-semibold text-gray-100">收件箱</p>
                <span class="rounded-full bg-[#2b3037] px-2 py-0.5 text-xs font-medium tabular-nums text-gray-300">{{ messages.length }}</span>
              </div>

              <div class="max-h-[19rem] overflow-y-auto overscroll-contain md:max-h-[calc(100dvh-12.5rem)] md:min-h-[34rem]">
                <button
                  v-for="message in messages"
                  :key="message.id"
                  type="button"
                  class="w-full min-w-0 border-b border-[#31353c] px-4 py-3.5 text-left transition-colors last:border-b-0 hover:bg-[#1b1f24] focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500"
                  :class="selectedMessageID === message.id ? 'bg-[#1b1f24] shadow-[inset_3px_0_0] shadow-primary-500' : ''"
                  :data-testid="`team-mailbox-message-${message.id}`"
                  @click="void selectMessage(message.id)"
                >
                  <div class="flex min-w-0 items-start gap-2.5">
                    <span class="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary-100 text-[11px] font-bold text-primary-700 dark:bg-primary-500/20 dark:text-primary-200">{{ senderInitials(message.from) }}</span>
                    <span class="min-w-0 flex-1">
                      <span class="flex min-w-0 items-center justify-between gap-2">
                        <span class="truncate text-xs font-semibold text-gray-200">{{ senderName(message.from) }}</span>
                        <time v-if="message.received_at" class="shrink-0 text-[11px] text-gray-500">{{ formatReceivedAt(message.received_at) }}</time>
                      </span>
                      <span class="mt-1 block truncate text-sm font-semibold text-gray-50">{{ message.subject || '无主题邮件' }}</span>
                      <span v-if="message.preview" class="mt-1 block line-clamp-2 text-xs leading-5 text-gray-400">{{ message.preview }}</span>
                    </span>
                  </div>
                </button>

                <div v-if="!messages.length && !checking" class="flex min-h-52 flex-col items-center justify-center px-5 py-8 text-center">
                  <span class="flex h-10 w-10 items-center justify-center rounded-2xl bg-[#20242a] text-gray-500 shadow-sm">
                    <Icon name="mail" size="md" :stroke-width="2" />
                  </span>
                  <p class="mt-3 text-sm font-medium text-gray-300">暂无邮件</p>
                </div>
              </div>
            </aside>

            <section class="flex min-w-0 flex-col bg-[#15171b]">
              <div v-if="messageLoading" class="flex min-h-64 flex-1 items-center justify-center px-5 py-10 text-center text-sm text-gray-400">
                正在打开邮件
              </div>

              <article v-else-if="selectedMessage" class="flex min-h-0 flex-1 flex-col">
                <header class="min-w-0 border-b border-[#31353c] px-5 py-5 sm:px-8 sm:py-6">
                  <h2 class="break-words text-xl font-semibold leading-8 text-gray-50">{{ selectedMessage.subject || '无主题邮件' }}</h2>
                  <div class="mt-5 flex min-w-0 items-start gap-3">
                    <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-primary-100 text-sm font-bold text-primary-700 dark:bg-primary-500/20 dark:text-primary-200">{{ senderInitials(selectedMessage.from) }}</span>
                    <div class="min-w-0 flex-1">
                      <p class="truncate text-sm font-semibold text-gray-100">{{ senderName(selectedMessage.from) }}</p>
                      <p class="mt-0.5 break-all text-xs leading-5 text-gray-400">{{ senderAddress(selectedMessage.from) || '未知发件人' }}</p>
                      <p v-if="email" class="mt-0.5 truncate text-xs leading-5 text-gray-500">发送至 {{ email }}</p>
                    </div>
                    <time v-if="selectedMessage.received_at" class="shrink-0 pt-0.5 text-right text-xs leading-5 text-gray-400">{{ formatReceivedAt(selectedMessage.received_at, true) }}</time>
                  </div>
                </header>
                <div class="min-h-0 flex-1 overflow-y-auto bg-[#0d0f11] px-4 py-6 sm:px-8 sm:py-8">
                  <div class="mx-auto w-full max-w-3xl overflow-hidden rounded-xl border border-[#333840] bg-[#101214] shadow-sm">
                    <iframe
                      v-if="selectedMessageDocument"
                      ref="emailFrame"
                      class="team-mailbox-email-frame"
                      :srcdoc="selectedMessageDocument"
                      sandbox="allow-same-origin allow-popups"
                      referrerpolicy="no-referrer"
                      title="邮件正文"
                      @load="handleEmailFrameLoad"
                    ></iframe>
                    <pre v-else class="m-0 whitespace-pre-wrap break-words px-5 py-6 font-sans text-[15px] leading-7 text-gray-200 sm:px-8 sm:py-8">{{ selectedMessage.body || selectedMessage.preview || '邮件内容为空' }}</pre>
                  </div>
                </div>
              </article>

              <div v-else class="flex min-h-64 flex-1 flex-col items-center justify-center px-5 py-10 text-center">
                <span class="flex h-12 w-12 items-center justify-center rounded-2xl bg-[#20242a] text-gray-500">
                  <Icon name="mail" size="lg" :stroke-width="2" />
                </span>
                <p class="mt-4 text-sm font-medium text-gray-300">选择一封邮件查看内容</p>
              </div>
            </section>
          </div>

          <p class="border-t border-[#31353c] px-4 py-2.5 text-center text-xs text-gray-400">{{ checkedAtLabel }}</p>
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
const emailFrame = ref<HTMLIFrameElement | null>(null)
const checking = ref(false)
const messageLoading = ref(false)
const checkedAt = ref('')
const errorMessage = ref('')
const linkState = ref<LinkState>('ready')
let pollTimer: ReturnType<typeof setTimeout> | null = null
let disposed = false
let inboxRequestVersion = 0
let messageRequestVersion = 0
let removeEmailFrameListeners = () => {}

const selectedMessageHTML = computed(() => sanitizeEmailHTML(selectedMessage.value?.html || ''))
const selectedMessageDocument = computed(() => buildMailboxEmailDocument(selectedMessageHTML.value))
const checkedAtLabel = computed(() => {
  if (!checkedAt.value) return '正在连接邮箱'
  const parsed = new Date(checkedAt.value)
  if (Number.isNaN(parsed.getTime())) return '已检查邮箱'
  return `最近检查 ${parsed.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}`
})

onMounted(() => {
  window.addEventListener('hashchange', handleShareTokenChange)
  if (!shareToken.value) return
  void fetchInbox()
})

onBeforeUnmount(() => {
  disposed = true
  if (pollTimer) clearTimeout(pollTimer)
  window.removeEventListener('hashchange', handleShareTokenChange)
  clearEmailFrameListeners()
})

function readShareToken(): string {
  const params = new URLSearchParams(window.location.hash.replace(/^#/, ''))
  return String(params.get('t') || '').trim()
}

function handleShareTokenChange(): void {
  const nextToken = readShareToken()
  if (nextToken === shareToken.value) return
  resetMailboxForTokenChange()
  shareToken.value = nextToken
  if (nextToken) void fetchInbox()
}

function resetMailboxForTokenChange(): void {
  inboxRequestVersion += 1
  messageRequestVersion += 1
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = null
  clearEmailFrameListeners()
  email.value = ''
  messages.value = []
  selectedMessageID.value = ''
  selectedMessage.value = null
  checking.value = false
  messageLoading.value = false
  checkedAt.value = ''
  errorMessage.value = ''
  linkState.value = 'ready'
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
      clearEmailFrameListeners()
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
  clearEmailFrameListeners()
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

function clearEmailFrameListeners(): void {
  removeEmailFrameListeners()
  removeEmailFrameListeners = () => {}
}

function handleEmailFrameLoad(): void {
  clearEmailFrameListeners()
  const frame = emailFrame.value
  const document = frame?.contentDocument
  if (!frame || !document?.body || !document.documentElement) return

  const resize = () => {
    const height = Math.max(
      320,
      document.body.scrollHeight,
      document.body.offsetHeight,
      document.documentElement.scrollHeight,
      document.documentElement.offsetHeight,
    )
    frame.style.height = `${Math.ceil(height)}px`
  }
  const images = Array.from(document.images)
  for (const image of images) {
    image.addEventListener('load', resize)
    image.addEventListener('error', resize)
  }
  removeEmailFrameListeners = () => {
    for (const image of images) {
      image.removeEventListener('load', resize)
      image.removeEventListener('error', resize)
    }
  }
  resize()
  if (typeof window.requestAnimationFrame === 'function') {
    window.requestAnimationFrame(resize)
  }
}

const mailboxHTMLTags = [
  'a', 'b', 'blockquote', 'br', 'center', 'code', 'div', 'em', 'font',
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'i',
  'img', 'li', 'ol', 'p', 'pre', 's', 'small', 'span', 'strong',
  'sub', 'sup', 'table', 'tbody', 'td', 'tfoot', 'th', 'thead', 'tr', 'u', 'ul',
]

const mailboxHTMLAttributes = [
  'align', 'bgcolor', 'border', 'cellpadding', 'cellspacing', 'colspan',
  'class', 'dir', 'height', 'href', 'id', 'rel', 'role', 'rowspan', 'src',
  'style', 'target', 'title', 'alt', 'valign', 'width',
]

function sanitizeEmailHTML(value: string): string {
  const raw = String(value || '').trim()
  if (!raw) return ''
  const sourceTemplate = document.createElement('template')
  sourceTemplate.innerHTML = raw
  const retainedStyles = Array.from(sourceTemplate.content.querySelectorAll('style'))
    .map((style) => style.textContent || '')
    .filter((style) => style.trim() !== '' && !hasUnsafeMailboxStyle(style))
  const sanitized = DOMPurify.sanitize(raw, {
    ALLOWED_TAGS: mailboxHTMLTags,
    ALLOWED_ATTR: mailboxHTMLAttributes,
    ALLOW_DATA_ATTR: false,
    ADD_DATA_URI_TAGS: ['img'],
    ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|data:image\/(?:png|jpeg|gif|webp);base64,[a-z0-9+/=\s]+$)/i,
    FORBID_TAGS: ['audio', 'base', 'button', 'canvas', 'embed', 'form', 'iframe', 'input', 'link', 'math', 'object', 'picture', 'script', 'select', 'source', 'style', 'svg', 'template', 'textarea', 'track', 'video'],
    FORBID_ATTR: ['action', 'formaction', 'srcset'],
  })
  const template = document.createElement('template')
  template.innerHTML = sanitized
  for (const element of template.content.querySelectorAll<HTMLElement>('[style]')) {
    if (hasUnsafeMailboxStyle(element.getAttribute('style') || '')) {
      element.removeAttribute('style')
    }
  }
  for (const style of template.content.querySelectorAll('style')) {
    if (hasUnsafeMailboxStyle(style.textContent || '')) style.remove()
  }
  for (const image of template.content.querySelectorAll('img')) {
    if (!isSafeMailboxImageSource(image.getAttribute('src') || '')) image.remove()
  }
  for (const link of template.content.querySelectorAll('a')) {
    const href = link.getAttribute('href') || ''
    if (!isSafeMailboxLink(href)) {
      link.removeAttribute('href')
      link.removeAttribute('target')
      link.removeAttribute('rel')
      continue
    }
    link.setAttribute('target', '_blank')
    link.setAttribute('rel', 'noopener noreferrer nofollow')
  }
  for (const content of retainedStyles.reverse()) {
    const style = document.createElement('style')
    style.textContent = content
    template.content.prepend(style)
  }
  return template.innerHTML
}

function hasUnsafeMailboxStyle(value: string): boolean {
  return /(?:url\s*\(|expression\s*\(|@import|behavior\s*:|-moz-binding|javascript\s*:|vbscript\s*:|<|>|\\)/i.test(value)
}

function isSafeMailboxImageSource(value: string): boolean {
  const source = value.trim()
  return /^(?:https?:\/\/|data:image\/(?:png|jpeg|gif|webp);base64,[a-z0-9+/=\s]+$)/i.test(source)
}

function isSafeMailboxLink(value: string): boolean {
  return /^(?:https?:\/\/|mailto:)/i.test(value.trim())
}

function buildMailboxEmailDocument(content: string): string {
  if (!content) return ''
  return `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="referrer" content="no-referrer">
    <meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src https: http: data:; style-src 'unsafe-inline'; font-src 'none'; connect-src 'none'; media-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'">
    <style>
      :root { color-scheme: dark; background: #101214; }
      html, body { margin: 0; min-width: 0; background: #101214; color: #e5e7eb; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
      .mailbox-email-content { box-sizing: border-box; min-width: 0; overflow-x: hidden; padding: 24px; font-size: 15px; line-height: 1.7; overflow-wrap: anywhere; word-break: break-word; }
      .mailbox-email-content *, .mailbox-email-content *::before, .mailbox-email-content *::after { box-sizing: border-box; max-width: 100%; overflow-wrap: anywhere; word-break: break-word; }
      .mailbox-email-content table { max-width: 100% !important; }
      .mailbox-email-content img { max-width: 100% !important; height: auto !important; }
      .mailbox-email-content a { color: inherit; }
      @media (max-width: 640px) { .mailbox-email-content { padding: 16px; } }
    </style>
  </head>
  <body><main class="mailbox-email-content">${content}</main></body>
</html>`
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
.team-mailbox-email-frame {
  display: block;
  width: 100%;
  min-height: 20rem;
  border: 0;
  background: #101214;
}
</style>

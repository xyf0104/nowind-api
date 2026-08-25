<template>
  <section data-testid="team-child-history-panel" class="overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
    <header class="flex min-w-0 items-start justify-between gap-3 border-b border-gray-200 px-4 py-3.5 dark:border-dark-700">
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-primary-50 text-primary-700 dark:bg-primary-950/50 dark:text-primary-300">
            <Icon name="inbox" size="sm" :stroke-width="2" />
          </span>
          <div class="min-w-0">
            <h2 class="whitespace-nowrap text-base font-semibold text-gray-900 dark:text-gray-100">历史 Team 账号</h2>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">邮箱、登录密码、账号状态与最新额度</p>
          </div>
        </div>
      </div>
      <button type="button" class="btn btn-secondary flex h-8 w-8 shrink-0 items-center justify-center p-0" title="刷新历史账号" aria-label="刷新历史账号" :disabled="loading" @click="emit('refresh')">
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" :stroke-width="2" />
      </button>
    </header>

    <div v-if="loading && entries.length === 0" class="flex items-center gap-2 px-4 py-5 text-sm text-gray-500 dark:text-gray-400">
      <Icon name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
      <span>正在读取历史邮箱与账号状态</span>
    </div>
    <div v-else-if="entries.length === 0" class="px-4 py-5 text-sm text-gray-500 dark:text-gray-400">
      暂无历史 Team 邮箱。创建或导入成功后会显示在这里。
    </div>
    <div v-else class="max-h-[34rem] divide-y divide-gray-200 overflow-y-auto dark:divide-dark-700">
      <article v-for="entry in entries" :key="entry.email" class="px-4 py-3.5" :data-testid="`team-history-${entry.email}`">
        <div class="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div class="min-w-0">
            <div class="flex min-w-0 flex-wrap items-center gap-2">
              <code class="min-w-0 truncate font-mono text-sm font-semibold text-gray-900 dark:text-gray-100" :title="entry.email">{{ entry.email }}</code>
              <span v-if="normalizedActiveEmail === normalizeEmail(entry.email)" class="shrink-0 rounded-md bg-primary-100 px-1.5 py-0.5 text-[11px] font-medium text-primary-700 dark:bg-primary-950/50 dark:text-primary-300">当前邮箱</span>
              <span class="shrink-0 rounded-md px-1.5 py-0.5 text-[11px] font-medium" :class="accountStatusClass(entry)">{{ accountStatusLabel(entry) }}</span>
            </div>
            <p v-if="entry.account" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              账号 #{{ entry.account.id }}<span v-if="entry.account.last_used_at"> · 最近调用 {{ formatDateTime(entry.account.last_used_at) }}</span>
            </p>
            <p v-else class="mt-1 text-xs text-gray-500 dark:text-gray-400">邮箱已保留，尚未导入 XIASS 账号管理</p>
            <p
              v-if="accountIssue(entry)?.description"
              class="mt-1.5 max-w-2xl text-xs leading-5"
              :class="accountIssue(entry)?.tone === 'error' ? 'text-red-600 dark:text-red-300' : 'text-amber-600 dark:text-amber-300'"
              :title="accountIssue(entry)?.rawReason"
              :data-testid="`team-history-issue-${entry.account?.id || entry.email}`"
            >
              {{ accountIssue(entry)?.description }}
            </p>
          </div>
          <div class="flex shrink-0 flex-wrap items-center gap-2">
            <button
              v-if="entry.account && accountIssue(entry)?.needsReauth"
              type="button"
              class="btn flex items-center gap-1.5 whitespace-nowrap border-red-300 bg-red-50 !px-2.5 !py-1.5 text-xs font-semibold text-red-700 hover:bg-red-100 dark:border-red-800 dark:bg-red-950/35 dark:text-red-300 dark:hover:bg-red-950/55"
              :disabled="reauthorizingAccountID === entry.account.id || !entry.passwordAvailable"
              :title="entry.passwordAvailable ? '复用历史邮箱和保存的密码，从 XIASS 官方 OAuth 登录节点重新授权' : '该账号没有可用于自动重新授权的保存密码'"
              :data-testid="`team-history-reauthorize-${entry.account.id}`"
              @click="emit('reauthorize', entry)"
            >
              <Icon name="key" size="xs" :class="reauthorizingAccountID === entry.account.id ? 'animate-pulse' : ''" :stroke-width="2.5" />
              <span>{{ reauthorizingAccountID === entry.account.id ? '正在重新授权' : '重新授权' }}</span>
            </button>
            <button type="button" class="btn btn-secondary flex items-center gap-1.5 whitespace-nowrap !px-2.5 !py-1.5 text-xs" :disabled="openingEmail === entry.email" @click="emit('open-mailbox', entry.email)">
              <Icon name="mail" size="xs" :class="openingEmail === entry.email ? 'animate-pulse' : ''" :stroke-width="2" />
              <span>{{ openingEmail === entry.email ? '打开中' : '接收验证码' }}</span>
            </button>
            <router-link v-if="entry.account" :to="{ name: 'AdminAccounts', query: { account_id: String(entry.account.id) } }" class="btn btn-secondary flex items-center gap-1.5 whitespace-nowrap !px-2.5 !py-1.5 text-xs" :data-testid="`team-history-manage-${entry.account.id}`">
              <Icon name="arrowRight" size="xs" :stroke-width="2" />
              <span>账号管理</span>
            </router-link>
            <button
              v-if="entry.account"
              type="button"
              class="btn btn-secondary flex items-center gap-1.5 whitespace-nowrap !px-2.5 !py-1.5 text-xs text-red-600 hover:text-red-700 dark:text-red-400"
              :disabled="deletingAccountID === entry.account.id"
              :data-testid="`team-history-delete-${entry.account.id}`"
              @click="deletingEntry = entry"
            >
              <Icon name="trash" size="xs" :class="deletingAccountID === entry.account.id ? 'animate-pulse' : ''" :stroke-width="2" />
              <span>{{ deletingAccountID === entry.account.id ? '删除中' : '删除账号' }}</span>
            </button>
          </div>
        </div>

        <div v-if="entry.account" class="mt-3 grid grid-cols-3 divide-x divide-gray-200 rounded-md bg-gray-50 px-2 py-2.5 dark:divide-dark-600 dark:bg-dark-900/50">
          <div class="min-w-0 px-2 text-center">
            <div class="text-[11px] text-gray-500 dark:text-gray-400">5h 已用</div>
            <div class="mt-0.5 truncate text-xs font-semibold tabular-nums text-gray-800 dark:text-gray-200">{{ formatUsage(entry.usage?.five_hour?.utilization) }}</div>
          </div>
          <div class="min-w-0 px-2 text-center">
            <div class="text-[11px] text-gray-500 dark:text-gray-400">7d 已用</div>
            <div class="mt-0.5 truncate text-xs font-semibold tabular-nums text-gray-800 dark:text-gray-200">{{ formatUsage(entry.usage?.seven_day?.utilization) }}</div>
          </div>
          <div class="min-w-0 px-2 text-center">
            <div class="text-[11px] text-gray-500 dark:text-gray-400">周额度</div>
            <div class="mt-0.5 truncate text-xs font-semibold tabular-nums text-gray-800 dark:text-gray-200">{{ formatWeeklyEstimate(entry.usage?.seven_day?.weekly_estimate_usd) }}</div>
          </div>
        </div>

        <div v-if="entry.passwordAvailable && entry.account" class="mt-3 flex min-w-0 items-center gap-2 rounded-md border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-600 dark:bg-dark-900/45">
          <span class="shrink-0 text-xs font-medium text-gray-600 dark:text-gray-300">登录密码</span>
          <code class="min-w-0 flex-1 truncate font-mono text-xs text-gray-900 dark:text-gray-100">{{ revealedPasswords[entry.account.id] || '•••••••••••••' }}</code>
          <button v-if="revealedPasswords[entry.account.id]" type="button" class="btn btn-secondary flex h-7 w-7 shrink-0 items-center justify-center p-0" title="复制登录密码" aria-label="复制登录密码" @click="emit('copy-password', revealedPasswords[entry.account.id])">
            <Icon name="copy" size="xs" :stroke-width="2" />
          </button>
          <button type="button" class="btn btn-secondary flex shrink-0 items-center gap-1.5 whitespace-nowrap !px-2 !py-1 text-[11px]" :disabled="passwordLoadingAccountID === entry.account.id" @click="emit('toggle-password', entry)">
            <Icon :name="passwordLoadingAccountID === entry.account.id ? 'refresh' : revealedPasswords[entry.account.id] ? 'eyeOff' : 'eye'" size="xs" :class="passwordLoadingAccountID === entry.account.id ? 'animate-spin' : ''" :stroke-width="2" />
            <span>{{ passwordLoadingAccountID === entry.account.id ? '验证中' : revealedPasswords[entry.account.id] ? '隐藏' : '查看' }}</span>
          </button>
        </div>
      </article>
    </div>
    <ConfirmDialog
      :show="Boolean(deletingEntry?.account)"
      title="删除 Team 账号"
      :message="`确定从 XIASS 账号管理中删除 ${deletingEntry?.email || '该 Team 账号'} 吗？历史邮箱仍会保留，可继续接收验证码。`"
      confirm-text="删除账号"
      cancel-text="取消"
      danger
      @confirm="confirmDelete"
      @cancel="deletingEntry = null"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { Icon } from '@/components/icons'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { Account, AccountUsageInfo } from '@/types'

interface TeamChildHistoryEntry {
  email: string
  account: Account | null
  usage: AccountUsageInfo | null
  passwordAvailable: boolean
}

const props = withDefaults(defineProps<{
  entries: TeamChildHistoryEntry[]
  activeMailboxEmail?: string
  openingEmail?: string
  loading?: boolean
  passwordLoadingAccountID?: number | null
  reauthorizingAccountID?: number | null
  deletingAccountID?: number | null
  revealedPasswords?: Record<number, string>
}>(), {
  activeMailboxEmail: '',
  openingEmail: '',
  loading: false,
  passwordLoadingAccountID: null,
  reauthorizingAccountID: null,
  deletingAccountID: null,
  revealedPasswords: () => ({})
})

const emit = defineEmits<{
  refresh: []
  'open-mailbox': [email: string]
  'toggle-password': [entry: TeamChildHistoryEntry]
  'copy-password': [password: string]
  reauthorize: [entry: TeamChildHistoryEntry]
  'delete-account': [entry: TeamChildHistoryEntry]
}>()

const normalizedActiveEmail = computed(() => normalizeEmail(props.activeMailboxEmail))
const deletingEntry = ref<TeamChildHistoryEntry | null>(null)

function confirmDelete() {
  if (!deletingEntry.value?.account) return
  emit('delete-account', deletingEntry.value)
  deletingEntry.value = null
}

function normalizeEmail(value: string): string {
  return String(value || '').trim().toLowerCase()
}

function accountStatusLabel(entry: TeamChildHistoryEntry): string {
  const issue = accountIssue(entry)
  if (issue) return issue.label
  if (!entry.account) return '仅邮箱'
  if (entry.account.status !== 'active') return '已停用'
  if (!entry.account.schedulable) return '暂停调度'
  return '正常'
}

function accountStatusClass(entry: TeamChildHistoryEntry): string {
  const issue = accountIssue(entry)
  if (issue?.tone === 'error') return 'bg-red-100 text-red-700 dark:bg-red-950/40 dark:text-red-300'
  if (issue) return 'bg-amber-100 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300'
  if (!entry.account) return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  if (entry.account.status !== 'active' || !entry.account.schedulable) return 'bg-amber-100 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300'
  return 'bg-green-100 text-green-700 dark:bg-green-950/35 dark:text-green-300'
}

interface TeamAccountIssue {
  label: string
  description: string
  rawReason: string
  tone: 'error' | 'warning'
  needsReauth: boolean
}

function accountIssue(entry: TeamChildHistoryEntry): TeamAccountIssue | null {
  const account = entry.account
  if (!account) return null
  const extra = account.extra as Record<string, unknown> | undefined
  const errorCode = String(entry.usage?.error_code || extra?.error_code || '').trim().toLowerCase()
  const rawReason = [
    entry.usage?.error,
    account.error_message,
    account.temp_unschedulable_reason,
    typeof extra?.error === 'string' ? extra.error : ''
  ].filter(Boolean).join(' · ').replace(/\s+/g, ' ').trim().slice(0, 240)
  const statusMatch = rawReason.match(/\b(401|403|408|409|429|5\d\d)\b/)
  const statusCode = statusMatch?.[1] || ''
  const needsReauth = entry.usage?.needs_reauth === true
    || extra?.needs_reauth === true
    || errorCode === 'unauthenticated'
    || statusCode === '401'
    || /unauthori[sz]ed|invalid[_ ](?:grant|token)|token\s*(?:expired|invalid|失效|过期)/i.test(rawReason)
  if (needsReauth) {
    return {
      label: '401 · 授权失效',
      description: '401：OpenAI OAuth 凭据已失效，需要重新授权',
      rawReason,
      tone: 'error',
      needsReauth: true
    }
  }
  if (errorCode === 'forbidden' || statusCode === '403' || entry.usage?.is_forbidden) {
    return { label: '403 · 访问受限', description: '403：OpenAI 拒绝访问，需检查验证或账号状态', rawReason, tone: 'error', needsReauth: false }
  }
  if (errorCode === 'rate_limited' || statusCode === '429') {
    return { label: '429 · 请求限流', description: '429：当前账号被上游限流，请等待额度窗口恢复', rawReason, tone: 'warning', needsReauth: false }
  }
  if (errorCode === 'network_error' || /network|timeout|connection|dns|tls|网络|连接|超时/i.test(rawReason)) {
    return { label: '网络异常', description: '网络异常：暂时无法连接 OpenAI，上游恢复后可继续调度', rawReason, tone: 'warning', needsReauth: false }
  }
  if (account.status === 'error' || errorCode || statusCode) {
    const code = statusCode || (errorCode && errorCode !== 'network_error' ? errorCode.toUpperCase() : '')
    return {
      label: code ? `${code} · 账号异常` : '账号异常',
      description: rawReason ? `账号异常：${rawReason.slice(0, 120)}` : '账号异常：服务器未返回更具体的错误原因',
      rawReason,
      tone: 'error',
      needsReauth: false
    }
  }
  return null
}

function formatUsage(value: number | null | undefined): string {
  return typeof value === 'number' && Number.isFinite(value) ? `${Math.max(0, value).toFixed(value % 1 === 0 ? 0 : 1)}%` : '待刷新'
}

function formatWeeklyEstimate(value: number | null | undefined): string {
  return typeof value === 'number' && Number.isFinite(value) ? `$${Math.max(0, value).toFixed(2)}` : '统计中'
}

function formatDateTime(value: string): string {
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
</script>

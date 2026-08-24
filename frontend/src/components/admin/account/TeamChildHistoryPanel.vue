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
          </div>
          <div class="flex shrink-0 flex-wrap items-center gap-2">
            <button type="button" class="btn btn-secondary flex items-center gap-1.5 whitespace-nowrap !px-2.5 !py-1.5 text-xs" :disabled="openingEmail === entry.email" @click="emit('open-mailbox', entry.email)">
              <Icon name="mail" size="xs" :class="openingEmail === entry.email ? 'animate-pulse' : ''" :stroke-width="2" />
              <span>{{ openingEmail === entry.email ? '打开中' : '接收验证码' }}</span>
            </button>
            <router-link v-if="entry.account" to="/admin/accounts" class="btn btn-secondary flex items-center gap-1.5 whitespace-nowrap !px-2.5 !py-1.5 text-xs">
              <Icon name="arrowRight" size="xs" :stroke-width="2" />
              <span>账号管理</span>
            </router-link>
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
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Icon } from '@/components/icons'
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
  revealedPasswords?: Record<number, string>
}>(), {
  activeMailboxEmail: '',
  openingEmail: '',
  loading: false,
  passwordLoadingAccountID: null,
  revealedPasswords: () => ({})
})

const emit = defineEmits<{
  refresh: []
  'open-mailbox': [email: string]
  'toggle-password': [entry: TeamChildHistoryEntry]
  'copy-password': [password: string]
}>()

const normalizedActiveEmail = computed(() => normalizeEmail(props.activeMailboxEmail))

function normalizeEmail(value: string): string {
  return String(value || '').trim().toLowerCase()
}

function accountStatusLabel(entry: TeamChildHistoryEntry): string {
  if (!entry.account) return '仅邮箱'
  if (entry.account.status === 'error') return '异常'
  if (entry.account.status !== 'active') return '已停用'
  if (!entry.account.schedulable) return '暂停调度'
  return '正常'
}

function accountStatusClass(entry: TeamChildHistoryEntry): string {
  if (!entry.account) return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  if (entry.account.status === 'error') return 'bg-red-100 text-red-700 dark:bg-red-950/40 dark:text-red-300'
  if (entry.account.status !== 'active' || !entry.account.schedulable) return 'bg-amber-100 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300'
  return 'bg-green-100 text-green-700 dark:bg-green-950/35 dark:text-green-300'
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

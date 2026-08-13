<template>
  <BaseDialog
    :show="show"
    :title="t('usage.oauthBillingDetails')"
    width="extra-wide"
    @close="emit('close')"
  >
    <div v-if="account" class="space-y-4">
      <div class="flex flex-col gap-3 border-b border-gray-200 pb-4 dark:border-dark-700 xl:flex-row xl:items-start xl:justify-between">
        <div class="min-w-0">
          <p class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ account.name }}</p>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ rangeDescription }}</p>
        </div>
        <div class="flex min-w-0 flex-col gap-2">
          <div class="flex flex-wrap items-center gap-1.5">
            <button
              type="button"
              :class="rangeButtonClass(usingExactWindow)"
              @click="restoreExactWindow"
            >
              {{ t('usage.currentUsageWindow', { window: initialRange.windowLabel }) }}
            </button>
            <button
              v-for="preset in quickRanges"
              :key="preset.minutes"
              type="button"
              :data-test="`billing-quick-${preset.minutes}`"
              :class="rangeButtonClass(activeQuickRange === preset.minutes)"
              @click="applyQuickRange(preset.minutes)"
            >
              {{ t(preset.labelKey) }}
            </button>
          </div>
          <div class="flex flex-wrap items-end gap-2">
            <label class="min-w-[180px] flex-1">
              <span class="mb-1 block text-[11px] text-gray-500 dark:text-gray-400">{{ t('usage.startTime') }}</span>
              <input
                v-model="startDateTime"
                type="datetime-local"
                step="60"
                class="input h-9 w-full text-xs"
                data-test="billing-start-time"
                @keydown.enter="applyCustomRange"
              />
            </label>
            <label class="min-w-[180px] flex-1">
              <span class="mb-1 block text-[11px] text-gray-500 dark:text-gray-400">{{ t('usage.endTime') }}</span>
              <input
                v-model="endDateTime"
                type="datetime-local"
                step="60"
                class="input h-9 w-full text-xs"
                data-test="billing-end-time"
                @keydown.enter="applyCustomRange"
              />
            </label>
            <button
              type="button"
              class="btn btn-primary h-9 shrink-0 px-4 text-xs"
              data-test="billing-apply-time"
              @click="applyCustomRange"
            >
              {{ t('usage.applyTimeRange') }}
            </button>
          </div>
        </div>
      </div>

      <div class="grid grid-cols-2 divide-x divide-gray-200 overflow-hidden rounded-md border border-gray-200 bg-gray-50 dark:divide-dark-600 dark:border-dark-600 dark:bg-dark-800 sm:grid-cols-4">
        <div class="px-3 py-2.5">
          <p class="text-[11px] text-gray-500 dark:text-gray-400">{{ t('usage.requestCount') }}</p>
          <p class="mt-0.5 text-sm font-semibold text-gray-900 dark:text-white">{{ formatInteger(summary.requests) }}</p>
        </div>
        <div class="border-t border-gray-200 px-3 py-2.5 dark:border-dark-600 sm:border-t-0">
          <p class="text-[11px] text-gray-500 dark:text-gray-400">Token</p>
          <p class="mt-0.5 text-sm font-semibold text-gray-900 dark:text-white">{{ formatCompactNumber(summary.tokens) }}</p>
        </div>
        <div class="border-t border-gray-200 px-3 py-2.5 dark:border-dark-600 sm:border-t-0">
          <p class="text-[11px] font-medium text-emerald-600 dark:text-emerald-400">{{ t('usage.accountBilled') }}</p>
          <p class="mt-0.5 text-sm font-semibold text-emerald-600 dark:text-emerald-400">${{ formatMoney(summary.account_cost) }}</p>
        </div>
        <div class="border-t border-gray-200 px-3 py-2.5 dark:border-dark-600 sm:border-t-0">
          <p class="text-[11px] font-medium text-amber-700 dark:text-amber-300">{{ t('usage.userBilled') }}</p>
          <p class="mt-0.5 text-sm font-semibold text-amber-700 dark:text-amber-300">¥{{ formatMoney(summary.user_cost) }}</p>
        </div>
      </div>

      <div v-if="selectedUser" class="flex items-center gap-2 border-b border-gray-200 pb-3 dark:border-dark-700">
        <button
          type="button"
          class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-md text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white"
          :title="t('usage.backToUsers')"
          @click="backToUsers"
        >
          <Icon name="arrowLeft" size="sm" />
        </button>
        <div class="min-w-0">
          <p class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ selectedUser.username || selectedUser.email }}</p>
          <p class="truncate text-xs text-gray-500 dark:text-gray-400">{{ selectedUser.email }}</p>
        </div>
        <span class="ml-auto shrink-0 text-xs text-gray-500 dark:text-gray-400">{{ t('usage.modelBillingDetails') }}</span>
      </div>

      <div v-if="loading" class="flex min-h-48 items-center justify-center">
        <LoadingSpinner />
      </div>
      <div v-else-if="error" class="flex min-h-48 flex-col items-center justify-center gap-3 text-center">
        <p class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
        <button type="button" class="btn btn-secondary btn-sm" @click="loadBreakdown">{{ t('common.retry') }}</button>
      </div>
      <div v-else-if="rows.length === 0" class="flex min-h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('usage.noBillingDetails') }}
      </div>
      <div v-else class="overflow-hidden rounded-md border border-gray-200 dark:border-dark-600">
        <div class="hidden grid-cols-[minmax(220px,1fr)_110px_120px_130px_130px] bg-gray-50 px-4 py-2 text-[11px] font-medium text-gray-500 dark:bg-dark-800 dark:text-gray-400 md:grid">
          <span>{{ selectedUser ? t('usage.upstreamModel') : t('usage.user') }}</span>
          <span class="text-right">{{ t('usage.requestCount') }}</span>
          <span class="text-right">Token</span>
          <span class="text-right text-emerald-600 dark:text-emerald-400">{{ t('usage.accountBilled') }}</span>
          <span class="text-right text-amber-700 dark:text-amber-300">{{ t('usage.userBilled') }}</span>
        </div>
        <button
          v-for="row in rows"
          :key="rowKey(row)"
          type="button"
          :disabled="Boolean(selectedUser)"
          class="grid min-h-14 w-full grid-cols-2 gap-x-3 gap-y-1 border-t border-gray-100 px-4 py-3 text-left transition-colors first:border-t-0 hover:bg-gray-50 disabled:cursor-default disabled:hover:bg-transparent dark:border-dark-700 dark:hover:bg-dark-800/60 dark:disabled:hover:bg-transparent md:grid-cols-[minmax(220px,1fr)_110px_120px_130px_130px] md:items-center md:gap-0"
          @click="openUser(row)"
        >
          <span class="col-span-2 min-w-0 md:col-span-1">
            <span class="block truncate text-sm font-medium text-gray-900 dark:text-white">{{ rowTitle(row) }}</span>
            <span v-if="!selectedUser" class="block truncate text-[11px] text-gray-500 dark:text-gray-400">{{ rowSubtitle(row) }}</span>
          </span>
          <span class="text-xs text-gray-600 dark:text-gray-300 md:text-right">{{ formatInteger(row.requests) }} {{ t('usage.requestCountUnit') }}</span>
          <span class="text-right text-xs text-gray-500 dark:text-gray-400">{{ formatCompactNumber(row.tokens) }} Token</span>
          <span class="text-xs font-medium text-emerald-600 dark:text-emerald-400 md:text-right">{{ t('usage.accountBilled') }} ${{ formatMoney(row.account_cost) }}</span>
          <span class="text-right text-xs font-medium text-amber-700 dark:text-amber-300">{{ t('usage.userBilled') }} ¥{{ formatMoney(row.user_cost) }}</span>
        </button>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type {
  Account,
  OAuthAccountBillingBreakdown,
  OAuthAccountBillingModel,
  OAuthAccountBillingSummary,
  OAuthAccountBillingUser
} from '@/types'
import { formatCompactNumber } from '@/utils/format'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'

export interface OAuthBillingInitialRange {
  windowLabel: string
  startTime: string
  endTime: string
}

const props = defineProps<{
  show: boolean
  account: Account | null
  initialRange: OAuthBillingInitialRange
}>()

const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()

const emptySummary = (): OAuthAccountBillingSummary => ({ requests: 0, tokens: 0, account_cost: 0, user_cost: 0 })
const loading = ref(false)
const error = ref('')
const breakdown = ref<OAuthAccountBillingBreakdown | null>(null)
const selectedUser = ref<OAuthAccountBillingBreakdown['selected_user'] | null>(null)
const usingExactWindow = ref(true)
const exactStartTime = ref('')
const exactEndTime = ref('')
const startDateTime = ref('')
const endDateTime = ref('')
const activeQuickRange = ref<number | null>(null)
let requestRevision = 0

const quickRanges = [
  { minutes: 30, labelKey: 'usage.last30Minutes' },
  { minutes: 60, labelKey: 'usage.last1Hour' },
  { minutes: 120, labelKey: 'usage.last2Hours' },
  { minutes: 360, labelKey: 'usage.last6Hours' },
  { minutes: 1440, labelKey: 'usage.last24Hours' }
] as const

const summary = computed(() => breakdown.value?.summary ?? emptySummary())
const rows = computed<Array<OAuthAccountBillingUser | OAuthAccountBillingModel>>(() => {
  return selectedUser.value ? (breakdown.value?.models ?? []) : (breakdown.value?.users ?? [])
})

const rangeDescription = computed(() => {
  if (usingExactWindow.value) return t('usage.currentUsageWindow', { window: props.initialRange.windowLabel })
  return `${formatRangeDateTime(exactStartTime.value)} - ${formatRangeDateTime(exactEndTime.value)}`
})

const toDateTimeLocal = (value: string | Date) => {
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

const formatRangeDateTime = (iso: string) => {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '-'
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hour = String(date.getHours()).padStart(2, '0')
  const minute = String(date.getMinutes()).padStart(2, '0')
  return `${year}-${month}-${day} ${hour}:${minute}`
}

const rangeButtonClass = (active: boolean) => [
  'rounded-md border px-2.5 py-1.5 text-xs font-medium transition-colors',
  active
    ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
    : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-dark-600 dark:text-gray-300 dark:hover:bg-dark-700'
]

const restoreExactWindow = () => {
  usingExactWindow.value = true
  activeQuickRange.value = null
  exactStartTime.value = props.initialRange.startTime
  exactEndTime.value = props.initialRange.endTime
  startDateTime.value = toDateTimeLocal(exactStartTime.value)
  endDateTime.value = toDateTimeLocal(exactEndTime.value)
  selectedUser.value = null
  loadBreakdown()
}

const applyQuickRange = (minutes: number) => {
  const end = new Date()
  const start = new Date(end.getTime() - minutes * 60_000)
  exactStartTime.value = start.toISOString()
  exactEndTime.value = end.toISOString()
  startDateTime.value = toDateTimeLocal(start)
  endDateTime.value = toDateTimeLocal(end)
  usingExactWindow.value = false
  activeQuickRange.value = minutes
  selectedUser.value = null
  loadBreakdown()
}

const applyCustomRange = () => {
  const start = new Date(startDateTime.value)
  const end = new Date(endDateTime.value)
  if (
    !startDateTime.value ||
    !endDateTime.value ||
    Number.isNaN(start.getTime()) ||
    Number.isNaN(end.getTime()) ||
    start.getTime() >= end.getTime()
  ) {
    error.value = t('usage.invalidMinuteRange')
    return
  }
  exactStartTime.value = start.toISOString()
  exactEndTime.value = end.toISOString()
  usingExactWindow.value = false
  activeQuickRange.value = null
  selectedUser.value = null
  loadBreakdown()
}

const requestParams = () => {
  const params: Record<string, string | number> = {
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'Asia/Shanghai'
  }
  params.start_time = exactStartTime.value
  params.end_time = exactEndTime.value
  if (selectedUser.value) params.user_id = selectedUser.value.user_id
  return params
}

const loadBreakdown = async () => {
  if (!props.account) return
  const revision = ++requestRevision
  loading.value = true
  error.value = ''
  try {
    const response = await adminAPI.accounts.getOAuthBillingBreakdown(props.account.id, requestParams())
    if (revision !== requestRevision) return
    breakdown.value = response
    if (response.selected_user) selectedUser.value = response.selected_user
  } catch (cause: unknown) {
    if (revision !== requestRevision) return
    const message = cause instanceof Error ? cause.message : String(cause)
    error.value = message || t('usage.billingDetailsFailed')
  } finally {
    if (revision === requestRevision) loading.value = false
  }
}

const openUser = (row: OAuthAccountBillingUser | OAuthAccountBillingModel) => {
  if (!('user_id' in row)) return
  selectedUser.value = { user_id: row.user_id, email: row.email, username: row.username }
  loadBreakdown()
}

const backToUsers = () => {
  selectedUser.value = null
  loadBreakdown()
}

const rowKey = (row: OAuthAccountBillingUser | OAuthAccountBillingModel) => 'user_id' in row ? `u-${row.user_id}` : `m-${row.model}`
const rowTitle = (row: OAuthAccountBillingUser | OAuthAccountBillingModel) => 'user_id' in row ? (row.username || row.email) : (row.model || '-')
const rowSubtitle = (row: OAuthAccountBillingUser | OAuthAccountBillingModel) => 'user_id' in row ? row.email : ''
const formatMoney = (value: number) => Number(value || 0).toFixed(2)
const formatInteger = (value: number) => Math.max(0, Math.trunc(value || 0)).toLocaleString('zh-CN')

watch(
  () => [props.show, props.account?.id, props.initialRange.startTime, props.initialRange.endTime] as const,
  ([show]) => {
    if (!show || !props.account) return
    exactStartTime.value = props.initialRange.startTime
    exactEndTime.value = props.initialRange.endTime
    startDateTime.value = toDateTimeLocal(props.initialRange.startTime)
    endDateTime.value = toDateTimeLocal(props.initialRange.endTime)
    usingExactWindow.value = true
    activeQuickRange.value = null
    selectedUser.value = null
    breakdown.value = null
    loadBreakdown()
  },
  { immediate: true }
)
</script>

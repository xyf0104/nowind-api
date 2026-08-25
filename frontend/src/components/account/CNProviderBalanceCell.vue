<template>
  <div v-if="visible" class="space-y-1">
    <!-- Balance value row: static display (snapshot or probe result) -->
    <div class="flex flex-wrap items-center gap-1.5">
      <span
        data-test="cn-provider-balance-value"
        :class="['text-[10px] font-medium leading-4', platformTextClass(account.platform)]"
        :title="t('admin.accounts.cnProviders.balanceProbeTooltip')"
      >
        {{ balanceLabel }}
      </span>

      <!-- Low balance badge (reactive 402/429 marker or probe-detected) -->
      <span
        v-if="balanceLow"
        class="inline-flex items-center rounded bg-red-100 px-1 py-0.5 text-[10px] font-medium text-red-700 dark:bg-red-900/30 dark:text-red-300"
      >
        {{ t('admin.accounts.cnProviders.balanceLow') }}
      </span>
    </div>

    <!-- Explicit refresh action (aligned with the OpenAI "Query" / Grok "Probe"
         buttons): the old chip doubled as the data display and a hidden click
         target, which users could not discover. The verb label makes the
         affordance explicit. -->
    <div class="flex flex-wrap items-center gap-1.5">
      <button
        type="button"
        data-test="cn-provider-balance-probe"
        class="inline-flex items-center gap-0.5 whitespace-nowrap rounded px-1.5 py-0.5 text-[10px] font-medium leading-4 text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
        :disabled="loading"
        :title="t('admin.accounts.cnProviders.balanceProbeTooltip')"
        @click="handleProbe"
      >
        <svg
          class="h-2.5 w-2.5"
          :class="{ 'animate-spin': loading }"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
          />
        </svg>
        {{ t('admin.accounts.cnProviders.probe') }}
      </button>
    </div>

    <div v-if="error" class="truncate text-[10px] text-red-600 dark:text-red-400" :title="error">
      {{ truncatedError }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { CNProviderBalanceEntry, CNProviderBalanceResult } from '@/api/admin/cnProviders'
import type { Account } from '@/types'
import { platformTextClass } from '@/utils/platformColors'
import { cnBalanceCellVisible } from './credentialsBuilder'

const props = defineProps<{ account: Account }>()
const { t } = useI18n()

const readMode = (): string => {
  const mode = props.account.credentials?.account_mode
  return typeof mode === 'string' ? mode : ''
}

const visible = computed(() => cnBalanceCellVisible(props.account.platform, readMode()))
const loading = ref(false)
const error = ref<string | null>(null)
const data = ref<CNProviderBalanceResult | null>(null)
const extraKey = (suffix: string) => `${props.account.platform}_${suffix}`

const snapshotBalance = computed(() => {
  const value = props.account.extra?.[extraKey('balance')]
  return typeof value === 'number' ? value : null
})
const snapshotCurrency = computed(() => {
  const value = props.account.extra?.[extraKey('balance_currency')]
  return typeof value === 'string' ? value : ''
})
const snapshotBalances = computed<CNProviderBalanceEntry[]>(() => {
  const value = props.account.extra?.[extraKey('balances')]
  if (!Array.isArray(value)) return []
  return value.flatMap((item): CNProviderBalanceEntry[] => {
    if (!item || typeof item !== 'object') return []
    const { currency, balance } = item as Record<string, unknown>
    if (typeof currency !== 'string' || typeof balance !== 'number') return []
    return [{ currency, balance }]
  })
})
const balanceLow = computed(() => props.account.extra?.[extraKey('balance_low')] === true)

const currentEntries = computed<CNProviderBalanceEntry[]>(() => {
  if (data.value?.success) {
    if (data.value.balances?.length) return data.value.balances
    return [{ currency: data.value.currency || '', balance: data.value.balance }]
  }
  if (snapshotBalances.value.length) return snapshotBalances.value
  if (snapshotBalance.value != null) {
    return [{ currency: snapshotCurrency.value, balance: snapshotBalance.value }]
  }
  return []
})

const formatEntry = (entry: CNProviderBalanceEntry): string => {
  const fixed = entry.balance >= 100 ? entry.balance.toFixed(0) : entry.balance.toFixed(2)
  return `${entry.currency || '¥'} ${fixed}`
}

const balanceLabel = computed(() =>
  currentEntries.value.length
    ? currentEntries.value.map(formatEntry).join(' · ')
    : t('admin.accounts.cnProviders.balance')
)

const extractErrorMessage = (value: unknown): string => {
  const err = value as {
    message?: string
    reason?: string
    response?: { data?: { message?: string; error?: string } }
  }
  return (
    err?.message ||
    err?.reason ||
    err?.response?.data?.message ||
    err?.response?.data?.error ||
    t('common.error')
  )
}

const truncatedError = computed(() => {
  if (!error.value) return ''
  return error.value.length > 80 ? `${error.value.slice(0, 80)}...` : error.value
})

const handleProbe = async () => {
  if (loading.value) return
  loading.value = true
  error.value = null
  try {
    const result = await adminAPI.cnProviders.queryBalance(props.account.id)
    if (result.success) data.value = result
    else error.value = result.error || t('common.error')
  } catch (value) {
    error.value = extractErrorMessage(value)
  } finally {
    loading.value = false
  }
}

watch(
  () => props.account.id,
  () => {
    data.value = null
    error.value = null
    loading.value = false
  }
)
</script>

<template>
  <BaseDialog
    :show="show"
    :title="t('tokenConverter.adminAccounts.picker.title')"
    width="wide"
    @close="handleCancel"
  >
    <div class="min-w-0 max-w-full overflow-x-hidden">
      <div class="space-y-3">
        <div class="relative min-w-0">
          <Icon
            name="search"
            size="sm"
            class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"
          />
          <input
            v-model="searchQuery"
            type="search"
            class="input h-10 w-full min-w-0 pl-9"
            :placeholder="t('tokenConverter.adminAccounts.picker.searchPlaceholder')"
            :aria-label="t('tokenConverter.adminAccounts.picker.searchAria')"
          />
        </div>

        <div
          v-if="platformFilters.length"
          class="flex max-w-full flex-wrap gap-2"
          :aria-label="t('tokenConverter.adminAccounts.picker.platformFilter')"
        >
          <button
            type="button"
            class="inline-flex min-h-9 flex-none items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40"
            :class="platformButtonClass('all')"
            :aria-pressed="selectedPlatform === 'all'"
            data-testid="platform-filter-all"
            @click="selectedPlatform = 'all'"
          >
            <Icon name="grid" size="sm" />
            {{ t('tokenConverter.adminAccounts.picker.all') }}
            <span class="tabular-nums text-xs opacity-70">{{ accounts.length }}</span>
          </button>
          <button
            v-for="platform in platformFilters"
            :key="platform.value"
            type="button"
            class="inline-flex min-h-9 flex-none items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40"
            :class="platformButtonClass(platform.value)"
            :aria-pressed="selectedPlatform === platform.value"
            :data-testid="`platform-filter-${platform.value}`"
            @click="selectedPlatform = platform.value"
          >
            <PlatformIcon :platform="platform.value" size="sm" />
            {{ platformLabel(platform.value) }}
            <span class="tabular-nums text-xs opacity-70">{{ platform.count }}</span>
          </button>
        </div>

        <div class="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <p class="text-xs text-gray-500 dark:text-gray-400" aria-live="polite">
            {{ t('tokenConverter.adminAccounts.picker.selectedSummary', { selected: selectedIds.size, total: accounts.length }) }}
          </p>
          <button
            type="button"
            class="btn btn-secondary btn-sm w-full justify-center sm:w-auto"
            :disabled="loading || filteredEntries.length === 0"
            @click="selectAllFiltered"
          >
            <Icon name="check" size="sm" />
            {{ t('tokenConverter.adminAccounts.picker.selectAllFiltered', { count: filteredEntries.length }) }}
          </button>
        </div>
      </div>

      <div
        v-if="loading"
        class="flex min-h-48 items-center justify-center gap-2 py-8 text-sm text-gray-500 dark:text-gray-400"
        data-testid="account-picker-loading"
      >
        <span class="h-4 w-4 animate-spin rounded-full border-2 border-gray-300 border-t-primary-500" />
        {{ t('tokenConverter.adminAccounts.picker.loading') }}
      </div>

      <div
        v-else-if="accounts.length === 0"
        class="flex min-h-48 flex-col items-center justify-center gap-2 py-8 text-center text-sm text-gray-500 dark:text-gray-400"
      >
        <Icon name="inbox" size="lg" />
        {{ t('tokenConverter.adminAccounts.picker.empty') }}
      </div>

      <div
        v-else-if="filteredEntries.length === 0"
        class="flex min-h-48 flex-col items-center justify-center gap-2 py-8 text-center text-sm text-gray-500 dark:text-gray-400"
      >
        <Icon name="search" size="lg" />
        {{ t('tokenConverter.adminAccounts.picker.noMatches') }}
      </div>

      <div v-else class="mt-3 max-h-[56vh] min-w-0 space-y-4 overflow-y-auto overflow-x-hidden pr-1">
        <section
          v-for="group in groupedEntries"
          :key="group.platform"
          class="min-w-0"
          :data-testid="`platform-group-${group.platform}`"
        >
          <div class="mb-2 flex min-w-0 items-center gap-2">
            <PlatformIcon :platform="group.platform" size="sm" />
            <h4 class="truncate text-xs font-semibold uppercase text-gray-500 dark:text-gray-400">
              {{ platformLabel(group.platform) }}
            </h4>
            <span class="text-xs tabular-nums text-gray-400 dark:text-gray-500">
              {{ group.entries.length }}
            </span>
          </div>

          <div class="min-w-0 divide-y divide-gray-100 overflow-hidden rounded-md border border-gray-200 dark:divide-dark-700 dark:border-dark-600">
            <label
              v-for="entry in group.entries"
              :key="entry.account.id"
              class="flex min-w-0 cursor-pointer items-start gap-3 bg-white px-3 py-3 transition-colors hover:bg-gray-50 dark:bg-dark-800 dark:hover:bg-dark-700/80 sm:items-center"
              :data-testid="`account-option-${entry.account.id}`"
            >
              <input
                type="checkbox"
                class="mt-0.5 h-4 w-4 flex-none rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500 dark:bg-dark-700 sm:mt-0"
                :checked="selectedIds.has(entry.account.id)"
                :aria-label="t('tokenConverter.adminAccounts.picker.selectAccount', { name: entry.account.name })"
                @change="toggleAccount(entry.account.id)"
              />

              <span class="min-w-0 flex-1">
                <span class="flex min-w-0 flex-col gap-1 sm:flex-row sm:items-center sm:gap-2">
                  <span
                    class="min-w-0 break-all text-sm font-medium text-gray-900 dark:text-white"
                    :title="entry.account.name"
                  >
                    {{ entry.account.name }}
                  </span>
                  <span class="w-fit flex-none rounded bg-gray-100 px-1.5 py-0.5 text-[11px] font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">
                    {{ accountTypeLabel(entry.account.type) }}
                  </span>
                </span>
                <span
                  v-if="entry.account.notes"
                  class="mt-1 block min-w-0 break-words text-xs text-gray-500 dark:text-gray-400"
                >
                  {{ entry.account.notes }}
                </span>
                <span class="mt-1 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-[11px] text-gray-400 dark:text-gray-500">
                  <span>{{ t('tokenConverter.adminAccounts.picker.concurrency', { value: entry.account.concurrency }) }}</span>
                  <span>{{ t('tokenConverter.adminAccounts.picker.priority', { value: entry.account.priority }) }}</span>
                </span>
              </span>
            </label>
          </div>
        </section>
      </div>
    </div>

    <template #footer>
      <div class="flex w-full flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <button type="button" class="btn btn-secondary w-full justify-center sm:w-auto" @click="handleCancel">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary w-full justify-center sm:w-auto"
          :disabled="loading || selectedIds.size === 0"
          @click="handleConfirm"
        >
          <Icon name="check" size="sm" />
          {{ t('tokenConverter.adminAccounts.picker.confirm', { count: selectedIds.size }) }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import type {
  Account,
  AccountPlatform,
  AccountType,
} from '@/types'

interface AccountEntry {
  account: Account
}

interface PlatformGroup {
  platform: AccountPlatform
  entries: AccountEntry[]
}

const props = defineProps<{
  show: boolean
  accounts: Account[]
  loading: boolean
}>()

const emit = defineEmits<{
  (event: 'cancel'): void
  (event: 'confirm', accountIds: number[]): void
}>()

const { t } = useI18n()

const searchQuery = ref('')
const selectedPlatform = ref<'all' | AccountPlatform>('all')
const selectedIds = ref<Set<number>>(new Set())

const accounts = computed(() => props.accounts.filter((account) => account.type === 'oauth'))

const platformFilters = computed(() => {
  const counts = new Map<AccountPlatform, number>()
  for (const account of accounts.value) {
    counts.set(account.platform, (counts.get(account.platform) ?? 0) + 1)
  }
  return Array.from(counts, ([value, count]) => ({ value, count }))
})

const filteredEntries = computed<AccountEntry[]>(() => {
  const query = searchQuery.value.trim().toLocaleLowerCase()
  return accounts.value
    .map((account) => ({ account }))
    .filter(({ account }) => {
      if (selectedPlatform.value !== 'all' && account.platform !== selectedPlatform.value) {
        return false
      }
      if (!query) return true

      const searchableFields = [
        account.name,
        account.notes ?? '',
        account.platform,
        account.type,
      ]
      return searchableFields.some((value) => value.toLocaleLowerCase().includes(query))
    })
})

const groupedEntries = computed<PlatformGroup[]>(() => {
  const groups = new Map<AccountPlatform, AccountEntry[]>()
  for (const entry of filteredEntries.value) {
    const entries = groups.get(entry.account.platform) ?? []
    entries.push(entry)
    groups.set(entry.account.platform, entries)
  }
  return Array.from(groups, ([platform, entries]) => ({ platform, entries }))
})

watch(
  () => props.show,
  (shown) => {
    if (shown) resetSelection()
  },
)

watch(
  () => props.accounts,
  () => {
    if (props.show) resetSelection()
  },
)

watch(platformFilters, (filters) => {
  if (
    selectedPlatform.value !== 'all' &&
    !filters.some((filter) => filter.value === selectedPlatform.value)
  ) {
    selectedPlatform.value = 'all'
  }
})

function resetSelection(): void {
  searchQuery.value = ''
  selectedPlatform.value = 'all'
  selectedIds.value = new Set()
}

function platformLabel(platform: AccountPlatform): string {
  return t(`tokenConverter.adminAccounts.picker.platforms.${platform}`)
}

function accountTypeLabel(type: AccountType): string {
  return t(`tokenConverter.adminAccounts.picker.types.${type}`)
}

function platformButtonClass(platform: 'all' | AccountPlatform): string {
  return selectedPlatform.value === platform
    ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-950/40 dark:text-primary-300'
    : 'border-gray-200 bg-white text-gray-600 hover:border-gray-300 hover:text-gray-900 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:border-dark-500 dark:hover:text-white'
}

function toggleAccount(accountId: number): void {
  if (!accounts.value.some((account) => account.id === accountId)) return
  const next = new Set(selectedIds.value)
  if (next.has(accountId)) next.delete(accountId)
  else next.add(accountId)
  selectedIds.value = next
}

function selectAllFiltered(): void {
  const next = new Set(selectedIds.value)
  for (const entry of filteredEntries.value) next.add(entry.account.id)
  selectedIds.value = next
}

function handleCancel(): void {
  emit('cancel')
}

function handleConfirm(): void {
  if (selectedIds.value.size === 0) return
  const oauthAccountIds = accounts.value
    .filter((account) => selectedIds.value.has(account.id))
    .map((account) => account.id)
  if (oauthAccountIds.length === 0) return
  emit('confirm', oauthAccountIds)
}
</script>

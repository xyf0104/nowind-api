<template>
  <BaseDialog
    :show="show"
    :title="t('admin.announcements.emailDispatch.title')"
    width="wide"
    :z-index="80"
    @close="handleClose"
  >
    <div v-if="announcement" class="space-y-5">
      <div class="border-b border-gray-200 pb-4 dark:border-dark-700">
        <p class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ announcement.title }}</p>
        <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-dark-400">
          <span>{{ t('admin.announcements.emailDispatch.deliveryRecorded') }}</span>
          <span v-if="deliverySummary.total > 0">
            {{ t('admin.announcements.emailDispatch.deliverySummary', {
              sent: deliverySummary.sent,
              failed: deliverySummary.failed,
              pending: deliverySummary.claimed
            }) }}
          </span>
        </div>
      </div>

      <div class="grid grid-cols-2 overflow-hidden rounded-lg border border-gray-200 p-1 dark:border-dark-600">
        <button
          type="button"
          data-testid="announcement-email-scope-all"
          class="min-h-10 rounded-md px-3 text-sm font-medium transition-colors"
          :class="scope === 'all'
            ? 'bg-primary-600 text-white shadow-sm'
            : 'text-gray-600 hover:bg-gray-100 dark:text-dark-300 dark:hover:bg-dark-700'"
          @click="scope = 'all'"
        >
          {{ t('admin.announcements.emailDispatch.allUsers') }}
        </button>
        <button
          type="button"
          data-testid="announcement-email-scope-selected"
          class="min-h-10 rounded-md px-3 text-sm font-medium transition-colors"
          :class="scope === 'selected'
            ? 'bg-primary-600 text-white shadow-sm'
            : 'text-gray-600 hover:bg-gray-100 dark:text-dark-300 dark:hover:bg-dark-700'"
          @click="scope = 'selected'"
        >
          {{ t('admin.announcements.emailDispatch.selectUsers') }}
        </button>
      </div>

      <div v-if="scope === 'all'" class="flex items-baseline justify-between gap-4 border-b border-gray-100 pb-4 dark:border-dark-700/70">
        <span class="text-sm text-gray-600 dark:text-dark-300">{{ t('admin.announcements.emailDispatch.activeUsers') }}</span>
        <span class="shrink-0 text-lg font-semibold text-gray-900 dark:text-white">{{ userPagination.total }}</span>
      </div>

      <template v-else>
        <div class="flex flex-col gap-3 sm:flex-row sm:items-center">
          <label class="relative min-w-0 flex-1">
            <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
            <input
              v-model="searchQuery"
              type="search"
              class="input w-full !py-2 !pl-9"
              :placeholder="t('admin.announcements.emailDispatch.searchUsers')"
              @input="handleSearch"
            />
          </label>
          <div class="flex items-center justify-between gap-3 sm:justify-end">
            <span class="text-sm text-gray-500 dark:text-dark-400">
              {{ t('admin.announcements.emailDispatch.selectedCount', { count: selectedUserIDs.size }) }}
            </span>
            <button
              type="button"
              class="btn btn-secondary shrink-0 !px-2.5"
              :disabled="loadingUsers"
              :title="t('common.refresh')"
              @click="loadUsers"
            >
              <Icon name="refresh" size="sm" :class="loadingUsers ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>

        <div class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
          <label class="flex min-h-11 cursor-pointer items-center gap-3 border-b border-gray-200 px-3 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 dark:border-dark-600 dark:text-dark-200 dark:hover:bg-dark-700/60">
            <input
              type="checkbox"
              data-testid="announcement-email-select-page"
              class="h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              :checked="allCurrentPageSelected"
              :indeterminate.prop="someCurrentPageSelected"
              :disabled="loadingUsers || users.length === 0"
              @change="toggleCurrentPage"
            />
            <span class="min-w-0 flex-1 truncate">{{ t('admin.announcements.emailDispatch.selectPage') }}</span>
            <span class="shrink-0 text-xs font-normal text-gray-400 dark:text-dark-500">{{ users.length }}</span>
          </label>

          <div class="max-h-[min(46dvh,30rem)] overflow-y-auto">
            <div v-if="loadingUsers" class="flex min-h-48 items-center justify-center text-sm text-gray-500 dark:text-dark-400">
              <Icon name="refresh" size="sm" class="mr-2 animate-spin" />
              {{ t('common.loading') }}
            </div>
            <div v-else-if="users.length === 0" class="flex min-h-48 items-center justify-center px-4 text-center text-sm text-gray-500 dark:text-dark-400">
              {{ t('admin.announcements.emailDispatch.noActiveUsers') }}
            </div>
            <label
              v-for="user in users"
              :key="user.id"
              class="grid min-h-14 cursor-pointer grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 border-b border-gray-100 px-3 py-2 last:border-b-0 hover:bg-gray-50 dark:border-dark-700/70 dark:hover:bg-dark-700/45"
            >
              <input
                type="checkbox"
                :data-testid="`announcement-email-select-user-${user.id}`"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                :checked="selectedUserIDs.has(user.id)"
                @change="toggleUser(user.id)"
              />
              <span class="min-w-0">
                <span class="block truncate text-sm font-medium text-gray-800 dark:text-dark-100">{{ user.email }}</span>
                <span class="mt-0.5 block truncate text-xs text-gray-500 dark:text-dark-400">
                  #{{ user.id }}<template v-if="user.username"> · {{ user.username }}</template>
                </span>
              </span>
            </label>
          </div>
        </div>

        <Pagination
          v-if="userPagination.total > 0 && userPagination.pages > 1"
          :page="userPagination.page"
          :total="userPagination.total"
          :page-size="userPagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>

      <div v-if="lastDispatchResult" class="flex flex-wrap gap-x-4 gap-y-1 border-t border-gray-200 pt-4 text-sm dark:border-dark-700">
        <span class="font-medium text-green-700 dark:text-green-300">
          {{ t('admin.announcements.emailDispatch.sentCount', { count: lastDispatchResult.sent }) }}
        </span>
        <span class="text-gray-600 dark:text-dark-300">
          {{ t('admin.announcements.emailDispatch.alreadySentCount', { count: lastDispatchResult.already_sent }) }}
        </span>
        <span v-if="lastDispatchResult.failed > 0" class="font-medium text-red-700 dark:text-red-300">
          {{ t('admin.announcements.emailDispatch.failedCount', { count: lastDispatchResult.failed }) }}
        </span>
        <span v-if="lastDispatchResult.skipped > 0" class="text-amber-700 dark:text-amber-300">
          {{ t('admin.announcements.emailDispatch.skippedCount', { count: lastDispatchResult.skipped }) }}
        </span>
      </div>
    </div>

    <template #footer>
      <div class="flex flex-wrap justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="dispatching" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          data-testid="announcement-email-confirm"
          class="btn btn-primary flex items-center gap-2 whitespace-nowrap"
          :disabled="!canConfirmDispatch"
          @click="showConfirmDialog = true"
        >
          <Icon name="mail" size="sm" />
          {{ t('admin.announcements.emailDispatch.confirmNotify') }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <BaseDialog
    :show="showConfirmDialog"
    :title="t('admin.announcements.emailDispatch.confirmTitle')"
    width="narrow"
    :z-index="100"
    @close="showConfirmDialog = false"
  >
    <div class="space-y-2 text-sm text-gray-600 dark:text-dark-300">
      <p>{{ t('admin.announcements.emailDispatch.confirmMessage', { count: dispatchTargetCount }) }}</p>
      <p class="font-medium text-gray-900 dark:text-white">{{ announcement?.title }}</p>
    </div>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="dispatching" @click="showConfirmDialog = false">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          data-testid="announcement-email-send-now"
          class="btn btn-primary flex items-center gap-2"
          :disabled="dispatching"
          @click="dispatchNotifications"
        >
          <Icon name="refresh" size="sm" :class="dispatching ? 'animate-spin' : ''" />
          {{ dispatching ? t('admin.announcements.emailDispatch.sending') : t('admin.announcements.emailDispatch.sendNow') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { AdminUser, Announcement, AnnouncementEmailDeliverySummary, AnnouncementEmailDispatchResult } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Pagination from '@/components/common/Pagination.vue'

const props = defineProps<{
  show: boolean
  announcement: Announcement | null
}>()

const emit = defineEmits<{
  close: []
  sent: [result: AnnouncementEmailDispatchResult]
}>()

const { t } = useI18n()
const appStore = useAppStore()

const scope = ref<'all' | 'selected'>('all')
const users = ref<AdminUser[]>([])
const selectedUserIDs = ref(new Set<number>())
const searchQuery = ref('')
const loadingUsers = ref(false)
const dispatching = ref(false)
const showConfirmDialog = ref(false)
const lastDispatchResult = ref<AnnouncementEmailDispatchResult | null>(null)
const deliverySummary = reactive<AnnouncementEmailDeliverySummary>({ total: 0, claimed: 0, sent: 0, failed: 0 })
const userPagination = reactive({ page: 1, page_size: 70, total: 0, pages: 1 })

let usersController: AbortController | null = null
let searchTimer: number | null = null

const currentPageIDs = computed(() => users.value.map((user) => user.id))
const allCurrentPageSelected = computed(() => currentPageIDs.value.length > 0 && currentPageIDs.value.every((id) => selectedUserIDs.value.has(id)))
const someCurrentPageSelected = computed(() => !allCurrentPageSelected.value && currentPageIDs.value.some((id) => selectedUserIDs.value.has(id)))
const dispatchTargetCount = computed(() => scope.value === 'all' ? userPagination.total : selectedUserIDs.value.size)
const canConfirmDispatch = computed(() => {
  if (loadingUsers.value || dispatching.value || !props.announcement) return false
  if (scope.value === 'all') return userPagination.total > 0
  return selectedUserIDs.value.size > 0 && selectedUserIDs.value.size <= 1000
})

async function loadUsers() {
  usersController?.abort()
  const controller = new AbortController()
  usersController = controller
  loadingUsers.value = true
  try {
    const response = await adminAPI.users.list(userPagination.page, userPagination.page_size, {
      status: 'active',
      search: searchQuery.value.trim() || undefined,
    sort_by: 'last_activity_at',
    sort_order: 'desc',
    include_subscriptions: false,
    role: 'user',
    email_eligible: true,
    }, { signal: controller.signal })
    if (controller.signal.aborted || usersController !== controller) return
    users.value = response.items
    userPagination.total = response.total
    userPagination.page = response.page
    userPagination.page_size = response.page_size
    userPagination.pages = response.pages
  } catch (error: any) {
    if (controller.signal.aborted || error?.name === 'AbortError' || error?.code === 'ERR_CANCELED') return
    appStore.showError(error?.response?.data?.message || t('admin.announcements.emailDispatch.failedToLoadUsers'))
  } finally {
    if (usersController === controller) {
      loadingUsers.value = false
      usersController = null
    }
  }
}

async function loadDeliverySummary() {
  const announcement = props.announcement
  if (!announcement || announcement.notify_mode !== 'email') return
  try {
    const summary = await adminAPI.announcements.getEmailDeliverySummary(announcement.id)
    deliverySummary.total = summary.total
    deliverySummary.claimed = summary.claimed
    deliverySummary.sent = summary.sent
    deliverySummary.failed = summary.failed
  } catch (error: any) {
    if (error?.response?.status !== 400) {
      appStore.showError(error?.response?.data?.message || t('admin.announcements.emailDispatch.failedToLoadSummary'))
    }
  }
}

function resetDialogState() {
  scope.value = 'all'
  users.value = []
  selectedUserIDs.value = new Set()
  searchQuery.value = ''
  userPagination.page = 1
  userPagination.page_size = 70
  userPagination.pages = 1
  userPagination.total = 0
  lastDispatchResult.value = null
  deliverySummary.total = 0
  deliverySummary.claimed = 0
  deliverySummary.sent = 0
  deliverySummary.failed = 0
  showConfirmDialog.value = false
}

function handleClose() {
  if (dispatching.value) return
  usersController?.abort()
  usersController = null
  showConfirmDialog.value = false
  emit('close')
}

function handleSearch() {
  if (searchTimer) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => {
    userPagination.page = 1
    void loadUsers()
  }, 250)
}

function handlePageChange(page: number) {
  userPagination.page = page
  void loadUsers()
}

function handlePageSizeChange(pageSize: number) {
  userPagination.page_size = pageSize
  userPagination.page = 1
  void loadUsers()
}

function toggleUser(userID: number) {
  const next = new Set(selectedUserIDs.value)
  if (next.has(userID)) next.delete(userID)
  else next.add(userID)
  selectedUserIDs.value = next
}

function toggleCurrentPage() {
  const next = new Set(selectedUserIDs.value)
  if (allCurrentPageSelected.value) {
    currentPageIDs.value.forEach((id) => next.delete(id))
  } else {
    currentPageIDs.value.forEach((id) => next.add(id))
  }
  selectedUserIDs.value = next
}

async function dispatchNotifications() {
  const announcement = props.announcement
  if (!announcement || !canConfirmDispatch.value) return

  dispatching.value = true
  try {
    const result = await adminAPI.announcements.dispatchEmailNotifications(announcement.id, {
      scope: scope.value,
      user_ids: scope.value === 'selected' ? [...selectedUserIDs.value] : undefined,
    })
    lastDispatchResult.value = result
    await loadDeliverySummary()
    showConfirmDialog.value = false
    appStore.showSuccess(t('admin.announcements.emailDispatch.sentResult', { count: result.sent }))
    emit('sent', result)
  } catch (error: any) {
    appStore.showError(error?.response?.data?.message || t('admin.announcements.emailDispatch.failedToSend'))
  } finally {
    dispatching.value = false
  }
}

watch(
  () => [props.show, props.announcement?.id] as const,
  ([show]) => {
    if (!show) return
    resetDialogState()
    void Promise.all([loadUsers(), loadDeliverySummary()])
  },
  { immediate: true }
)

onUnmounted(() => {
  usersController?.abort()
  if (searchTimer) window.clearTimeout(searchTimer)
})
</script>

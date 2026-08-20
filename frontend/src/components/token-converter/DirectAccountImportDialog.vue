<template>
  <BaseDialog
    :show="show"
    :title="t('tokenConverter.directImport.title')"
    width="wide"
    :close-on-click-outside="!importing"
    @close="handleClose"
  >
    <div class="space-y-4">
      <p class="text-sm leading-6 text-gray-600 dark:text-gray-300">
        {{ t('tokenConverter.directImport.description', { count: accountCount }) }}
      </p>

      <div class="rounded-md border border-amber-200 bg-amber-50 px-3 py-2.5 text-xs leading-5 text-amber-800 dark:border-amber-800/70 dark:bg-amber-950/30 dark:text-amber-200">
        {{ t('tokenConverter.directImport.explicitUpload') }}
      </div>

      <div v-if="result" data-test="direct-import-result" class="space-y-3 rounded-md border border-gray-200 p-4 dark:border-dark-700">
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.dataImportResult') }}
        </div>
        <p class="text-sm text-gray-700 dark:text-gray-300">
          {{ t('admin.accounts.dataImportResultSummary', result) }}
        </p>
        <div v-if="errorItems.length" class="max-h-52 space-y-1 overflow-y-auto rounded-md bg-gray-50 p-3 font-mono text-xs text-red-700 dark:bg-dark-800 dark:text-red-300">
          <div v-for="(item, index) in errorItems" :key="`${item.kind}-${item.name || item.proxy_key || index}`" class="break-words">
            {{ item.kind }} {{ item.name || item.proxy_key || '-' }}: {{ item.message }}
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="importing" @click="handleClose">
          {{ result ? t('common.close') : t('common.cancel') }}
        </button>
        <button
          v-if="!result"
          type="button"
          data-test="confirm-direct-import"
          class="btn btn-primary"
          :disabled="importing || !payload || accountCount < 1"
          @click="importAccounts"
        >
          <span v-if="importing" class="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
          <Icon v-else name="upload" size="sm" />
          {{ importing ? t('tokenConverter.directImport.importing') : t('tokenConverter.directImport.confirm') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import type { AdminDataImportResult, AdminDataPayload } from '@/types'

interface Props {
  show: boolean
  payload: AdminDataPayload | null
  accountCount: number
}

interface Emits {
  (event: 'close'): void
  (event: 'imported', result: AdminDataImportResult): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()
const { t } = useI18n()
const appStore = useAppStore()

const importing = ref(false)
const result = ref<AdminDataImportResult | null>(null)
const errorItems = computed(() => result.value?.errors ?? [])

watch(
  () => props.show,
  (show) => {
    if (show) result.value = null
  },
)

function handleClose(): void {
  if (importing.value) return
  emit('close')
}

async function importAccounts(): Promise<void> {
  if (importing.value || !props.payload || props.accountCount < 1) return
  importing.value = true
  try {
    const response = await adminAPI.accounts.importData({
      data: props.payload,
      skip_default_group_bind: true,
    })
    result.value = response
    emit('imported', response)

    const messageParams = {
      account_created: response.account_created,
      account_failed: response.account_failed,
      proxy_created: response.proxy_created,
      proxy_reused: response.proxy_reused,
      proxy_failed: response.proxy_failed,
    }
    if (response.account_failed > 0 || response.proxy_failed > 0) {
      appStore.showError(t('admin.accounts.dataImportCompletedWithErrors', messageParams))
    } else {
      appStore.showSuccess(t('admin.accounts.dataImportSuccess', messageParams))
    }
  } catch (error) {
    const message = (error as { message?: string })?.message
    appStore.showError(message || t('admin.accounts.dataImportFailed'))
  } finally {
    importing.value = false
  }
}
</script>

<template>
  <BaseDialog
    :show="show"
    :title="title"
    width="narrow"
    :close-on-click-outside="!pending"
    @close="requestCancel"
  >
    <div class="space-y-3">
      <p class="text-sm leading-6 text-gray-700 dark:text-gray-200">{{ message }}</p>
      <p v-if="detail" class="rounded-md border border-gray-200 bg-gray-50 px-3 py-2 text-xs leading-5 text-gray-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300">
        {{ detail }}
      </p>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="pending" @click="requestCancel">
          取消
        </button>
        <button
          type="button"
          data-testid="confirm-sms-receiver-action"
          class="btn"
          :class="danger ? 'border-red-600 bg-red-600 text-white hover:border-red-700 hover:bg-red-700' : 'btn-primary'"
          :disabled="pending"
          @click="$emit('confirm')"
        >
          <span v-if="pending" class="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
          <Icon v-else :name="danger ? 'x' : 'chat'" size="sm" />
          {{ pending ? pendingLabel : confirmLabel }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

interface Props {
  show: boolean
  title: string
  message: string
  detail?: string
  confirmLabel: string
  pendingLabel: string
  pending?: boolean
  danger?: boolean
}

interface Emits {
  (event: 'cancel'): void
  (event: 'confirm'): void
}

const props = withDefaults(defineProps<Props>(), {
  detail: '',
  pending: false,
  danger: false,
})
const emit = defineEmits<Emits>()

function requestCancel(): void {
  if (!props.pending) emit('cancel')
}
</script>

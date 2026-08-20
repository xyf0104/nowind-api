<template>
  <section
    class="rounded-lg border border-blue-300 bg-white/80 p-4 dark:border-blue-600 dark:bg-gray-800/80"
    aria-live="polite"
  >
    <div class="flex items-center justify-between gap-3">
      <div class="flex min-w-0 items-center gap-3">
        <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-blue-600 text-xs font-bold text-white">
          3
        </span>
        <div class="min-w-0">
          <h5 class="text-base font-medium text-blue-900 dark:text-blue-200">获取手机号</h5>
          <p class="truncate text-xs" :class="statusClass">{{ statusText }}</p>
        </div>
      </div>
      <button
        type="button"
        class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-blue-700 transition-colors hover:bg-blue-100 hover:text-blue-950 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 dark:text-blue-300 dark:hover:bg-blue-900/70 dark:hover:text-white"
        title="管理接码卡密"
        aria-label="管理接码卡密"
        @click="openKeyManager"
      >
        <Icon name="cog" size="sm" />
      </button>
    </div>

    <div v-if="needsManualStart" class="mt-3">
      <button
        type="button"
        class="btn btn-primary w-full !py-2.5 text-sm"
        data-testid="request-sms-phone"
        :disabled="!active || isStartingPhone || confirmationAction !== null"
        :title="active ? '领取用于当前授权的手机号' : '请先生成授权链接'"
        @click="requestPhoneConfirmation"
      >
        <svg v-if="isStartingPhone" class="mr-1.5 h-4 w-4 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
        </svg>
        <Icon v-else name="chat" size="sm" class="mr-1.5" />
        获取手机号
      </button>
    </div>

    <div v-else class="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1.2fr)_minmax(10rem,0.8fr)]">
      <div class="min-w-0 rounded-md border border-blue-200 bg-white/85 px-3 py-2.5 dark:border-blue-900/80 dark:bg-gray-900/70">
        <span class="block text-[11px] font-medium text-blue-700 dark:text-blue-300">手机号</span>
        <div class="mt-1 flex min-w-0 items-center gap-2">
          <span
            v-if="countryCallingCode"
            class="shrink-0 rounded border border-blue-200 bg-blue-50 px-1.5 py-0.5 font-mono text-xs font-semibold text-blue-800 dark:border-blue-800 dark:bg-blue-950/60 dark:text-blue-200"
          >
            +{{ countryCallingCode }}
          </span>
          <button
            type="button"
            class="group flex min-w-0 flex-1 items-center justify-between gap-2 text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
            :disabled="!phoneForCopy"
            :title="phoneForCopy ? '点击复制完整国际号码' : '等待获取手机号'"
            @click="copyPhone"
          >
            <span class="truncate font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{{ localPhoneNumber }}</span>
            <Icon
              :name="phoneCopied ? 'check' : 'copy'"
              size="sm"
              :class="phoneCopied ? 'shrink-0 text-emerald-600 dark:text-emerald-300' : 'shrink-0 text-blue-600 opacity-0 transition-opacity group-hover:opacity-100 dark:text-blue-300'"
            />
          </button>
        </div>
      </div>

      <div class="min-w-0 rounded-md border border-blue-200 bg-white/85 px-3 py-2.5 dark:border-blue-900/80 dark:bg-gray-900/70">
        <span class="block text-[11px] font-medium text-blue-700 dark:text-blue-300">地区</span>
        <span class="mt-1 flex min-w-0 items-center gap-1.5 text-sm font-medium text-gray-900 dark:text-gray-100">
          <span v-if="countryFlag" class="shrink-0 text-base leading-none" aria-hidden="true">{{ countryFlag }}</span>
          <span class="truncate">{{ region }}</span>
        </span>
      </div>

      <button
        type="button"
        class="group flex min-w-0 items-center justify-between gap-3 rounded-md border border-cyan-200 bg-white/85 px-3 py-2.5 text-left transition-colors hover:border-cyan-400 hover:bg-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500 dark:border-cyan-900/80 dark:bg-gray-900/70 dark:hover:border-cyan-600 dark:hover:bg-gray-900"
        :disabled="!code || code === '--'"
        :title="code && code !== '--' ? '点击复制验证码' : '正在等待验证码'"
        @click="copyCode"
      >
        <span>
          <span class="block text-[11px] font-medium text-cyan-700 dark:text-cyan-300">验证码</span>
          <span class="mt-0.5 block font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{{ code }}</span>
        </span>
        <Icon
          :name="codeCopied ? 'check' : 'copy'"
          size="sm"
          :class="codeCopied ? 'text-emerald-600 dark:text-emerald-300' : 'text-cyan-600 opacity-0 transition-opacity group-hover:opacity-100 dark:text-cyan-300'"
        />
      </button>
    </div>

    <div
      v-if="!needsManualStart && sessionExpiresAt"
      class="mt-2 flex items-center justify-between gap-3 rounded-md border border-blue-200 bg-blue-50/70 px-3 py-2 text-xs dark:border-blue-900/80 dark:bg-blue-950/30"
      data-testid="sms-session-countdown"
    >
      <span class="text-blue-700 dark:text-blue-300">号码有效期</span>
      <time :datetime="sessionExpiresAt" class="font-mono font-semibold tabular-nums text-gray-900 dark:text-gray-100">
        {{ sessionExpiryText }}
      </time>
    </div>

    <div v-if="!needsManualStart" class="mt-3 flex flex-wrap items-center gap-2">
      <button
        type="button"
        class="btn btn-secondary !px-2.5 !py-1.5 text-xs"
        :disabled="!canRefresh"
        @click="refresh"
      >
        <svg v-if="isRefreshing" class="mr-1 h-3.5 w-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
        </svg>
        <Icon v-else name="refresh" size="xs" class="mr-1" />
        刷新
      </button>
      <button
        type="button"
        class="btn btn-secondary !px-2.5 !py-1.5 text-xs"
        :disabled="!canChangeNumber"
        @click="changeNumber"
      >
        <svg v-if="isChangingNumber" class="mr-1 h-3.5 w-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
        </svg>
        <Icon v-else name="swap" size="xs" class="mr-1" />
        换号
      </button>
      <button
        type="button"
        class="btn btn-secondary !px-2.5 !py-1.5 text-xs text-red-600 hover:!border-red-300 hover:!bg-red-50 hover:!text-red-700 dark:text-red-300 dark:hover:!border-red-700 dark:hover:!bg-red-950/40"
        :disabled="!canCancel || confirmationAction !== null"
        @click="requestCancelConfirmation"
      >
        <svg v-if="isCancelling" class="mr-1 h-3.5 w-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
        </svg>
        <Icon v-else name="x" size="xs" class="mr-1" />
        取消
      </button>
      <span class="ml-auto text-xs text-gray-500 dark:text-gray-400">待用卡密 {{ queuedKeyCount }} 个</span>
    </div>

    <p v-if="!needsManualStart && phase === 'unavailable'" class="mt-2 text-xs text-amber-700 dark:text-amber-300">
      请点右上角齿轮添加接码卡密；卡密会加密保存到 XIASS API 服务器，收到验证码后才会自动清除。
    </p>
  </section>

  <BaseDialog
    :show="showKeyManager"
    title="接码卡密"
    width="narrow"
    @close="showKeyManager = false"
  >
    <div class="space-y-4">
      <p class="text-sm leading-6 text-gray-600 dark:text-gray-300">
        可一次粘贴多个卡密（换行、空格或逗号分隔）。卡密会加密保存到服务器；只有收到验证码后才会彻底清除，不会下发到浏览器。
      </p>
      <textarea
        v-model="cardKeysInput"
        rows="6"
        class="input w-full resize-y font-mono text-sm"
        placeholder="每行一个接码卡密"
        spellcheck="false"
      />
      <div class="rounded-md border border-gray-200 bg-gray-50 px-3 py-2 text-sm text-gray-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300">
        当前待用：<span class="font-semibold text-gray-900 dark:text-white">{{ queuedKeyCount }}</span> 个
      </div>
    </div>
    <template #footer>
      <div class="flex items-center justify-between gap-3">
        <button
          type="button"
          class="btn btn-secondary text-red-600 hover:!border-red-300 hover:!bg-red-50 hover:!text-red-700 dark:text-red-300 dark:hover:!border-red-700 dark:hover:!bg-red-950/40"
          :disabled="queuedKeyCount === 0 || isClearingKeys"
          @click="clearQueuedKeys"
        >
          {{ isClearingKeys ? '清空中…' : '清空待用' }}
        </button>
        <div class="flex gap-2">
          <button type="button" class="btn btn-secondary" @click="showKeyManager = false">关闭</button>
          <button type="button" class="btn btn-primary" :disabled="!cardKeysInput.trim() || isSavingKeys" @click="saveKeys">
            {{ isSavingKeys ? '保存中…' : '添加卡密' }}
          </button>
        </div>
      </div>
    </template>
  </BaseDialog>

  <SMSReceiverActionDialog
    :show="confirmationAction !== null"
    :title="confirmationAction === 'cancel' ? '取消当前号码' : '领取授权手机号'"
    :message="confirmationAction === 'cancel'
      ? '确认取消当前手机号并结束本次 OAuth 授权接码会话吗？'
      : '确认领取一个用于当前 OAuth 授权的手机号吗？'"
    :detail="confirmationAction === 'cancel'
      ? '尚未收到验证码时会释放当前号码和卡密；已经收到验证码时仍按实际结果处理。'
      : '领取成功后会立即开始监听验证码，可随时刷新、换号或在未收到验证码时取消。'"
    :confirm-label="confirmationAction === 'cancel' ? '确认取消号码' : '确认领取号码'"
    :pending-label="confirmationAction === 'cancel' ? '正在取消…' : '正在领取…'"
    :pending="confirmationPending"
    :danger="confirmationAction === 'cancel'"
    @cancel="closeConfirmation"
    @confirm="confirmReceiverAction"
  />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { usePixlabSMSReceiver } from '@/composables/usePixlabSMSReceiver'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import SMSReceiverActionDialog from '@/components/sms/SMSReceiverActionDialog.vue'

const props = withDefaults(defineProps<{
  active?: boolean
}>(), {
  active: false
})

const appStore = useAppStore()
const { copyToClipboard } = useClipboard()
const receiver = usePixlabSMSReceiver()
const {
  phase,
  phoneForCopy,
  countryCallingCode,
  localPhoneNumber,
  region,
  countryFlag,
  code,
  queuedKeyCount,
  statusText,
  statusClass,
  sessionExpiresAt,
  sessionExpiryText,
  canRefresh,
  canChangeNumber,
  canCancel,
  isRefreshing,
  isChangingNumber,
  isCancelling
} = receiver
const showKeyManager = ref(false)
const cardKeysInput = ref('')
const phoneCopied = ref(false)
const codeCopied = ref(false)
const isSavingKeys = ref(false)
const isClearingKeys = ref(false)
const hasManuallyStarted = ref(false)
const isStartingPhone = ref(false)
const confirmationAction = ref<'claim' | 'cancel' | null>(null)
const needsManualStart = computed(() => !hasManuallyStarted.value)
const confirmationPending = computed(() => confirmationAction.value === 'claim' ? isStartingPhone.value : isCancelling.value)

function showError(error: unknown): void {
  appStore.showError(error instanceof Error ? error.message : '接码服务暂时不可用，请稍后重试。')
}

async function begin(): Promise<boolean> {
  try {
    await receiver.start()
    return true
  } catch (error) {
    showError(error)
    return false
  }
}

async function requestPhone(): Promise<void> {
  if (!props.active || isStartingPhone.value) return
  isStartingPhone.value = true
  hasManuallyStarted.value = true
  const started = await begin()
  if (!started || ['expired', 'unavailable'].includes(receiver.phase.value)) {
    hasManuallyStarted.value = false
  }
  isStartingPhone.value = false
}

function requestPhoneConfirmation(): void {
  if (!props.active || isStartingPhone.value) return
  confirmationAction.value = 'claim'
}

async function refresh(): Promise<void> {
  try {
    await receiver.refresh()
  } catch (error) {
    showError(error)
  }
}

async function changeNumber(): Promise<void> {
  try {
    await receiver.changeNumber()
  } catch (error) {
    showError(error)
  }
}

async function cancel(): Promise<void> {
  try {
    const outcome = await receiver.cancel()
    if (outcome === 'received') {
      hasManuallyStarted.value = true
      appStore.showSuccess('验证码已到达，已为您保留。')
      return
    }
    hasManuallyStarted.value = false
    appStore.showInfo('已取消当前手机号。')
  } catch (error) {
    showError(error)
  }
}

function requestCancelConfirmation(): void {
  if (!canCancel.value || isCancelling.value) return
  confirmationAction.value = 'cancel'
}

function closeConfirmation(): void {
  if (!confirmationPending.value) confirmationAction.value = null
}

async function confirmReceiverAction(): Promise<void> {
  const action = confirmationAction.value
  if (!action || confirmationPending.value) return
  try {
    if (action === 'claim') await requestPhone()
    else await cancel()
  } finally {
    if (confirmationAction.value === action) confirmationAction.value = null
  }
}

async function copyPhone(): Promise<void> {
  if (await copyToClipboard(receiver.phoneForCopy.value, '手机号已复制')) {
    phoneCopied.value = true
    window.setTimeout(() => { phoneCopied.value = false }, 2_000)
  }
}

async function copyCode(): Promise<void> {
  if (await copyToClipboard(receiver.code.value, '验证码已复制')) {
    codeCopied.value = true
    window.setTimeout(() => { codeCopied.value = false }, 2_000)
  }
}

async function openKeyManager(): Promise<void> {
  showKeyManager.value = true
  try {
    await receiver.refreshQueueStatus()
  } catch (error) {
    showError(error)
  }
}

async function saveKeys(): Promise<void> {
  isSavingKeys.value = true
  try {
    const added = await receiver.appendCardKeys(cardKeysInput.value)
    cardKeysInput.value = ''
    if (added === 0) {
      appStore.showInfo('没有发现可添加的新卡密。')
      return
    }
    appStore.showSuccess(`已保存 ${added} 个接码卡密到服务器。`)
    showKeyManager.value = false
    if (props.active && receiver.phase.value === 'unavailable') hasManuallyStarted.value = false
  } catch (error) {
    showError(error)
  } finally {
    isSavingKeys.value = false
  }
}

async function clearQueuedKeys(): Promise<void> {
  isClearingKeys.value = true
  try {
    const deleted = await receiver.clearQueuedCardKeys()
    appStore.showInfo(deleted > 0 ? `已清空 ${deleted} 个待用接码卡密。` : '当前没有待用接码卡密。')
  } catch (error) {
    showError(error)
  } finally {
    isClearingKeys.value = false
  }
}

watch(
  () => props.active,
  (active) => {
    if (!active) {
      hasManuallyStarted.value = false
      confirmationAction.value = null
      receiver.stop()
      return
    }
    receiver.stop()
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  confirmationAction.value = null
  receiver.stop()
})
</script>

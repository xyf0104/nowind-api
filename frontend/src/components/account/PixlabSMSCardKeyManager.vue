<template>
  <section class="card overflow-hidden" data-testid="pixlab-sms-card-key-manager">
    <div class="border-b border-cyan-100 bg-cyan-50/60 px-6 py-4 dark:border-cyan-900/60 dark:bg-cyan-950/20">
      <div class="flex items-start gap-3">
        <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-cyan-600 text-white shadow-sm">
          <Icon name="chat" size="sm" :stroke-width="2" />
        </span>
        <div class="min-w-0">
          <h2 class="text-lg font-semibold text-cyan-950 dark:text-cyan-100">授权接码卡密</h2>
          <p class="mt-1 text-sm text-cyan-800/80 dark:text-cyan-200/80">
            卡密加密保存在 XIASS API 服务器。领取号码时暂时占用一张；只有收到验证码后才会彻底清除。
          </p>
        </div>
      </div>
    </div>

    <div class="space-y-4 p-6">
      <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_220px]">
        <div>
          <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-200">接码卡密</label>
          <textarea
            v-model="cardKeysInput"
            rows="5"
            class="input w-full resize-y font-mono text-sm"
            placeholder="每行一个卡密，也支持空格或逗号分隔"
            spellcheck="false"
          />
          <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
            已保存的卡密不会再次显示或下发至浏览器；重复卡密会自动忽略。
          </p>
        </div>

        <div class="rounded-lg border border-cyan-100 bg-cyan-50/50 p-4 dark:border-cyan-900/60 dark:bg-cyan-950/20">
          <p class="text-xs font-medium text-cyan-800 dark:text-cyan-200">服务器待用卡密</p>
          <p class="mt-1 font-mono text-3xl font-semibold text-cyan-950 dark:text-cyan-50">{{ queuedCount }}</p>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">进行中的号码 {{ activeCount }} 个</p>
          <button
            type="button"
            class="btn btn-secondary btn-sm mt-3 w-full"
            :disabled="loading"
            @click="loadStatus"
          >
            <Icon name="refresh" size="xs" class="mr-1" />
            刷新状态
          </button>
        </div>
      </div>

      <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 pt-4 dark:border-dark-700">
        <button
          type="button"
          class="btn btn-secondary text-red-600 hover:!border-red-300 hover:!bg-red-50 hover:!text-red-700 dark:text-red-300 dark:hover:!border-red-700 dark:hover:!bg-red-950/40"
          :disabled="queuedCount === 0 || clearing"
          @click="clearQueue"
        >
          {{ clearing ? '清空中…' : '清空待用卡密' }}
        </button>
        <button type="button" class="btn btn-primary" :disabled="!cardKeysInput.trim() || saving" @click="saveKeys">
          {{ saving ? '保存中…' : '加密保存卡密' }}
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import * as smsReceiverAPI from '@/api/admin/smsReceiver'
import { useAppStore } from '@/stores'
import Icon from '@/components/icons/Icon.vue'

const appStore = useAppStore()
const cardKeysInput = ref('')
const queuedCount = ref(0)
const activeCount = ref(0)
const loading = ref(false)
const saving = ref(false)
const clearing = ref(false)

function showError(error: unknown): void {
  const message = typeof error === 'object' && error !== null && 'message' in error
    ? String((error as { message?: unknown }).message || '读取接码卡密状态失败。')
    : '读取接码卡密状态失败。'
  appStore.showError(message)
}

async function loadStatus(): Promise<void> {
  loading.value = true
  try {
    const status = await smsReceiverAPI.getStatus()
    queuedCount.value = status.queued_count
    activeCount.value = status.active_count
  } catch (error) {
    showError(error)
  } finally {
    loading.value = false
  }
}

async function saveKeys(): Promise<void> {
  saving.value = true
  try {
    const result = await smsReceiverAPI.addCardKeys(cardKeysInput.value)
    cardKeysInput.value = ''
    queuedCount.value = result.queued_count
    if (result.added_count === 0) {
      appStore.showInfo('没有发现可添加的新卡密。')
    } else {
      appStore.showSuccess(`已加密保存 ${result.added_count} 个接码卡密。`)
    }
  } catch (error) {
    showError(error)
  } finally {
    saving.value = false
  }
}

async function clearQueue(): Promise<void> {
  clearing.value = true
  try {
    const result = await smsReceiverAPI.clearCardKeys()
    queuedCount.value = result.queued_count
    appStore.showInfo(result.deleted_count > 0 ? `已清空 ${result.deleted_count} 个待用接码卡密。` : '当前没有待用接码卡密。')
  } catch (error) {
    showError(error)
  } finally {
    clearing.value = false
  }
}

onMounted(() => { void loadStatus() })
</script>

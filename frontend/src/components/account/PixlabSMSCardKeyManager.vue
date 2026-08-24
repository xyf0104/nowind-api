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
            卡密加密保存于 XIASS API 服务器。本管理员设置页可直接查看原文、删除失效卡密并调整待用队列顺序。
          </p>
        </div>
      </div>
    </div>

    <div class="space-y-5 p-6">
      <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_220px]">
        <div>
          <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-200">添加接码卡密</label>
          <textarea
            v-model="cardKeysInput"
            rows="5"
            class="input w-full resize-y font-mono text-sm"
            placeholder="每行一个卡密，也支持空格或逗号分隔"
            spellcheck="false"
          />
          <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
            卡密始终加密存储；明文仅会返回到当前已登录的管理员系统设置页面，不会下发给会员或 OAuth 接码页面。重复卡密会自动忽略。
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
            @click="loadCardKeys(true)"
          >
            <Icon name="refresh" size="xs" class="mr-1" :class="{ 'animate-spin': loading }" />
            刷新列表
          </button>
        </div>
      </div>

      <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">待用卡密队列</h3>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">调度会优先均衡使用次数；相同次数时按这里的顺序领取。</p>
          </div>
          <span class="rounded-md bg-gray-100 px-2 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">
            共 {{ cardKeys.length }} 张
          </span>
        </div>

        <div v-if="loading && cardKeys.length === 0" class="flex items-center gap-2 py-6 text-sm text-gray-500 dark:text-gray-400">
          <Icon name="refresh" size="sm" class="animate-spin" />
          正在读取卡密队列…
        </div>
        <div v-else-if="cardKeys.length === 0" class="rounded-lg border border-dashed border-gray-200 px-4 py-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
          暂无已保存的接码卡密。
        </div>
        <div v-else class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
          <div class="min-w-[760px] divide-y divide-gray-100 dark:divide-dark-700">
            <div class="grid grid-cols-[minmax(250px,1fr)_84px_104px_116px_150px_104px] items-center gap-3 bg-gray-50 px-4 py-2 text-xs font-medium text-gray-500 dark:bg-dark-800 dark:text-gray-400">
              <span>卡密原文</span>
              <span>状态</span>
              <span>已使用</span>
              <span>队列顺序</span>
              <span>最后领取</span>
              <span class="text-right">操作</span>
            </div>
            <div
              v-for="cardKey in cardKeys"
              :key="cardKey.id"
              class="grid grid-cols-[minmax(250px,1fr)_84px_104px_116px_150px_104px] items-center gap-3 px-4 py-3"
            >
              <code class="min-w-0 select-all truncate rounded bg-gray-50 px-2 py-1.5 font-mono text-sm text-gray-900 dark:bg-dark-800 dark:text-gray-100" :title="cardKey.card_key">
                {{ cardKey.card_key }}
              </code>
              <span class="w-fit rounded-md px-2 py-1 text-xs font-medium" :class="statusClass(cardKey.status)">
                {{ statusLabel(cardKey.status) }}
              </span>
              <span class="font-mono text-sm text-gray-700 dark:text-gray-200">{{ cardKey.claim_count }} / 5</span>
              <span class="font-mono text-sm text-gray-600 dark:text-gray-300">{{ cardKey.status === 'queued' ? `#${queuedPosition(cardKey.id)}` : '-' }}</span>
              <span class="text-xs text-gray-500 dark:text-gray-400">{{ formatLastClaimedAt(cardKey.last_claimed_at) }}</span>
              <div class="flex items-center justify-end gap-1">
                <button
                  type="button"
                  class="flex h-8 w-8 items-center justify-center rounded-md text-gray-500 transition-colors hover:bg-cyan-50 hover:text-cyan-700 disabled:cursor-not-allowed disabled:opacity-35 dark:text-gray-400 dark:hover:bg-cyan-950/40 dark:hover:text-cyan-200"
                  title="上移待用卡密"
                  aria-label="上移待用卡密"
                  :disabled="!canMoveCardKey(cardKey.id, -1) || reordering"
                  @click="moveCardKey(cardKey.id, -1)"
                >
                  <Icon name="arrowUp" size="sm" />
                </button>
                <button
                  type="button"
                  class="flex h-8 w-8 items-center justify-center rounded-md text-gray-500 transition-colors hover:bg-cyan-50 hover:text-cyan-700 disabled:cursor-not-allowed disabled:opacity-35 dark:text-gray-400 dark:hover:bg-cyan-950/40 dark:hover:text-cyan-200"
                  title="下移待用卡密"
                  aria-label="下移待用卡密"
                  :disabled="!canMoveCardKey(cardKey.id, 1) || reordering"
                  @click="moveCardKey(cardKey.id, 1)"
                >
                  <Icon name="arrowDown" size="sm" />
                </button>
                <button
                  type="button"
                  class="flex h-8 w-8 items-center justify-center rounded-md text-red-500 transition-colors hover:bg-red-50 hover:text-red-700 disabled:cursor-not-allowed disabled:opacity-35 dark:text-red-300 dark:hover:bg-red-950/40 dark:hover:text-red-200"
                  :title="cardKey.status === 'active' ? '删除使用中的卡密并终止该本地接码会话' : '删除此卡密'"
                  aria-label="删除此卡密"
                  :disabled="deletingCardKeyID === cardKey.id"
                  @click="deleteCardKey(cardKey)"
                >
                  <Icon name="trash" size="sm" />
                </button>
              </div>
            </div>
          </div>
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
import { computed, onMounted, ref } from 'vue'
import * as smsReceiverAPI from '@/api/admin/smsReceiver'
import { useAppStore } from '@/stores'
import Icon from '@/components/icons/Icon.vue'

const appStore = useAppStore()
const cardKeysInput = ref('')
const cardKeys = ref<smsReceiverAPI.SMSReceiverCardKey[]>([])
const queuedCount = ref(0)
const activeCount = ref(0)
const loading = ref(false)
const saving = ref(false)
const clearing = ref(false)
const reordering = ref(false)
const deletingCardKeyID = ref<number | null>(null)

const queuedCardKeys = computed(() => cardKeys.value.filter((cardKey) => cardKey.status === 'queued'))

function describeError(error: unknown, fallback: string): string {
  if (typeof error !== 'object' || error === null) return fallback
  const candidate = error as {
    message?: unknown
    response?: { data?: { message?: unknown } }
  }
  const serverMessage = candidate.response?.data?.message
  if (typeof serverMessage === 'string' && serverMessage.trim()) return serverMessage
  if (typeof candidate.message === 'string' && candidate.message.trim()) return candidate.message
  return fallback
}

function showError(error: unknown, fallback = '读取接码卡密失败。'): void {
  appStore.showError(describeError(error, fallback))
}

function statusLabel(status: string): string {
  if (status === 'queued') return '待用'
  if (status === 'active') return '使用中'
  if (status === 'exhausted') return '已耗尽'
  return status || '未知'
}

function statusClass(status: string): string {
  if (status === 'queued') return 'bg-cyan-50 text-cyan-700 dark:bg-cyan-950/40 dark:text-cyan-200'
  if (status === 'active') return 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-200'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
}

function formatLastClaimedAt(value?: string): string {
  if (!value) return '未使用'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '未使用'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false
  }).format(date)
}

function queuedPosition(cardKeyID: number): number {
  return queuedCardKeys.value.findIndex((cardKey) => cardKey.id === cardKeyID) + 1
}

function canMoveCardKey(cardKeyID: number, direction: -1 | 1): boolean {
  const index = queuedCardKeys.value.findIndex((cardKey) => cardKey.id === cardKeyID)
  return index >= 0 && index + direction >= 0 && index + direction < queuedCardKeys.value.length
}

function updateStatus(status: smsReceiverAPI.SMSReceiverQueueStatus): void {
  queuedCount.value = Number.isFinite(status?.queued_count) ? status.queued_count : 0
  activeCount.value = Number.isFinite(status?.active_count) ? status.active_count : 0
}

async function loadCardKeys(showFeedback = false): Promise<void> {
  loading.value = true
  try {
    const result = await smsReceiverAPI.listCardKeys()
    cardKeys.value = Array.isArray(result?.card_keys) ? result.card_keys : []
    updateStatus(result)
    if (showFeedback) appStore.showSuccess('接码卡密队列已刷新。')
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
    await loadCardKeys()
    if (result.added_count === 0) {
      appStore.showInfo('没有发现可添加的新卡密。')
    } else {
      appStore.showSuccess(`已加密保存 ${result.added_count} 个接码卡密。`)
    }
  } catch (error) {
    showError(error, '保存接码卡密失败。')
  } finally {
    saving.value = false
  }
}

async function clearQueue(): Promise<void> {
  if (!window.confirm('确定要清空全部待用卡密吗？进行中的接码会话不会被影响。')) return
  clearing.value = true
  try {
    const result = await smsReceiverAPI.clearCardKeys()
    queuedCount.value = result.queued_count
    await loadCardKeys()
    appStore.showInfo(result.deleted_count > 0 ? `已清空 ${result.deleted_count} 个待用接码卡密。` : '当前没有待用接码卡密。')
  } catch (error) {
    showError(error, '清空待用接码卡密失败。')
  } finally {
    clearing.value = false
  }
}

async function deleteCardKey(cardKey: smsReceiverAPI.SMSReceiverCardKey): Promise<void> {
  const warning = cardKey.status === 'active'
    ? '此卡密正在使用。删除后会终止该本地接码会话，确定继续吗？'
    : `确定删除卡密 ${cardKey.card_key} 吗？`
  if (!window.confirm(warning)) return

  deletingCardKeyID.value = cardKey.id
  try {
    const status = await smsReceiverAPI.deleteCardKey(cardKey.id)
    updateStatus(status)
    await loadCardKeys()
    appStore.showSuccess('接码卡密已删除。')
  } catch (error) {
    showError(error, '删除接码卡密失败。')
  } finally {
    deletingCardKeyID.value = null
  }
}

async function moveCardKey(cardKeyID: number, direction: -1 | 1): Promise<void> {
  const orderedQueued = [...queuedCardKeys.value]
  const index = orderedQueued.findIndex((cardKey) => cardKey.id === cardKeyID)
  const nextIndex = index + direction
  if (index < 0 || nextIndex < 0 || nextIndex >= orderedQueued.length) return

  const current = orderedQueued[index]
  orderedQueued[index] = orderedQueued[nextIndex]
  orderedQueued[nextIndex] = current
  let queuedIndex = 0
  cardKeys.value = cardKeys.value.map((cardKey) => (
    cardKey.status === 'queued' ? orderedQueued[queuedIndex++] : cardKey
  ))

  reordering.value = true
  try {
    const status = await smsReceiverAPI.reorderCardKeys(orderedQueued.map((cardKey) => cardKey.id))
    updateStatus(status)
    await loadCardKeys()
    appStore.showSuccess('待用卡密顺序已更新。')
  } catch (error) {
    showError(error, '调整待用卡密顺序失败。')
    await loadCardKeys()
  } finally {
    reordering.value = false
  }
}

onMounted(() => { void loadCardKeys() })
</script>

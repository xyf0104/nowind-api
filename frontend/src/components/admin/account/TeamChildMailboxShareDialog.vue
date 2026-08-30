<template>
  <BaseDialog
    :show="show"
    title="接码链接"
    width="normal"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <div class="rounded-2xl border border-gray-200 bg-gray-50 px-4 py-3.5 dark:border-dark-600 dark:bg-dark-900/45">
        <div class="text-xs font-medium text-gray-500 dark:text-gray-400">接收邮箱</div>
        <code class="mt-1 block truncate font-mono text-sm font-semibold text-gray-900 dark:text-gray-100" :title="email">{{ email }}</code>
      </div>

      <div v-if="loading" class="flex min-h-20 items-center justify-center gap-2 text-sm text-gray-500 dark:text-gray-400">
        <Icon name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
        <span>正在读取链接状态</span>
      </div>

      <template v-else>
        <button
          v-if="shareLink"
          type="button"
          class="group w-full rounded-2xl border border-primary-200 bg-primary-50 p-3.5 text-left transition-colors hover:bg-primary-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-primary-900/70 dark:bg-primary-950/25 dark:hover:bg-primary-950/40"
          title="点击复制验证码网页链接"
          data-testid="team-mailbox-share-link"
          @click="copyShareLink"
        >
          <div class="flex min-w-0 items-center gap-2">
            <Icon :name="copied ? 'check' : 'copy'" size="sm" class="shrink-0 text-primary-700 dark:text-primary-300" :stroke-width="2" />
            <span class="text-sm font-semibold text-primary-900 dark:text-primary-100">{{ copied ? '已复制链接' : '接码链接' }}</span>
          </div>
          <code class="mt-2 block truncate font-mono text-xs leading-5 text-primary-800 dark:text-primary-200">{{ shareLink }}</code>
        </button>

        <div v-else-if="shareStatus?.active" class="rounded-2xl border border-green-200 bg-green-50 px-4 py-3.5 dark:border-green-900/60 dark:bg-green-950/20">
          <div class="flex items-center gap-2 text-sm font-semibold text-green-800 dark:text-green-200">
            <Icon name="check" size="sm" :stroke-width="2.5" />
            <span>接码链接已启用</span>
          </div>
          <p class="mt-1.5 text-xs leading-5 text-green-700 dark:text-green-300">这是历史链接，仍可正常使用。替换后可随时重新打开此处复制新链接。</p>
        </div>

        <div v-else class="rounded-2xl border border-gray-200 bg-white px-4 py-3.5 text-sm text-gray-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300">
          暂未生成接码链接。
        </div>
      </template>

      <div v-if="confirmationAction" class="rounded-2xl border p-3.5" :class="confirmationAction === 'revoke' ? 'border-red-200 bg-red-50 dark:border-red-900/60 dark:bg-red-950/20' : 'border-amber-200 bg-amber-50 dark:border-amber-900/60 dark:bg-amber-950/20'">
        <p class="text-sm font-medium" :class="confirmationAction === 'revoke' ? 'text-red-800 dark:text-red-200' : 'text-amber-800 dark:text-amber-200'">
          {{ confirmationAction === 'revoke' ? '撤销后当前链接将立即失效。' : shareStatus?.active ? '替换后原链接将立即失效。' : '将生成一条长期可用的独立收件箱链接。' }}
        </p>
        <div class="mt-3 flex flex-wrap justify-end gap-2">
          <button type="button" class="btn btn-secondary whitespace-nowrap" :disabled="submitting" @click="confirmationAction = null">取消</button>
          <button
            type="button"
            class="btn whitespace-nowrap"
            :class="confirmationAction === 'revoke' ? 'bg-red-600 text-white hover:bg-red-700' : 'btn-primary'"
            :disabled="submitting"
            @click="confirmAction"
          >
            <Icon v-if="submitting" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
            <span>{{ submitting ? '处理中' : confirmationAction === 'revoke' ? '确认撤销' : confirmationAction === 'replace' ? '确认替换' : '确认生成' }}</span>
          </button>
        </div>
      </div>

      <p v-if="errorMessage" class="text-sm leading-5 text-red-600 dark:text-red-300" role="alert">{{ errorMessage }}</p>
    </div>

    <template #footer>
      <div class="flex min-w-0 flex-wrap justify-end gap-2">
        <button type="button" class="btn btn-secondary whitespace-nowrap" :disabled="submitting" @click="emit('close')">关闭</button>
        <template v-if="!confirmationAction">
          <button
            v-if="shareStatus?.active"
            type="button"
            class="btn btn-secondary flex items-center gap-1.5 whitespace-nowrap"
            :disabled="loading || submitting"
            @click="confirmationAction = 'replace'"
          >
            <Icon name="refresh" size="sm" :stroke-width="2" />
            <span>替换链接</span>
          </button>
          <button
            v-if="shareStatus?.active"
            type="button"
            class="btn flex items-center gap-1.5 whitespace-nowrap bg-red-600 text-white hover:bg-red-700"
            :disabled="loading || submitting"
            @click="confirmationAction = 'revoke'"
          >
            <Icon name="trash" size="sm" :stroke-width="2" />
            <span>撤销链接</span>
          </button>
          <button
            v-else
            type="button"
            class="btn btn-primary flex items-center gap-1.5 whitespace-nowrap"
            :disabled="loading || submitting || !hasTarget"
            @click="confirmationAction = 'create'"
          >
            <Icon name="link" size="sm" :stroke-width="2" />
            <span>生成链接</span>
          </button>
        </template>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Icon } from '@/components/icons'
import BaseDialog from '@/components/common/BaseDialog.vue'
import {
  createPendingTeamChildMailboxShare,
  createTeamChildMailboxShare,
  getPendingTeamChildMailboxShare,
  getTeamChildMailboxShare,
  revokePendingTeamChildMailboxShare,
  revokeTeamChildMailboxShare,
  type TeamChildMailboxShareStatus,
} from '@/api/admin/teamChild'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = withDefaults(defineProps<{
  show: boolean
  accountId: number | null
  email: string
  scope?: 'account' | 'mailbox'
}>(), {
  accountId: null,
  email: '',
  scope: 'account',
})

const emit = defineEmits<{
  close: []
}>()

const loading = ref(false)
const submitting = ref(false)
const copied = ref(false)
const shareLink = ref('')
const shareStatus = ref<TeamChildMailboxShareStatus | null>(null)
const errorMessage = ref('')
const confirmationAction = ref<'create' | 'replace' | 'revoke' | null>(null)
let latestLoad = 0

const normalizedAccountID = computed(() => {
  const value = Number(props.accountId)
  return Number.isSafeInteger(value) && value > 0 ? value : null
})
const normalizedEmail = computed(() => props.email.trim())
const hasTarget = computed(() => props.scope === 'account' ? Boolean(normalizedAccountID.value) : Boolean(normalizedEmail.value))

watch(
  () => [props.show, props.scope, normalizedAccountID.value, normalizedEmail.value] as const,
  ([show, scope, accountID, email]) => {
    const requestID = ++latestLoad
    shareLink.value = ''
    copied.value = false
    errorMessage.value = ''
    confirmationAction.value = null
    shareStatus.value = null
    if (!show || (scope === 'account' && !accountID) || (scope === 'mailbox' && !email)) {
      loading.value = false
      return
    }
    void loadStatus(requestID)
  },
  { immediate: true }
)

async function loadStatus(requestID = ++latestLoad): Promise<void> {
  const accountID = normalizedAccountID.value
  const email = normalizedEmail.value
  if ((props.scope === 'account' && !accountID) || (props.scope === 'mailbox' && !email)) return
  loading.value = true
  try {
    const result = props.scope === 'account'
      ? await getTeamChildMailboxShare(accountID!)
      : await getPendingTeamChildMailboxShare(email, accountID || undefined)
    if (requestID !== latestLoad) return
    shareStatus.value = result
    shareLink.value = result.token ? buildShareLink(result.token) : ''
  } catch (error) {
    if (requestID !== latestLoad) return
    errorMessage.value = extractApiErrorMessage(error, '无法读取验证码网页链接状态')
  } finally {
    if (requestID === latestLoad) loading.value = false
  }
}

async function confirmAction(): Promise<void> {
  const action = confirmationAction.value
  const accountID = normalizedAccountID.value
  const email = normalizedEmail.value
  if (!action || submitting.value || (props.scope === 'account' && !accountID) || (props.scope === 'mailbox' && !email)) return
  submitting.value = true
  errorMessage.value = ''
  try {
    if (action === 'revoke') {
      if (props.scope === 'account') {
        await revokeTeamChildMailboxShare(accountID!)
      } else {
        await revokePendingTeamChildMailboxShare(email, accountID || undefined)
      }
      shareStatus.value = { active: false, email: props.email }
      shareLink.value = ''
      copied.value = false
      return
    }
    const result = props.scope === 'account'
      ? await createTeamChildMailboxShare(accountID!, action === 'replace')
      : await createPendingTeamChildMailboxShare(email, action === 'replace', accountID || undefined)
    shareStatus.value = result
    shareLink.value = buildShareLink(result.token)
    copied.value = false
  } catch (error) {
    errorMessage.value = extractApiErrorMessage(error, action === 'replace' ? '无法替换验证码网页链接' : '无法生成验证码网页链接')
  } finally {
    confirmationAction.value = null
    submitting.value = false
  }
}

function buildShareLink(token: string): string {
  const url = new URL('/team-mail', window.location.origin)
  url.hash = `t=${encodeURIComponent(token)}`
  return url.toString()
}

async function copyShareLink(): Promise<void> {
  if (!shareLink.value) return
  try {
    await copyText(shareLink.value)
    copied.value = true
  } catch {
    errorMessage.value = '复制失败，请长按或选择链接后复制'
  }
}

async function copyText(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = value
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  const copied = document.execCommand('copy')
  textarea.remove()
  if (!copied) throw new Error('clipboard unavailable')
}
</script>

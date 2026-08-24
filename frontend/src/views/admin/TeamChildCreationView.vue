<template>
  <div class="mx-auto max-w-[1600px] space-y-6 p-4 md:p-6">
    <header class="flex flex-col gap-3 border-b border-gray-200 pb-5 dark:border-dark-700 sm:flex-row sm:items-end sm:justify-between">
      <div>
        <div class="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-primary-600 dark:text-primary-400">
          <Icon name="users" size="sm" :stroke-width="2" />
          <span>管理员工作流</span>
        </div>
        <h1 class="text-2xl font-semibold text-gray-900 dark:text-gray-100">Team 子号创建</h1>
        <p class="mt-1 max-w-2xl text-sm text-gray-500 dark:text-gray-400">
          一次处理一个账号。邮箱由 XIASS 服务端托管，流程状态、接码动作和回调导入集中在当前工作区。
        </p>
      </div>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <div class="flex items-center gap-2 text-sm" :class="statusTone.text">
          <Icon :name="statusTone.icon" size="sm" :class="status === 'creating' || status === 'polling' ? 'animate-spin' : ''" :stroke-width="2" />
          <span>{{ statusLabel }}</span>
        </div>
        <input ref="mailboxConfigFileInput" type="file" class="hidden" accept=".json,.env,.txt,application/json,text/plain" @change="handleMailboxConfigFileChange" />
        <button type="button" class="btn btn-secondary flex items-center gap-2 whitespace-nowrap" :disabled="configImporting" @click="mailboxConfigFileInput?.click()">
          <Icon v-if="configImporting" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
          <Icon v-else name="upload" size="sm" :stroke-width="2" />
          <span>{{ configImporting ? '正在导入...' : '导入邮箱配置' }}</span>
        </button>
      </div>
    </header>

    <div v-if="errorMessage" class="flex items-start gap-3 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/20 dark:text-red-300">
      <Icon name="exclamationTriangle" size="sm" class="mt-0.5 flex-shrink-0" :stroke-width="2" />
      <span>{{ errorMessage }}</span>
    </div>

    <div class="grid gap-6 xl:grid-cols-[minmax(280px,0.42fr)_minmax(0,1.58fr)]">
      <div class="space-y-5">
        <section class="space-y-5">
        <div class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <div class="mb-5 flex items-start justify-between gap-4">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">流程状态</h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">先获取临时邮箱，再选择可替换席位；若普通席位已人工腾出，系统会实时确认后直接邀请。</p>
            </div>
            <button v-if="isStarted" type="button" class="btn btn-secondary flex items-center gap-2 whitespace-nowrap" :disabled="busy" @click="resetFlow">
              <Icon name="refresh" size="sm" :stroke-width="2" />
              <span>重新开始</span>
            </button>
          </div>

          <ol class="space-y-4">
            <li v-for="(item, index) in steps" :key="item.key" class="flex gap-3">
              <div class="flex flex-col items-center">
                <div class="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full border text-xs font-semibold" :class="stepClass(item.key)">
                  <Icon v-if="isStepComplete(item.key)" name="check" size="sm" :stroke-width="2.5" />
                  <span v-else>{{ index + 1 }}</span>
                </div>
                <div v-if="index < steps.length - 1" class="mt-1 h-full min-h-6 w-px bg-gray-200 dark:bg-dark-600"></div>
              </div>
              <div class="min-w-0 flex-1 pb-3">
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <div class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ item.label }}</div>
                  <button
                    v-if="item.key !== 'mailbox' && isStarted"
                    type="button"
                    :data-testid="'team-step-' + item.key"
                    class="btn btn-secondary flex min-w-[5.75rem] items-center justify-center gap-1.5 whitespace-nowrap px-2.5 py-1.5 text-xs"
                    :disabled="!canRunTopLevelStep(item.key)"
                    @click="runTopLevelStep(item.key)"
                  >
                    <Icon :name="isTopLevelStepRunning(item.key) ? 'refresh' : isStepComplete(item.key) ? 'check' : 'play'" size="sm" :class="isTopLevelStepRunning(item.key) ? 'animate-spin' : ''" :stroke-width="2" />
                    <span>{{ isTopLevelStepRunning(item.key) ? '执行中' : isStepComplete(item.key) ? '已完成' : '执行此步' }}</span>
                  </button>
                </div>
                <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ item.description }}</div>
              </div>
            </li>
          </ol>

          <div class="mt-3 rounded-lg border border-dashed border-gray-300 bg-gray-50 p-3 dark:border-dark-600 dark:bg-dark-900/40">
            <div class="flex items-center justify-between gap-3">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">当前邮箱</span>
              <span v-if="mailbox?.expires_at" class="text-xs text-gray-400 dark:text-gray-500">有效期至 {{ formatTime(mailbox.expires_at) }}</span>
            </div>
            <div class="mt-1 flex flex-wrap items-center gap-2">
              <code v-if="mailbox?.email" class="break-all text-sm font-semibold text-gray-900 dark:text-gray-100">{{ mailbox.email }}</code>
              <span v-else class="text-sm text-gray-400 dark:text-gray-500">点击“获取临时邮箱”后生成</span>
              <button v-if="mailbox?.email" type="button" class="btn btn-secondary p-2" title="复制邮箱" @click="copyText(mailbox.email)"><Icon name="copy" size="sm" :stroke-width="2" /></button>
            </div>
            <div class="mt-3 flex items-end gap-2">
              <div class="min-w-0 flex-1">
                <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">邮箱验证码</label>
                <input :value="mailboxCode || ''" readonly class="input h-9 w-44 max-w-full cursor-copy font-mono text-sm tracking-[0.16em]" :class="mailboxCode ? 'text-center' : ''" :title="mailboxCode ? '点击验证码复制' : undefined" :aria-label="mailboxCode ? '邮箱验证码，点击复制' : '邮箱验证码'" :placeholder="mailboxCodeLoading ? '正在检查...' : '等待邮件到达'" @click="mailboxCode && copyText(mailboxCode)" />
              </div>
              <button type="button" class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0" title="立即检查邮箱" aria-label="立即检查邮箱" :disabled="!mailbox || mailboxCodeLoading || Boolean(mailboxCode)" @click="pollMailbox"><Icon name="refresh" size="sm" :class="mailboxCodeLoading ? 'animate-spin' : ''" :stroke-width="2" /></button>
            </div>
            <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">{{ mailboxCodeHint }}</p>
          </div>

          <button v-if="!isStarted" type="button" class="btn btn-primary mt-5 flex w-full items-center justify-center gap-2 whitespace-nowrap" :disabled="busy || !mailboxConfigured" @click="startFlow">
            <Icon v-if="busy" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
            <Icon v-else name="play" size="sm" :stroke-width="2" />
            <span>{{ mailboxConfigured ? '获取临时邮箱' : '邮箱服务未配置' }}</span>
          </button>
        </div>

      </section>

        <section v-if="teamWorkflow || callbackURL || createdAccount" class="space-y-5">
          <TeamChildOAuthWorkspace
            v-if="oauthWorkspaceVisible && teamWorkflow"
            :workflow="teamWorkflow"
            :mailbox-email="mailbox?.email || ''"
            :auth-url="authUrl || ''"
            :callback-url="callbackURL"
            @session-cancelled="restartWorkflowOAuth"
            @copy-auth="copyText(authUrl || '')"
            @copy-callback="copyText(callbackURL)"
          />

          <div v-if="teamWorkflow || callbackURL || createdAccount" class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
            <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">导入 XIASS</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">完成官方授权后，把地址栏中的完整回调 URL 粘贴到这里；工作流只在内存中校验 code/state，确认无误后再导入。</p>
            <label class="mt-4 mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">完整回调 URL</label>
            <textarea v-model="callbackURL" rows="3" class="input w-full resize-none font-mono text-xs" placeholder="https://.../callback?code=...&state=..."></textarea>
            <p v-if="parsedCallbackError" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ parsedCallbackError }}</p>
            <button
              v-if="teamWorkflow && teamWorkflow.status !== 'callback_ready'"
              type="button"
              class="btn btn-secondary mt-3 flex items-center gap-2 whitespace-nowrap"
              :disabled="!canConfirmCallback || callbackConfirming"
              @click="confirmWorkflowCallback"
            >
              <Icon v-if="callbackConfirming" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
              <Icon v-else name="check" size="sm" :stroke-width="2" />
              <span>{{ callbackConfirming ? '正在校验回调...' : '确认回调状态' }}</span>
            </button>
            <div class="mt-4"><label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">账号名称</label><input v-model="accountName" class="input w-full" :placeholder="mailbox?.email || 'OpenAI OAuth Account'" /></div>
            <div class="mt-4"><GroupSelector v-model="selectedGroupIDs" :groups="groups" platform="openai" /></div>
            <div class="mt-4 grid gap-4 sm:grid-cols-2"><div><label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">并发数</label><input value="10" readonly class="input w-full bg-gray-50 dark:bg-dark-900" /></div><div><label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">优先级</label><input value="1" readonly class="input w-full bg-gray-50 dark:bg-dark-900" /></div></div>
            <button v-if="!createdAccount" type="button" class="btn btn-primary mt-5 flex w-full items-center justify-center gap-2 whitespace-nowrap" :disabled="!canImport || importing" @click="importAccount"><Icon v-if="importing" name="refresh" size="sm" class="animate-spin" :stroke-width="2" /><Icon v-else name="upload" size="sm" :stroke-width="2" /><span>{{ importing ? '正在导入...' : '校验并导入' }}</span></button>
            <div v-else class="mt-5 flex items-start gap-3 rounded-md border border-green-200 bg-green-50 p-4 dark:border-green-900/60 dark:bg-green-950/20"><Icon name="check" size="md" class="mt-0.5 text-green-600 dark:text-green-400" :stroke-width="2.5" /><div class="min-w-0"><div class="font-semibold text-green-800 dark:text-green-200">已导入 XIASS</div><div class="mt-1 break-all text-sm text-green-700 dark:text-green-300">{{ createdAccount.name || mailbox?.email }}</div><router-link to="/admin/accounts" class="mt-3 inline-flex items-center gap-1 text-sm font-medium text-green-700 hover:underline dark:text-green-300">前往账号管理 <Icon name="arrowRight" size="sm" :stroke-width="2" /></router-link></div></div>
          </div>
        </section>
      </div>

      <TeamChildMembersWorkspace
        v-if="!browserVisible"
        :members="members"
        :pending-invites="pendingInvites"
        :loading="membersLoading"
        :error="membersError"
        :ready="membersReady"
        :seat-email="seatEmail"
        :workspace-name="workspaceName"
        :invitation-email="mailbox?.email || ''"
        :selected-email="selectedMemberEmail"
        :seat-already-removed="manualSeatReady"
        :workflow-ready="workflowReady"
        :workflow-busy="workflowBusy"
        :workflow="teamWorkflow"
        :workflow-continuing="workflowContinuing"
        :browser-configured="browserConfigured"
        :browser-loading="browserLoading"
        @refresh="refreshMembers"
        @inspect="inspectSeat"
        @select="selectedMemberEmail = $event"
        @invite="inviteMember"
        @edit="editMember"
        @remove="removeMember"
        @open-browser="openBrowserWorkspace"
        @start-workflow="openWorkflowConfirmation"
        @continue-workflow="continueWorkflow"
        @run-step="requestWorkflowStep"
      />
      <TeamChildBrowserWorkspace
        v-else
        :configured="browserConfigured"
        :embed-url="browserEmbedURL"
        :loading="browserLoading"
        :error="browserError"
        :mailbox-email="mailbox?.email || ''"
        :members-ready="membersReady"
        :control-conflict="browserControlConflict"
        @reload="reloadBrowserWorkspace"
        @copy-mailbox="copyMailboxEmail"
        @open-modular="closeBrowserWorkspace"
        @force-take-over="browserTakeOverConfirmOpen = true"
      />
    </div>
    <ConfirmDialog
      :show="workflowConfirmOpen"
      :title="workflowConfirmationTitle"
      :message="workflowConfirmationMessage"
      :confirm-text="workflowStartStep === 'oauth' ? '准备授权' : manualSeatReady ? '邀请并授权' : '移除并邀请'"
      cancel-text="取消"
      :danger="workflowStartStep !== 'oauth'"
      @confirm="startConfirmedWorkflow"
      @cancel="workflowConfirmOpen = false"
    />
    <ConfirmDialog
      :show="workflowStepConfirmOpen"
      :title="workflowStepConfirmationTitle"
      :message="workflowStepConfirmationMessage"
      :confirm-text="workflowStepToRun === 'remove' ? '确认移除' : workflowStepToRun === 'invite' ? '确认邀请' : '继续执行'"
      cancel-text="取消"
      :danger="workflowStepToRun === 'remove' || workflowStepToRun === 'invite'"
      @confirm="confirmWorkflowStep"
      @cancel="cancelWorkflowStep"
    />
    <ConfirmDialog
      :show="browserTakeOverConfirmOpen"
      title="接管服务器浏览器"
      message="另一台设备可能正在查看同一个服务器浏览器。接管会结束对方的图形浏览器控制，但不会影响已登录状态或正在运行的自动化工作流。"
      confirm-text="确认接管"
      cancel-text="取消"
      @confirm="confirmBrowserTakeOver"
      @cancel="browserTakeOverConfirmOpen = false"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Icon } from '@/components/icons'
import TeamChildBrowserWorkspace from '@/components/admin/account/TeamChildBrowserWorkspace.vue'
import TeamChildOAuthWorkspace from '@/components/admin/account/TeamChildOAuthWorkspace.vue'
import TeamChildMembersWorkspace from '@/components/admin/account/TeamChildMembersWorkspace.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import { useOpenAIOAuth } from '@/composables/useOpenAIOAuth'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import { teamChildAPI, type TeamChildMailbox, type TeamChildMember, type TeamChildWorkflow, type TeamChildWorkflowStepKey } from '@/api/admin/teamChild'
import { groupsAPI } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'

type FlowStatus = 'idle' | 'creating' | 'ready' | 'workflow' | 'waiting' | 'polling' | 'received' | 'callback' | 'importing' | 'completed' | 'error'
type StepKey = 'mailbox' | 'replace' | 'verify' | 'import'

const appStore = useAppStore()
const openaiOAuth = useOpenAIOAuth()
const status = ref<FlowStatus>('idle')
const mailboxConfigured = ref(false)
const browserConfigured = ref(false)
const browserEmbedURL = ref('')
const browserLoading = ref(false)
const browserError = ref('')
const browserVisible = ref(false)
const browserControllerID = ref('')
const browserControlConflict = ref(false)
const browserTakeOverConfirmOpen = ref(false)
const membersReady = ref(false)
const membersLoading = ref(false)
const membersError = ref('')
const members = ref<TeamChildMember[]>([])
const pendingInvites = ref(0)
const seatEmail = ref('')
const selectedMemberEmail = ref('')
const workspaceName = ref('')
const configImporting = ref(false)
const mailboxConfigFileInput = ref<HTMLInputElement | null>(null)
const mailbox = ref<TeamChildMailbox | null>(null)
const mailboxCode = ref('')
const mailboxCodeLoading = ref(false)
const mailboxCodeError = ref('')
const authUrl = openaiOAuth.authUrl
const oauthState = openaiOAuth.oauthState
const oauthSessionID = openaiOAuth.sessionId
const oauthGenerating = openaiOAuth.loading
const oauthError = openaiOAuth.error
const callbackURL = ref('')
const accountName = ref('')
const groups = ref<AdminGroup[]>([])
const selectedGroupIDs = ref<number[]>([])
const errorMessage = ref('')
const createdAccount = ref<{ id?: number; name?: string; [key: string]: unknown } | null>(null)
const teamWorkflow = ref<TeamChildWorkflow | null>(null)
const workflowConfirmOpen = ref(false)
const workflowStartStep = ref<TeamChildWorkflowStepKey>('members')
const workflowRunOnlyStep = ref(false)
const workflowStepConfirmOpen = ref(false)
const workflowStepToRun = ref<TeamChildWorkflowStepKey | null>(null)
const workflowStepRunning = ref(false)
const workflowStarting = ref(false)
const workflowContinuing = ref(false)
const callbackConfirming = ref(false)
let pollTimer: ReturnType<typeof setTimeout> | null = null
let workflowPollTimer: ReturnType<typeof setTimeout> | null = null
let browserHeartbeatTimer: ReturnType<typeof setTimeout> | null = null

const steps: Array<{ key: StepKey; label: string; description: string }> = [
  { key: 'mailbox', label: '生成临时邮箱', description: '由服务器端邮箱服务创建并保管访问令牌。' },
  { key: 'replace', label: '确认 Team 席位', description: '确认后替换选中的普通成员；若普通席位已人工腾出，会跳过移除并仅发送邀请。' },
  { key: 'verify', label: '完成 OpenAI 授权', description: '展示 XIASS 官方 PKCE 链接；完成官方授权后粘贴完整回调并校验。' },
  { key: 'import', label: '导入 XIASS', description: '使用固定并发数 10、优先级 1 创建一个 OpenAI OAuth 账号。' }
]

const busy = computed(() => ['creating', 'polling', 'workflow', 'importing'].includes(status.value) || workflowStarting.value || workflowContinuing.value)
// Mailbox polling is a background task and must not make workflow controls
// appear, disappear, or resize while the refresh icon is spinning.
const workflowActionBusy = computed(() => ['creating', 'workflow', 'importing'].includes(status.value) || workflowStarting.value || workflowContinuing.value)
const isStarted = computed(() => Boolean(mailbox.value))
const importing = computed(() => status.value === 'importing')
const statusLabel = computed(() => ({ idle: '未开始', creating: '创建中', ready: '等待授权', workflow: '正在替换成员', waiting: '等待外部验证', polling: '检查邮箱', received: '已收到验证码', callback: '已获取回调', importing: '正在导入', completed: '已完成', error: '需要处理' })[status.value])
const statusTone = computed(() => {
  if (status.value === 'error') return { text: 'text-red-600 dark:text-red-400', icon: 'exclamationTriangle' as const }
  if (status.value === 'completed' || status.value === 'received' || status.value === 'callback') return { text: 'text-green-600 dark:text-green-400', icon: 'check' as const }
  if (busy.value) return { text: 'text-primary-600 dark:text-primary-400', icon: 'refresh' as const }
  return { text: 'text-gray-500 dark:text-gray-400', icon: 'clock' as const }
})
const mailboxCodeHint = computed(() => {
  if (mailboxCode.value) return '验证码已从服务器端邮箱正文中提取。'
  if (mailboxCodeError.value) return `${mailboxCodeError.value} 系统会继续重试。`
  if (mailboxCodeLoading.value) return '正在检查邮箱，收到新邮件后会自动显示。'
  return '邮箱已进入持续监听；完成 OpenAI 页面邮箱验证后，这里会自动显示验证码。'
})
const parsedCallback = computed(() => {
  if (!callbackURL.value.trim()) return null
  try {
    const parsed = new URL(callbackURL.value.trim())
    const code = parsed.searchParams.get('code')?.trim() || ''
    const state = parsed.searchParams.get('state')?.trim() || ''
    if (!code || !state) return null
    return { code, state }
  } catch {
    return null
  }
})
const parsedCallbackError = computed(() => {
  if (!callbackURL.value.trim()) return ''
  if (!parsedCallback.value) return '请输入包含 code 和 state 的完整回调 URL。'
  if (oauthState.value && parsedCallback.value.state !== oauthState.value) return '回调 state 与本次授权不匹配，请重新粘贴当前会话的回调地址。'
  return ''
})
function isReplaceableMember(member: TeamChildMember | undefined) {
  return Boolean(member) && /^(member|成员)$/i.test(member!.role.trim()) && !member!.protected
}
function isProtectedMember(member: TeamChildMember) {
  return Boolean(member.protected) || /^(owner|所有者|admin|administrator|管理员)$/i.test(member.role.trim())
}
const selectedReplaceableMember = computed(() => members.value.find((member) => normalizeTeamChildEmail(member.email) === normalizeTeamChildEmail(selectedMemberEmail.value)))
const workflowBusy = computed(() => workflowStarting.value || workflowContinuing.value || workflowStepRunning.value || teamWorkflow.value?.status === 'running')
const oauthWorkspaceVisible = computed(() => {
  const workflow = teamWorkflow.value
  if (!workflow) return false
  if (['manual_required', 'callback_ready'].includes(workflow.status)) return true
  return workflow.steps.some((step) => step.key === 'oauth' && ['running', 'completed'].includes(step.status))
})
const manualSeatReady = computed(() => membersReady.value && members.value.length > 0 && !members.value.some((member) => isReplaceableMember(member)) && members.value.every(isProtectedMember))
const workflowReady = computed(() => Boolean(mailbox.value?.email) && Boolean(authUrl.value) && (isReplaceableMember(selectedReplaceableMember.value) || manualSeatReady.value) && membersReady.value && !workflowBusy.value && !['manual_required', 'callback_ready', 'failed'].includes(teamWorkflow.value?.status || ''))
const workflowConfirmationTitle = computed(() => manualSeatReady.value ? '确认邀请并授权' : '确认替换成员并授权')
const workflowConfirmationMessage = computed(() => {
  if (!mailbox.value?.email) return '当前缺少临时邮箱。'
  if (workflowStartStep.value === 'oauth') return workflowRunOnlyStep.value
    ? '已由实时页面确认成员席位和 Pending invites 状态。只执行 OAuth 步骤并停在外部验证，不会重复移除成员或发送邀请。'
    : '已由实时页面确认成员席位和 Pending invites 状态。将准备 XIASS 官方 OpenAI 授权链接，不会重复移除成员或发送邀请。'
  if (workflowStartStep.value === 'remove') return manualSeatReady.value
    ? workflowRunOnlyStep.value
      ? '普通成员席位已经由人工腾出，本次只确认并跳过移除步骤。'
      : '普通成员席位已经由人工腾出，将跳过移除并从邀请步骤继续。'
    : workflowRunOnlyStep.value
      ? `将从实时成员页面处理已选普通成员 ${selectedMemberEmail.value}，只执行当前成员席位步骤。`
      : `将从实时成员页面处理已选普通成员 ${selectedMemberEmail.value}，完成后继续邀请和授权。`
  if (manualSeatReady.value) return `已实时确认普通成员席位已由人工腾出，当前仅剩受保护成员。将不移除任何成员，直接向 ${mailbox.value.email} 发送邀请，并准备官方 OAuth 链接。`
  if (!selectedMemberEmail.value) return '当前缺少待替换成员。'
  return `将从 ChatGPT 工作区移除 ${selectedMemberEmail.value}，随后向 ${mailbox.value.email} 发送邀请，并准备官方 OAuth 链接。已完成的外部操作不会自动回滚。`
})
const workflowStepConfirmationTitle = computed(() => {
  if (workflowStepToRun.value === 'remove') return '执行成员移除步骤'
  if (workflowStepToRun.value === 'invite') return '执行成员邀请步骤'
  if (workflowStepToRun.value === 'oauth') return '执行 OpenAI 授权步骤'
  if (workflowStepToRun.value === 'verify') return '检查授权回调'
  return '执行成员页面步骤'
})
const workflowStepConfirmationMessage = computed(() => {
  const key = workflowStepToRun.value
  if (key === 'remove') return '会先刷新实时成员页面；如果目标成员已由人工移除，系统只确认状态，不会重复提交移除。'
  if (key === 'invite') return '会先精确检查 Members 和 Pending invites 中的当前临时邮箱；已存在时只确认成功，不会重复发送邀请。'
  if (key === 'oauth') return '会复用当前 XIASS 官方 OpenAI OAuth 会话并展示授权链接，不会重新生成第二条授权链接。'
  if (key === 'verify') return '等待粘贴完整回调 URL，并校验当前会话的 code/state；校验通过后才可导入。'
  return '会刷新实时成员页面并确认当前工作区状态，然后继续未完成的步骤。'
})
const canImport = computed(() => Boolean(parsedCallback.value) && !parsedCallbackError.value && Boolean(mailbox.value) && Boolean(oauthSessionID.value) && !importing.value)
const canConfirmCallback = computed(() => Boolean(teamWorkflow.value) && Boolean(parsedCallback.value) && !parsedCallbackError.value && !callbackConfirming.value)

function assertOfficialOAuthSession(auth: { auth_url: string; session_id: string }) {
  const parsed = new URL(auth.auth_url)
  const query = parsed.searchParams
  const valid = parsed.protocol === 'https:'
    && parsed.hostname.toLowerCase() === 'auth.openai.com'
    && parsed.pathname === '/oauth/authorize'
    && query.get('response_type') === 'code'
    && query.get('client_id') === 'app_EMoamEEZ73f0CkXaXp7hrann'
    && query.get('redirect_uri') === 'http://localhost:1455/auth/callback'
    && query.get('scope') === 'openid profile email offline_access'
    && Boolean(query.get('state'))
    && Boolean(query.get('code_challenge'))
    && query.get('code_challenge_method') === 'S256'
    && query.get('codex_cli_simplified_flow') === 'true'
    && query.get('id_token_add_organizations') === 'true'
  if (!valid || !auth.session_id) throw new Error('内置 OpenAI OAuth 会话无效，请重新生成授权链接')
}

function normalizeTeamChildEmail(value: string) {
  const normalized = String(value || '')
    .normalize('NFKC')
    .replace(/[\u200B-\u200D\uFEFF]/g, '')
    .trim()
    .toLowerCase()
  return normalized.match(/[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}/i)?.[0] || normalized
}

async function generateFreshOAuthSession() {
  if (oauthGenerating.value) return null
  callbackURL.value = ''
  try {
    // Delegate to the same composable used by the standard Add Account form.
    // The XIASS backend owns PKCE, state and session persistence; this view
    // only consumes the returned session.
    const generated = await openaiOAuth.generateAuthUrl()
    if (!generated || !authUrl.value || !oauthSessionID.value) {
      throw new Error(oauthError.value || '生成 XIASS 官方 OpenAI 授权链接失败')
    }
    const auth = { auth_url: authUrl.value, session_id: oauthSessionID.value }
    assertOfficialOAuthSession(auth)
    return auth
  } catch (error) {
    const message = oauthError.value || extractApiErrorMessage(error, '生成 XIASS 官方 OpenAI 授权链接失败')
    openaiOAuth.resetState()
    oauthError.value = message
    throw error
  }
}

function isStepComplete(key: StepKey) {
  if (key === 'mailbox') return Boolean(mailbox.value)
  if (key === 'replace') return teamWorkflow.value?.steps.find((item) => item.key === 'invite')?.status === 'completed'
  if (key === 'verify') return Boolean(callbackURL.value) && Boolean(parsedCallback.value)
  return Boolean(createdAccount.value)
}
function stepClass(key: StepKey) {
  if (isStepComplete(key)) return 'border-green-500 bg-green-500 text-white'
  if ((key === 'mailbox' && isStarted.value) || (key === 'replace' && teamWorkflow.value) || (key === 'verify' && teamWorkflow.value?.status === 'manual_required')) return 'border-primary-500 bg-primary-50 text-primary-600 dark:bg-primary-950/30 dark:text-primary-400'
  return 'border-gray-300 text-gray-400 dark:border-dark-600 dark:text-gray-500'
}
function formatTime(value: string) {
  return new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
async function copyText(value: string) {
  try { await navigator.clipboard.writeText(value); appStore.showSuccess('已复制') } catch { appStore.showError('复制失败，请手动复制') }
}
async function handleMailboxConfigFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  configImporting.value = true
  try {
    const result = await teamChildAPI.importMailboxConfig(file)
    mailboxConfigured.value = result.configured
    appStore.showSuccess(`邮箱配置已导入（${result.domain || '默认域名'}），无需重启即可使用`)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, '邮箱配置导入失败'))
  } finally {
    configImporting.value = false
  }
}
function copyMailboxEmail() { if (mailbox.value?.email) void copyText(mailbox.value.email) }
function clearBrowserHeartbeat() {
  if (browserHeartbeatTimer) clearTimeout(browserHeartbeatTimer)
  browserHeartbeatTimer = null
}
function scheduleBrowserHeartbeat() {
  clearBrowserHeartbeat()
  if (!browserVisible.value || !browserControllerID.value) return
  browserHeartbeatTimer = setTimeout(() => void heartbeatBrowserControl(), 45000)
}
async function heartbeatBrowserControl() {
  if (!browserVisible.value || !browserControllerID.value) return
  try {
    await teamChildAPI.heartbeatTeamChildBrowserControl(browserControllerID.value)
    scheduleBrowserHeartbeat()
  } catch (error) {
    clearBrowserHeartbeat()
    browserEmbedURL.value = ''
    browserControlConflict.value = extractApiErrorCode(error) === 'TEAM_CHILD_BROWSER_CONTROL_LOST'
    browserError.value = browserControlConflict.value
      ? '浏览器控制权已由其他设备接管。请返回自动化工作区，或在确认后重新接管。'
      : extractApiErrorMessage(error, '浏览器控制续期失败，请重新接管后再操作。')
  }
}
async function releaseBrowserControl() {
  clearBrowserHeartbeat()
  const controllerID = browserControllerID.value
  browserControllerID.value = ''
  if (controllerID) await teamChildAPI.releaseTeamChildBrowserControl(controllerID).catch(() => undefined)
}
async function reloadBrowserWorkspace(takeOver = false) {
  if (!browserConfigured.value || browserLoading.value) return
  browserLoading.value = true
  browserError.value = ''
  browserControlConflict.value = false
  try {
    const session = await teamChildAPI.createBrowserSession({
      ...(browserControllerID.value ? { controller_id: browserControllerID.value } : {}),
      ...(takeOver ? { take_over: true } : {})
    })
    browserEmbedURL.value = session.embed_url
    browserControllerID.value = session.controller_id
    scheduleBrowserHeartbeat()
  } catch (error) {
    browserControlConflict.value = extractApiErrorCode(error) === 'TEAM_CHILD_BROWSER_CONTROLLED'
    browserError.value = browserControlConflict.value
      ? '已有设备正在手动查看服务器浏览器。需要继续时，请明确确认接管。'
      : extractApiErrorMessage(error, '无法连接服务器浏览器，请检查部署状态。')
  } finally {
    browserLoading.value = false
  }
}
async function openBrowserWorkspace() {
  browserVisible.value = true
  await reloadBrowserWorkspace()
}
async function confirmBrowserTakeOver() {
  browserTakeOverConfirmOpen.value = false
  await reloadBrowserWorkspace(true)
}
async function closeBrowserWorkspace() {
  browserVisible.value = false
  browserEmbedURL.value = ''
  browserError.value = ''
  browserControlConflict.value = false
  await releaseBrowserControl()
}
function applyMembersResult(result: { members?: TeamChildMember[]; pending_invites?: number; seat_email?: string; workspace_name?: string; ready?: boolean }) {
  const nextMembers = result.members || []
  members.value = nextMembers
  pendingInvites.value = result.pending_invites || 0
  seatEmail.value = result.seat_email || ''
  workspaceName.value = result.workspace_name || ''
  membersReady.value = Boolean(result.ready)

  const replaceable = nextMembers.find((member) => isReplaceableMember(member))
  const selectedStillAvailable = nextMembers.some((member) => member.email === selectedMemberEmail.value && isReplaceableMember(member))
  if (!selectedStillAvailable) selectedMemberEmail.value = seatEmail.value || replaceable?.email || ''
}
async function loadMembers(notify = false) {
  if (membersLoading.value) return
  membersLoading.value = true
  membersError.value = ''
  try {
    const result = await teamChildAPI.listTeamChildMembers()
    applyMembersResult(result)
    if (notify && membersReady.value) appStore.showSuccess(`成员信息已读取（${members.value.length} 位成员）`)
  } catch (error) {
    membersReady.value = false
    membersError.value = extractApiErrorMessage(error, '无法读取成员信息，请先在服务器浏览器中登录。')
  } finally {
    membersLoading.value = false
  }
}
async function refreshMembers(notify = true) {
  if (membersLoading.value) return
  membersLoading.value = true
  membersError.value = ''
  try {
    const result = await teamChildAPI.refreshTeamChildMembers()
    applyMembersResult(result)
    if (notify && membersReady.value) appStore.showSuccess(`成员信息已刷新（${members.value.length} 位成员）`)
  } catch (error) {
    membersReady.value = false
    membersError.value = extractApiErrorMessage(error, '无法读取成员信息，请先在服务器浏览器中登录。')
  } finally {
    membersLoading.value = false
  }
}
async function inspectSeat() {
  if (membersLoading.value) return
  membersLoading.value = true
  membersError.value = ''
  try {
    const result = await teamChildAPI.inspectTeamChildSeat()
    applyMembersResult(result)
    appStore.showSuccess(seatEmail.value ? `已识别席位邮箱：${seatEmail.value}` : '已读取成员页，但页面未提供明确的席位邮箱')
  } catch (error) {
    membersReady.value = false
    membersError.value = extractApiErrorMessage(error, '无法识别席位邮箱，请先在服务器浏览器中登录。')
  } finally {
    membersLoading.value = false
  }
}
async function inviteMember(email: string) {
  membersLoading.value = true
  const inviteEmail = normalizeTeamChildEmail(email)
  try {
    const result = await teamChildAPI.inviteTeamChildMember(inviteEmail)
    if (result.operation?.confirmed !== true) {
      await refreshMembers(false)
      appStore.showError('邀请请求已提交，但服务器未确认成员状态，请刷新后核实')
      return
    }
    members.value = result.members || members.value
    pendingInvites.value = result.pending_invites || 0
    appStore.showSuccess(`邀请已发送：${inviteEmail}`)
  } catch (error) { appStore.showError(extractApiErrorMessage(error, '邀请成员失败')) } finally { membersLoading.value = false }
}
async function editMember(email: string, role: string) {
  membersLoading.value = true
  try {
    const result = await teamChildAPI.updateTeamChildMember(email, role)
    if (result.operation?.confirmed !== true) {
      await refreshMembers(false)
      appStore.showError('角色更新未得到服务器确认，请刷新后核实')
      return
    }
    members.value = result.members || members.value
    appStore.showSuccess('成员角色已更新')
  } catch (error) { appStore.showError(extractApiErrorMessage(error, '编辑成员失败')) } finally { membersLoading.value = false }
}
async function removeMember(email: string) {
  membersLoading.value = true
  try {
    const result = await teamChildAPI.removeTeamChildMember(email)
    if (result.operation?.confirmed !== true) {
      await refreshMembers(false)
      appStore.showError('移除请求已提交，但服务器未确认成员状态，请刷新后核实')
      return
    }
    members.value = result.members || members.value
    pendingInvites.value = result.pending_invites || 0
    if (selectedMemberEmail.value === email) selectedMemberEmail.value = ''
    appStore.showSuccess('成员已从工作空间移除')
  } catch (error) { appStore.showError(extractApiErrorMessage(error, '移除成员失败')) } finally { membersLoading.value = false }
}
function canRunTopLevelStep(key: StepKey) {
  if (workflowActionBusy.value || workflowBusy.value) return false
  if (key === 'mailbox') return !mailbox.value && mailboxConfigured.value
  if (key === 'replace') return Boolean(mailbox.value?.email && authUrl.value && membersReady.value && (isReplaceableMember(selectedReplaceableMember.value) || manualSeatReady.value) && !teamWorkflow.value)
  if (key === 'verify') return Boolean(mailbox.value?.email && authUrl.value && membersReady.value && manualSeatReady.value && pendingInvites.value > 0 && !teamWorkflow.value)
  return canImport.value
}
function isTopLevelStepRunning(key: StepKey) {
  if (key === 'mailbox') return status.value === 'creating'
  if (key === 'replace') return workflowStarting.value || teamWorkflow.value?.steps.some((step) => ['members', 'remove', 'invite'].includes(step.key) && step.status === 'running') === true
  if (key === 'verify') return teamWorkflow.value?.steps.some((step) => ['oauth', 'verify'].includes(step.key) && step.status === 'running') === true
  return status.value === 'importing'
}
function runTopLevelStep(key: StepKey) {
  if (!canRunTopLevelStep(key)) return
  if (key === 'mailbox') return void startFlow()
  if (key === 'replace') return openWorkflowConfirmation('remove', true)
  if (key === 'verify') return openWorkflowConfirmation('oauth', true)
  return void importAccount()
}
function openWorkflowConfirmation(startStep: TeamChildWorkflowStepKey = 'members', runOnlyStep = false) {
  if (startStep === 'oauth') {
    if (!canRunTopLevelStep('verify')) {
      appStore.showError('请先刷新确认目标临时邮箱已出现在 Pending invites 中')
      return
    }
  } else if (!workflowReady.value) {
    appStore.showError('请先获取临时邮箱，并选择可替换成员或刷新确认已人工腾出的席位')
    return
  }
  workflowStartStep.value = startStep
  workflowRunOnlyStep.value = runOnlyStep
  workflowConfirmOpen.value = true
}
function clearWorkflowPoll() {
  if (workflowPollTimer) clearTimeout(workflowPollTimer)
  workflowPollTimer = null
}
function scheduleWorkflowPoll(delay = 1200) {
  clearWorkflowPoll()
  if (!teamWorkflow.value || !['running', 'manual_required'].includes(teamWorkflow.value.status)) return
  workflowPollTimer = setTimeout(() => void pollWorkflow(), delay)
}
async function startConfirmedWorkflow(startStep: TeamChildWorkflowStepKey = workflowStartStep.value) {
  const seatAlreadyRemoved = manualSeatReady.value
  if (!mailbox.value?.email || workflowStarting.value || (!seatAlreadyRemoved && !isReplaceableMember(selectedReplaceableMember.value))) return
  if (!authUrl.value || !oauthSessionID.value) {
    appStore.showError('请先使用 XIASS 内置 OpenAI 手动授权流程生成授权链接')
    return
  }
  workflowConfirmOpen.value = false
  workflowStarting.value = true
  errorMessage.value = ''
  try {
    // Keep the exact URL/session generated by XIASS's native OpenAI manual
    // authorization flow. Do not mint a second session here: the browser page
    // opened by the automation and the later callback exchange must refer to
    // the same PKCE state visible in this workspace.
    callbackURL.value = ''
    teamWorkflow.value = await teamChildAPI.startTeamChildWorkflow({
      ...(seatAlreadyRemoved ? {} : { seat_email: normalizeTeamChildEmail(selectedMemberEmail.value) }),
      invite_email: normalizeTeamChildEmail(mailbox.value.email),
      auth_url: authUrl.value,
      seat_already_removed: seatAlreadyRemoved,
      start_step: startStep,
      ...(workflowRunOnlyStep.value ? { run_only_step: true } : {}),
      confirmed: true
    })
    status.value = 'workflow'
    scheduleWorkflowPoll()
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '无法启动成员替换和授权工作流')
  } finally {
    workflowStarting.value = false
  }
}
function requestWorkflowStep(step: TeamChildWorkflowStepKey) {
  if (!teamWorkflow.value || workflowBusy.value) return
  const selected = teamWorkflow.value.steps.find((item) => item.key === step)
  if (!selected || !['pending', 'waiting', 'failed'].includes(selected.status)) return
  workflowStepToRun.value = step
  workflowStepConfirmOpen.value = true
}
function cancelWorkflowStep() {
  workflowStepConfirmOpen.value = false
  workflowStepToRun.value = null
}
async function confirmWorkflowStep() {
  const workflow = teamWorkflow.value
  const step = workflowStepToRun.value
  if (!workflow || !step || workflowStepRunning.value) return
  workflowStepConfirmOpen.value = false
  workflowStepRunning.value = true
  errorMessage.value = ''
  const selected = workflow.steps.find((item) => item.key === step)
  if (selected) {
    selected.status = 'running'
    selected.message = `正在执行第 ${selected.number} 步：${selected.label}`
  }
  workflow.status = 'running'
  status.value = 'workflow'
  scheduleWorkflowPoll(250)
  try {
    teamWorkflow.value = await teamChildAPI.runTeamChildWorkflowStep(workflow.id, step)
    if (teamWorkflow.value.status === 'callback_ready' && teamWorkflow.value.callback_url) {
      callbackURL.value = teamWorkflow.value.callback_url
      status.value = 'callback'
      clearWorkflowPoll()
      appStore.showSuccess('已校验授权回调地址，可以导入 XIASS')
    } else {
      status.value = teamWorkflow.value.status === 'manual_required' ? 'waiting' : 'workflow'
      scheduleWorkflowPoll(500)
    }
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '指定步骤执行失败')
    await syncWorkflowAfterActionError()
  } finally {
    workflowStepRunning.value = false
    workflowStepToRun.value = null
  }
}
async function continueWorkflow() {
  if (!teamWorkflow.value || teamWorkflow.value.status !== 'failed' || workflowContinuing.value) return
  workflowContinuing.value = true
  errorMessage.value = ''
  try {
    teamWorkflow.value = await teamChildAPI.continueTeamChildWorkflow(teamWorkflow.value.id)
    status.value = 'workflow'
    scheduleWorkflowPoll(500)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '无法继续自动化工作流')
  } finally {
    workflowContinuing.value = false
  }
}

async function restartWorkflowOAuth() {
  const workflow = teamWorkflow.value
  if (!workflow || !['manual_required', 'failed'].includes(workflow.status)) return
  clearWorkflowPoll()
  try {
    const auth = await generateFreshOAuthSession()
    if (!auth) throw new Error('未生成 XIASS 官方 OpenAI 授权链接')
    teamWorkflow.value = await teamChildAPI.restartTeamChildWorkflowOAuth(workflow.id, auth.auth_url)
    callbackURL.value = ''
    status.value = 'workflow'
    errorMessage.value = ''
    appStore.showInfo('已取消当前接码会话，新的官方 OAuth 链接已准备')
    scheduleWorkflowPoll(900)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '重置 OAuth 步骤失败，请重新生成官方授权链接')
  }
}

async function syncWorkflowAfterActionError() {
  const workflow = teamWorkflow.value
  if (!workflow) return
  try {
    const latest = await teamChildAPI.getTeamChildWorkflow(workflow.id)
    teamWorkflow.value = latest
    if (latest.status === 'failed') {
      status.value = 'error'
      return
    }
    if (latest.status === 'manual_required') {
      status.value = 'waiting'
      scheduleWorkflowPoll(2500)
    }
  } catch {
    // Keep the original action error visible when the status read also fails.
  }
}

async function pollWorkflow() {
  if (!teamWorkflow.value || !['running', 'manual_required'].includes(teamWorkflow.value.status)) return
  try {
    const workflow = await teamChildAPI.getTeamChildWorkflow(teamWorkflow.value.id)
    teamWorkflow.value = workflow
    if (workflow.status === 'callback_ready' && workflow.callback_url) {
      callbackURL.value = workflow.callback_url
      status.value = 'callback'
      clearWorkflowPoll()
      appStore.showSuccess('已校验授权回调地址，可以导入 XIASS')
      return
    }
    if (workflow.status === 'manual_required') {
      if (status.value !== 'received') status.value = 'waiting'
      if (!mailboxCode.value && !mailboxCodeLoading.value) schedulePoll()
    }
    if (workflow.status === 'failed') {
      status.value = 'error'
      errorMessage.value = workflow.error || '成员替换或 OAuth 授权交接未完成'
      clearWorkflowPoll()
      return
    }
    scheduleWorkflowPoll(workflow.status === 'running' ? 1200 : 2500)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '无法读取授权工作流状态')
    clearWorkflowPoll()
  }
}
async function startFlow() {
  if (busy.value || !mailboxConfigured.value) return
  if (pollTimer) clearTimeout(pollTimer)
  clearWorkflowPoll()
  errorMessage.value = ''; mailboxCodeError.value = ''; createdAccount.value = null; teamWorkflow.value = null; openaiOAuth.resetState(); callbackURL.value = ''; mailboxCode.value = ''; status.value = 'creating'
  try {
    mailbox.value = await teamChildAPI.createMailbox()
    accountName.value = mailbox.value.email
    schedulePoll(250)
    await generateFreshOAuthSession()
    status.value = 'ready'
  } catch (error) { status.value = 'error'; errorMessage.value = extractApiErrorMessage(error, '流程启动失败') }
}

async function restoreActiveMailbox() {
  if (!mailboxConfigured.value || mailbox.value || busy.value) return
  try {
    const restored = await teamChildAPI.getActiveMailbox()
    if (!restored) return
    mailbox.value = restored
    accountName.value = restored.email
    mailboxCodeError.value = ''
    schedulePoll(250)
    await generateFreshOAuthSession()
    status.value = 'ready'
    appStore.showInfo('已恢复当前临时邮箱，可以继续成员邀请和授权')
  } catch {
    // A missing or expired mailbox is normal after its short server-side TTL.
  }
}
function schedulePoll(delay = 4000) {
  if (pollTimer) clearTimeout(pollTimer)
  if (!mailbox.value || mailboxCode.value || ['completed', 'callback'].includes(status.value)) return
  pollTimer = setTimeout(() => void pollMailbox(), Math.max(0, delay))
}
async function pollMailbox() {
  if (!mailbox.value || mailboxCode.value || ['completed', 'callback'].includes(status.value) || mailboxCodeLoading.value) return
  mailboxCodeLoading.value = true
  if (status.value === 'ready' || status.value === 'waiting' || status.value === 'error') status.value = 'polling'
  try {
    const result = await teamChildAPI.pollMailboxCode(mailbox.value.session_id)
    mailboxCodeError.value = ''
    if (result.status === 'received' && result.code) {
      mailboxCode.value = result.code
      status.value = 'received'
      if (pollTimer) clearTimeout(pollTimer)
    } else {
      if (status.value === 'polling') status.value = 'waiting'
      schedulePoll()
    }
  } catch (error) {
    const statusCode = Number((error as { status?: unknown; response?: { status?: unknown } })?.status ?? (error as { response?: { status?: unknown } })?.response?.status)
    const retryable = !Number.isFinite(statusCode) || statusCode === 408 || statusCode === 429 || statusCode >= 500
    mailboxCodeError.value = retryable ? '邮箱服务暂时没有响应。' : extractApiErrorMessage(error, '邮箱检查失败')
    if (retryable) {
      if (status.value === 'polling') status.value = 'waiting'
      schedulePoll()
    } else {
      status.value = 'error'
      if (pollTimer) clearTimeout(pollTimer)
      errorMessage.value = mailboxCodeError.value
    }
  } finally { mailboxCodeLoading.value = false }
}
async function importAccount() {
  if (!mailbox.value || !oauthSessionID.value || !parsedCallback.value || parsedCallbackError.value || importing.value) return
  errorMessage.value = ''; status.value = 'importing'
  try {
    if (teamWorkflow.value && teamWorkflow.value.status !== 'callback_ready') {
      const confirmed = await confirmWorkflowCallback()
      if (!confirmed) {
        status.value = 'error'
        return
      }
    }
    createdAccount.value = await teamChildAPI.createOpenAIAccountFromOAuth({ session_id: oauthSessionID.value, code: parsedCallback.value.code, state: parsedCallback.value.state, name: accountName.value.trim() || mailbox.value.email, concurrency: 10, priority: 1, group_ids: selectedGroupIDs.value })
    status.value = 'completed'
    if (pollTimer) clearTimeout(pollTimer)
    await teamChildAPI.deleteMailboxSession(mailbox.value.session_id).catch(() => undefined)
  } catch (error) { status.value = 'error'; errorMessage.value = extractApiErrorMessage(error, '导入失败') }
}

async function confirmWorkflowCallback(): Promise<boolean> {
  const workflow = teamWorkflow.value
  const callback = callbackURL.value.trim()
  if (!workflow || !callback || !parsedCallback.value || parsedCallbackError.value || callbackConfirming.value) return false
  callbackConfirming.value = true
  try {
    teamWorkflow.value = await teamChildAPI.submitTeamChildWorkflowCallback(workflow.id, callback)
    callbackURL.value = teamWorkflow.value.callback_url || callback
    status.value = 'callback'
    clearWorkflowPoll()
    appStore.showSuccess('回调 state 已校验，可以导入 XIASS')
    return true
  } catch (error) {
    errorMessage.value = extractApiErrorMessage(error, '回调校验失败，请确认使用当前 OAuth 会话的完整 URL')
    return false
  } finally {
    callbackConfirming.value = false
  }
}
async function resetFlow() {
  if (pollTimer) clearTimeout(pollTimer)
  clearWorkflowPoll()
  if (teamWorkflow.value && ['manual_required', 'failed'].includes(teamWorkflow.value.status)) await teamChildAPI.cancelTeamChildWorkflow(teamWorkflow.value.id).catch(() => undefined)
  if (mailbox.value) await teamChildAPI.deleteMailboxSession(mailbox.value.session_id).catch(() => undefined)
  mailbox.value = null; mailboxCode.value = ''; mailboxCodeError.value = ''; teamWorkflow.value = null; openaiOAuth.resetState(); callbackURL.value = ''; accountName.value = ''; selectedGroupIDs.value = []; createdAccount.value = null; errorMessage.value = ''; status.value = 'idle'
}

async function restoreActiveWorkflow() {
  if (teamWorkflow.value) return
  try {
    const restored = await teamChildAPI.getActiveTeamChildWorkflow()
    if (!restored) return
    teamWorkflow.value = restored
    if (restored.status === 'callback_ready' && restored.callback_url) {
      callbackURL.value = restored.callback_url
      status.value = 'callback'
      return
    }
    if (restored.status === 'failed') {
      status.value = 'error'
      errorMessage.value = restored.error || '自动化步骤未完成，请点击继续自动化'
      return
    }
    status.value = restored.status === 'manual_required' ? 'waiting' : 'workflow'
    scheduleWorkflowPoll(restored.status === 'running' ? 500 : 1500)
  } catch {
    // A missing or expired workflow is normal after its short in-memory TTL.
  }
}

onMounted(async () => {
  const [statusResult, groupsResult] = await Promise.allSettled([
    teamChildAPI.getMailboxStatus(),
    groupsAPI.getAll('openai')
  ])
  mailboxConfigured.value = statusResult.status === 'fulfilled' && Boolean(statusResult.value.configured)
  browserConfigured.value = statusResult.status === 'fulfilled' && Boolean(statusResult.value.browser_configured)
  if (groupsResult.status === 'fulfilled') groups.value = groupsResult.value
  await restoreActiveMailbox()
  await restoreActiveWorkflow()
  if (browserConfigured.value) await loadMembers()
})
onBeforeUnmount(() => {
  if (pollTimer) clearTimeout(pollTimer)
  clearWorkflowPoll()
  void releaseBrowserControl()
})
</script>

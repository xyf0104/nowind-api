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
          一次处理一个账号。邮箱由 XIASS 服务端托管，外部登录和验证在服务器浏览器中完成。
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
              <div class="min-w-0 pb-3">
                <div class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ item.label }}</div>
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
                <input :value="mailboxCode || ''" readonly class="input h-9 w-full font-mono text-sm tracking-[0.16em]" :placeholder="mailboxCodeLoading ? '正在检查...' : '等待邮件到达'" />
              </div>
              <button type="button" class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0" title="立即检查邮箱" aria-label="立即检查邮箱" :disabled="!mailbox || mailboxCodeLoading || Boolean(mailboxCode)" @click="pollMailbox"><Icon name="refresh" size="sm" :class="mailboxCodeLoading ? 'animate-spin' : ''" :stroke-width="2" /></button>
              <button v-if="mailboxCode" type="button" class="btn btn-secondary flex h-9 items-center gap-2 whitespace-nowrap" @click="copyText(mailboxCode)"><Icon name="copy" size="sm" :stroke-width="2" /><span>复制</span></button>
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
          <details v-if="smsWorkflowVisible" class="rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
            <summary class="flex cursor-pointer items-center justify-between gap-3 px-5 py-4 text-sm font-medium text-gray-900 dark:text-gray-100">接码服务 <Icon name="arrowRight" size="sm" class="text-gray-400" :stroke-width="2" /></summary>
            <div class="border-t border-gray-200 p-4 dark:border-dark-700">
              <PixlabSMSReceiver
                :active="true"
                :replacement-required="teamWorkflow?.phone_rejected === true"
                @phone-ready="submitWorkflowPhone"
                @code-received="submitWorkflowCode"
                @session-cancelled="restartWorkflowOAuth"
              />
            </div>
          </details>

          <div v-if="callbackURL || createdAccount" class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
            <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">导入 XIASS</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">回调地址已由工作流识别，也可以在这里手动修正后导入。</p>
            <label class="mt-4 mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">完整回调 URL</label>
            <textarea v-model="callbackURL" rows="3" class="input w-full resize-none font-mono text-xs" placeholder="https://.../callback?code=...&state=..."></textarea>
            <p v-if="parsedCallbackError" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ parsedCallbackError }}</p>
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
        @refresh="refreshMembers"
        @inspect="inspectSeat"
        @select="selectedMemberEmail = $event"
        @invite="inviteMember"
        @edit="editMember"
        @remove="removeMember"
        @open-browser="openBrowserWorkspace"
        @start-workflow="openWorkflowConfirmation"
        @continue-workflow="continueWorkflow"
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
      :confirm-text="manualSeatReady ? '邀请并授权' : '移除并邀请'"
      cancel-text="取消"
      danger
      @confirm="startConfirmedWorkflow"
      @cancel="workflowConfirmOpen = false"
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
import TeamChildMembersWorkspace from '@/components/admin/account/TeamChildMembersWorkspace.vue'
import PixlabSMSReceiver from '@/components/account/PixlabSMSReceiver.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import { teamChildAPI, type TeamChildMailbox, type TeamChildMember, type TeamChildWorkflow } from '@/api/admin/teamChild'
import { groupsAPI } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'

type FlowStatus = 'idle' | 'creating' | 'ready' | 'workflow' | 'waiting' | 'polling' | 'received' | 'callback' | 'importing' | 'completed' | 'error'
type StepKey = 'mailbox' | 'replace' | 'verify' | 'import'

const appStore = useAppStore()
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
const authUrl = ref('')
const oauthState = ref('')
const oauthSessionID = ref('')
const callbackURL = ref('')
const accountName = ref('')
const groups = ref<AdminGroup[]>([])
const selectedGroupIDs = ref<number[]>([])
const errorMessage = ref('')
const createdAccount = ref<{ id?: number; name?: string; [key: string]: unknown } | null>(null)
const teamWorkflow = ref<TeamChildWorkflow | null>(null)
const workflowConfirmOpen = ref(false)
const workflowStarting = ref(false)
const workflowContinuing = ref(false)
let pollTimer: ReturnType<typeof setTimeout> | null = null
let workflowPollTimer: ReturnType<typeof setTimeout> | null = null
let browserHeartbeatTimer: ReturnType<typeof setTimeout> | null = null

const steps: Array<{ key: StepKey; label: string; description: string }> = [
  { key: 'mailbox', label: '生成临时邮箱', description: '由服务器端邮箱服务创建并保管访问令牌。' },
  { key: 'replace', label: '确认 Team 席位', description: '确认后替换选中的普通成员；若普通席位已人工腾出，会跳过移除并仅发送邀请。' },
  { key: 'verify', label: '完成 OpenAI 授权', description: '授权页会在服务器浏览器的新标签中打开，回调自动识别。' },
  { key: 'import', label: '导入 XIASS', description: '使用固定并发数 10、优先级 1 创建一个 OpenAI OAuth 账号。' }
]

const busy = computed(() => ['creating', 'polling', 'workflow', 'importing'].includes(status.value) || workflowStarting.value || workflowContinuing.value)
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
const selectedReplaceableMember = computed(() => members.value.find((member) => member.email === selectedMemberEmail.value))
const workflowBusy = computed(() => workflowStarting.value || workflowContinuing.value || teamWorkflow.value?.status === 'running')
const smsWorkflowVisible = computed(() => {
  const workflow = teamWorkflow.value
  if (!workflow) return false
  if (workflow.status === 'manual_required') return true
  return Boolean(
    workflow.status === 'failed'
    && workflow.steps.some((step) => step.key === 'oauth' && step.status === 'completed')
  )
})
const manualSeatReady = computed(() => membersReady.value && members.value.length > 0 && !members.value.some((member) => isReplaceableMember(member)) && members.value.every(isProtectedMember))
const workflowReady = computed(() => Boolean(mailbox.value?.email) && Boolean(authUrl.value) && (isReplaceableMember(selectedReplaceableMember.value) || manualSeatReady.value) && membersReady.value && !workflowBusy.value && !['manual_required', 'callback_ready', 'failed'].includes(teamWorkflow.value?.status || ''))
const workflowConfirmationTitle = computed(() => manualSeatReady.value ? '确认邀请并授权' : '确认替换成员并授权')
const workflowConfirmationMessage = computed(() => {
  if (!mailbox.value?.email) return '当前缺少临时邮箱。'
  if (manualSeatReady.value) return `已实时确认普通成员席位已由人工腾出，当前仅剩受保护成员。将不移除任何成员，直接向 ${mailbox.value.email} 发送邀请，并在服务器浏览器的新标签中打开授权页。`
  if (!selectedMemberEmail.value) return '当前缺少待替换成员。'
  return `将从 ChatGPT 工作区移除 ${selectedMemberEmail.value}，随后向 ${mailbox.value.email} 发送邀请，并在服务器浏览器的新标签中打开授权页。已完成的外部操作不会自动回滚。`
})
const canImport = computed(() => Boolean(parsedCallback.value) && !parsedCallbackError.value && Boolean(mailbox.value) && Boolean(oauthSessionID.value) && !importing.value)

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
async function refreshMembers(notify = true, force = false) {
  if (membersLoading.value && !force) return
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
  try {
    const result = await teamChildAPI.inviteTeamChildMember(email)
    if (result.operation?.confirmed !== true) {
      await refreshMembers(false, true)
      appStore.showError('邀请请求已提交，但服务器未确认成员状态，请刷新后核实')
      return
    }
    members.value = result.members || members.value
    pendingInvites.value = result.pending_invites || 0
    appStore.showSuccess(`邀请已发送：${email}`)
  } catch (error) { appStore.showError(extractApiErrorMessage(error, '邀请成员失败')) } finally { membersLoading.value = false }
}
async function editMember(email: string, role: string) {
  membersLoading.value = true
  try {
    const result = await teamChildAPI.updateTeamChildMember(email, role)
    if (result.operation?.confirmed !== true) {
      await refreshMembers(false, true)
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
      await refreshMembers(false, true)
      appStore.showError('移除请求已提交，但服务器未确认成员状态，请刷新后核实')
      return
    }
    members.value = result.members || members.value
    pendingInvites.value = result.pending_invites || 0
    if (selectedMemberEmail.value === email) selectedMemberEmail.value = ''
    appStore.showSuccess('成员已从工作空间移除')
  } catch (error) { appStore.showError(extractApiErrorMessage(error, '移除成员失败')) } finally { membersLoading.value = false }
}
function openWorkflowConfirmation() {
  if (!workflowReady.value) {
    appStore.showError('请先获取临时邮箱，并选择可替换成员或刷新确认已人工腾出的席位')
    return
  }
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
async function startConfirmedWorkflow() {
  const seatAlreadyRemoved = manualSeatReady.value
  if (!mailbox.value?.email || !authUrl.value || workflowStarting.value || (!seatAlreadyRemoved && !isReplaceableMember(selectedReplaceableMember.value))) return
  workflowConfirmOpen.value = false
  workflowStarting.value = true
  errorMessage.value = ''
  try {
    teamWorkflow.value = await teamChildAPI.startTeamChildWorkflow({
      ...(seatAlreadyRemoved ? {} : { seat_email: selectedMemberEmail.value }),
      invite_email: mailbox.value.email,
      auth_url: authUrl.value,
      seat_already_removed: seatAlreadyRemoved,
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

async function submitWorkflowPhone(phone: string) {
  const workflow = teamWorkflow.value
  if (!workflow || !['manual_required', 'failed'].includes(workflow.status)) return
  try {
    teamWorkflow.value = await teamChildAPI.submitTeamChildWorkflowPhone(workflow.id, phone)
    status.value = teamWorkflow.value.status === 'callback_ready' ? 'callback' : 'waiting'
    if (teamWorkflow.value.status === 'callback_ready' && teamWorkflow.value.callback_url) {
      callbackURL.value = teamWorkflow.value.callback_url
      clearWorkflowPoll()
      appStore.showSuccess('手机号已更新，授权回调已就绪')
      return
    }
    appStore.showInfo('新手机号已填入授权页面，将继续当前步骤')
    scheduleWorkflowPoll(900)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '新手机号未能填入授权页面，请在浏览器中手动完成当前步骤')
    await syncWorkflowAfterActionError()
  }
}

async function submitWorkflowCode(code: string) {
  const workflow = teamWorkflow.value
  if (!workflow || !['manual_required', 'failed'].includes(workflow.status)) return
  try {
    teamWorkflow.value = await teamChildAPI.submitTeamChildWorkflowCode(workflow.id, code)
    status.value = teamWorkflow.value.status === 'callback_ready' ? 'callback' : 'waiting'
    if (teamWorkflow.value.status === 'callback_ready' && teamWorkflow.value.callback_url) {
      callbackURL.value = teamWorkflow.value.callback_url
      clearWorkflowPoll()
      appStore.showSuccess('验证码已提交，授权回调已就绪')
      return
    }
    appStore.showInfo('验证码已填入授权页面，将继续当前步骤')
    scheduleWorkflowPoll(900)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '验证码未能填入授权页面，请在浏览器中手动完成当前步骤')
    await syncWorkflowAfterActionError()
  }
}

async function restartWorkflowOAuth() {
  const workflow = teamWorkflow.value
  if (!workflow || !['manual_required', 'failed'].includes(workflow.status)) return
  clearWorkflowPoll()
  try {
    const auth = await teamChildAPI.generateOpenAIAuthUrl()
    authUrl.value = auth.auth_url
    oauthSessionID.value = auth.session_id
    oauthState.value = new URL(auth.auth_url).searchParams.get('state') || ''
    teamWorkflow.value = await teamChildAPI.restartTeamChildWorkflowOAuth(workflow.id, auth.auth_url)
    callbackURL.value = ''
    status.value = 'workflow'
    errorMessage.value = ''
    appStore.showInfo('已取消旧手机号，正在重新打开授权步骤')
    scheduleWorkflowPoll(900)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '取消号码后未能重新打开授权步骤，请手动接管浏览器')
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
      appStore.showSuccess('已识别授权回调地址，可以导入 XIASS')
      return
    }
    if (workflow.status === 'manual_required') {
      if (status.value !== 'received') status.value = 'waiting'
      if (!mailboxCode.value && !mailboxCodeLoading.value) schedulePoll()
    }
    if (workflow.status === 'failed') {
      status.value = 'error'
      errorMessage.value = workflow.error || '成员替换或授权页打开未完成'
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
  errorMessage.value = ''; mailboxCodeError.value = ''; createdAccount.value = null; teamWorkflow.value = null; authUrl.value = ''; oauthState.value = ''; oauthSessionID.value = ''; callbackURL.value = ''; mailboxCode.value = ''; status.value = 'creating'
  try {
    mailbox.value = await teamChildAPI.createMailbox()
    accountName.value = mailbox.value.email
    schedulePoll(250)
    const auth = await teamChildAPI.generateOpenAIAuthUrl()
    authUrl.value = auth.auth_url
    oauthSessionID.value = auth.session_id
    oauthState.value = new URL(auth.auth_url).searchParams.get('state') || ''
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
    const auth = await teamChildAPI.generateOpenAIAuthUrl()
    authUrl.value = auth.auth_url
    oauthSessionID.value = auth.session_id
    oauthState.value = new URL(auth.auth_url).searchParams.get('state') || ''
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
    createdAccount.value = await teamChildAPI.createOpenAIAccountFromOAuth({ session_id: oauthSessionID.value, code: parsedCallback.value.code, state: parsedCallback.value.state, name: accountName.value.trim() || mailbox.value.email, concurrency: 10, priority: 1, group_ids: selectedGroupIDs.value })
    status.value = 'completed'
    if (pollTimer) clearTimeout(pollTimer)
    await teamChildAPI.deleteMailboxSession(mailbox.value.session_id).catch(() => undefined)
  } catch (error) { status.value = 'error'; errorMessage.value = extractApiErrorMessage(error, '导入失败') }
}
async function resetFlow() {
  if (pollTimer) clearTimeout(pollTimer)
  clearWorkflowPoll()
  if (teamWorkflow.value && ['manual_required', 'failed'].includes(teamWorkflow.value.status)) await teamChildAPI.cancelTeamChildWorkflow(teamWorkflow.value.id).catch(() => undefined)
  if (mailbox.value) await teamChildAPI.deleteMailboxSession(mailbox.value.session_id).catch(() => undefined)
  mailbox.value = null; mailboxCode.value = ''; mailboxCodeError.value = ''; teamWorkflow.value = null; authUrl.value = ''; oauthState.value = ''; oauthSessionID.value = ''; callbackURL.value = ''; accountName.value = ''; selectedGroupIDs.value = []; createdAccount.value = null; errorMessage.value = ''; status.value = 'idle'
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
  if (browserConfigured.value) await refreshMembers(false)
})
onBeforeUnmount(() => {
  if (pollTimer) clearTimeout(pollTimer)
  clearWorkflowPoll()
  void releaseBrowserControl()
})
</script>

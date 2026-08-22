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
        <button type="button" class="btn btn-secondary flex items-center gap-2" :disabled="configImporting" @click="mailboxConfigFileInput?.click()">
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

    <div class="grid gap-6 xl:grid-cols-[minmax(340px,0.74fr)_minmax(0,1.26fr)]">
      <div class="space-y-5">
        <section class="space-y-5">
        <div class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <div class="mb-5 flex items-start justify-between gap-4">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">流程状态</h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">完成服务器浏览器中的外部页面后，把回调地址粘贴到下方导入区。</p>
            </div>
            <button v-if="isStarted" type="button" class="btn btn-secondary flex items-center gap-2" :disabled="busy" @click="resetFlow">
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

          <div class="mt-3 rounded-lg border border-dashed border-gray-300 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-900/40">
            <div class="flex items-center justify-between gap-3">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">当前邮箱</span>
              <span v-if="mailbox?.expires_at" class="text-xs text-gray-400 dark:text-gray-500">有效期至 {{ formatTime(mailbox.expires_at) }}</span>
            </div>
            <div class="mt-2 flex flex-wrap items-center gap-2">
              <code v-if="mailbox?.email" class="break-all text-sm font-semibold text-gray-900 dark:text-gray-100">{{ mailbox.email }}</code>
              <span v-else class="text-sm text-gray-400 dark:text-gray-500">点击“开始创建”后生成</span>
              <button v-if="mailbox?.email" type="button" class="btn btn-secondary p-2" title="复制邮箱" @click="copyText(mailbox.email)"><Icon name="copy" size="sm" :stroke-width="2" /></button>
            </div>
            <div class="mt-4 flex items-end gap-3">
              <div class="min-w-0 flex-1">
                <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">邮箱验证码</label>
                <input :value="mailboxCode || ''" readonly class="input w-full font-mono tracking-[0.2em]" :placeholder="mailboxCodeLoading ? '正在检查...' : '等待邮件到达'" />
              </div>
              <button v-if="mailboxCode" type="button" class="btn btn-secondary flex items-center gap-2" @click="copyText(mailboxCode)"><Icon name="copy" size="sm" :stroke-width="2" /><span>复制</span></button>
            </div>
            <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ mailboxCodeHint }}</p>
          </div>

          <button v-if="!isStarted" type="button" class="btn btn-primary mt-5 flex w-full items-center justify-center gap-2" :disabled="busy || !mailboxConfigured" @click="startFlow">
            <Icon v-if="busy" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
            <Icon v-else name="play" size="sm" :stroke-width="2" />
            <span>{{ mailboxConfigured ? '开始创建' : '邮箱服务未配置' }}</span>
          </button>
        </div>

        <div class="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/20 dark:text-amber-200">
          <div class="flex gap-3">
            <Icon name="shield" size="sm" class="mt-0.5 flex-shrink-0" :stroke-width="2" />
            <div>
              <div class="font-semibold">需要你在 OpenAI 页面完成验证</div>
              <p class="mt-1 text-xs leading-5">XIASS 不会代填或绕过 CAPTCHA、短信、身份验证或工作区操作。请使用浏览器打开授权页，完成页面提示后粘贴回调 URL。</p>
            </div>
          </div>
        </div>
      </section>

        <section class="space-y-5">
        <div class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <div class="mb-4 flex items-center justify-between gap-3">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">OpenAI 授权</h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">授权链接只在当前会话中使用。</p>
            </div>
            <button type="button" class="btn btn-secondary flex items-center gap-2" :disabled="!authUrl" @click="copyAuthUrl"><Icon name="copy" size="sm" :stroke-width="2" /><span>复制到服务器浏览器</span></button>
          </div>
          <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">授权链接</label>
          <textarea v-model="authUrl" readonly rows="4" class="input w-full resize-none break-all font-mono text-xs" placeholder="开始流程后生成"></textarea>
          <div class="mt-4 flex items-center gap-2">
            <button type="button" class="btn btn-secondary flex items-center gap-2" :disabled="!authUrl" @click="copyText(authUrl)"><Icon name="copy" size="sm" :stroke-width="2" /><span>复制链接</span></button>
            <span v-if="oauthState" class="truncate text-xs text-gray-400 dark:text-gray-500">state: {{ oauthState.slice(0, 12) }}...</span>
          </div>
        </div>

        <!-- Reuse the OAuth receiver surface and composable. This keeps Team
             child phone allocation, polling, billing, and confirmation rules
             identical to the existing OpenAI authorization flow. -->
        <PixlabSMSReceiver :active="Boolean(authUrl)" />

        <div class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">导入 XIASS</h2>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">只接受完整回调 URL，系统会校验 state 后调用既有导入接口。</p>
          <label class="mt-4 mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">完整回调 URL</label>
          <textarea v-model="callbackURL" rows="4" class="input w-full resize-none font-mono text-xs" placeholder="https://.../callback?code=...&state=..."></textarea>
          <p v-if="parsedCallbackError" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ parsedCallbackError }}</p>

          <div class="mt-4">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">账号名称</label>
              <input v-model="accountName" class="input w-full" :placeholder="mailbox?.email || 'OpenAI OAuth Account'" />
            </div>
          </div>
          <div class="mt-4">
            <GroupSelector v-model="selectedGroupIDs" :groups="groups" platform="openai" />
          </div>
          <div class="mt-4 grid gap-4 sm:grid-cols-2">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">并发数</label>
              <input value="10" readonly class="input w-full bg-gray-50 dark:bg-dark-900" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">优先级</label>
              <input value="1" readonly class="input w-full bg-gray-50 dark:bg-dark-900" />
            </div>
          </div>
          <button type="button" class="btn btn-primary mt-5 flex w-full items-center justify-center gap-2" :disabled="!canImport || importing" @click="importAccount">
            <Icon v-if="importing" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
            <Icon v-else name="upload" size="sm" :stroke-width="2" />
            <span>{{ importing ? '正在导入...' : '校验并导入' }}</span>
          </button>
        </div>

        <div v-if="createdAccount" class="rounded-lg border border-green-200 bg-green-50 p-5 dark:border-green-900/60 dark:bg-green-950/20">
          <div class="flex items-start gap-3">
            <Icon name="check" size="md" class="mt-0.5 text-green-600 dark:text-green-400" :stroke-width="2.5" />
            <div class="min-w-0">
              <div class="font-semibold text-green-800 dark:text-green-200">已导入 XIASS</div>
              <div class="mt-1 break-all text-sm text-green-700 dark:text-green-300">{{ createdAccount.name || mailbox?.email }}</div>
              <div v-if="createdAccount.id" class="mt-1 text-xs text-green-700/80 dark:text-green-300/80">账号 ID：{{ createdAccount.id }}</div>
              <router-link to="/admin/accounts" class="mt-3 inline-flex items-center gap-1 text-sm font-medium text-green-700 hover:underline dark:text-green-300">前往账号管理 <Icon name="arrowRight" size="sm" :stroke-width="2" /></router-link>
            </div>
          </div>
        </div>
        </section>
      </div>

      <TeamChildMembersWorkspace
        v-if="membersReady && !browserVisible"
        :members="members"
        :pending-invites="pendingInvites"
        :loading="membersLoading"
        :error="membersError"
        :seat-email="seatEmail"
        :workspace-name="workspaceName"
        @refresh="refreshMembers"
        @inspect="inspectSeat"
        @invite="inviteMember"
        @edit="editMember"
        @remove="removeMember"
        @open-browser="browserVisible = true"
      />
      <TeamChildBrowserWorkspace
        v-else
        :configured="browserConfigured"
        :embed-url="browserEmbedURL"
        :loading="browserLoading"
        :error="browserError"
        :mailbox-email="mailbox?.email || ''"
        :members-ready="membersReady"
        @reload="reloadAll"
        @copy-mailbox="copyMailboxEmail"
        @open-modular="browserVisible = false"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Icon } from '@/components/icons'
import TeamChildBrowserWorkspace from '@/components/admin/account/TeamChildBrowserWorkspace.vue'
import TeamChildMembersWorkspace from '@/components/admin/account/TeamChildMembersWorkspace.vue'
import PixlabSMSReceiver from '@/components/account/PixlabSMSReceiver.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { teamChildAPI, type TeamChildMailbox, type TeamChildMember } from '@/api/admin/teamChild'
import { groupsAPI } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'

type FlowStatus = 'idle' | 'creating' | 'waiting' | 'polling' | 'received' | 'importing' | 'completed' | 'error'
type StepKey = 'mailbox' | 'auth' | 'callback' | 'import'

const appStore = useAppStore()
const status = ref<FlowStatus>('idle')
const mailboxConfigured = ref(false)
const browserConfigured = ref(false)
const browserEmbedURL = ref('')
const browserLoading = ref(false)
const browserError = ref('')
const browserVisible = ref(false)
const membersReady = ref(false)
const membersLoading = ref(false)
const membersError = ref('')
const members = ref<TeamChildMember[]>([])
const pendingInvites = ref(0)
const seatEmail = ref('')
const workspaceName = ref('')
const configImporting = ref(false)
const mailboxConfigFileInput = ref<HTMLInputElement | null>(null)
const mailbox = ref<TeamChildMailbox | null>(null)
const mailboxCode = ref('')
const mailboxCodeLoading = ref(false)
const authUrl = ref('')
const oauthState = ref('')
const oauthSessionID = ref('')
const callbackURL = ref('')
const accountName = ref('')
const groups = ref<AdminGroup[]>([])
const selectedGroupIDs = ref<number[]>([])
const errorMessage = ref('')
const createdAccount = ref<{ id?: number; name?: string; [key: string]: unknown } | null>(null)
let pollTimer: ReturnType<typeof setTimeout> | null = null

const steps: Array<{ key: StepKey; label: string; description: string }> = [
  { key: 'mailbox', label: '生成临时邮箱', description: '由服务器端邮箱服务创建并保管访问令牌。' },
  { key: 'auth', label: '完成 OpenAI 授权', description: '在浏览器中完成登录、验证码、手机号和工作区提示。' },
  { key: 'callback', label: '粘贴授权回调', description: '粘贴完整 URL，系统校验 code 和 state。' },
  { key: 'import', label: '导入 XIASS', description: '使用固定并发数 10、优先级 1 创建一个 OpenAI OAuth 账号。' }
]

const busy = computed(() => ['creating', 'polling', 'importing'].includes(status.value))
const isStarted = computed(() => Boolean(mailbox.value))
const importing = computed(() => status.value === 'importing')
const statusLabel = computed(() => ({ idle: '未开始', creating: '创建中', waiting: '等待验证邮件', polling: '检查邮箱', received: '已收到验证码', importing: '正在导入', completed: '已完成', error: '需要处理' })[status.value])
const statusTone = computed(() => {
  if (status.value === 'error') return { text: 'text-red-600 dark:text-red-400', icon: 'exclamationTriangle' as const }
  if (status.value === 'completed' || status.value === 'received') return { text: 'text-green-600 dark:text-green-400', icon: 'check' as const }
  if (busy.value) return { text: 'text-primary-600 dark:text-primary-400', icon: 'refresh' as const }
  return { text: 'text-gray-500 dark:text-gray-400', icon: 'clock' as const }
})
const mailboxCodeHint = computed(() => mailboxCode.value ? '验证码已从服务器端邮箱正文中提取。' : mailboxCodeLoading.value ? '每 4 秒检查一次新邮件。' : '完成 OpenAI 页面邮箱验证后，这里会自动显示验证码。')
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
const canImport = computed(() => status.value === 'received' && Boolean(parsedCallback.value) && !parsedCallbackError.value && Boolean(mailbox.value) && Boolean(oauthSessionID.value))

function isStepComplete(key: StepKey) {
  if (key === 'mailbox') return Boolean(mailbox.value)
  if (key === 'auth') return Boolean(authUrl.value)
  if (key === 'callback') return canImport.value
  return Boolean(createdAccount.value)
}
function stepClass(key: StepKey) {
  if (isStepComplete(key)) return 'border-green-500 bg-green-500 text-white'
  if ((key === 'mailbox' && isStarted.value) || (key === 'auth' && authUrl.value) || (key === 'callback' && callbackURL.value)) return 'border-primary-500 bg-primary-50 text-primary-600 dark:bg-primary-950/30 dark:text-primary-400'
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
function copyAuthUrl() {
  if (!authUrl.value) return
  void copyText(authUrl.value)
  appStore.showInfo('授权链接已复制，请粘贴到右侧服务器浏览器的地址栏。')
}
async function reloadBrowserWorkspace() {
  if (!browserConfigured.value || browserLoading.value) return
  browserLoading.value = true
  browserError.value = ''
  try {
    const session = await teamChildAPI.createBrowserSession()
    browserEmbedURL.value = session.embed_url
  } catch (error) {
    browserError.value = extractApiErrorMessage(error, '无法连接服务器浏览器，请检查部署状态。')
  } finally {
    browserLoading.value = false
  }
}
async function refreshMembers(notify = true, force = false) {
  if (membersLoading.value && !force) return
  membersLoading.value = true
  membersError.value = ''
  try {
    const result = await teamChildAPI.refreshTeamChildMembers()
    members.value = result.members || []
    pendingInvites.value = result.pending_invites || 0
    seatEmail.value = result.seat_email || ''
    workspaceName.value = result.workspace_name || ''
    membersReady.value = Boolean(result.ready)
    if (membersReady.value) browserVisible.value = false
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
    members.value = result.members || []
    pendingInvites.value = result.pending_invites || 0
    seatEmail.value = result.seat_email || ''
    workspaceName.value = result.workspace_name || ''
    membersReady.value = Boolean(result.ready)
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
    appStore.showSuccess('成员已从工作空间移除')
  } catch (error) { appStore.showError(extractApiErrorMessage(error, '移除成员失败')) } finally { membersLoading.value = false }
}
async function reloadAll() {
  await reloadBrowserWorkspace()
  await refreshMembers()
}
async function startFlow() {
  if (busy.value || !mailboxConfigured.value) return
  errorMessage.value = ''; createdAccount.value = null; authUrl.value = ''; oauthState.value = ''; oauthSessionID.value = ''; callbackURL.value = ''; mailboxCode.value = ''; status.value = 'creating'
  try {
    mailbox.value = await teamChildAPI.createMailbox()
    accountName.value = mailbox.value.email
    const auth = await teamChildAPI.generateOpenAIAuthUrl()
    authUrl.value = auth.auth_url
    oauthSessionID.value = auth.session_id
    oauthState.value = new URL(auth.auth_url).searchParams.get('state') || ''
    status.value = 'waiting'
    schedulePoll()
  } catch (error) { status.value = 'error'; errorMessage.value = extractApiErrorMessage(error, '流程启动失败') }
}
function schedulePoll() {
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = setTimeout(pollMailbox, 4000)
}
async function pollMailbox() {
  if (!mailbox.value || mailboxCode.value || status.value === 'completed') return
  mailboxCodeLoading.value = true; status.value = 'polling'
  try {
    const result = await teamChildAPI.pollMailboxCode(mailbox.value.session_id)
    if (result.status === 'received' && result.code) { mailboxCode.value = result.code; status.value = 'received' } else { status.value = 'waiting'; schedulePoll() }
  } catch (error) { status.value = 'error'; errorMessage.value = extractApiErrorMessage(error, '邮箱检查失败') } finally { mailboxCodeLoading.value = false }
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
  if (mailbox.value) await teamChildAPI.deleteMailboxSession(mailbox.value.session_id).catch(() => undefined)
  mailbox.value = null; mailboxCode.value = ''; authUrl.value = ''; oauthState.value = ''; oauthSessionID.value = ''; callbackURL.value = ''; accountName.value = ''; selectedGroupIDs.value = []; createdAccount.value = null; errorMessage.value = ''; status.value = 'idle'
}

onMounted(async () => {
  const [statusResult, groupsResult] = await Promise.allSettled([
    teamChildAPI.getMailboxStatus(),
    groupsAPI.getAll('openai')
  ])
  mailboxConfigured.value = statusResult.status === 'fulfilled' && Boolean(statusResult.value.configured)
  browserConfigured.value = statusResult.status === 'fulfilled' && Boolean(statusResult.value.browser_configured)
  if (groupsResult.status === 'fulfilled') groups.value = groupsResult.value
  if (browserConfigured.value) {
    await reloadBrowserWorkspace()
    await refreshMembers(false)
  }
})
onBeforeUnmount(() => { if (pollTimer) clearTimeout(pollTimer) })
</script>

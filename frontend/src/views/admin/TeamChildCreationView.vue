<template>
  <div class="team-child-page mx-auto w-full max-w-[1680px] min-w-0 space-y-5 p-3 sm:space-y-6 sm:p-4 md:p-6">
    <header class="flex min-w-0 flex-col gap-3 border-b border-gray-200 pb-5 dark:border-dark-700 sm:flex-row sm:items-end sm:justify-between">
      <div>
        <div class="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-primary-600 dark:text-primary-400">
          <Icon name="users" size="sm" :stroke-width="2" />
          <span>管理员工作流</span>
        </div>
        <h1 class="whitespace-nowrap text-2xl font-semibold text-gray-900 dark:text-gray-100">Team 子号创建</h1>
        <p class="mt-1 max-w-2xl text-sm text-gray-500 dark:text-gray-400">
          悬浮进度卡持续显示当前节点，实际操作集中在下方工作区。
        </p>
      </div>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <div class="flex min-w-[6.5rem] items-center gap-2 whitespace-nowrap text-sm" :class="statusTone.text">
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

    <div class="team-child-layout grid min-w-0 gap-5 xl:grid-cols-[minmax(340px,420px)_minmax(0,1fr)]">
      <TeamChildOAuthWorkspace
        v-if="oauthWorkspaceVisible && teamWorkflow"
        class="xl:col-span-2"
        :workflow="teamWorkflow"
        :mailbox-email="mailbox?.email || ''"
        :auth-url="authUrl || ''"
        :callback-url="callbackURL"
        :show-reauthorize="workflowNeedsReauth"
        :revealed-password="revealedWorkflowPassword"
        :password-loading="passwordLoading"
        :show-one-click="true"
        :one-click-disabled="workspaceOneClickDisabled"
        :one-click-label="workspaceOneClickLabel"
        :history-mailboxes="knownMailboxes"
        :selected-mailbox-email="selectedMailboxEmail"
        :mailbox-code="mailboxCode"
        :mailbox-code-loading="mailboxCodeLoading"
        :mailbox-selecting="mailboxSelecting"
        :mailbox-configured="mailboxConfigured"
        :mailbox-actions-disabled="workflowBusy"
        :create-mailbox-disabled="busy || Boolean(teamWorkflow)"
        :browser-visible="browserVisible"
        :browser-configured="browserConfigured"
        :browser-embed-url="browserEmbedURL"
        :browser-loading="browserLoading"
        :browser-error="browserError"
        :browser-control-conflict="browserControlConflict"
        @session-cancelled="restartWorkflowOAuth"
        @reauthorize="restartWorkflowOAuth"
        @one-click="handleWorkspaceOneClick"
        @open-browser="openBrowserWorkspace"
        @continue-workflow="continueWorkflow"
        @reveal-password="revealWorkflowPassword"
        @clear-password="clearRevealedPassword"
        @copy-password="copyText(revealedWorkflowPassword)"
        @copy-auth="copyText(authUrl || '')"
        @copy-callback="copyText(callbackURL)"
        @copy-mailbox-code="copyText"
        @phone-ready="submitWorkflowPhone"
        @sms-code-received="submitWorkflowSMSCode"
        @select-mailbox="selectedMailboxEmail = $event"
        @create-mailbox="startFlow"
        @open-mailbox="openHistoryMailbox"
        @poll-mailbox="pollMailbox"
        @copy-mailbox="copyText"
        @open-history="scrollToTeamChildHistory"
        @reload-browser="reloadBrowserWorkspace"
        @open-modular="closeBrowserWorkspace"
        @force-take-over="browserTakeOverConfirmOpen = true"
      />

      <div class="min-w-0 space-y-5">
        <section class="space-y-5">
        <div class="team-child-panel rounded-lg border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
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

          <ol v-if="!teamWorkflow" class="space-y-4">
            <li v-for="(item, index) in steps" :key="item.key" class="flex gap-3">
              <div class="flex flex-col items-center">
                <div class="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full border text-xs font-semibold" :class="stepClass(item.key)">
                  <Icon v-if="isStepComplete(item.key)" name="check" size="sm" :stroke-width="2.5" />
                  <span v-else>{{ index + 1 }}</span>
                </div>
                <div v-if="index < steps.length - 1" class="mt-1 h-full min-h-6 w-px bg-gray-200 dark:bg-dark-600"></div>
              </div>
              <div class="min-w-0 flex-1 pb-3">
                <div class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ item.label }}</div>
                <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ item.description }}</div>
              </div>
            </li>
          </ol>

          <div class="team-child-mailbox-panel mt-3 rounded-lg border border-dashed border-gray-300 bg-gray-50 p-3 dark:border-dark-600 dark:bg-dark-900/40">
            <div class="flex items-center justify-between gap-3">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">当前邮箱</span>
              <span v-if="mailbox?.expires_at" class="text-xs text-gray-400 dark:text-gray-500">有效期至 {{ formatTime(mailbox.expires_at) }}</span>
            </div>
            <div class="mt-1 min-w-0">
              <button v-if="mailbox?.email" type="button" class="team-child-mailbox-copy flex min-w-0 w-full items-center rounded-md px-2 py-1 text-left text-sm font-semibold text-gray-900 hover:bg-gray-100 hover:text-primary-600 dark:text-gray-100 dark:hover:bg-dark-800 dark:hover:text-primary-300" :title="`点击邮箱框复制 ${mailbox.email}`" @click="copyText(mailbox.email)">{{ mailbox.email }}</button>
              <span v-else class="text-sm text-gray-400 dark:text-gray-500">点击“获取临时邮箱”后生成</span>
            </div>
            <div class="mt-3 border-t border-gray-200 pt-3 dark:border-dark-700">
              <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">复用已有 Team 邮箱</label>
              <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2">
                <input v-model.trim="selectedMailboxEmail" list="team-child-known-mailboxes" type="email" class="input h-9 min-w-0 text-sm" placeholder="team1003@example.com" :disabled="mailboxSelecting || workflowBusy" @keyup.enter="selectExistingMailbox" />
                <datalist id="team-child-known-mailboxes">
                  <option v-for="email in knownMailboxes" :key="email" :value="email"></option>
                </datalist>
                <button type="button" class="btn btn-secondary flex h-9 items-center gap-2 whitespace-nowrap px-3" :disabled="!selectedMailboxEmail || mailboxSelecting || workflowBusy" @click="selectExistingMailbox">
                  <Icon name="mail" size="sm" :stroke-width="2" />
                  <span>{{ mailboxSelecting ? '打开中' : '打开邮箱' }}</span>
                </button>
              </div>
              <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">重新签发服务端邮箱会话；邮箱访问令牌不会发送到当前浏览器。</p>
            </div>
            <div class="mt-3 grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-end gap-2">
              <div class="min-w-0 flex-1">
                <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">邮箱验证码</label>
                <input :value="mailboxCode || ''" readonly class="team-child-code-input input h-9 w-44 cursor-copy font-mono text-sm tracking-[0.16em]" :class="mailboxCode ? 'text-center' : ''" :title="mailboxCode ? '点击验证码复制' : undefined" :aria-label="mailboxCode ? '邮箱验证码，点击复制' : '邮箱验证码'" :placeholder="mailboxCodeLoading ? '正在检查...' : '等待邮件到达'" @click="mailboxCode && copyText(mailboxCode)" />
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
          <div v-if="teamWorkflow || callbackURL || createdAccount" class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
            <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">导入 XIASS</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">自动化会从服务器浏览器捕获并校验回调；若页面结构变化，可在这里粘贴完整回调 URL 进行人工兜底。</p>
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
            <div class="mt-4">
              <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">账号名称</label>
              <div class="input flex w-full items-center bg-gray-50 font-mono text-sm text-gray-700 dark:bg-dark-900/50 dark:text-gray-300">
                <span class="min-w-0 truncate">{{ mailbox?.email || '等待 Team 邮箱' }}</span>
              </div>
            </div>
            <div class="mt-4 rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-900/50">
              <div class="flex min-w-0 items-center justify-between gap-3">
                <h3 class="min-w-0 text-sm font-semibold text-gray-900 dark:text-gray-100">本次导入配置</h3>
                <span class="shrink-0 text-xs text-gray-500 dark:text-gray-400">{{ createdAccount ? '已锁定' : '可在导入前调整' }}</span>
              </div>
              <dl class="mt-3 grid min-w-0 gap-3 text-xs sm:grid-cols-3">
                <div class="min-w-0">
                  <dt class="text-gray-500 dark:text-gray-400">分组</dt>
                  <dd class="mt-1 truncate font-medium text-gray-900 dark:text-gray-100" :title="displayedGroupsLabel">{{ displayedGroupsLabel }}</dd>
                </div>
                <div>
                  <dt class="text-gray-500 dark:text-gray-400">并发数</dt>
                  <dd class="mt-1 font-semibold tabular-nums text-gray-900 dark:text-gray-100">{{ displayedImportConfig.concurrency }}</dd>
                </div>
                <div>
                  <dt class="text-gray-500 dark:text-gray-400">优先级</dt>
                  <dd class="mt-1 font-semibold tabular-nums text-gray-900 dark:text-gray-100">{{ displayedImportConfig.priority }}</dd>
                </div>
              </dl>
            </div>
            <fieldset :disabled="Boolean(createdAccount)" class="mt-4 min-w-0"><GroupSelector v-model="selectedGroupIDs" :groups="groups" platform="openai" /></fieldset>
            <div class="mt-4 grid min-w-0 gap-4 sm:grid-cols-2"><div class="min-w-0"><label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">并发数</label><input v-model.number="accountConcurrency" type="number" min="1" max="1000" class="input w-full" :disabled="Boolean(createdAccount)" /></div><div class="min-w-0"><label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">优先级</label><input v-model.number="accountPriority" type="number" min="1" max="999" class="input w-full" :disabled="Boolean(createdAccount)" /></div></div>
            <button v-if="!createdAccount" type="button" class="btn btn-primary mt-5 flex w-full items-center justify-center gap-2 whitespace-nowrap" :disabled="!canImport || importing" @click="importAccount"><Icon v-if="importing" name="refresh" size="sm" class="animate-spin" :stroke-width="2" /><Icon v-else name="upload" size="sm" :stroke-width="2" /><span>{{ importing ? '正在导入...' : '校验并导入' }}</span></button>
            <div v-else class="mt-5 min-w-0 rounded-lg border border-green-200 bg-green-50 p-4 dark:border-green-900/60 dark:bg-green-950/20">
              <div class="flex min-w-0 items-start gap-3">
                <Icon name="check" size="md" class="mt-0.5 shrink-0 text-green-600 dark:text-green-400" :stroke-width="2.5" />
                <div class="min-w-0 flex-1">
                  <div class="font-semibold text-green-800 dark:text-green-200">已导入 XIASS</div>
                  <div class="mt-1 break-all text-sm text-green-700 dark:text-green-300">{{ createdAccount.name || createdAccount.email || mailbox?.email }}</div>
                </div>
              </div>
              <dl class="mt-4 grid min-w-0 gap-3 border-t border-green-200 pt-3 text-xs dark:border-green-900/60 sm:grid-cols-2">
                <div class="min-w-0">
                  <dt class="text-green-700/70 dark:text-green-300/70">状态</dt>
                  <dd class="mt-1 font-semibold text-green-800 dark:text-green-200">{{ createdAccount.status || '已创建' }}</dd>
                </div>
                <div v-if="createdAccount.id" class="min-w-0">
                  <dt class="text-green-700/70 dark:text-green-300/70">账号 ID</dt>
                  <dd class="mt-1 font-semibold tabular-nums text-green-800 dark:text-green-200">{{ createdAccount.id }}</dd>
                </div>
                <div class="min-w-0 sm:col-span-2">
                  <dt class="text-green-700/70 dark:text-green-300/70">分组</dt>
                  <dd class="mt-1 break-words font-semibold text-green-800 dark:text-green-200">{{ displayedGroupsLabel }}</dd>
                </div>
                <div>
                  <dt class="text-green-700/70 dark:text-green-300/70">并发数</dt>
                  <dd class="mt-1 font-semibold tabular-nums text-green-800 dark:text-green-200">{{ displayedImportConfig.concurrency }}</dd>
                </div>
                <div>
                  <dt class="text-green-700/70 dark:text-green-300/70">优先级</dt>
                  <dd class="mt-1 font-semibold tabular-nums text-green-800 dark:text-green-200">{{ displayedImportConfig.priority }}</dd>
                </div>
              </dl>
              <router-link to="/admin/accounts" class="mt-4 inline-flex max-w-full items-center gap-1 text-sm font-medium text-green-700 hover:underline dark:text-green-300"><span class="truncate">前往账号管理</span> <Icon name="arrowRight" size="sm" :stroke-width="2" /></router-link>
            </div>
          </div>
        </section>
      </div>

      <TeamChildMembersWorkspace
        v-if="!teamWorkflow && !browserVisible"
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
      />
      <TeamChildBrowserWorkspace
        v-else-if="!teamWorkflow && browserVisible"
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

      <TeamChildHistoryPanel
        ref="teamChildHistoryPanelRef"
        class="xl:col-span-2"
        :entries="teamChildHistory"
        :active-mailbox-email="mailbox?.email || ''"
        :opening-email="historyOpeningEmail"
        :loading="teamChildHistoryLoading"
        :password-loading-account-id="historyPasswordLoadingAccountID"
        :revealed-passwords="revealedHistoryPasswords"
        @refresh="loadTeamChildHistory"
        @open-mailbox="openHistoryMailbox"
        @toggle-password="toggleHistoryPassword"
        @copy-password="copyText"
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
    <TotpStepUpDialog :controller="passwordStepUp" />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Icon } from '@/components/icons'
import TeamChildBrowserWorkspace from '@/components/admin/account/TeamChildBrowserWorkspace.vue'
import TeamChildOAuthWorkspace from '@/components/admin/account/TeamChildOAuthWorkspace.vue'
import TeamChildMembersWorkspace from '@/components/admin/account/TeamChildMembersWorkspace.vue'
import TeamChildHistoryPanel from '@/components/admin/account/TeamChildHistoryPanel.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useAppStore } from '@/stores/app'
import { useOpenAIOAuth } from '@/composables/useOpenAIOAuth'
import { isStepUpBlocked, isStepUpCancelled, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import { teamChildAPI, type TeamChildMailbox, type TeamChildMember, type TeamChildWorkflow } from '@/api/admin/teamChild'
import { accountsAPI } from '@/api/admin/accounts'
import { groupsAPI } from '@/api/admin/groups'
import type { Account, AccountUsageInfo, AdminGroup } from '@/types'

type FlowStatus = 'idle' | 'creating' | 'ready' | 'workflow' | 'waiting' | 'polling' | 'received' | 'callback' | 'importing' | 'completed' | 'error'
type StepKey = 'mailbox' | 'replace' | 'verify' | 'import'

const appStore = useAppStore()
const openaiOAuth = useOpenAIOAuth()
const passwordStepUp = useStepUp()
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
const browserHeartbeatFailures = ref(0)
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
const teamChildHistoryPanelRef = ref<InstanceType<typeof TeamChildHistoryPanel> | null>(null)
const mailbox = ref<TeamChildMailbox | null>(null)
const knownMailboxes = ref<string[]>([])
const teamChildHistory = ref<Array<{ email: string; account: Account | null; usage: AccountUsageInfo | null; passwordAvailable: boolean }>>([])
const teamChildHistoryLoading = ref(false)
const historyOpeningEmail = ref('')
const historyPasswordLoadingAccountID = ref<number | null>(null)
const revealedHistoryPasswords = ref<Record<number, string>>({})
const selectedMailboxEmail = ref('')
const mailboxSelecting = ref(false)
const mailboxCode = ref('')
const mailboxCodeLoading = ref(false)
const mailboxCodeError = ref('')
const authUrl = openaiOAuth.authUrl
const oauthState = openaiOAuth.oauthState
const oauthSessionID = openaiOAuth.sessionId
const oauthGenerating = openaiOAuth.loading
const oauthError = openaiOAuth.error
const callbackURL = ref('')
const groups = ref<AdminGroup[]>([])
const selectedGroupIDs = ref<number[]>([])
const accountConcurrency = ref(10)
const accountPriority = ref(1)
const errorMessage = ref('')
const createdAccount = ref<{ id?: number; name?: string; [key: string]: unknown } | null>(null)
const appliedImportConfig = ref<{ groupIDs: number[]; groupNames: string[]; concurrency: number; priority: number } | null>(null)
const teamWorkflow = ref<TeamChildWorkflow | null>(null)
const revealedWorkflowPassword = ref('')
const passwordLoading = ref(false)
const workflowConfirmOpen = ref(false)
const workflowStarting = ref(false)
const workflowContinuing = ref(false)
const callbackConfirming = ref(false)
const emailCodeSubmitting = ref(false)
const phoneSubmitting = ref(false)
const smsCodeSubmitting = ref(false)
const lastSubmittedEmailCode = ref('')
const lastSubmittedPhone = ref('')
const lastSubmittedSMSCode = ref('')
let pollTimer: ReturnType<typeof setTimeout> | null = null
let workflowPollTimer: ReturnType<typeof setTimeout> | null = null
let browserHeartbeatTimer: ReturnType<typeof setTimeout> | null = null

const steps: Array<{ key: StepKey; label: string; description: string }> = [
  { key: 'mailbox', label: '生成临时邮箱', description: '由服务器端邮箱服务创建并保管访问令牌。' },
  { key: 'replace', label: '确认 Team 席位', description: '确认后替换选中的普通成员；若普通席位已人工腾出，会跳过移除并仅发送邀请。' },
  { key: 'verify', label: '完成 OpenAI 授权', description: '使用 XIASS 官方 PKCE 链路，自动完成邮箱、手机号、资料和工作空间节点。' },
  { key: 'import', label: '导入 XIASS', description: '按当前勾选的分组、并发和优先级创建 OpenAI OAuth 账号。' }
]

const busy = computed(() => ['creating', 'polling', 'workflow', 'importing'].includes(status.value) || workflowStarting.value || workflowContinuing.value)
// Mailbox polling is a background task and must not make workflow controls
// appear, disappear, or resize while the refresh icon is spinning.
const isStarted = computed(() => Boolean(mailbox.value))
const importing = computed(() => status.value === 'importing')
const statusLabel = computed(() => ({ idle: '未开始', creating: '创建中', ready: '等待授权', workflow: '自动化执行中', waiting: '等待当前节点', polling: '检查邮箱', received: '已收到验证码', callback: '已获取回调', importing: '正在导入', completed: '已完成', error: '需要处理' })[status.value])
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
const workflowBusy = computed(() => workflowStarting.value || workflowContinuing.value || teamWorkflow.value?.status === 'running')
const workspaceOneClickLabel = computed(() => {
  if (workflowContinuing.value || teamWorkflow.value?.status === 'running') return '自动执行中'
  if (teamWorkflow.value?.status === 'failed') return '继续自动化'
  if (teamWorkflow.value?.status === 'callback_ready') return '准备导入'
  if (teamWorkflow.value?.status === 'completed') return '已完成'
  return '一键授权'
})
const workspaceOneClickDisabled = computed(() => teamWorkflow.value?.status !== 'failed' || workflowContinuing.value)
const oauthWorkspaceVisible = computed(() => {
  return Boolean(teamWorkflow.value)
})
const workflowNeedsReauth = computed(() => {
  const workflow = teamWorkflow.value
  if (!workflow || !['manual_required', 'failed'].includes(workflow.status)) return false
  const messages = [workflow.error || '', ...workflow.nodes.map((node) => node.message || '')].join(' ')
  return /\b401\b|unauthori[sz]ed|token\s*(?:已|已被)?(?:失效|过期)|重新授权/i.test(messages)
})
const manualSeatReady = computed(() => membersReady.value && members.value.length > 0 && !members.value.some((member) => isReplaceableMember(member)) && members.value.every(isProtectedMember))
const workflowReady = computed(() => Boolean(mailbox.value?.email) && Boolean(authUrl.value) && (isReplaceableMember(selectedReplaceableMember.value) || manualSeatReady.value) && membersReady.value && !workflowBusy.value && !['manual_required', 'callback_ready', 'failed'].includes(teamWorkflow.value?.status || ''))
const workflowConfirmationTitle = computed(() => manualSeatReady.value ? '确认邀请并授权' : '确认替换成员并授权')
const workflowConfirmationMessage = computed(() => {
  if (!mailbox.value?.email) return '当前缺少临时邮箱。'
  if (manualSeatReady.value) return `已实时确认普通成员席位已由人工腾出，当前仅剩受保护成员。将不移除任何成员，直接向 ${mailbox.value.email} 发送邀请，并准备官方 OAuth 链接。`
  if (!selectedMemberEmail.value) return '当前缺少待替换成员。'
  return `将从 ChatGPT 工作区移除 ${selectedMemberEmail.value}，随后向 ${mailbox.value.email} 发送邀请，并准备官方 OAuth 链接。已完成的外部操作不会自动回滚。`
})
const canImport = computed(() => Boolean(parsedCallback.value) && !parsedCallbackError.value && Boolean(mailbox.value) && Boolean(oauthSessionID.value) && Boolean(teamWorkflow.value?.id) && !importing.value)
const canConfirmCallback = computed(() => Boolean(teamWorkflow.value) && Boolean(parsedCallback.value) && !parsedCallbackError.value && !callbackConfirming.value)
const normalizedConcurrency = computed(() => Math.min(1000, Math.max(1, Math.trunc(Number(accountConcurrency.value) || 10))))
const normalizedPriority = computed(() => Math.min(999, Math.max(1, Math.trunc(Number(accountPriority.value) || 1))))
const selectedGroupNames = computed(() => selectedGroupIDs.value
  .map((id) => groups.value.find((group) => group.id === id)?.name || `#${id}`))
const displayedImportConfig = computed(() => appliedImportConfig.value || {
  groupIDs: selectedGroupIDs.value,
  groupNames: selectedGroupNames.value,
  concurrency: normalizedConcurrency.value,
  priority: normalizedPriority.value
})
const displayedGroupsLabel = computed(() => displayedImportConfig.value.groupNames.length > 0 ? displayedImportConfig.value.groupNames.join('、') : '未选择分组')

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
  if (key === 'replace') return teamWorkflow.value?.nodes.find((item) => item.key === 'invite_confirm')?.status === 'completed'
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
async function loadKnownMailboxes() {
  if (!mailboxConfigured.value) return
  try {
    knownMailboxes.value = await teamChildAPI.listMailboxes()
  } catch {
    knownMailboxes.value = []
  }
}

function teamChildAccountEmail(account: Account): string {
  const extra = account.extra as Record<string, unknown> | undefined
  const stored = typeof extra?.xiass_team_child_email === 'string' ? extra.xiass_team_child_email : ''
  const candidate = stored || account.name
  return normalizeTeamChildEmail(candidate)
}

async function loadTeamChildHistory() {
  if (teamChildHistoryLoading.value) return
  teamChildHistoryLoading.value = true
  try {
    const [mailboxResult, accountResult] = await Promise.allSettled([
      teamChildAPI.listMailboxes(),
      accountsAPI.list(1, 200, { platform: 'openai', type: 'oauth' })
    ])
    const mailboxEmails = mailboxResult.status === 'fulfilled' ? mailboxResult.value : []
    const accounts = accountResult.status === 'fulfilled' && Array.isArray(accountResult.value?.items)
      ? accountResult.value.items
      : []
    const teamAccounts = accounts.filter((account) => {
      const extra = account.extra as Record<string, unknown> | undefined
      return extra?.xiass_team_child === true && Boolean(teamChildAccountEmail(account))
    })
    const usageResults = await Promise.allSettled(teamAccounts.map((account) => accountsAPI.getUsage(account.id, 'passive')))
    const usageByAccountID = new Map<number, AccountUsageInfo>()
    usageResults.forEach((result, index) => {
      if (result.status === 'fulfilled') usageByAccountID.set(teamAccounts[index].id, result.value)
    })
    const byEmail = new Map<string, { email: string; account: Account | null; usage: AccountUsageInfo | null; passwordAvailable: boolean }>()
    mailboxEmails.forEach((email) => {
      const normalized = normalizeTeamChildEmail(email)
      if (normalized) byEmail.set(normalized, { email: normalized, account: null, usage: null, passwordAvailable: false })
    })
    teamAccounts.forEach((account) => {
      const email = teamChildAccountEmail(account)
      if (!email) return
      const existing = byEmail.get(email)
      byEmail.set(email, {
        email,
        account,
        usage: usageByAccountID.get(account.id) || null,
        passwordAvailable: account.credentials_status?.has_xiass_team_child_password_encrypted === true || existing?.passwordAvailable === true
      })
    })
    if (mailbox.value?.email) {
      const email = normalizeTeamChildEmail(mailbox.value.email)
      if (email && !byEmail.has(email)) byEmail.set(email, { email, account: null, usage: null, passwordAvailable: false })
    }
    teamChildHistory.value = [...byEmail.values()].sort((left, right) => left.email.localeCompare(right.email))
  } catch {
    // History is supplementary. A stale usage endpoint or an expired mailbox
    // session must never delay or interrupt member/OAuth workflow startup.
    teamChildHistory.value = mailbox.value?.email
      ? [{ email: normalizeTeamChildEmail(mailbox.value.email), account: null, usage: null, passwordAvailable: false }]
      : []
  } finally {
    teamChildHistoryLoading.value = false
  }
}

async function openHistoryMailbox(email: string) {
  if (workflowBusy.value) {
    appStore.showInfo('当前自动化正在运行，完成或停止后再切换邮箱')
    return
  }
  historyOpeningEmail.value = email
  selectedMailboxEmail.value = email
  try {
    await selectExistingMailbox()
  } finally {
    historyOpeningEmail.value = ''
  }
}

function scrollToTeamChildHistory() {
  const element = teamChildHistoryPanelRef.value?.$el as HTMLElement | undefined
  element?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function toggleHistoryPassword(entry: { email: string; account: Account | null }) {
  const account = entry.account
  if (!account) return
  if (revealedHistoryPasswords.value[account.id]) {
    const next = { ...revealedHistoryPasswords.value }
    delete next[account.id]
    revealedHistoryPasswords.value = next
    return
  }
  if (historyPasswordLoadingAccountID.value === account.id) return
  historyPasswordLoadingAccountID.value = account.id
  try {
    const secret = await passwordStepUp.run(() => teamChildAPI.revealTeamChildAccountPassword(account.id))
    if (normalizeTeamChildEmail(secret.email) !== normalizeTeamChildEmail(entry.email)) throw new Error('历史账号密码与邮箱记录不一致')
    revealedHistoryPasswords.value = { ...revealedHistoryPasswords.value, [account.id]: secret.password }
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (isStepUpBlocked(error)) {
      appStore.showError(stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? '请使用管理员网页登录会话查看密码' : '请先为当前管理员启用 TOTP 二次验证')
      return
    }
    appStore.showError(extractApiErrorMessage(error, '无法查看历史 Team 登录密码'))
  } finally {
    historyPasswordLoadingAccountID.value = null
  }
}

async function selectExistingMailbox() {
  const email = normalizeTeamChildEmail(selectedMailboxEmail.value)
  if (!email || mailboxSelecting.value || workflowBusy.value) return
  mailboxSelecting.value = true
  errorMessage.value = ''
  const previous = mailbox.value
  try {
    const selected = await teamChildAPI.selectMailbox(email)
    mailbox.value = selected
    selectedMailboxEmail.value = selected.email
    mailboxCode.value = ''
    mailboxCodeError.value = ''
    lastSubmittedEmailCode.value = ''
    schedulePoll(250)
    await loadKnownMailboxes()
    if (!authUrl.value || !oauthSessionID.value) await generateFreshOAuthSession()
    status.value = 'ready'
    if (previous && previous.session_id !== selected.session_id) {
      await teamChildAPI.deleteMailboxSession(previous.session_id).catch(() => undefined)
    }
    appStore.showSuccess(`已打开 Team 邮箱：${selected.email}`)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '无法打开已有 Team 邮箱')
  } finally {
    mailboxSelecting.value = false
  }
}

function clearRevealedPassword() {
  revealedWorkflowPassword.value = ''
}

async function revealWorkflowPassword() {
  const workflow = teamWorkflow.value
  if (!workflow?.password_available || passwordLoading.value) return
  passwordLoading.value = true
  try {
    const secret = await passwordStepUp.run(() => teamChildAPI.revealTeamChildWorkflowPassword(workflow.id))
    if (!mailbox.value || normalizeTeamChildEmail(secret.email) !== normalizeTeamChildEmail(mailbox.value.email)) {
      throw new Error('工作流密码邮箱与当前邮箱不一致')
    }
    revealedWorkflowPassword.value = secret.password
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (isStepUpBlocked(error)) {
      appStore.showError(stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? '请使用管理员网页登录会话查看密码' : '请先为当前管理员启用 TOTP 二次验证')
      return
    }
    appStore.showError(extractApiErrorMessage(error, '无法查看当前 Team 登录密码'))
  } finally {
    passwordLoading.value = false
  }
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
  const retryDelay = browserHeartbeatFailures.value > 0
    ? Math.min(15_000, 2_500 * (2 ** Math.min(browserHeartbeatFailures.value - 1, 2)))
    : 45_000
  browserHeartbeatTimer = setTimeout(() => void heartbeatBrowserControl(), retryDelay)
}
async function heartbeatBrowserControl() {
  if (!browserVisible.value || !browserControllerID.value) return
  try {
    await teamChildAPI.heartbeatTeamChildBrowserControl(browserControllerID.value)
    browserHeartbeatFailures.value = 0
    scheduleBrowserHeartbeat()
  } catch (error) {
    const errorCode = extractApiErrorCode(error)
    if (errorCode === 'TEAM_CHILD_BROWSER_CONTROL_LOST' || errorCode === 'TEAM_CHILD_BROWSER_CONTROLLED') {
      clearBrowserHeartbeat()
      browserEmbedURL.value = ''
      browserControlConflict.value = true
      browserError.value = '浏览器控制权已由其他设备接管。请返回自动化工作区，或在确认后重新接管。'
      return
    }

    // A single timeout/502 must not tear down the visible iframe. Retry the
    // lease quickly while keeping the current Chromium page available for
    // copying and manual recovery.
    browserHeartbeatFailures.value += 1
    if (browserHeartbeatFailures.value < 4) {
      scheduleBrowserHeartbeat()
      return
    }
    // Keep the iframe visible even after repeated transient failures. The
    // explicit reload action remains available, while silently retrying the
    // lease avoids replacing a usable page with a full-screen reconnect mask.
    browserError.value = ''
    scheduleBrowserHeartbeat()
  }
}
async function releaseBrowserControl() {
  clearBrowserHeartbeat()
  browserHeartbeatFailures.value = 0
  const controllerID = browserControllerID.value
  browserControllerID.value = ''
  if (controllerID) await teamChildAPI.releaseTeamChildBrowserControl(controllerID).catch(() => undefined)
}
async function reloadBrowserWorkspace(takeOver = false): Promise<boolean> {
  if (!browserConfigured.value) return false
  // Keep the mounted iframe and its persistent Chromium tab stable when a
  // second action targets the already-open workspace. An explicit reload or
  // takeover is the only path that mints a new viewer session.
  if (!takeOver && browserEmbedURL.value && browserControllerID.value) return true
  if (browserLoading.value) return false
  browserLoading.value = true
  browserError.value = ''
  browserControlConflict.value = false
  browserHeartbeatFailures.value = 0
  try {
    const session = await teamChildAPI.createBrowserSession({
      ...(browserControllerID.value ? { controller_id: browserControllerID.value } : {}),
      ...(takeOver ? { take_over: true } : {})
    })
    browserEmbedURL.value = session.embed_url
    browserControllerID.value = session.controller_id
    scheduleBrowserHeartbeat()
    return true
  } catch (error) {
    browserControlConflict.value = extractApiErrorCode(error) === 'TEAM_CHILD_BROWSER_CONTROLLED'
    browserError.value = browserControlConflict.value
      ? '已有设备正在手动查看服务器浏览器。需要继续时，请明确确认接管。'
      : extractApiErrorMessage(error, '无法连接服务器浏览器，请检查部署状态。')
    return false
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
      oauth_session_id: oauthSessionID.value,
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

function handleWorkspaceOneClick() {
  if (teamWorkflow.value?.status === 'failed') void continueWorkflow()
}

async function restartWorkflowOAuth() {
  const workflow = teamWorkflow.value
  if (!workflow || !['manual_required', 'failed'].includes(workflow.status)) return
  clearWorkflowPoll()
  try {
    const activeMailbox = await teamChildAPI.getActiveMailbox()
    const reusedMailbox = activeMailbox || mailbox.value
    if (!reusedMailbox) throw new Error('当前临时邮箱已过期，请先重新获取临时邮箱')
    if (activeMailbox) mailbox.value = activeMailbox
    mailboxCode.value = ''
    mailboxCodeError.value = ''
    schedulePoll(250)
    const auth = await generateFreshOAuthSession()
    if (!auth) throw new Error('未生成 XIASS 官方 OpenAI 授权链接')
    teamWorkflow.value = await teamChildAPI.restartTeamChildWorkflowOAuth(workflow.id, auth.auth_url, auth.session_id)
    callbackURL.value = ''
    status.value = 'workflow'
    errorMessage.value = ''
    appStore.showInfo(`已复用临时邮箱 ${reusedMailbox.email}，新的官方 OAuth 链接已准备`)
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
      appStore.showSuccess('已自动捕获并校验授权回调，正在导入 XIASS')
      await importAccount()
      return
    }
    if (workflow.status === 'manual_required') {
      if (status.value !== 'received') status.value = 'waiting'
      if (!mailboxCode.value && !mailboxCodeLoading.value) schedulePoll()
      await maybeSubmitMailboxCode()
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
  errorMessage.value = ''; mailboxCodeError.value = ''; createdAccount.value = null; appliedImportConfig.value = null; teamWorkflow.value = null; openaiOAuth.resetState(); callbackURL.value = ''; mailboxCode.value = ''; lastSubmittedEmailCode.value = ''; lastSubmittedPhone.value = ''; lastSubmittedSMSCode.value = ''; status.value = 'creating'
  try {
    mailbox.value = await teamChildAPI.createMailbox()
    selectedMailboxEmail.value = mailbox.value.email
    await loadKnownMailboxes()
    schedulePoll(250)
    if (!teamWorkflow.value) await generateFreshOAuthSession()
    status.value = 'ready'
  } catch (error) { status.value = 'error'; errorMessage.value = extractApiErrorMessage(error, '流程启动失败') }
}

async function restoreActiveMailbox() {
  if (!mailboxConfigured.value || mailbox.value) return
  try {
    const restored = await teamChildAPI.getActiveMailbox()
    if (!restored) return
    mailbox.value = restored
    selectedMailboxEmail.value = restored.email
    mailboxCodeError.value = ''
    schedulePoll(250)
    if (!teamWorkflow.value) {
      await generateFreshOAuthSession()
      status.value = 'ready'
    } else if (teamWorkflow.value.status === 'callback_ready') {
      await importAccount()
    }
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
      void maybeSubmitMailboxCode()
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

async function maybeSubmitMailboxCode() {
  const workflow = teamWorkflow.value
  const code = mailboxCode.value.trim()
  if (!workflow || !['mailbox', 'email_code'].includes(workflow.current_node || '') || !code || emailCodeSubmitting.value || lastSubmittedEmailCode.value === code) return
  emailCodeSubmitting.value = true
  lastSubmittedEmailCode.value = code
  try {
    teamWorkflow.value = await teamChildAPI.submitTeamChildWorkflowEmailCode(workflow.id, code)
    status.value = 'workflow'
    scheduleWorkflowPoll(400)
  } catch (error) {
    lastSubmittedEmailCode.value = ''
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '邮箱验证码自动填入失败')
    await syncWorkflowAfterActionError()
  } finally {
    emailCodeSubmitting.value = false
  }
}

async function submitWorkflowPhone(phone: string) {
  const workflow = teamWorkflow.value
  const normalized = phone.replace(/[\s()-]/g, '')
  if (!workflow || !['sms_confirm', 'phone_submit'].includes(workflow.current_node || '') || phoneSubmitting.value || lastSubmittedPhone.value === normalized) return
  phoneSubmitting.value = true
  lastSubmittedPhone.value = normalized
  try {
    teamWorkflow.value = await teamChildAPI.submitTeamChildWorkflowPhone(workflow.id, normalized)
    status.value = 'workflow'
    scheduleWorkflowPoll(400)
  } catch (error) {
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '手机号自动填入失败')
    await syncWorkflowAfterActionError()
  } finally {
    phoneSubmitting.value = false
  }
}

async function submitWorkflowSMSCode(code: string) {
  const workflow = teamWorkflow.value
  const normalized = code.replace(/\s+/g, '')
  if (!workflow || !['sms_poll', 'sms_code'].includes(workflow.current_node || '') || smsCodeSubmitting.value || lastSubmittedSMSCode.value === normalized) return
  smsCodeSubmitting.value = true
  lastSubmittedSMSCode.value = normalized
  try {
    teamWorkflow.value = await teamChildAPI.submitTeamChildWorkflowSMSCode(workflow.id, normalized)
    status.value = 'workflow'
    scheduleWorkflowPoll(400)
  } catch (error) {
    lastSubmittedSMSCode.value = ''
    status.value = 'error'
    errorMessage.value = extractApiErrorMessage(error, '短信验证码自动填入失败')
    await syncWorkflowAfterActionError()
  } finally {
    smsCodeSubmitting.value = false
  }
}
async function importAccount() {
  if (!mailbox.value || !oauthSessionID.value || !parsedCallback.value || parsedCallbackError.value || importing.value) return
  const importConfig = {
    groupIDs: [...selectedGroupIDs.value],
    groupNames: [...selectedGroupNames.value],
    concurrency: normalizedConcurrency.value,
    priority: normalizedPriority.value
  }
  appliedImportConfig.value = importConfig
  errorMessage.value = ''; status.value = 'importing'
  try {
    if (teamWorkflow.value && teamWorkflow.value.status !== 'callback_ready') {
      const confirmed = await confirmWorkflowCallback()
      if (!confirmed) {
        status.value = 'error'
        return
      }
    }
    createdAccount.value = await teamChildAPI.createOpenAIAccountFromOAuth({ session_id: oauthSessionID.value, code: parsedCallback.value.code, state: parsedCallback.value.state, name: mailbox.value.email, concurrency: importConfig.concurrency, priority: importConfig.priority, group_ids: importConfig.groupIDs, team_child: true, workflow_id: teamWorkflow.value!.id })
    if (teamWorkflow.value) {
      teamWorkflow.value = await teamChildAPI.completeTeamChildWorkflow(teamWorkflow.value.id).catch(() => teamWorkflow.value!)
    }
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
  if (teamWorkflow.value && ['manual_required', 'callback_ready', 'failed'].includes(teamWorkflow.value.status)) await teamChildAPI.cancelTeamChildWorkflow(teamWorkflow.value.id).catch(() => undefined)
  if (mailbox.value) await teamChildAPI.deleteMailboxSession(mailbox.value.session_id).catch(() => undefined)
  mailbox.value = null; selectedMailboxEmail.value = ''; mailboxCode.value = ''; mailboxCodeError.value = ''; teamWorkflow.value = null; clearRevealedPassword(); openaiOAuth.resetState(); callbackURL.value = ''; selectedGroupIDs.value = []; accountConcurrency.value = 10; accountPriority.value = 1; createdAccount.value = null; appliedImportConfig.value = null; errorMessage.value = ''; lastSubmittedEmailCode.value = ''; lastSubmittedPhone.value = ''; lastSubmittedSMSCode.value = ''; status.value = 'idle'
}

async function restoreActiveWorkflow() {
  if (teamWorkflow.value) return
  try {
    const restored = await teamChildAPI.getActiveTeamChildWorkflow()
    if (!restored) return
    teamWorkflow.value = restored
    if (restored.oauth_session_id) oauthSessionID.value = restored.oauth_session_id
    if (restored.oauth_state) oauthState.value = restored.oauth_state
    if (restored.status === 'callback_ready' && restored.callback_url) {
      callbackURL.value = restored.callback_url
      status.value = 'callback'
      await importAccount()
      return
    }
    if (restored.status === 'completed') {
      status.value = 'completed'
      return
    }
    if (restored.status === 'failed') {
      status.value = 'error'
      errorMessage.value = restored.error || '自动化步骤未完成，请点击继续自动化'
      return
    }
    status.value = restored.status === 'manual_required' ? 'waiting' : 'workflow'
    scheduleWorkflowPoll(restored.status === 'running' ? 500 : 1500)
  } catch (error) {
    const message = extractApiErrorMessage(error, '')
    if (/运行组件版本不匹配/.test(message)) {
      status.value = 'error'
      errorMessage.value = message
    }
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
  await loadKnownMailboxes()
  void loadTeamChildHistory()
  await restoreActiveWorkflow()
  await restoreActiveMailbox()
  if (browserConfigured.value) await loadMembers()
})

watch(
  [() => teamWorkflow.value?.current_node, mailboxCode],
  () => { void maybeSubmitMailboxCode() }
)
watch(
  () => teamWorkflow.value?.id,
  (nextID, previousID) => {
    if (nextID !== previousID) clearRevealedPassword()
  }
)
onBeforeUnmount(() => {
  if (pollTimer) clearTimeout(pollTimer)
  clearWorkflowPoll()
  clearRevealedPassword()
  void releaseBrowserControl()
})
</script>

<style scoped>
.team-child-page {
  overflow-x: clip;
}

.team-child-layout {
  align-items: stretch;
}

.team-child-layout > * {
  min-width: 0;
}

.team-child-code-input {
  width: 9.5rem;
  min-width: 9.5rem;
  max-width: 100%;
}

.team-child-panel,
.team-child-mailbox-panel {
  contain: layout;
}

.team-child-page :deep(.btn),
.team-child-page :deep(button),
.team-child-page :deep(h1),
.team-child-page :deep(h2),
.team-child-page :deep(h3) {
  text-wrap: nowrap;
}

.team-child-page :deep(.input),
.team-child-page :deep(textarea),
.team-child-page :deep(code) {
  min-width: 0;
}

.team-child-page :deep(textarea),
.team-child-page :deep(code) {
  overflow-wrap: anywhere;
}

@media (max-width: 639px) {
  .team-child-page :deep(.btn) {
    max-width: 100%;
  }

  .team-child-code-input {
    width: 8.5rem;
    min-width: 8.5rem;
  }
}

@media (min-width: 1280px) {
  .team-child-layout :deep(.team-child-members-workspace),
  .team-child-layout :deep(.team-child-browser-workspace) {
    min-height: min(720px, calc(100dvh - 12rem));
  }
}
</style>

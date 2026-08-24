<template>
  <div
    data-testid="team-oauth-workspace"
    class="team-oauth-workspace min-w-0 space-y-3"
    aria-live="polite"
  >
    <section data-testid="team-oauth-progress-card" class="team-oauth-progress-card sticky top-3 z-20 overflow-hidden rounded-lg border border-primary-200 bg-white/95 shadow-lg backdrop-blur dark:border-primary-900/70 dark:bg-dark-900/95">
      <header class="flex min-w-0 flex-col gap-3 border-b border-primary-100 bg-primary-50/60 px-4 py-3 dark:border-primary-900/60 dark:bg-primary-950/20 sm:flex-row sm:items-center sm:justify-between">
      <div class="flex min-w-0 items-start gap-3">
        <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-primary-100 text-primary-700 dark:bg-primary-950/60 dark:text-primary-300">
          <Icon name="key" size="md" :stroke-width="2" />
        </span>
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-2">
            <h2 class="whitespace-nowrap text-base font-semibold text-gray-900 dark:text-gray-100">成员授权工作区</h2>
            <span class="whitespace-nowrap rounded-md border border-primary-200 bg-white px-2 py-0.5 text-[11px] font-medium text-primary-700 dark:border-primary-800 dark:bg-dark-800 dark:text-primary-300">XIASS PKCE</span>
          </div>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">当前授权会话：{{ mailboxEmail || '等待临时邮箱' }}</p>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <span class="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium" :class="statusClass">
          <span class="h-1.5 w-1.5 rounded-full" :class="statusDotClass"></span>
          {{ statusLabel }}
        </span>
      </div>
      </header>

      <div class="bg-gray-50/80 px-4 py-3 dark:bg-dark-950/35">
      <div class="flex items-center justify-between gap-3 text-xs">
        <span class="font-medium text-gray-600 dark:text-gray-300">自动化进度</span>
        <span class="font-mono font-semibold tabular-nums text-gray-700 dark:text-gray-200">{{ completedCount }} / {{ workflowNodes.length }}</span>
      </div>
      <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
        <div class="h-full rounded-full bg-primary-500 transition-[width] duration-300" :style="{ width: progressPercent + '%' }"></div>
      </div>

      <div class="mt-3 grid min-w-0 gap-2 lg:grid-cols-[minmax(0,1fr)_minmax(180px,0.55fr)]">
        <div
          data-testid="team-oauth-current-node"
          class="team-oauth-current-node min-w-0 rounded-md border px-3 py-3 shadow-[0_8px_24px_rgba(15,23,42,0.06)] dark:shadow-none"
          :class="currentNodeClass"
        >
          <div class="flex min-w-0 items-start gap-3">
            <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border text-xs font-semibold" :class="currentNodeIconClass">
              <Icon v-if="currentNode?.status === 'completed'" name="check" size="sm" :stroke-width="2.5" />
              <Icon v-else-if="currentNode?.status === 'running'" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
              <Icon v-else-if="currentNode?.status === 'failed'" name="exclamationTriangle" size="sm" :stroke-width="2" />
              <span v-else>{{ currentNode?.number || '-' }}</span>
            </span>
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2 text-[11px] font-medium text-gray-500 dark:text-gray-400">
                <span>{{ currentNode?.status === 'failed' ? '需要处理' : currentNode?.status === 'waiting' ? '等待当前节点' : '当前节点' }}</span>
                <span v-if="currentNode">第 {{ currentNode.number }} 步</span>
              </div>
              <h3 class="mt-0.5 truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ currentNodeLabel }}</h3>
              <p class="mt-1 line-clamp-2 text-xs leading-5 text-gray-600 dark:text-gray-300">{{ currentNode?.message || currentNodeFallbackMessage }}</p>
            </div>
          </div>
        </div>

        <div v-if="nextNode" data-testid="team-oauth-next-node" class="min-w-0 rounded-md border border-gray-200 bg-white px-3 py-3 dark:border-dark-600 dark:bg-dark-800">
          <div class="text-[11px] font-medium text-gray-500 dark:text-gray-400">下一项</div>
          <div class="mt-1 flex min-w-0 items-center gap-2">
            <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-gray-300 text-[11px] font-semibold text-gray-500 dark:border-dark-500 dark:text-gray-400">{{ nextNode.number }}</span>
            <span class="truncate text-sm font-medium text-gray-800 dark:text-gray-200">{{ displayNodeLabel(nextNode) }}</span>
          </div>
        </div>
      </div>

      <div v-if="recentCompleted.length" class="mt-2 flex min-w-0 items-center gap-2 text-[11px] text-gray-500 dark:text-gray-400">
        <Icon name="check" size="xs" class="shrink-0 text-green-600 dark:text-green-400" :stroke-width="2.5" />
        <span class="shrink-0">已完成</span>
        <span class="min-w-0 truncate">{{ recentCompleted.map(displayNodeLabel).join('、') }}</span>
      </div>
      </div>
    </section>

    <section data-testid="team-oauth-operation-card" class="overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900">
      <div class="grid min-w-0 gap-4 p-4 xl:grid-cols-[minmax(0,1fr)_minmax(240px,280px)]">
      <div class="min-w-0">
        <div class="mt-4 flex items-start gap-2 rounded-md border border-gray-200 bg-gray-50 px-3 py-2.5 text-xs leading-5 text-gray-600 dark:border-dark-600 dark:bg-dark-900/40 dark:text-gray-300">
          <Icon name="shield" size="sm" class="mt-0.5 shrink-0 text-gray-500 dark:text-gray-400" :stroke-width="2" />
          <p>成员移除、邀请和 Pending invites 会先在当前服务器页面确认；后续 OAuth、邮箱验证码、手机号、短信和回调继续使用同一工作流。密码、验证码和浏览器凭据不会在此处展示或写入工作流记录。</p>
        </div>

        <div v-if="authUrl" class="mt-3 rounded-md border border-primary-200 bg-primary-50/40 p-3 dark:border-primary-900/60 dark:bg-primary-950/15">
          <div class="flex items-center justify-between gap-3">
            <span class="text-xs font-medium text-primary-800 dark:text-primary-200">XIASS 官方 OAuth PKCE 链接</span>
            <button type="button" class="btn btn-secondary flex h-8 w-8 items-center justify-center p-0" title="复制官方授权链接" aria-label="复制官方授权链接" @click="emit('copy-auth')">
              <Icon name="copy" size="sm" :stroke-width="2" />
            </button>
          </div>
          <code class="mt-2 block max-h-16 overflow-auto break-all rounded-md border border-primary-100 bg-white px-2.5 py-2 text-[11px] leading-4 text-gray-700 dark:border-primary-900/50 dark:bg-dark-900 dark:text-gray-300">{{ authUrl }}</code>
          <p class="mt-2 text-xs leading-5 text-primary-700 dark:text-primary-300">XIASS 服务器浏览器会在当前标签页打开官方 PKCE 链接；上方只显示当前节点，节点完成后自动切换到下一项。</p>
        </div>

        <div v-if="workflow.error" class="mt-3 flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2.5 text-xs leading-5 text-red-700 dark:border-red-900/60 dark:bg-red-950/20 dark:text-red-300">
          <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" :stroke-width="2" />
          <span>{{ workflow.error }}</span>
        </div>

        <div v-if="(authUrl && !workflow.nodes?.length) || showReauthorize || workflow.status === 'failed'" class="mt-3 flex flex-wrap items-center gap-2">
          <button
            v-if="authUrl && !workflow.nodes?.length"
            type="button"
            class="btn btn-secondary flex items-center gap-2 whitespace-nowrap"
            title="在 XIASS 服务器内嵌浏览器的当前标签页打开官方 OAuth 授权页"
            data-testid="team-open-auth"
            @click="emit('open-auth-in-browser')"
          >
            <Icon name="server" size="sm" :stroke-width="2" />
            <span>在内嵌浏览器打开授权</span>
          </button>
          <button
            v-if="showReauthorize"
            type="button"
            class="btn flex items-center gap-2 whitespace-nowrap border-red-200 bg-red-50 text-red-700 hover:bg-red-100 dark:border-red-900/70 dark:bg-red-950/25 dark:text-red-300 dark:hover:bg-red-950/40"
            title="检测到 401，复用当前临时邮箱并重新准备官方授权"
            data-testid="team-reauthorize"
            @click="emit('reauthorize')"
          >
            <Icon name="key" size="sm" :stroke-width="2" />
            <span>重新授权</span>
          </button>
          <button v-if="workflow.status === 'failed'" type="button" data-testid="team-open-browser-after-failure" class="btn btn-secondary flex items-center gap-2 whitespace-nowrap !px-2.5 !py-1.5 text-xs" @click="emit('open-browser')">
            <Icon name="server" size="xs" :stroke-width="2" />
            <span>打开内嵌浏览器</span>
          </button>
          <button v-if="workflow.status === 'failed' && !replacementRequired" type="button" data-testid="team-continue-after-failure" class="btn btn-primary flex items-center gap-2 whitespace-nowrap !px-2.5 !py-1.5 text-xs" @click="emit('continue-workflow')">
            <Icon name="refresh" size="xs" :stroke-width="2" />
            <span>继续自动化</span>
          </button>
        </div>

        <div v-if="receiverActive" class="mt-4 rounded-md border border-amber-200 bg-amber-50 px-3 py-2.5 text-xs leading-5 text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/20 dark:text-amber-200">
          已进入手机号验证阶段。请在右侧接码模块领取并选择短信；如果官方提示号码不可用，右侧会停在换号操作，确认后只重新提交新号码，不重复点击旧号码。
        </div>
      </div>

      <div class="min-w-0 border-t border-gray-200 pt-4 dark:border-dark-700 xl:border-l xl:border-t-0 xl:pl-4 xl:pt-0">
        <PixlabSMSReceiver
          :active="receiverActive"
          :replacement-required="replacementRequired"
          :submission-key="workflow.current_node || ''"
          :auto-submit="true"
          @phone-ready="emit('phone-ready', $event)"
          @code-received="emit('sms-code-received', $event)"
          @session-cancelled="emit('session-cancelled')"
        />
      </div>
      </div>

      <footer v-if="callbackURL" class="flex flex-col gap-2 border-t border-green-200 bg-green-50/70 px-4 py-3 dark:border-green-900/60 dark:bg-green-950/20 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex min-w-0 items-center gap-2 text-xs text-green-800 dark:text-green-200">
          <Icon name="check" size="sm" class="shrink-0" :stroke-width="2.5" />
          <span class="truncate">已校验授权回调</span>
        </div>
        <button type="button" class="btn btn-secondary flex items-center justify-center gap-2 whitespace-nowrap" @click="emit('copy-callback')">
          <Icon name="clipboard" size="sm" :stroke-width="2" />
          <span>复制回调地址</span>
        </button>
      </footer>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Icon } from '@/components/icons'
import PixlabSMSReceiver from '@/components/account/PixlabSMSReceiver.vue'
import type { TeamChildWorkflow, TeamChildWorkflowNode } from '@/api/admin/teamChild'

const props = withDefaults(defineProps<{
  workflow: TeamChildWorkflow
  mailboxEmail?: string
  callbackURL?: string
  authUrl?: string
  showReauthorize?: boolean
}>(), {
  mailboxEmail: '',
  callbackURL: '',
  authUrl: '',
  showReauthorize: false
})

const emit = defineEmits<{
  'session-cancelled': []
  reauthorize: []
  'open-auth-in-browser': []
  'copy-auth': []
  'copy-callback': []
  'open-browser': []
  'continue-workflow': []
  'phone-ready': [phone: string]
  'sms-code-received': [code: string]
}>()

const receiverNodes = new Set(['sms_confirm', 'phone_submit', 'sms_poll', 'sms_code'])
const receiverActive = computed(() => !['callback_ready', 'completed', 'cancelled'].includes(props.workflow.status)
  && receiverNodes.has(props.workflow.current_node || ''))
const replacementRequired = computed(() => props.workflow.status === 'failed'
  && props.workflow.current_node === 'phone_submit'
  && /未进入短信验证码页面|手机号.*(?:不可用|次数过多)|phone.*(?:invalid|unavailable|too many)|verification.*page/i.test(props.workflow.error || ''))

const statusLabel = computed(() => {
  if (props.workflow.status === 'callback_ready') return '回调已就绪'
  if (props.workflow.status === 'completed') return '已导入 XIASS'
  if (props.workflow.status === 'failed') return '需要处理'
  if (props.workflow.status === 'manual_required') return '等待当前页面'
  return '执行中'
})

const statusClass = computed(() => {
  if (['callback_ready', 'completed'].includes(props.workflow.status)) return 'bg-green-100 text-green-700 dark:bg-green-950/30 dark:text-green-300'
  if (props.workflow.status === 'failed') return 'bg-red-100 text-red-700 dark:bg-red-950/30 dark:text-red-300'
  if (props.workflow.status === 'manual_required') return 'bg-amber-100 text-amber-700 dark:bg-amber-950/30 dark:text-amber-300'
  return 'bg-primary-100 text-primary-700 dark:bg-primary-950/40 dark:text-primary-300'
})

const statusDotClass = computed(() => {
  if (['callback_ready', 'completed'].includes(props.workflow.status)) return 'bg-green-500'
  if (props.workflow.status === 'failed') return 'bg-red-500'
  if (props.workflow.status === 'manual_required') return 'bg-amber-500'
  return 'bg-primary-500'
})

const legacyNodeLabels: Partial<Record<TeamChildWorkflowNode['key'], string>> = {
  sms_confirm: '确认领取手机号',
  phone_submit: '填入号码并选择 Text message',
  sms_poll: '轮询短信验证码',
  sms_code: '自动填入短信验证码',
  callback: '捕获 OAuth 回调',
  import: '按勾选配置导入 XIASS'
}
const workflowNodes = computed<TeamChildWorkflowNode[]>(() => {
  if (props.workflow.nodes?.length) return props.workflow.nodes
  const nodes = props.workflow.steps.map((step) => ({ ...step, key: step.key as TeamChildWorkflowNode['key'] }))
  const currentKey = props.workflow.current_node
  if (currentKey && !nodes.some((node) => node.key === currentKey)) {
    nodes.push({
      key: currentKey,
      number: nodes.length + 1,
      label: legacyNodeLabels[currentKey] || currentKey,
      status: props.workflow.status === 'failed' ? 'failed' : props.workflow.status === 'manual_required' ? 'waiting' : 'running',
      ...(props.workflow.error ? { message: props.workflow.error } : {})
    })
  }
  return nodes
})
const currentNode = computed(() => {
  const reported = props.workflow.current_node
    ? workflowNodes.value.find((node) => node.key === props.workflow.current_node)
    : undefined
  if (reported) return reported
  const active = workflowNodes.value.find((node) => ['running', 'waiting', 'failed'].includes(node.status))
  if (active) return active
  const pending = workflowNodes.value.find((node) => node.status === 'pending')
  if (pending) return pending
  return workflowNodes.value[workflowNodes.value.length - 1]
})
const currentNodeIndex = computed(() => Math.max(0, workflowNodes.value.findIndex((node) => node.key === currentNode.value?.key)))
const completedCount = computed(() => workflowNodes.value.filter((node) => node.status === 'completed').length)
const progressPercent = computed(() => workflowNodes.value.length ? Math.round((completedCount.value / workflowNodes.value.length) * 100) : 0)
const recentCompleted = computed(() => workflowNodes.value.filter((node) => node.status === 'completed').slice(-3))
const nextNode = computed(() => workflowNodes.value.slice(currentNodeIndex.value + 1).find((node) => node.status !== 'completed'))
const currentNodeLabel = computed(() => currentNode.value ? displayNodeLabel(currentNode.value) : '等待工作流开始')
const currentNodeFallbackMessage = computed(() => {
  if (!currentNode.value) return '等待工作流状态同步。'
  if (currentNode.value.key === 'sms_confirm') return '请在右侧接码模块确认领取手机号。'
  if (currentNode.value.key === 'sms_poll') return '正在等待 XIASS SMS 服务返回验证码。'
  if (currentNode.value.key === 'import') return '回调已就绪，等待按已勾选配置导入。'
  return '正在等待服务器自动化状态。'
})
const currentNodeClass = computed(() => {
  if (currentNode.value?.status === 'failed') return 'border-red-300 bg-red-50/80 dark:border-red-900/70 dark:bg-red-950/25'
  if (currentNode.value?.status === 'completed') return 'border-green-300 bg-green-50/80 dark:border-green-900/70 dark:bg-green-950/25'
  return 'border-primary-300 bg-primary-50/80 dark:border-primary-900/70 dark:bg-primary-950/25'
})
const currentNodeIconClass = computed(() => {
  if (currentNode.value?.status === 'failed') return 'border-red-400 bg-red-100 text-red-700 dark:border-red-800 dark:bg-red-950/50 dark:text-red-300'
  if (currentNode.value?.status === 'completed') return 'border-green-500 bg-green-500 text-white'
  return 'border-primary-400 bg-primary-100 text-primary-700 dark:border-primary-800 dark:bg-primary-950/60 dark:text-primary-300'
})

function displayNodeLabel(node: { key: string; label: string }) {
  if (node.key === 'members') return '读取并确认成员席位'
  if (node.key === 'remove') return '移除普通成员'
  if (node.key === 'invite') return '邀请并确认 Pending invites'
  if (node.key === 'invite_confirm') return '确认 Pending invites'
  return node.label
}

</script>

<style scoped>
.team-oauth-stage {
  min-height: 4.5rem;
}

@media (max-width: 639px) {
  .team-oauth-stage {
    min-height: 4.25rem;
  }
}
</style>

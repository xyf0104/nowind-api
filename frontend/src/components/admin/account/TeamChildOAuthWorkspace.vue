<template>
  <section
    data-testid="team-oauth-workspace"
    class="overflow-hidden rounded-lg border border-primary-200 bg-white shadow-sm dark:border-primary-900/70 dark:bg-dark-800"
    aria-live="polite"
  >
    <header class="flex flex-col gap-3 border-b border-primary-100 bg-primary-50/60 px-4 py-4 dark:border-primary-900/60 dark:bg-primary-950/20 sm:flex-row sm:items-start sm:justify-between">
      <div class="flex min-w-0 items-start gap-3">
        <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-primary-100 text-primary-700 dark:bg-primary-950/60 dark:text-primary-300">
          <Icon name="key" size="md" :stroke-width="2" />
        </span>
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-2">
            <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">官方 OAuth 验证</h2>
            <span class="rounded-md border border-primary-200 bg-white px-2 py-0.5 text-[11px] font-medium text-primary-700 dark:border-primary-800 dark:bg-dark-800 dark:text-primary-300">XIASS PKCE</span>
          </div>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">当前授权会话：{{ mailboxEmail || '等待临时邮箱' }}</p>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <span class="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium" :class="statusClass">
          <span class="h-1.5 w-1.5 rounded-full" :class="statusDotClass"></span>
          {{ statusLabel }}
        </span>
        <button
          v-if="canOpenBrowser"
          type="button"
          class="btn btn-secondary flex items-center gap-2 whitespace-nowrap"
          title="仅在官方页面需要人工接管时打开服务器浏览器"
          @click="emit('open-browser')"
        >
          <Icon name="globe" size="sm" :stroke-width="2" />
          <span>打开官方页面</span>
        </button>
      </div>
    </header>

    <div class="grid gap-4 p-4 lg:grid-cols-[minmax(0,1fr)_minmax(15rem,0.72fr)]">
      <div class="min-w-0">
        <div class="grid gap-2 sm:grid-cols-4">
          <div v-for="item in stages" :key="item.key" class="min-w-0 border-l-2 px-3 py-2" :class="stageClass(item.key)">
            <div class="flex items-center gap-2">
              <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full border text-[11px] font-semibold" :class="stageIconClass(item.key)">
                <Icon v-if="item.status === 'completed'" name="check" size="xs" :stroke-width="2.5" />
                <Icon v-else-if="item.status === 'running'" name="refresh" size="xs" class="animate-spin" :stroke-width="2" />
                <span v-else>{{ item.number }}</span>
              </span>
              <span class="truncate text-xs font-medium text-gray-800 dark:text-gray-100">{{ item.label }}</span>
            </div>
            <p class="mt-1 line-clamp-2 text-[11px] leading-4 text-gray-500 dark:text-gray-400">{{ item.message }}</p>
          </div>
        </div>

        <div class="mt-4 flex items-start gap-2 rounded-md border border-gray-200 bg-gray-50 px-3 py-2.5 text-xs leading-5 text-gray-600 dark:border-dark-600 dark:bg-dark-900/40 dark:text-gray-300">
          <Icon name="shield" size="sm" class="mt-0.5 shrink-0 text-gray-500 dark:text-gray-400" :stroke-width="2" />
          <p>外部账号的登录、注册和身份验证仍在官方页面完成；XIASS 只转发当前动作所需的手机号或一次性验证码，并在内存中识别回调。</p>
        </div>

        <div v-if="workflow.error" class="mt-3 flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2.5 text-xs leading-5 text-red-700 dark:border-red-900/60 dark:bg-red-950/20 dark:text-red-300">
          <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" :stroke-width="2" />
          <span>{{ workflow.error }}</span>
        </div>

        <div v-if="canSubmitManualCode" class="mt-4 flex flex-col gap-2 sm:flex-row sm:items-end">
          <label class="min-w-0 flex-1">
            <span class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-300">一次性验证码</span>
            <input
              v-model="manualCode"
              data-testid="team-oauth-manual-code"
              inputmode="numeric"
              autocomplete="one-time-code"
              maxlength="8"
              class="input w-full font-mono tracking-[0.16em]"
              placeholder="输入当前页面要求的验证码"
              @keyup.enter="submitManualCode"
            />
          </label>
          <button type="button" class="btn btn-secondary flex items-center justify-center gap-2 whitespace-nowrap" :disabled="!validManualCode || busy" @click="submitManualCode">
            <Icon v-if="busy" name="refresh" size="sm" class="animate-spin" :stroke-width="2" />
            <Icon v-else name="arrowRight" size="sm" :stroke-width="2" />
            <span>提交验证码</span>
          </button>
        </div>
      </div>

      <div class="min-w-0 border-t border-gray-200 pt-4 dark:border-dark-700 lg:border-l lg:border-t-0 lg:pl-4 lg:pt-0">
        <PixlabSMSReceiver
          :active="receiverActive"
          :replacement-required="workflow.phone_rejected === true"
          @phone-ready="emit('phone-ready', $event)"
          @code-received="emit('code-received', $event)"
          @session-cancelled="emit('session-cancelled')"
        />
      </div>
    </div>

    <footer v-if="callbackURL" class="flex flex-col gap-2 border-t border-green-200 bg-green-50/70 px-4 py-3 dark:border-green-900/60 dark:bg-green-950/20 sm:flex-row sm:items-center sm:justify-between">
      <div class="flex min-w-0 items-center gap-2 text-xs text-green-800 dark:text-green-200">
        <Icon name="check" size="sm" class="shrink-0" :stroke-width="2.5" />
        <span class="truncate">已捕获授权回调</span>
      </div>
      <button type="button" class="btn btn-secondary flex items-center justify-center gap-2 whitespace-nowrap" @click="emit('copy-callback')">
        <Icon name="clipboard" size="sm" :stroke-width="2" />
        <span>复制回调地址</span>
      </button>
    </footer>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { Icon } from '@/components/icons'
import PixlabSMSReceiver from '@/components/account/PixlabSMSReceiver.vue'
import type { TeamChildWorkflow, TeamChildWorkflowStep } from '@/api/admin/teamChild'

const props = withDefaults(defineProps<{
  workflow: TeamChildWorkflow
  mailboxEmail?: string
  callbackURL?: string
  busy?: boolean
}>(), {
  mailboxEmail: '',
  callbackURL: '',
  busy: false
})

const emit = defineEmits<{
  'phone-ready': [phone: string]
  'code-received': [code: string]
  'session-cancelled': []
  'open-browser': []
  'copy-callback': []
}>()

const manualCode = ref('')

const oauthStep = computed(() => props.workflow.steps.find((step) => step.key === 'oauth'))
const verifyStep = computed(() => props.workflow.steps.find((step) => step.key === 'verify'))
const receiverActive = computed(() => ['manual_required', 'failed'].includes(props.workflow.status) && oauthStep.value?.status === 'completed')
const canOpenBrowser = computed(() => receiverActive.value || props.workflow.status === 'running')
const canSubmitManualCode = computed(() => receiverActive.value && /验证码|code|短信|邮箱/.test(verifyStep.value?.message || ''))
const validManualCode = computed(() => /^\d{4,8}$/.test(manualCode.value.trim()))
const busy = computed(() => props.busy || props.workflow.status === 'running')

const statusLabel = computed(() => {
  if (props.workflow.status === 'callback_ready') return '回调已就绪'
  if (props.workflow.status === 'failed') return '需要处理'
  if (props.workflow.status === 'manual_required') return '等待当前页面'
  return '执行中'
})

const statusClass = computed(() => {
  if (props.workflow.status === 'callback_ready') return 'bg-green-100 text-green-700 dark:bg-green-950/30 dark:text-green-300'
  if (props.workflow.status === 'failed') return 'bg-red-100 text-red-700 dark:bg-red-950/30 dark:text-red-300'
  if (props.workflow.status === 'manual_required') return 'bg-amber-100 text-amber-700 dark:bg-amber-950/30 dark:text-amber-300'
  return 'bg-primary-100 text-primary-700 dark:bg-primary-950/40 dark:text-primary-300'
})

const statusDotClass = computed(() => {
  if (props.workflow.status === 'callback_ready') return 'bg-green-500'
  if (props.workflow.status === 'failed') return 'bg-red-500'
  if (props.workflow.status === 'manual_required') return 'bg-amber-500'
  return 'bg-primary-500'
})

const stages = computed(() => [
  stage('oauth', 1, '官方授权页', props.workflow.steps.find((step) => step.key === 'oauth'), '已使用 XIASS 原生 PKCE 会话打开'),
  stage('external', 2, '当前外部页面', props.workflow.steps.find((step) => step.key === 'verify'), props.workflow.status === 'manual_required' ? (props.workflow.steps.find((step) => step.key === 'verify')?.message || '等待页面状态') : '等待授权页'),
  stage('verification', 3, '验证动作', props.workflow.steps.find((step) => step.key === 'verify'), verificationMessage()),
  stage('callback', 4, '回调与导入', props.workflow.status === 'callback_ready' ? { key: 'verify', number: 5, label: '回调', status: 'completed', message: '回调地址已识别' } : undefined, props.callbackURL ? '可在下方导入 XIASS' : '等待回调地址')
])

function stage(key: string, number: number, label: string, source: TeamChildWorkflowStep | undefined, fallback: string) {
  return { key, number, label, status: source?.status || 'pending', message: source?.message || fallback }
}

function verificationMessage() {
  const message = verifyStep.value?.message || ''
  if (/手机号/.test(message)) return '手机号动作可在右侧接码模块确认后提交'
  if (/验证码|code|短信|邮箱/.test(message)) return '验证码可由接码模块提供，或在此临时输入'
  return '根据官方页面提示完成当前验证'
}

function stageClass(key: string) {
  const item = stages.value.find((stageItem) => stageItem.key === key)
  if (item?.status === 'failed') return 'border-red-400 bg-red-50/50 dark:border-red-900 dark:bg-red-950/10'
  if (item?.status === 'completed') return 'border-green-400 bg-green-50/50 dark:border-green-900 dark:bg-green-950/10'
  if (item?.status === 'running' || item?.status === 'waiting') return 'border-primary-400 bg-primary-50/50 dark:border-primary-900 dark:bg-primary-950/10'
  return 'border-gray-200 dark:border-dark-600'
}

function stageIconClass(key: string) {
  const item = stages.value.find((stageItem) => stageItem.key === key)
  if (item?.status === 'completed') return 'border-green-500 bg-green-500 text-white'
  if (item?.status === 'failed') return 'border-red-500 bg-red-50 text-red-600 dark:bg-red-950/20 dark:text-red-300'
  if (item?.status === 'running' || item?.status === 'waiting') return 'border-primary-500 bg-primary-50 text-primary-600 dark:bg-primary-950/30 dark:text-primary-300'
  return 'border-gray-300 text-gray-400 dark:border-dark-600 dark:text-gray-500'
}

function submitManualCode() {
  const code = manualCode.value.trim()
  if (!/^\d{4,8}$/.test(code) || busy.value) return
  manualCode.value = ''
  emit('code-received', code)
}
</script>

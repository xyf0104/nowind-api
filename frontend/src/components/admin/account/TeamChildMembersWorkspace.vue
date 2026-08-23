<template>
  <section class="flex min-h-[540px] flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800 xl:min-h-[calc(100vh-10.5rem)]">
    <header class="flex flex-col gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
      <div class="flex min-w-0 items-center gap-3">
        <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-primary-50 text-primary-600 dark:bg-primary-950/40 dark:text-primary-400">
          <Icon name="users" size="md" :stroke-width="2" />
        </span>
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">成员自动化工作区</h2>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">使用已登录浏览器的实时成员数据；需要外部人工处理时再接管浏览器。</p>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2 whitespace-nowrap">
        <span class="inline-flex items-center gap-1.5 text-xs font-medium" :class="ready ? 'text-green-600 dark:text-green-400' : 'text-amber-600 dark:text-amber-400'"><span class="h-1.5 w-1.5 rounded-full" :class="ready ? 'bg-green-500' : 'bg-amber-500'"></span>{{ ready ? '已登录' : '等待识别' }}</span>
        <button type="button" class="btn btn-secondary flex items-center gap-2 whitespace-nowrap" :disabled="loading" @click="emit('inspect')"><Icon name="mail" size="sm" :stroke-width="2" /><span>识别席位邮箱</span></button>
        <button type="button" class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0" :disabled="loading" title="刷新成员信息" aria-label="刷新成员信息" @click="emit('refresh')">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" :stroke-width="2" />
        </button>
        <button type="button" class="btn btn-secondary flex items-center gap-2 whitespace-nowrap" @click="emit('open-browser')"><Icon name="globe" size="sm" :stroke-width="2" /><span>手动接管</span></button>
        <button type="button" class="btn btn-primary flex items-center gap-2 whitespace-nowrap" :disabled="!workflowReady || workflowBusy" :title="workflowReady ? (seatAlreadyRemoved ? '确认已人工腾出席位，并邀请临时邮箱后打开授权页' : '确认替换已选普通成员并打开授权页') : '先获取临时邮箱并选择普通成员席位，或刷新确认已人工腾出的席位'" @click="emit('start-workflow')">
          <Icon :name="workflowBusy ? 'refresh' : 'play'" size="sm" :class="workflowBusy ? 'animate-spin' : ''" :stroke-width="2" />
          <span>{{ workflowBusy ? '正在执行' : '一键授权' }}</span>
        </button>
      </div>
    </header>

    <section v-if="workflow" class="border-b border-gray-200 bg-gray-50 px-4 py-4 dark:border-dark-700 dark:bg-dark-900/35">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-2">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-gray-100">当前工作流</h3>
            <span class="rounded-md px-2 py-0.5 text-xs font-medium" :class="workflowStatusClass">{{ workflowStatusLabel }}</span>
          </div>
          <p v-if="workflow.error" class="mt-1 text-xs leading-5 text-red-700 dark:text-red-300">{{ workflow.error }}</p>
          <p v-else class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">步骤会以当前浏览器页面的实时结果为准，不复用旧成员缓存。</p>
        </div>
        <div class="flex flex-wrap items-center gap-2 whitespace-nowrap">
          <button v-if="workflow.status === 'failed'" type="button" class="btn btn-primary flex items-center gap-2 whitespace-nowrap" :disabled="workflowContinuing || loading" @click="emit('continue-workflow')">
            <Icon name="refresh" size="sm" :class="workflowContinuing ? 'animate-spin' : ''" :stroke-width="2" />
            <span>{{ workflowContinuing ? '正在继续' : '继续自动化' }}</span>
          </button>
          <button v-else-if="workflow.status === 'manual_required'" type="button" class="btn btn-secondary flex items-center gap-2 whitespace-nowrap" @click="emit('open-browser')"><Icon name="globe" size="sm" :stroke-width="2" /><span>处理外部页面</span></button>
        </div>
      </div>
      <ol class="mt-4 grid gap-2 sm:grid-cols-2 xl:grid-cols-5">
        <li
          v-for="step in workflow.steps"
          :key="step.key"
          class="min-w-0 border-l-2 px-3 py-1.5"
          :class="[workflowStepClass(step.status), canRunWorkflowStep(step) ? 'cursor-pointer hover:bg-primary-50/70 dark:hover:bg-primary-950/20' : '']"
          :role="canRunWorkflowStep(step) ? 'button' : undefined"
          :tabindex="canRunWorkflowStep(step) ? 0 : undefined"
          :aria-label="canRunWorkflowStep(step) ? `执行第 ${step.number} 步：${step.label}` : undefined"
          @click="canRunWorkflowStep(step) && emit('run-step', step.key)"
          @keydown.enter="canRunWorkflowStep(step) && emit('run-step', step.key)"
        >
          <div class="flex items-center gap-2"><span class="text-xs font-semibold tabular-nums">{{ step.number }}</span><span class="truncate text-xs font-medium text-gray-800 dark:text-gray-100">{{ step.label }}</span></div>
          <p v-if="step.message" class="mt-1 line-clamp-2 text-xs leading-4 text-gray-500 dark:text-gray-400">{{ step.message }}</p>
          <span v-if="canRunWorkflowStep(step)" class="mt-1 inline-block text-[11px] font-medium text-primary-600 dark:text-primary-400">点击执行</span>
        </li>
      </ol>
    </section>

    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-700 dark:bg-dark-900/35">
      <div class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300"><Icon name="users" size="sm" :stroke-width="2" /><span>{{ members.length }} 位成员<span v-if="pendingInvites">，{{ pendingInvites }} 个待处理邀请</span></span></div>
      <button type="button" class="btn btn-primary flex items-center gap-2 whitespace-nowrap" :disabled="!ready" @click="openInvite"><Icon name="userPlus" size="sm" :stroke-width="2" /><span>邀请成员</span></button>
    </div>

    <div v-if="seatEmail || workspaceName || invitationEmail" class="grid gap-3 border-b border-gray-200 px-4 py-3 text-xs dark:border-dark-700 sm:grid-cols-2">
      <div v-if="invitationEmail" class="min-w-0"><span class="font-medium text-gray-500 dark:text-gray-400">本次临时邮箱</span><code class="mt-1 block truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ invitationEmail }}</code></div>
      <div v-if="seatEmail" class="min-w-0"><span class="font-medium text-gray-500 dark:text-gray-400">默认待替换席位</span><code class="mt-1 block truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ seatEmail }}</code></div>
      <span v-if="workspaceName"><span class="font-medium">工作区：</span>{{ workspaceName }}</span>
    </div>

    <div v-if="seatAlreadyRemoved" class="flex items-start gap-2 border-b border-green-200 bg-green-50 px-4 py-3 text-xs text-green-800 dark:border-green-900/60 dark:bg-green-950/20 dark:text-green-200">
      <Icon name="check" size="sm" class="mt-0.5 flex-shrink-0" :stroke-width="2.5" />
      <span>已实时确认普通成员席位已由人工腾出，当前仅保留受保护成员。一键授权将跳过移除步骤，仅邀请当前临时邮箱。</span>
    </div>

    <div v-if="error" class="m-4 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/20 dark:text-red-300"><Icon name="exclamationTriangle" size="sm" class="mt-0.5 flex-shrink-0" :stroke-width="2" /><span>{{ error }}</span></div>

    <div v-if="members.length" class="flex-1 divide-y divide-gray-100 dark:divide-dark-700">
      <div v-for="member in members" :key="member.email || member.id" class="flex flex-col gap-3 px-4 py-4 transition-colors sm:flex-row sm:items-center sm:justify-between" :class="memberRowClass(member)">
        <div class="flex min-w-0 items-center gap-3">
          <input v-if="canManageMember(member)" :id="'team-member-' + member.id" type="radio" name="team-child-member" class="h-4 w-4 border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-600" :checked="selectedEmail === member.email" :aria-label="'选择 ' + member.email" @change="emit('select', member.email)" />
          <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full text-sm font-semibold" :class="isProtectedMember(member) ? 'bg-gray-200 text-gray-500 dark:bg-dark-700 dark:text-gray-400' : 'bg-primary-50 text-primary-600 dark:bg-primary-950/40 dark:text-primary-400'">{{ initials(member.name || member.email) }}</span>
          <label class="min-w-0" :for="canManageMember(member) ? 'team-member-' + member.id : undefined"><p class="truncate text-sm font-medium" :class="isProtectedMember(member) ? 'text-gray-500 dark:text-gray-400' : 'text-gray-900 dark:text-gray-100'">{{ member.name || '未提供姓名' }}</p><p class="truncate text-xs" :class="isProtectedMember(member) ? 'text-gray-400 dark:text-gray-500' : 'text-gray-500 dark:text-gray-400'">{{ member.email }}</p></label>
        </div>
        <div class="flex items-center gap-2 self-end sm:self-auto">
          <span v-if="member.seat_type" class="rounded-md border border-gray-200 px-2 py-1 text-xs font-medium text-gray-500 dark:border-dark-600 dark:text-gray-400">{{ member.seat_type }}</span>
          <span class="rounded-md px-2.5 py-1 text-xs font-medium" :class="isProtectedMember(member) ? 'bg-gray-200 text-gray-500 dark:bg-dark-700 dark:text-gray-400' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'">{{ memberRoleLabel(member) }}</span>
          <button v-if="canManageMember(member)" type="button" class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0" title="编辑成员角色" :aria-label="'编辑 ' + member.email" @click="openEdit(member)"><Icon name="edit" size="sm" :stroke-width="2" /></button>
          <button v-if="canManageMember(member)" type="button" class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0 text-red-600 hover:text-red-700 dark:text-red-400" title="移除成员" :aria-label="'移除 ' + member.email" @click="openRemove(member)"><Icon name="trash" size="sm" :stroke-width="2" /></button>
        </div>
      </div>
    </div>
    <div v-else class="flex flex-1 flex-col items-center justify-center px-4 py-12 text-center text-sm text-gray-500 dark:text-gray-400">
      <p>{{ ready ? '当前没有可显示的成员。点击刷新重新读取成员页面。' : '尚未读取到已登录工作区。先刷新成员；需要人工处理登录页面时再手动接管浏览器。' }}</p>
      <button v-if="!ready" type="button" class="btn btn-secondary mt-4 inline-flex items-center gap-2 whitespace-nowrap" @click="emit('open-browser')"><Icon name="globe" size="sm" :stroke-width="2" /><span>打开手动浏览器</span></button>
    </div>

    <BaseDialog :show="inviteOpen" title="邀请成员" width="normal" @close="inviteOpen = false">
      <div class="space-y-4"><p class="text-sm text-gray-500 dark:text-gray-400">邀请会在已登录的 ChatGPT 工作区中发送。</p><label class="block text-sm font-medium text-gray-700 dark:text-gray-200">成员邮箱</label><input v-model="inviteEmail" type="email" class="input w-full" placeholder="name@example.com" @keyup.enter="submitInvite" /><p v-if="invitationEmail" class="text-xs text-gray-500 dark:text-gray-400">已预填当前临时邮箱，可按需调整。</p></div>
      <template #footer><div class="flex justify-end gap-3"><button type="button" class="btn btn-secondary whitespace-nowrap" @click="inviteOpen = false">取消</button><button type="button" class="btn btn-primary flex items-center gap-2 whitespace-nowrap" :disabled="loading || !inviteEmail.trim()" @click="submitInvite"><Icon v-if="loading" name="refresh" size="sm" class="animate-spin" :stroke-width="2" /><span>发送邀请</span></button></div></template>
    </BaseDialog>

    <BaseDialog :show="editOpen" title="编辑成员" width="normal" @close="editOpen = false">
      <div class="space-y-4"><p class="break-all text-sm text-gray-500 dark:text-gray-400">{{ editingMember?.email }}</p><label class="block text-sm font-medium text-gray-700 dark:text-gray-200">角色</label><select v-model="editRole" class="input w-full"><option value="member">成员</option><option value="admin">管理员</option></select></div>
      <template #footer><div class="flex justify-end gap-3"><button type="button" class="btn btn-secondary whitespace-nowrap" @click="editOpen = false">取消</button><button type="button" class="btn btn-primary whitespace-nowrap" :disabled="loading" @click="submitEdit">保存修改</button></div></template>
    </BaseDialog>

    <ConfirmDialog :show="removeOpen" title="移除成员" :message="'确定从工作空间移除 ' + (removingMember?.email || '该成员') + ' 吗？此操作会在 ChatGPT 页面中提交。'" confirm-text="从工作空间移除" cancel-text="取消" danger @confirm="submitRemove" @cancel="removeOpen = false" />
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { Icon } from '@/components/icons'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { TeamChildMember, TeamChildWorkflow, TeamChildWorkflowStep } from '@/api/admin/teamChild'

const props = withDefaults(defineProps<{
  members: TeamChildMember[]
  pendingInvites?: number
  loading?: boolean
  error?: string
  ready?: boolean
  seatEmail?: string
  workspaceName?: string
  invitationEmail?: string
  selectedEmail?: string
  seatAlreadyRemoved?: boolean
  workflowReady?: boolean
  workflowBusy?: boolean
  workflow?: TeamChildWorkflow | null
  workflowContinuing?: boolean
}>(), { pendingInvites: 0, loading: false, error: '', ready: false, seatEmail: '', workspaceName: '', invitationEmail: '', selectedEmail: '', seatAlreadyRemoved: false, workflowReady: false, workflowBusy: false, workflow: null, workflowContinuing: false })
const emit = defineEmits<{ refresh: []; inspect: []; select: [email: string]; invite: [email: string]; edit: [email: string, role: string]; remove: [email: string]; 'open-browser': []; 'start-workflow': []; 'continue-workflow': []; 'run-step': [step: TeamChildWorkflowStep['key']] }>()
const inviteOpen = ref(false)
const editOpen = ref(false)
const removeOpen = ref(false)
const inviteEmail = ref('')
const editRole = ref('member')
const editingMember = ref<TeamChildMember | null>(null)
const removingMember = ref<TeamChildMember | null>(null)

const workflowStatusLabel = computed(() => ({ running: '正在执行', manual_required: '等待外部处理', callback_ready: '已获取回调', failed: '需要继续', cancelled: '已停止' })[props.workflow?.status || 'running'])
const workflowStatusClass = computed(() => {
  if (props.workflow?.status === 'failed') return 'bg-red-100 text-red-700 dark:bg-red-950/30 dark:text-red-300'
  if (props.workflow?.status === 'callback_ready') return 'bg-green-100 text-green-700 dark:bg-green-950/30 dark:text-green-300'
  if (props.workflow?.status === 'manual_required') return 'bg-amber-100 text-amber-700 dark:bg-amber-950/30 dark:text-amber-300'
  return 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300'
})

function initials(value: string) { return value.trim().slice(0, 1).toUpperCase() || '?' }
function normalizedRole(role: string) {
  const value = role.trim().toLowerCase()
  if (/owner|所有者/.test(value)) return 'owner'
  if (/admin|administrator|管理员/.test(value)) return 'admin'
  if (/member|成员/.test(value)) return 'member'
  return 'unknown'
}
function roleLabel(role: string) {
  const value = normalizedRole(role)
  if (value === 'owner') return '所有者'
  if (value === 'admin') return '管理员'
  if (value === 'member') return '成员'
  return '未知'
}
function isProtectedMember(member: TeamChildMember) {
  const role = normalizedRole(member.role)
  return Boolean(member.protected) || role === 'owner' || role === 'admin'
}
function memberRoleLabel(member: TeamChildMember) {
  if (isProtectedMember(member)) return normalizedRole(member.role) === 'owner' ? '所有者（受保护）' : '管理员（受保护）'
  return roleLabel(member.role)
}
function canManageMember(member: TeamChildMember) {
  return !isProtectedMember(member) && normalizedRole(member.role) === 'member'
}
function memberRowClass(member: TeamChildMember) {
  if (isProtectedMember(member)) return 'bg-gray-50/80 dark:bg-dark-900/20'
  return props.selectedEmail === member.email ? 'bg-primary-50/70 dark:bg-primary-950/15' : ''
}
function workflowStepClass(status: TeamChildWorkflowStep['status']) {
  if (status === 'completed') return 'border-green-500 bg-green-50/70 dark:bg-green-950/10'
  if (status === 'failed') return 'border-red-500 bg-red-50/70 dark:bg-red-950/10'
  if (status === 'running') return 'border-primary-500 bg-primary-50/70 dark:bg-primary-950/10'
  if (status === 'waiting') return 'border-amber-500 bg-amber-50/70 dark:bg-amber-950/10'
  if (status === 'cancelled') return 'border-gray-300 bg-gray-100/70 dark:border-dark-600 dark:bg-dark-900/30'
  return 'border-gray-300 dark:border-dark-600'
}
function canRunWorkflowStep(step: TeamChildWorkflowStep) {
  return Boolean(props.workflow)
    && !props.workflowBusy
    && ['pending', 'waiting', 'failed'].includes(step.status)
}
function openEdit(member: TeamChildMember) {
  if (!canManageMember(member)) return
  editingMember.value = member
  editRole.value = 'member'
  editOpen.value = true
}
function openRemove(member: TeamChildMember) {
  if (!canManageMember(member)) return
  removingMember.value = member
  removeOpen.value = true
}
function openInvite() { inviteEmail.value = props.invitationEmail; inviteOpen.value = true }
function submitInvite() { const email = inviteEmail.value.trim(); if (!email) return; emit('invite', email); inviteOpen.value = false; inviteEmail.value = '' }
function submitEdit() { if (!editingMember.value || !canManageMember(editingMember.value)) return; emit('edit', editingMember.value.email, editRole.value); editOpen.value = false }
function submitRemove() { if (!removingMember.value || !canManageMember(removingMember.value)) return; emit('remove', removingMember.value.email); removeOpen.value = false }
</script>

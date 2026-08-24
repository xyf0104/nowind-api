<template>
  <section class="team-child-members-workspace flex min-h-[540px] min-w-0 flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800 2xl:min-h-[calc(100vh-10.5rem)]">
    <header class="flex flex-col gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
      <div class="flex min-w-0 items-center gap-3">
        <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-primary-50 text-primary-600 dark:bg-primary-950/40 dark:text-primary-400">
          <Icon name="users" size="md" :stroke-width="2" />
        </span>
        <div class="min-w-0">
          <h2 class="whitespace-nowrap text-base font-semibold text-gray-900 dark:text-gray-100">成员自动化工作区</h2>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">成员、邀请和 OAuth 节点均以服务器端实时页面结果为准；未知页面可随时打开内嵌浏览器接管。</p>
        </div>
      </div>
      <div class="team-child-members-actions grid grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:items-center sm:justify-end">
        <span class="inline-flex items-center gap-1.5 text-xs font-medium" :class="ready ? 'text-green-600 dark:text-green-400' : 'text-amber-600 dark:text-amber-400'"><span class="h-1.5 w-1.5 rounded-full" :class="ready ? 'bg-green-500' : 'bg-amber-500'"></span>{{ ready ? '已登录' : '等待识别' }}</span>
        <button
          type="button"
          class="btn btn-secondary flex min-w-0 items-center justify-center gap-2 whitespace-nowrap"
          :disabled="browserLoading || !browserConfigured"
          :title="browserConfigured ? '打开 XIASS 内嵌服务器浏览器，处理 Pending invites 或登录页面' : '服务器浏览器尚未配置'"
          @click="emit('open-browser')"
        >
          <Icon name="server" size="sm" :class="browserLoading ? 'animate-spin' : ''" :stroke-width="2" />
          <span class="truncate">打开内嵌浏览器</span>
        </button>
        <button type="button" class="btn btn-secondary flex min-w-0 items-center justify-center gap-2 whitespace-nowrap" :disabled="loading" @click="emit('inspect')"><Icon name="mail" size="sm" :stroke-width="2" /><span class="truncate">识别席位邮箱</span></button>
        <button type="button" class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0" :disabled="loading" title="刷新成员信息" aria-label="刷新成员信息" @click="emit('refresh')">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" :stroke-width="2" />
        </button>
        <button type="button" class="btn btn-primary col-span-2 flex min-w-0 items-center justify-center gap-2 whitespace-nowrap sm:col-span-1" :disabled="!workflowReady || workflowBusy" :title="workflowReady ? (seatAlreadyRemoved ? '确认已人工腾出席位，并邀请临时邮箱后准备授权链接' : '确认替换已选普通成员并准备授权链接') : '先获取临时邮箱并选择普通成员席位，或刷新确认已人工腾出的席位'" @click="emit('start-workflow')">
          <Icon :name="workflowBusy ? 'refresh' : 'play'" size="sm" :class="workflowBusy ? 'animate-spin' : ''" :stroke-width="2" />
          <span>{{ workflowBusy ? '正在执行' : '一键授权' }}</span>
        </button>
      </div>
    </header>

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

    <div v-if="members.length" class="min-w-0 flex-1 divide-y divide-gray-100 dark:divide-dark-700">
      <div v-for="member in members" :key="member.email || member.id" class="team-child-member-row flex min-h-[74px] flex-col gap-3 px-4 py-4 transition-colors sm:flex-row sm:items-center sm:justify-between" :class="memberRowClass(member)">
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
      <p>{{ ready ? '当前没有可显示的成员。点击刷新重新读取成员页面。' : '尚未读取到已登录工作区。请先完成服务器端 ChatGPT 管理员登录，再刷新成员列表。' }}</p>
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
import { ref } from 'vue'
import { Icon } from '@/components/icons'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { TeamChildMember, TeamChildWorkflow } from '@/api/admin/teamChild'

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
  browserConfigured?: boolean
  browserLoading?: boolean
}>(), { pendingInvites: 0, loading: false, error: '', ready: false, seatEmail: '', workspaceName: '', invitationEmail: '', selectedEmail: '', seatAlreadyRemoved: false, workflowReady: false, workflowBusy: false, workflow: null, workflowContinuing: false, browserConfigured: false, browserLoading: false })
const emit = defineEmits<{ refresh: []; inspect: []; select: [email: string]; invite: [email: string]; edit: [email: string, role: string]; remove: [email: string]; 'open-browser': []; 'start-workflow': []; 'continue-workflow': [] }>()
const inviteOpen = ref(false)
const editOpen = ref(false)
const removeOpen = ref(false)
const inviteEmail = ref('')
const editRole = ref('member')
const editingMember = ref<TeamChildMember | null>(null)
const removingMember = ref<TeamChildMember | null>(null)

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

<style scoped>
.team-child-members-workspace {
  min-height: 540px;
}

.team-child-members-actions > .btn {
  min-height: 2.25rem;
}

@media (max-width: 639px) {
  .team-child-members-workspace {
    min-height: 480px;
  }

  .team-child-members-actions > .btn {
    width: 100%;
  }
}

@media (min-width: 1280px) {
  .team-child-members-workspace {
    height: min(720px, calc(100dvh - 12rem));
  }
}
</style>

<template>
  <section class="overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
    <header class="flex flex-col gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
      <div class="flex min-w-0 items-center gap-3">
        <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-primary-50 text-primary-600 dark:bg-primary-950/40 dark:text-primary-400">
          <Icon name="users" size="md" :stroke-width="2" />
        </span>
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">成员管理</h2>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">已连接持久化服务器浏览器，可直接操作 ChatGPT 工作区。</p>
        </div>
      </div>
      <div class="flex items-center gap-2">
        <span class="inline-flex items-center gap-1.5 text-xs font-medium text-green-600 dark:text-green-400"><span class="h-1.5 w-1.5 rounded-full bg-green-500"></span>已登录</span>
        <button type="button" class="btn btn-secondary flex items-center gap-2" :disabled="loading" @click="emit('inspect')"><Icon name="mail" size="sm" :stroke-width="2" /><span>识别席位邮箱</span></button>
        <button type="button" class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0" :disabled="loading" title="刷新成员信息" aria-label="刷新成员信息" @click="emit('refresh')">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" :stroke-width="2" />
        </button>
        <button type="button" class="btn btn-secondary flex items-center gap-2" @click="emit('open-browser')"><Icon name="globe" size="sm" :stroke-width="2" /><span>浏览器工作区</span></button>
      </div>
    </header>

    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-700 dark:bg-dark-900/35">
      <div class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300"><Icon name="users" size="sm" :stroke-width="2" /><span>{{ members.length }} 位成员<span v-if="pendingInvites">，{{ pendingInvites }} 个待处理邀请</span></span></div>
      <button type="button" class="btn btn-primary flex items-center gap-2" @click="inviteOpen = true"><Icon name="userPlus" size="sm" :stroke-width="2" /><span>邀请成员</span></button>
    </div>

    <div v-if="seatEmail || workspaceName" class="flex flex-wrap gap-x-6 gap-y-1 border-b border-gray-200 px-4 py-3 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">
      <span v-if="seatEmail"><span class="font-medium">当前席位：</span><code class="break-all text-gray-800 dark:text-gray-200">{{ seatEmail }}</code></span>
      <span v-if="workspaceName"><span class="font-medium">工作区：</span>{{ workspaceName }}</span>
    </div>

    <div v-if="error" class="m-4 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/20 dark:text-red-300"><Icon name="exclamationTriangle" size="sm" class="mt-0.5 flex-shrink-0" :stroke-width="2" /><span>{{ error }}</span></div>

    <div v-if="members.length" class="divide-y divide-gray-100 dark:divide-dark-700">
      <div v-for="member in members" :key="member.email || member.id" class="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex min-w-0 items-center gap-3">
          <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-gray-100 text-sm font-semibold text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ initials(member.name || member.email) }}</span>
          <div class="min-w-0"><p class="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{{ member.name || '未提供姓名' }}</p><p class="truncate text-xs text-gray-500 dark:text-gray-400">{{ member.email }}</p></div>
        </div>
        <div class="flex items-center gap-2 self-end sm:self-auto">
          <span class="rounded-md bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ roleLabel(member.role) }}</span>
          <button v-if="canManageMember(member)" type="button" class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0" title="编辑成员角色" :aria-label="`编辑 ${member.email}`" @click="openEdit(member)"><Icon name="edit" size="sm" :stroke-width="2" /></button>
          <button v-if="canManageMember(member)" type="button" class="btn btn-secondary flex h-9 w-9 items-center justify-center p-0 text-red-600 hover:text-red-700 dark:text-red-400" title="移除成员" :aria-label="`移除 ${member.email}`" @click="openRemove(member)"><Icon name="trash" size="sm" :stroke-width="2" /></button>
        </div>
      </div>
    </div>
    <div v-else class="px-4 py-12 text-center text-sm text-gray-500 dark:text-gray-400">当前没有可显示的成员。点击刷新重新读取成员页面。</div>

    <BaseDialog :show="inviteOpen" title="邀请成员" width="normal" @close="inviteOpen = false">
      <div class="space-y-4"><p class="text-sm text-gray-500 dark:text-gray-400">邀请会在已登录的 ChatGPT 工作区中发送。</p><label class="block text-sm font-medium text-gray-700 dark:text-gray-200">成员邮箱</label><input v-model="inviteEmail" type="email" class="input w-full" placeholder="name@example.com" @keyup.enter="submitInvite" /></div>
      <template #footer><div class="flex justify-end gap-3"><button type="button" class="btn btn-secondary" @click="inviteOpen = false">取消</button><button type="button" class="btn btn-primary flex items-center gap-2" :disabled="loading || !inviteEmail.trim()" @click="submitInvite"><Icon v-if="loading" name="refresh" size="sm" class="animate-spin" :stroke-width="2" /><span>发送邀请</span></button></div></template>
    </BaseDialog>

    <BaseDialog :show="editOpen" title="编辑成员" width="normal" @close="editOpen = false">
      <div class="space-y-4"><p class="break-all text-sm text-gray-500 dark:text-gray-400">{{ editingMember?.email }}</p><label class="block text-sm font-medium text-gray-700 dark:text-gray-200">角色</label><select v-model="editRole" class="input w-full"><option value="member">成员</option><option value="admin">管理员</option></select></div>
      <template #footer><div class="flex justify-end gap-3"><button type="button" class="btn btn-secondary" @click="editOpen = false">取消</button><button type="button" class="btn btn-primary" :disabled="loading" @click="submitEdit">保存修改</button></div></template>
    </BaseDialog>

    <ConfirmDialog :show="removeOpen" title="移除成员" :message="`确定从工作空间移除 ${removingMember?.email || '该成员'} 吗？此操作会在 ChatGPT 页面中提交。`" confirm-text="从工作空间移除" cancel-text="取消" danger @confirm="submitRemove" @cancel="removeOpen = false" />
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { Icon } from '@/components/icons'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { TeamChildMember } from '@/api/admin/teamChild'

withDefaults(defineProps<{ members: TeamChildMember[]; pendingInvites?: number; loading?: boolean; error?: string; seatEmail?: string; workspaceName?: string }>(), { pendingInvites: 0, loading: false, error: '', seatEmail: '', workspaceName: '' })
const emit = defineEmits<{ refresh: []; inspect: []; invite: [email: string]; edit: [email: string, role: string]; remove: [email: string]; 'open-browser': [] }>()
const inviteOpen = ref(false)
const editOpen = ref(false)
const removeOpen = ref(false)
const inviteEmail = ref('')
const editRole = ref('member')
const editingMember = ref<TeamChildMember | null>(null)
const removingMember = ref<TeamChildMember | null>(null)

function initials(value: string) { return value.trim().slice(0, 1).toUpperCase() || '?' }
function roleLabel(role: string) {
  if (/owner|所有者/i.test(role)) return '所有者'
  if (/admin|administrator|管理员/i.test(role)) return '管理员'
  if (/member|成员/i.test(role)) return '成员'
  return '未知'
}
function canManageMember(member: TeamChildMember) {
  return /^(admin|administrator|管理员|member|成员)$/i.test(member.role.trim())
}
function openEdit(member: TeamChildMember) {
  if (!canManageMember(member)) return
  editingMember.value = member
  editRole.value = /admin|administrator|管理员/i.test(member.role) ? 'admin' : 'member'
  editOpen.value = true
}
function openRemove(member: TeamChildMember) {
  if (!canManageMember(member)) return
  removingMember.value = member
  removeOpen.value = true
}
function submitInvite() { const email = inviteEmail.value.trim(); if (!email) return; emit('invite', email); inviteOpen.value = false; inviteEmail.value = '' }
function submitEdit() { if (!editingMember.value) return; emit('edit', editingMember.value.email, editRole.value); editOpen.value = false }
function submitRemove() { if (!removingMember.value) return; emit('remove', removingMember.value.email); removeOpen.value = false }
</script>

<template>
  <BaseDialog
    :show="show"
    title="Team 子账号创建成功"
    width="normal"
    @close="emit('close')"
  >
    <section v-if="account" data-testid="team-child-account-success-dialog" class="space-y-4">
      <div class="flex min-w-0 items-center gap-3 rounded-[18px] border border-emerald-200 bg-emerald-50/80 p-4 dark:border-emerald-900/60 dark:bg-emerald-950/25">
        <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-[14px] bg-emerald-600 text-white shadow-sm dark:bg-emerald-500">
          <Icon name="check" size="md" :stroke-width="2.5" />
        </span>
        <div class="min-w-0">
          <p class="text-sm font-semibold text-emerald-900 dark:text-emerald-100">已导入并保存到服务器</p>
          <p class="mt-0.5 truncate font-mono text-xs text-emerald-800 dark:text-emerald-200" :title="account.name">{{ account.name }}</p>
          <p class="mt-1 text-xs text-emerald-700/85 dark:text-emerald-300/85">账号 #{{ account.id }}</p>
        </div>
      </div>

      <GroupSelector
        :model-value="groupIds"
        :groups="groups"
        platform="openai"
        :searchable="false"
        @update:model-value="emit('update:group-ids', $event)"
      />

      <div class="grid min-w-0 grid-cols-2 gap-3">
        <label class="min-w-0">
          <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">并发数</span>
          <input
            :value="concurrency"
            type="number"
            min="1"
            max="1000"
            inputmode="numeric"
            class="input h-10 w-full text-center tabular-nums"
            :disabled="saving"
            @input="emit('update:concurrency', numericValue($event, 10))"
          />
        </label>
        <label class="min-w-0">
          <span class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">优先级</span>
          <input
            :value="priority"
            type="number"
            min="1"
            max="999"
            inputmode="numeric"
            class="input h-10 w-full text-center tabular-nums"
            :disabled="saving"
            @input="emit('update:priority', numericValue($event, 1))"
          />
        </label>
      </div>
    </section>

    <template #footer>
      <div class="flex w-full items-center justify-end gap-2">
        <button type="button" class="btn btn-secondary whitespace-nowrap" :disabled="saving" @click="emit('close')">稍后设置</button>
        <button type="button" class="btn btn-primary flex items-center gap-2 whitespace-nowrap" :disabled="saving || !account" @click="emit('save')">
          <Icon :name="saving ? 'refresh' : 'check'" size="sm" :class="saving ? 'animate-spin' : ''" :stroke-width="2" />
          <span>{{ saving ? '正在保存...' : '保存配置' }}</span>
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import { Icon } from '@/components/icons'
import type { Account, AdminGroup } from '@/types'

defineProps<{
  show: boolean
  account: Account | null
  groups: AdminGroup[]
  groupIds: number[]
  concurrency: number
  priority: number
  saving?: boolean
}>()

const emit = defineEmits<{
  close: []
  save: []
  'update:group-ids': [value: number[]]
  'update:concurrency': [value: number]
  'update:priority': [value: number]
}>()

function numericValue(event: Event, fallback: number): number {
  const value = Number((event.target as HTMLInputElement).value)
  return Number.isFinite(value) ? value : fallback
}
</script>

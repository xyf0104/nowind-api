import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import TeamChildMembersWorkspace from '../TeamChildMembersWorkspace.vue'

const BaseDialogStub = {
  props: ['show', 'title', 'width'],
  template: '<div v-if="show" :data-dialog="title"><slot /><slot name="footer" /></div>'
}

const ConfirmDialogStub = {
  props: ['show', 'title', 'message', 'confirmText', 'cancelText', 'danger'],
  template: '<div v-if="show" data-confirm-dialog><button type="button" data-confirm="true" @click="$emit(\'confirm\')">confirm</button><button type="button" @click="$emit(\'cancel\')">cancel</button></div>',
  emits: ['confirm', 'cancel']
}

const global = {
  stubs: {
    Icon: true,
    BaseDialog: BaseDialogStub,
    ConfirmDialog: ConfirmDialogStub
  }
}

const member = {
  id: 'member@example.test',
  email: 'member@example.test',
  name: 'Member',
  role: 'member',
  status: 'active'
}

describe('TeamChildMembersWorkspace', () => {
  it('uses the stable email identity for edit and remove operations', async () => {
    const wrapper = mount(TeamChildMembersWorkspace, {
      props: { members: [member] },
      global
    })

    await wrapper.get('[aria-label="编辑 member@example.test"]').trigger('click')
    await wrapper.get('[data-dialog="编辑成员"] select').setValue('admin')
    await wrapper.get('[data-dialog="编辑成员"] button.btn-primary').trigger('click')
    expect(wrapper.emitted('edit')).toEqual([['member@example.test', 'admin']])

    await wrapper.get('[aria-label="移除 member@example.test"]').trigger('click')
    await wrapper.get('[data-confirm="true"]').trigger('click')
    expect(wrapper.emitted('remove')).toEqual([['member@example.test']])
  })

  it('keeps inspect and refresh as separate explicit actions', async () => {
    const wrapper = mount(TeamChildMembersWorkspace, {
      props: { members: [member] },
      global
    })

    await wrapper.get('button[title="刷新成员信息"]').trigger('click')
    const inspectButton = wrapper.findAll('button').find((button) => button.text().includes('识别席位邮箱'))
    expect(inspectButton).toBeDefined()
    await inspectButton!.trigger('click')
    expect(wrapper.emitted('refresh')).toHaveLength(1)
    expect(wrapper.emitted('inspect')).toHaveLength(1)
  })

  it('does not silently treat an owner or unknown role as a member', () => {
    const wrapper = mount(TeamChildMembersWorkspace, {
      props: {
        members: [
          { ...member, email: 'owner@example.test', id: 'owner@example.test', role: 'owner' },
          { ...member, email: 'unknown@example.test', id: 'unknown@example.test', role: 'unexpected' }
        ]
      },
      global
    })

    expect(wrapper.text()).toContain('所有者')
    expect(wrapper.text()).toContain('未知')
    expect(wrapper.find('[aria-label="编辑 owner@example.test"]').exists()).toBe(false)
    expect(wrapper.find('[aria-label="编辑 unknown@example.test"]').exists()).toBe(false)
  })

  it('renders protected administrators as read-only gray rows', () => {
    const wrapper = mount(TeamChildMembersWorkspace, {
      props: {
        members: [
          { ...member, email: 'admin@example.test', id: 'admin@example.test', role: 'admin' },
          { ...member, email: 'protected@example.test', id: 'protected@example.test', protected: true }
        ]
      },
      global
    })

    expect(wrapper.text()).toContain('管理员（受保护）')
    expect(wrapper.find('[aria-label="选择 admin@example.test"]').exists()).toBe(false)
    expect(wrapper.find('[aria-label="编辑 admin@example.test"]').exists()).toBe(false)
    expect(wrapper.find('[aria-label="移除 admin@example.test"]').exists()).toBe(false)
    expect(wrapper.find('[aria-label="选择 protected@example.test"]').exists()).toBe(false)
  })

  it('exposes a continuation action only for an interrupted workflow', async () => {
    const wrapper = mount(TeamChildMembersWorkspace, {
      props: {
        members: [member],
        workflow: {
          id: 'workflow-token-abcdefghijklmnop',
          status: 'failed',
          expires_at: '2026-08-22T12:30:00.000Z',
          manual_required: false,
          error: '邀请状态需要复核',
          steps: [{ key: 'invite', number: 3, label: '邀请临时邮箱', status: 'failed' }]
        }
      },
      global
    })

    const continueButton = wrapper.findAll('button').find((button) => button.text().includes('继续自动化'))
    expect(continueButton).toBeDefined()
    await continueButton!.trigger('click')
    expect(wrapper.emitted('continue-workflow')).toHaveLength(1)
  })

  it('defaults invitations to the active temporary mailbox and exposes a replaceable selection', async () => {
    const wrapper = mount(TeamChildMembersWorkspace, {
      props: {
        members: [member, { ...member, email: 'owner@example.test', id: 'owner@example.test', role: 'owner' }],
        ready: true,
        invitationEmail: 'temporary@example.test',
        selectedEmail: 'member@example.test'
      },
      global
    })

    expect(wrapper.get('[aria-label="选择 member@example.test"]').element).toHaveProperty('checked', true)
    expect(wrapper.find('[aria-label="选择 owner@example.test"]').exists()).toBe(false)

    const inviteButton = wrapper.findAll('button').find((button) => button.text().includes('邀请成员'))
    expect(inviteButton).toBeDefined()
    await inviteButton!.trigger('click')
    expect((wrapper.get('[data-dialog="邀请成员"] input').element as HTMLInputElement).value).toBe('temporary@example.test')
  })

  it('shows a non-destructive workflow state after a verified manual seat release', () => {
    const wrapper = mount(TeamChildMembersWorkspace, {
      props: {
        members: [
          { ...member, email: 'owner@example.test', id: 'owner@example.test', role: 'owner', protected: true },
          { ...member, email: 'admin@example.test', id: 'admin@example.test', role: 'admin', protected: true }
        ],
        ready: true,
        seatAlreadyRemoved: true,
        workflowReady: true,
        invitationEmail: 'temporary@example.test'
      },
      global
    })

    expect(wrapper.text()).toContain('已实时确认普通成员席位已由人工腾出')
    const startButton = wrapper.findAll('button').find((button) => button.text().includes('一键授权'))
    expect(startButton).toBeDefined()
    expect(startButton!.attributes('disabled')).toBeUndefined()
    expect(startButton!.attributes('title')).toContain('确认已人工腾出席位')
  })
})

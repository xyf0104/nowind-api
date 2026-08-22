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
})

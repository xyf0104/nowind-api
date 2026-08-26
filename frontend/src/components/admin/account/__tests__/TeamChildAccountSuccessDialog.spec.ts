import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TeamChildAccountSuccessDialog from '../TeamChildAccountSuccessDialog.vue'

const BaseDialogStub = {
  props: ['show', 'title', 'width'],
  template: '<section v-if="show" data-testid="base-dialog" :data-title="title" :data-width="width"><slot /><slot name="footer" /></section>'
}

const GroupSelectorStub = {
  props: ['modelValue', 'groups'],
  template: '<button type="button" data-testid="group-selector" @click="$emit(\'update:model-value\', [9])">groups</button>'
}

const IconStub = { template: '<i />' }

function mountDialog() {
  return mount(TeamChildAccountSuccessDialog, {
    props: {
      show: true,
      account: { id: 300, name: 'team-child@example.test' },
      groups: [{ id: 9, name: 'OpenAI Team', platform: 'openai' }],
      groupIds: [7],
      concurrency: 10,
      priority: 1
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        GroupSelector: GroupSelectorStub,
        Icon: IconStub
      }
    }
  })
}

describe('TeamChildAccountSuccessDialog', () => {
  it('uses the compact card dialog rather than the full account editor', () => {
    const wrapper = mountDialog()

    expect(wrapper.get('[data-testid="base-dialog"]').attributes('data-width')).toBe('narrow')
    expect(wrapper.get('[data-testid="base-dialog"]').attributes('data-title')).toBe('Team 子账号创建成功')
    expect(wrapper.text()).toContain('已导入并保存到服务器')
    expect(wrapper.text()).toContain('并发数')
    expect(wrapper.text()).toContain('优先级')
  })

  it('emits only the selected post-create configuration fields', async () => {
    const wrapper = mountDialog()

    await wrapper.get('[data-testid="group-selector"]').trigger('click')
    await wrapper.get('input[min="1"][max="1000"]').setValue('18')
    await wrapper.get('input[min="1"][max="999"]').setValue('3')

    expect(wrapper.emitted('update:group-ids')).toEqual([[[9]]])
    expect(wrapper.emitted('update:concurrency')).toEqual([[18]])
    expect(wrapper.emitted('update:priority')).toEqual([[3]])
  })
})

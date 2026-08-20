import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import SMSReceiverActionDialog from '../SMSReceiverActionDialog.vue'

const BaseDialogStub = {
  props: ['show'],
  emits: ['close'],
  template: `
    <div v-if="show" data-testid="dialog">
      <slot />
      <slot name="footer" />
      <button type="button" data-testid="dialog-close" @click="$emit('close')">close</button>
    </div>
  `,
}

function mountDialog(pending = false) {
  return mount(SMSReceiverActionDialog, {
    props: {
      show: true,
      title: '领取号码',
      message: '确认领取吗？',
      detail: '领取后开始监听。',
      confirmLabel: '确认领取号码',
      pendingLabel: '正在领取…',
      pending,
    },
    global: { stubs: { BaseDialog: BaseDialogStub, Icon: true } },
  })
}

describe('SMSReceiverActionDialog', () => {
  it('emits the explicit in-app confirmation action', async () => {
    const wrapper = mountDialog()
    expect(wrapper.text()).toContain('确认领取吗？')

    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    expect(wrapper.emitted('confirm')).toHaveLength(1)
  })

  it('cannot close or confirm again while the request is pending', async () => {
    const wrapper = mountDialog(true)

    await wrapper.get('[data-testid="dialog-close"]').trigger('click')
    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')

    expect(wrapper.emitted('cancel')).toBeUndefined()
    expect(wrapper.emitted('confirm')).toBeUndefined()
    expect(wrapper.get('[data-testid="confirm-sms-receiver-action"]').attributes('disabled')).toBeDefined()
  })
})

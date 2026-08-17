import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AdminAccountPicker from '../AdminAccountPicker.vue'
import type { Account } from '@/types'

const translations: Record<string, string> = {
  'tokenConverter.adminAccounts.picker.selectAllFiltered': '全选当前筛选（{count}）',
  'tokenConverter.adminAccounts.picker.confirm': '确认选择（{count}）',
  'tokenConverter.adminAccounts.picker.selectAccount': '选择账号 {name}',
  'common.cancel': '取消',
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params: Record<string, string | number> = {}) => {
      const template = translations[key] ?? key.split('.').at(-1) ?? key
      return Object.entries(params).reduce(
        (result, [name, value]) => result.replaceAll(`{${name}}`, String(value)),
        template,
      )
    },
  }),
}))

const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: `
    <div v-if="show" data-testid="dialog" :aria-label="title">
      <slot />
      <slot name="footer" />
      <button type="button" data-testid="dialog-close" @click="$emit('close')">close</button>
    </div>
  `,
}

function createAccounts(): Account[] {
  return [
    {
      id: 11,
      name: 'OpenAI Pro',
      notes: 'Primary account',
      platform: 'openai',
      type: 'oauth',
      credentials: { access_token: 'must-not-render', refresh_token: 'must-not-render' },
      concurrency: 3,
      priority: 1,
    },
    {
      id: 22,
      name: 'Claude Team',
      notes: 'Backup account',
      platform: 'anthropic',
      type: 'oauth',
      credentials: { access_token: 'must-not-render' },
      concurrency: 2,
      priority: 2,
    },
    {
      id: 33,
      name: 'OpenAI Direct',
      platform: 'openai',
      type: 'apikey',
      credentials: { api_key: 'must-not-render' },
      concurrency: 1,
      priority: 3,
    },
  ] as Account[]
}

function mountPicker(accounts: Account[] = createAccounts(), loading = false) {
  return mount(AdminAccountPicker, {
    props: {
      show: true,
      accounts,
      loading,
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Icon: { template: '<i data-testid="icon" />' },
        PlatformIcon: {
          props: ['platform'],
          template: '<i data-testid="platform-icon" :data-platform="platform" />',
        },
      },
    },
  })
}

describe('AdminAccountPicker', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('shows only OAuth accounts even when the server payload contains API Key accounts', () => {
    const wrapper = mountPicker()

    expect(wrapper.get('[data-testid="platform-filter-openai"]').text()).toContain('1')
    expect(wrapper.get('[data-testid="platform-filter-anthropic"]').text()).toContain('1')
    expect(wrapper.find('[data-testid="platform-filter-gemini"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="platform-group-openai"]').text()).toContain('OpenAI Pro')
    expect(wrapper.get('[data-testid="platform-group-anthropic"]').text()).toContain('Claude Team')
    expect(wrapper.find('[data-testid="account-option-33"]').exists()).toBe(false)
    expect(wrapper.find('input[aria-label="选择账号 OpenAI Direct"]').exists()).toBe(false)

    const visibleText = wrapper.text()
    expect(visibleText).not.toContain('OpenAI Direct')
    expect(visibleText).not.toContain('must-not-render')
  })

  it('selects only the current search and platform filter, then emits account ids only', async () => {
    const accounts = createAccounts()
    const wrapper = mountPicker(accounts)

    await wrapper.get('[data-testid="platform-filter-openai"]').trigger('click')
    await wrapper.get('input[type="search"]').setValue('Primary')

    const selectAllButton = wrapper.findAll('button').find((button) =>
      button.text().includes('全选当前筛选'),
    )
    expect(selectAllButton).toBeDefined()
    await selectAllButton!.trigger('click')

    const confirmButton = wrapper.findAll('button').find((button) =>
      button.text().includes('确认选择'),
    )
    expect(confirmButton).toBeDefined()
    await confirmButton!.trigger('click')

    const confirmed = wrapper.emitted('confirm')?.[0]?.[0]
    expect(confirmed).toEqual([11])
  })

  it('supports individual OAuth selection and never emits an API Key id', async () => {
    const accounts = createAccounts()
    const wrapper = mountPicker(accounts)

    await wrapper.get('input[aria-label="选择账号 OpenAI Pro"]').setValue(true)
    await wrapper.get('input[aria-label="选择账号 Claude Team"]').setValue(true)

    const confirmButton = wrapper.findAll('button').find((button) =>
      button.text().includes('确认选择'),
    )
    await confirmButton!.trigger('click')

    const confirmed = wrapper.emitted('confirm')?.[0]?.[0]
    expect(confirmed).toEqual([11, 22])
    expect(confirmed).not.toContain(33)
  })

  it('emits cancel from both the cancel action and dialog close without mutating input', async () => {
    const accounts = createAccounts()
    const snapshot = structuredClone(accounts)
    const wrapper = mountPicker(accounts)

    const cancelButton = wrapper.findAll('button').find((button) => button.text() === '取消')
    await cancelButton!.trigger('click')
    await wrapper.get('[data-testid="dialog-close"]').trigger('click')

    expect(wrapper.emitted('cancel')).toHaveLength(2)
    expect(accounts).toEqual(snapshot)
  })

  it('disables selection while loading and keeps the picker horizontally contained', () => {
    const wrapper = mountPicker(createAccounts(), true)

    expect(wrapper.get('[data-testid="account-picker-loading"]').exists()).toBe(true)
    expect(wrapper.findAll('button').find((button) => button.text().includes('确认选择'))?.attributes('disabled')).toBeDefined()
    expect(wrapper.get('.overflow-x-hidden').classes()).toContain('max-w-full')
    expect(wrapper.html()).not.toContain('min-w-[')
  })
})

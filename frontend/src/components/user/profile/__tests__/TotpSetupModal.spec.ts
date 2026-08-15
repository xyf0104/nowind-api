import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TotpSetupModal from '@/components/user/profile/TotpSetupModal.vue'

const { getVerificationMethodMock, initiateSetupMock, showErrorMock } = vi.hoisted(() => ({
  getVerificationMethodMock: vi.fn(),
  initiateSetupMock: vi.fn(),
  showErrorMock: vi.fn(),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: (...args: unknown[]) => showErrorMock(...args),
    showSuccess: vi.fn(),
  }),
}))

vi.mock('@/api', () => ({
  totpAPI: {
    getVerificationMethod: (...args: unknown[]) => getVerificationMethodMock(...args),
    initiateSetup: (...args: unknown[]) => initiateSetupMock(...args),
    sendVerifyCode: vi.fn(),
    enable: vi.fn(),
  },
}))

vi.mock('qrcode', () => ({
  default: {
    toDataURL: vi.fn().mockResolvedValue('data:image/png;base64,qr'),
  },
}))

describe('TotpSetupModal', () => {
  beforeEach(() => {
    getVerificationMethodMock.mockReset()
    initiateSetupMock.mockReset()
    showErrorMock.mockReset()
    getVerificationMethodMock.mockResolvedValue({ method: 'password' })
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('renders the password and QR steps in a body-level dialog instead of inside the profile card', async () => {
    initiateSetupMock.mockResolvedValue({
      secret: 'TESTSECRET',
      qr_code_url: 'otpauth://totp/XIASS:test',
      setup_token: 'setup-token',
    })

    const wrapper = mount(TotpSetupModal)
    await flushPromises()

    const dialog = document.body.querySelector<HTMLElement>('[data-testid="totp-setup-dialog"]')
    expect(dialog).not.toBeNull()
    expect(dialog?.parentElement).toBe(document.body)
    expect(dialog?.querySelector('input[autocomplete="current-password"]')).not.toBeNull()

    const password = dialog?.querySelector<HTMLInputElement>('input[autocomplete="current-password"]')
    if (!password) throw new Error('password input not rendered')
    password.value = 'correct horse battery staple'
    password.dispatchEvent(new Event('input', { bubbles: true }))
    await wrapper.vm.$nextTick()

    const nextButton = Array.from(dialog?.querySelectorAll('button') ?? []).find((button) =>
      button.textContent?.includes('common.next')
    )
    nextButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    expect(initiateSetupMock).toHaveBeenCalledWith({ password: 'correct horse battery staple' })
    expect(dialog?.querySelector('img[alt="QR Code"]')).not.toBeNull()

    wrapper.unmount()
  })
})

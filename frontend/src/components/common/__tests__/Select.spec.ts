import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import Select from '../Select.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const originalInnerWidth = window.innerWidth
const originalVisualViewport = window.visualViewport
let unmountWrapper: (() => void) | undefined

const setViewportWidth = (width: number) => {
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    value: width,
  })
}

const setVisualViewport = (value: Partial<VisualViewport> | undefined) => {
  Object.defineProperty(window, 'visualViewport', {
    configurable: true,
    value: value
      ? {
          offsetLeft: 0,
          offsetTop: 0,
          width: window.innerWidth,
          height: window.innerHeight,
          addEventListener: vi.fn(),
          removeEventListener: vi.fn(),
          ...value,
        }
      : undefined,
  })
}

const mockTriggerRect = (left: number, width: number, top = 20, height = 40) => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    x: left,
    y: top,
    top,
    right: left + width,
    bottom: top + height,
    left,
    width,
    height,
    toJSON: () => ({}),
  })
}

const openSelect = async () => {
  const wrapper = mount(Select, {
    props: {
      modelValue: null,
      options: [
        {
          value: 'example',
          label: 'very-long-unbroken-option-value-that-must-not-overflow',
        },
      ],
    },
  })
  unmountWrapper = () => wrapper.unmount()

  await wrapper.get('button').trigger('click')
  await nextTick()

  return document.body.querySelector<HTMLElement>('.select-dropdown-portal')
}

afterEach(() => {
  unmountWrapper?.()
  unmountWrapper = undefined
  document.body.innerHTML = ''
  setViewportWidth(originalInnerWidth)
  setVisualViewport(originalVisualViewport)
  vi.restoreAllMocks()
})

describe('Select dropdown viewport constraints', () => {
  it('preserves the existing 200px minimum width when space is available', async () => {
    setViewportWidth(1024)
    mockTriggerRect(20, 80)

    const dropdown = await openSelect()

    expect(dropdown).not.toBeNull()
    expect(dropdown?.style.left).toBe('20px')
    expect(dropdown?.style.minWidth).toBe('200px')
    expect(dropdown?.style.maxWidth).toBe('996px')
  })

  it('moves the dropdown left before shrinking it near the right viewport edge', async () => {
    setViewportWidth(320)
    mockTriggerRect(220, 80)

    const dropdown = await openSelect()

    expect(dropdown).not.toBeNull()
    expect(dropdown?.style.left).toBe('112px')
    expect(dropdown?.style.minWidth).toBe('200px')
    expect(dropdown?.style.maxWidth).toBe('200px')
  })

  it('clamps a trigger left of the viewport to the safe padding', async () => {
    setViewportWidth(320)
    mockTriggerRect(-20, 80)

    const dropdown = await openSelect()

    expect(dropdown).not.toBeNull()
    expect(dropdown?.style.left).toBe('8px')
    expect(dropdown?.style.minWidth).toBe('200px')
    expect(dropdown?.style.maxWidth).toBe('304px')
  })

  it('keeps a usable dropdown width when the trigger is offscreen-right', async () => {
    setViewportWidth(320)
    mockTriggerRect(400, 80)

    const dropdown = await openSelect()

    expect(dropdown).not.toBeNull()
    expect(dropdown?.style.left).toBe('112px')
    expect(dropdown?.style.minWidth).toBe('200px')
    expect(dropdown?.style.maxWidth).toBe('200px')
  })

  it('uses the available viewport width on screens narrower than the preferred minimum', async () => {
    setViewportWidth(180)
    mockTriggerRect(20, 80)

    const dropdown = await openSelect()

    expect(dropdown).not.toBeNull()
    expect(dropdown?.style.left).toBe('8px')
    expect(dropdown?.style.minWidth).toBe('164px')
    expect(dropdown?.style.maxWidth).toBe('164px')
  })

  it('selects a small non-searchable option list with keyboard controls on the trigger', async () => {
    const wrapper = mount(Select, {
      props: {
        modelValue: null,
        options: [
          { value: 'first', label: 'First' },
          { value: 'second', label: 'Second' },
        ],
      },
    })
    unmountWrapper = () => wrapper.unmount()
    const trigger = wrapper.get('button')

    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await nextTick()
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await trigger.trigger('keydown', { key: 'Enter' })

    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['second'])
    expect(trigger.attributes('aria-expanded')).toBe('false')
  })

  it('chooses the larger visualViewport side and constrains the scrollable popup height', async () => {
    setViewportWidth(1024)
    setVisualViewport({
      offsetLeft: 0,
      offsetTop: 100,
      width: 320,
      height: 240,
    })
    mockTriggerRect(20, 100, 250, 40)

    const dropdown = await openSelect()
    await nextTick()

    expect(dropdown?.style.top).toBe('108px')
    expect(dropdown?.style.maxHeight).toBe('138px')
    expect(dropdown?.className).toContain('select-dropdown-portal')
    expect(dropdown?.querySelector('.select-options')).not.toBeNull()
  })

  it('keeps a forced-bottom dropdown below its trigger even when the upper side has more room', async () => {
    setViewportWidth(1024)
    setVisualViewport({
      offsetLeft: 0,
      offsetTop: 100,
      width: 320,
      height: 240,
    })
    mockTriggerRect(20, 100, 250, 40)

    const wrapper = mount(Select, {
      props: {
        modelValue: null,
        placement: 'bottom',
        options: [{ value: 'team101', label: 'team101@example.test' }],
      },
    })
    unmountWrapper = () => wrapper.unmount()

    await wrapper.get('button').trigger('click')
    await nextTick()

    const dropdown = document.body.querySelector<HTMLElement>('.select-dropdown-portal')
    expect(dropdown?.style.top).toBe('294px')
    expect(dropdown?.style.maxHeight).toBe('38px')
  })
})

import { nextTick } from 'vue'
import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'

import BaseDialog from '../BaseDialog.vue'

const wrappers: VueWrapper[] = []

function mountDialog(title: string, zIndex = 50) {
  const wrapper = mount(BaseDialog, {
    attachTo: document.body,
    props: {
      show: true,
      title,
      zIndex,
    },
    global: {
      stubs: {
        Icon: true,
      },
    },
  })
  wrappers.push(wrapper)
  return wrapper
}

describe('BaseDialog shared stack', () => {
  afterEach(() => {
    wrappers.splice(0).reverse().forEach(wrapper => wrapper.unmount())
    document.body.innerHTML = ''
    document.body.classList.remove('modal-open')
  })

  it('lets only the topmost dialog handle Escape and keeps scroll locked for its parent', async () => {
    const parent = mountDialog('Parent')
    const child = mountDialog('Child')

    expect(document.body.classList.contains('modal-open')).toBe(true)

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()

    expect(parent.emitted('close')).toBeUndefined()
    expect(child.emitted('close')).toHaveLength(1)

    await child.setProps({ show: false })
    expect(document.body.classList.contains('modal-open')).toBe(true)

    await parent.setProps({ show: false })
    expect(document.body.classList.contains('modal-open')).toBe(false)
  })

  it('uses z-index when determining the topmost open dialog', async () => {
    const higherLayer = mountDialog('Higher layer', 60)
    const laterButLowerLayer = mountDialog('Later lower layer', 50)

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()

    expect(higherLayer.emitted('close')).toHaveLength(1)
    expect(laterButLowerLayer.emitted('close')).toBeUndefined()
  })
})

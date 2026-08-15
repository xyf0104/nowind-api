import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(resolve(dir, '../LoginView.vue'), 'utf8')

describe('LoginView session controls', () => {
  it('does not present an unsupported 30-day remember-me option', () => {
    expect(source).not.toContain('保持登录 30 天')
    expect(source).not.toContain('form-checkbox')
  })
})

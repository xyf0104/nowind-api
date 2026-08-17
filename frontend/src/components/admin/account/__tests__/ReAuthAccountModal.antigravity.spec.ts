import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(resolve(dir, '../ReAuthAccountModal.vue'), 'utf8')

describe('ReAuthAccountModal Antigravity persistence contract', () => {
  it('uses the dedicated credential endpoint instead of generic update plus clearError', () => {
    const exchangeStart = source.indexOf('const handleExchangeCode = async () =>')
    const branchStart = source.indexOf("} else if (isAntigravity.value) {", exchangeStart)
    const branchEnd = source.indexOf("} else if (isGrok.value) {", branchStart)
    const branch = source.slice(branchStart, branchEnd)

    expect(branchStart).toBeGreaterThan(-1)
    expect(branchEnd).toBeGreaterThan(branchStart)
    expect(branch).toContain('antigravityOAuth.buildExtraInfo(tokenInfo)')
    expect(branch).toContain('adminAPI.accounts.applyOAuthCredentials')
    expect(branch).not.toContain('adminAPI.accounts.update')
    expect(branch).not.toContain('adminAPI.accounts.clearError')
  })
})

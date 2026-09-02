import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(resolve(dir, '../ReAuthAccountModal.vue'), 'utf8')

describe('ReAuthAccountModal refresh-token reauthorization', () => {
  it('offers the flow to OpenAI-family accounts and preserves the atomic credential write', () => {
    expect(source).toContain(':show-refresh-token-option="isOpenAILike || isAntigravity || isGrok"')
    expect(source).toContain('@validate-refresh-token="handleValidateRefreshToken"')
    expect(source).toContain('await grokOAuth.validateRefreshToken(refreshToken, props.account.proxy_id)')
    expect(source).toContain('await openaiOAuth.validateRefreshToken(refreshToken, props.account.proxy_id)')
    expect(source).toContain('openaiOAuth.buildCredentials(tokenInfo)')
    expect(source).toContain('antigravityOAuth.buildCredentials(tokenInfo, refreshToken)')
    expect(source).toContain('adminAPI.accounts.applyOAuthCredentials')
  })
})

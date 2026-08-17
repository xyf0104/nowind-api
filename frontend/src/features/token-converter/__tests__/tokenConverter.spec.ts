import { describe, expect, it } from 'vitest'
import {
  areTokenAccountsCompatibleWithFormat,
  exportTokenAccounts,
  isTokenOutputFormatCompatible,
  parseTokenInput,
} from '../tokenConverter'

const NOW = new Date('2026-08-17T05:00:00.000Z')

function jwt(payload: Record<string, unknown>): string {
  const encode = (value: unknown) => btoa(JSON.stringify(value)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_')
  return `${encode({ alg: 'none' })}.${encode(payload)}.signature`
}

describe('tokenConverter', () => {
  const platformCases = [
    ['openai', 'openai'],
    ['claude', 'anthropic'],
    ['gemini', 'gemini'],
    ['antigravity', 'antigravity'],
    ['grok', 'grok'],
    ['custom-provider', 'custom-provider'],
  ] as const

  it('normalizes a session JSON and enriches account identity from JWT claims', () => {
    const accessToken = jwt({
      email: 'owner@example.com',
      exp: 1786946400,
      sub: 'user-from-sub',
      'https://api.openai.com/auth': {
        chatgpt_account_id: 'account-1',
        chatgpt_user_id: 'user-1',
        chatgpt_plan_type: 'pro',
      },
    })
    const parsed = parseTokenInput(JSON.stringify({
      user: { id: 'user-session' },
      accessToken,
      refreshToken: 'refresh-1',
      idToken: 'id-1',
    }), NOW)

    expect(parsed.accounts).toHaveLength(1)
    expect(parsed.sourceFormats).toEqual(['session'])
    expect(parsed.accounts[0]).toMatchObject({
      platform: 'openai',
      accountType: 'oauth',
      email: 'owner@example.com',
      accountId: 'account-1',
      userId: 'user-session',
      planType: 'pro',
      refreshToken: 'refresh-1',
    })
    expect(parsed.warnings).toEqual([])

    const xiass = JSON.parse(exportTokenAccounts(parsed.accounts, 'xiass', { now: NOW }).text)
    expect(xiass.accounts[0].credentials).toMatchObject({
      access_token: accessToken,
      refresh_token: 'refresh-1',
      id_token: 'id-1',
    })
    expect(xiass.accounts[0].credentials).not.toHaveProperty('accessToken')
    expect(xiass.accounts[0].credentials).not.toHaveProperty('user')
  })

  it('round-trips XIASS account settings and exports a directly importable bundle', () => {
    const source = {
      type: 'sub2api-data',
      version: 1,
      exported_at: NOW.toISOString(),
      proxies: [{ proxy_key: 'proxy-1' }],
      accounts: [{
        name: 'Pro Account',
        notes: 'keep me',
        platform: 'openai',
        type: 'oauth',
        credentials: {
          access_token: 'access-1',
          refresh_token: 'refresh-1',
          client_id: 'custom-client',
        },
        extra: { codex_cli_only: true },
        proxy_key: 'proxy-1',
        concurrency: 8,
        priority: 3,
      }],
    }

    const parsed = parseTokenInput(JSON.stringify(source), NOW)
    const exported = JSON.parse(exportTokenAccounts(parsed.accounts, 'xiass', {
      proxies: parsed.proxies,
      now: NOW,
    }).text)

    expect(exported.type).toBe('sub2api-data')
    expect(exported.proxies).toEqual(source.proxies)
    expect(exported.accounts[0]).toMatchObject({
      name: 'Pro Account',
      notes: 'keep me',
      proxy_key: 'proxy-1',
      concurrency: 8,
      priority: 3,
      credentials: {
        access_token: 'access-1',
        refresh_token: 'refresh-1',
        client_id: 'custom-client',
      },
      extra: { codex_cli_only: true },
    })
  })

  it('losslessly round-trips every XIASS platform through XIASS and canonical JSON', () => {
    const accounts = [
      {
        name: 'OpenAI Pro',
        platform: 'openai',
        type: 'oauth',
        credentials: { access_token: 'oa-access', refresh_token: 'oa-refresh', client_id: 'custom-client' },
        proxy_key: 'proxy-one',
        concurrency: 8,
        priority: 1,
      },
      {
        name: 'Claude Setup',
        notes: '',
        platform: 'anthropic',
        type: 'setup-token',
        credentials: { setup_token: 'claude-setup', custom_headers: { 'x-route': 'one' } },
        concurrency: 4,
        priority: 2,
        rate_multiplier: null,
      },
      {
        name: 'Gemini Service Account',
        platform: 'gemini',
        type: 'service_account',
        credentials: {
          service_account_json: '{"type":"service_account","private_key":"secret"}',
          project_id: 'gemini-project',
        },
        concurrency: 3,
        priority: 3,
      },
      {
        name: 'Antigravity OAuth',
        platform: 'antigravity',
        type: 'oauth',
        credentials: { refresh_token: 'ag-refresh', project_id: 'ag-project', nested: { keep: [1, 2, 3] } },
        extra: { privacy_mode: true },
        concurrency: 2,
        priority: 4,
      },
      {
        name: 'Grok Upstream',
        platform: 'grok',
        type: 'upstream',
        credentials: { api_key: 'grok-key', base_url: 'https://grok.example/v1', custom: false },
        proxy_key: null,
        expires_at: null,
        auto_pause_on_expired: false,
        concurrency: 1,
        priority: 5,
      },
    ]
    const source = {
      type: 'sub2api-data',
      version: 1,
      exported_at: NOW.toISOString(),
      proxies: [{ proxy_key: 'proxy-one', protocol: 'socks5', host: '127.0.0.1', port: 1080 }],
      accounts,
    }

    const parsed = parseTokenInput(JSON.stringify(source), NOW)
    expect(parsed.accounts.map(({ platform, accountType, rawCredentials }) => ({
      platform,
      accountType,
      rawCredentials,
    }))).toEqual(accounts.map((account) => ({
      platform: account.platform,
      accountType: account.type,
      rawCredentials: account.credentials,
    })))
    expect(parsed.warnings).toEqual([])

    const directXiass = JSON.parse(exportTokenAccounts(parsed.accounts, 'xiass', {
      now: NOW,
      proxies: parsed.proxies,
    }).text)
    expect(directXiass.proxies).toEqual(source.proxies)
    expect(directXiass.accounts).toEqual(accounts)

    const canonical = exportTokenAccounts(parsed.accounts, 'canonical', {
      now: NOW,
      proxies: parsed.proxies,
    })
    const reparsed = parseTokenInput(canonical.text, NOW)
    const canonicalXiass = JSON.parse(exportTokenAccounts(reparsed.accounts, 'xiass', {
      now: NOW,
      proxies: reparsed.proxies,
    }).text)
    expect(canonicalXiass.proxies).toEqual(source.proxies)
    expect(canonicalXiass.accounts).toEqual(accounts)
  })

  it.each(platformCases)('canonicalizes %s OAuth aliases into backend-readable XIASS credentials', (inputPlatform, outputPlatform) => {
    const parsed = parseTokenInput(JSON.stringify({
      name: `${inputPlatform} OAuth`,
      platform: inputPlatform,
      accountType: 'oauth',
      accessToken: `${inputPlatform}-access`,
      refresh_token: `${inputPlatform}-refresh`,
      idToken: `${inputPlatform}-id`,
      session_token: `${inputPlatform}-session`,
      tokenType: 'Bearer',
      projectId: `${inputPlatform}-project`,
      tenantHint: `${inputPlatform}-tenant`,
    }), NOW)

    expect(parsed.accounts).toHaveLength(1)
    const exported = JSON.parse(exportTokenAccounts(parsed.accounts, 'xiass', { now: NOW }).text)
    expect(exported.accounts[0]).toMatchObject({
      name: `${inputPlatform} OAuth`,
      platform: outputPlatform,
      type: 'oauth',
      credentials: {
        access_token: `${inputPlatform}-access`,
        refresh_token: `${inputPlatform}-refresh`,
        id_token: `${inputPlatform}-id`,
        session_token: `${inputPlatform}-session`,
        token_type: 'Bearer',
        project_id: `${inputPlatform}-project`,
        tenantHint: `${inputPlatform}-tenant`,
      },
    })
    expect(exported.accounts[0].credentials).not.toHaveProperty('platform')
    expect(exported.accounts[0].credentials).not.toHaveProperty('accountType')
    expect(exported.accounts[0].credentials).not.toHaveProperty('name')
    expect(exported.accounts[0].credentials).not.toHaveProperty('accessToken')
    expect(exported.accounts[0].credentials).not.toHaveProperty('idToken')
    expect(exported.accounts[0].credentials).not.toHaveProperty('tokenType')
    expect(exported.accounts[0].credentials).not.toHaveProperty('projectId')

    const canonical = exportTokenAccounts(parsed.accounts, 'canonical', { now: NOW })
    const reparsed = parseTokenInput(canonical.text, NOW)
    const roundTripped = JSON.parse(exportTokenAccounts(reparsed.accounts, 'xiass', { now: NOW }).text)
    expect(roundTripped.accounts[0]).toMatchObject({
      platform: outputPlatform,
      type: 'oauth',
      credentials: exported.accounts[0].credentials,
    })
  })

  it.each(platformCases)('canonicalizes %s API Key aliases and survives canonical JSON round-trip', (inputPlatform, outputPlatform) => {
    const parsed = parseTokenInput(JSON.stringify({
      name: `${inputPlatform} API Key`,
      platform: inputPlatform,
      account_type: 'api_key',
      apiKey: `${inputPlatform}-key`,
      baseUrl: `https://${inputPlatform}.example.com/v1`,
      customHeaders: { 'x-tenant': inputPlatform },
      regionHint: 'test-region',
    }), NOW)

    expect(parsed.accounts).toHaveLength(1)
    expect(parsed.accounts[0]).toMatchObject({
      platform: outputPlatform,
      accountType: 'apikey',
      apiKey: `${inputPlatform}-key`,
    })

    const direct = JSON.parse(exportTokenAccounts(parsed.accounts, 'xiass', { now: NOW }).text)
    expect(direct.accounts[0].credentials).toEqual({
      regionHint: 'test-region',
      api_key: `${inputPlatform}-key`,
      base_url: `https://${inputPlatform}.example.com/v1`,
      custom_headers: { 'x-tenant': inputPlatform },
    })
    expect(direct.accounts[0].credentials).not.toHaveProperty('platform')
    expect(direct.accounts[0].credentials).not.toHaveProperty('account_type')
    expect(direct.accounts[0].credentials).not.toHaveProperty('apiKey')
    expect(direct.accounts[0].credentials).not.toHaveProperty('baseUrl')
    expect(direct.accounts[0].credentials).not.toHaveProperty('customHeaders')

    const canonical = exportTokenAccounts(parsed.accounts, 'canonical', { now: NOW })
    const reparsed = parseTokenInput(canonical.text, NOW)
    const roundTripped = JSON.parse(exportTokenAccounts(reparsed.accounts, 'xiass', { now: NOW }).text)
    expect(roundTripped.accounts[0]).toMatchObject({
      platform: outputPlatform,
      type: 'apikey',
      credentials: direct.accounts[0].credentials,
    })
  })

  it('normalizes aliases inside XIASS credentials without letting extras override canonical values', () => {
    const parsed = parseTokenInput(JSON.stringify({
      type: 'sub2api-data',
      version: 1,
      accounts: [{
        name: 'Mixed aliases',
        platform: 'anthropic',
        type: 'oauth',
        credentials: {
          access_token: 'snake-access',
          accessToken: 'camel-access',
          refreshToken: 'camel-refresh',
          apiKey: 'camel-key',
          baseUrl: 'https://claude.example.com',
          custom: { keep: true },
        },
      }],
    }), NOW)
    parsed.accounts[0].credentialExtras = {
      ...parsed.accounts[0].credentialExtras,
      accessToken: 'must-not-override',
      api_key: 'must-not-override',
    }

    const exported = JSON.parse(exportTokenAccounts(parsed.accounts, 'xiass', { now: NOW }).text)
    expect(exported.accounts[0].credentials).toEqual({
      custom: { keep: true },
      access_token: 'snake-access',
      refresh_token: 'camel-refresh',
      api_key: 'camel-key',
      base_url: 'https://claude.example.com',
    })
  })

  it('detects a flat API Key JSON without OAuth fields and infers the API Key account type', () => {
    const parsed = parseTokenInput(JSON.stringify({
      platform: 'grok',
      apiKey: 'grok-key',
      baseUrl: 'https://api.x.ai/v1',
    }), NOW)

    expect(parsed.accounts).toHaveLength(1)
    expect(parsed.accounts[0]).toMatchObject({ platform: 'grok', accountType: 'apikey', apiKey: 'grok-key' })
    const exported = JSON.parse(exportTokenAccounts(parsed.accounts, 'xiass', { now: NOW }).text)
    expect(exported.accounts[0].credentials).toEqual({
      api_key: 'grok-key',
      base_url: 'https://api.x.ai/v1',
    })
  })

  it('accepts Codex auth.json, OAuth JSON, JSON arrays, and JSONL in one input', () => {
    const auth = JSON.stringify({
      OPENAI_API_KEY: null,
      tokens: { access_token: 'access-a', refresh_token: 'refresh-a', account_id: 'account-a' },
      last_refresh: '2026-08-17T01:00:00.000Z',
    })
    const oauth = JSON.stringify({ access_token: 'access-b', refresh_token: 'refresh-b', email: 'b@example.com' })
    const parsed = parseTokenInput(`${auth}\n${oauth}`, NOW)

    expect(parsed.accounts).toHaveLength(2)
    expect(parsed.sourceFormats).toEqual(['codex-auth', 'oauth'])
    expect(parsed.accounts.map((item) => item.refreshToken)).toEqual(['refresh-a', 'refresh-b'])
  })

  it('never treats sessionToken as refresh_token and reports missing renewable credentials', () => {
    const parsed = parseTokenInput(JSON.stringify({ accessToken: 'access-only', sessionToken: 'session-cookie' }), NOW)
    const exported = JSON.parse(exportTokenAccounts(parsed.accounts, 'oauth', { now: NOW }).text)

    expect(exported.refresh_token).toBeUndefined()
    expect(exported.session_token).toBeUndefined()
    expect(parsed.warnings.map((warning) => warning.code)).toEqual([
      'session-token-not-convertible',
      'missing-refresh-token',
    ])
  })

  it('exports multiple accounts to session, OAuth, Codex auth, and canonical JSON formats', () => {
    const parsed = parseTokenInput(JSON.stringify([
      { access_token: 'access-1', refresh_token: 'refresh-1' },
      { access_token: 'access-2', refresh_token: 'refresh-2' },
    ]), NOW)

    const sessions = JSON.parse(exportTokenAccounts(parsed.accounts, 'session', { now: NOW }).text)
    const oauth = JSON.parse(exportTokenAccounts(parsed.accounts, 'oauth', { now: NOW }).text)
    const auth = JSON.parse(exportTokenAccounts(parsed.accounts, 'codex-auth', { now: NOW }).text)
    const canonical = JSON.parse(exportTokenAccounts(parsed.accounts, 'canonical', { now: NOW }).text)

    expect(sessions).toHaveLength(2)
    expect(oauth[1].refresh_token).toBe('refresh-2')
    expect(auth[0].tokens.access_token).toBe('access-1')
    expect(canonical).toMatchObject({ type: 'xiass-token-converter', version: 2 })
    expect(canonical.accounts).toHaveLength(2)
  })

  it('only allows OpenAI OAuth accounts to use OpenAI-specific output formats', () => {
    const parsed = parseTokenInput(JSON.stringify({
      type: 'sub2api-data',
      version: 1,
      exported_at: NOW.toISOString(),
      proxies: [],
      accounts: [
        {
          name: 'OpenAI OAuth',
          platform: 'openai',
          type: 'oauth',
          credentials: { access_token: 'access', refresh_token: 'refresh' },
          concurrency: 3,
          priority: 1,
        },
        {
          name: 'OpenAI API Key',
          platform: 'openai',
          type: 'apikey',
          credentials: { api_key: 'openai-key' },
          concurrency: 3,
          priority: 2,
        },
        {
          name: 'Claude OAuth',
          platform: 'anthropic',
          type: 'oauth',
          credentials: { access_token: 'claude-access', refresh_token: 'claude-refresh' },
          concurrency: 3,
          priority: 3,
        },
      ],
    }), NOW)

    expect(isTokenOutputFormatCompatible(parsed.accounts[0], 'session')).toBe(true)
    expect(isTokenOutputFormatCompatible(parsed.accounts[1], 'session')).toBe(false)
    expect(isTokenOutputFormatCompatible(parsed.accounts[2], 'oauth')).toBe(false)
    expect(areTokenAccountsCompatibleWithFormat(parsed.accounts, 'xiass')).toBe(true)
    expect(areTokenAccountsCompatibleWithFormat(parsed.accounts, 'canonical')).toBe(true)
    expect(areTokenAccountsCompatibleWithFormat(parsed.accounts, 'codex-auth')).toBe(false)
    expect(() => exportTokenAccounts(parsed.accounts, 'session', { now: NOW }))
      .toThrow('only supports OpenAI OAuth accounts')
  })

  it('uses requested XIASS defaults for records without preserved scheduling values', () => {
    const parsed = parseTokenInput(JSON.stringify({ access_token: 'access-1', refresh_token: 'refresh-1' }), NOW)
    const exported = JSON.parse(exportTokenAccounts(parsed.accounts, 'xiass', {
      concurrency: 10,
      priority: 7,
      now: NOW,
    }).text)

    expect(exported.accounts[0].concurrency).toBe(10)
    expect(exported.accounts[0].priority).toBe(7)
  })

  it('rejects malformed JSON instead of treating the fragment as an access token', () => {
    const parsed = parseTokenInput('{"broken":', NOW)

    expect(parsed.accounts).toEqual([])
    expect(parsed.skipped).toBe(1)
    expect(parsed.warnings).toEqual([{ code: 'invalid-json', index: 1 }])
  })

  it('still accepts an explicit opaque raw token', () => {
    const rawToken = 'eyJhbGciOiJub25lIn0.eyJzdWIiOiJ1c2VyIn0.signature'
    const parsed = parseTokenInput(rawToken, NOW)

    expect(parsed.accounts).toHaveLength(1)
    expect(parsed.accounts[0]).toMatchObject({
      sourceFormat: 'raw-token',
      platform: 'openai',
      accountType: 'oauth',
      accessToken: rawToken,
    })
  })
})

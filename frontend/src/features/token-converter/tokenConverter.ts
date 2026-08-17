export type TokenSourceFormat =
  | 'xiass'
  | 'session'
  | 'codex-auth'
  | 'oauth'
  | 'canonical'
  | 'raw-token'

export type TokenOutputFormat = 'xiass' | 'session' | 'codex-auth' | 'oauth' | 'canonical'

export type TokenWarningCode =
  | 'missing-access-token'
  | 'missing-refresh-token'
  | 'session-token-not-convertible'
  | 'raw-token-assumed-access'
  | 'invalid-json'
  | 'unsupported-entry'

export interface TokenWarning {
  code: TokenWarningCode
  index: number
  detail?: string
}

export interface NormalizedTokenAccount {
  index: number
  sourceFormat: TokenSourceFormat
  platform: string
  accountType: string
  name: string
  email?: string
  accountId?: string
  userId?: string
  planType?: string
  organizationId?: string
  accessToken?: string
  refreshToken?: string
  idToken?: string
  sessionToken?: string
  apiKey?: string
  expiresAt?: string
  lastRefresh?: string
  concurrency?: number
  priority?: number
  notes?: string | null
  proxyKey?: string | null
  rateMultiplier?: number | null
  accountExpiresAt?: number | null
  autoPauseOnExpired?: boolean
  rawCredentials: Record<string, unknown>
  rawAccount?: Record<string, unknown>
  credentialExtras?: Record<string, unknown>
  extra?: Record<string, unknown>
}

export interface TokenParseResult {
  accounts: NormalizedTokenAccount[]
  sourceFormats: TokenSourceFormat[]
  warnings: TokenWarning[]
  skipped: number
  proxies: unknown[]
}

export interface TokenExportOptions {
  concurrency?: number
  priority?: number
  proxies?: unknown[]
  now?: Date
}

export interface TokenExportResult {
  text: string
  fileName: string
  accountCount: number
}

type UnknownRecord = Record<string, unknown>

const CREDENTIAL_KEY_ALIASES: ReadonlyArray<readonly [string, readonly string[]]> = [
  ['access_token', ['access_token', 'accessToken', 'token']],
  ['refresh_token', ['refresh_token', 'refreshToken']],
  ['id_token', ['id_token', 'idToken']],
  ['session_token', ['session_token', 'sessionToken']],
  ['api_key', ['api_key', 'apiKey', 'apikey']],
  ['email', ['email']],
  ['chatgpt_account_id', ['chatgpt_account_id', 'chatgptAccountId', 'account_id', 'accountId']],
  ['chatgpt_user_id', ['chatgpt_user_id', 'chatgptUserId', 'user_id', 'userId']],
  ['plan_type', ['plan_type', 'planType']],
  ['organization_id', ['organization_id', 'organizationId', 'org_id', 'orgId']],
  ['expires_at', ['expires_at', 'expiresAt']],
  ['token_type', ['token_type', 'tokenType']],
  ['base_url', ['base_url', 'baseUrl']],
  ['setup_token', ['setup_token', 'setupToken']],
  ['client_id', ['client_id', 'clientId']],
  ['client_secret', ['client_secret', 'clientSecret']],
  ['project_id', ['project_id', 'projectId']],
  ['service_account_json', ['service_account_json', 'serviceAccountJson']],
  ['oauth_type', ['oauth_type', 'oauthType']],
  ['tier_id', ['tier_id', 'tierId']],
  ['user_agent', ['user_agent', 'userAgent']],
  ['model_mapping', ['model_mapping', 'modelMapping']],
  ['custom_headers', ['custom_headers', 'customHeaders']],
  ['header_overrides', ['header_overrides', 'headerOverrides']],
  ['header_override_enabled', ['header_override_enabled', 'headerOverrideEnabled']],
  ['grok_custom_base_url_enabled', ['grok_custom_base_url_enabled', 'grokCustomBaseUrlEnabled']],
  ['auth_mode', ['auth_mode', 'authMode']],
  ['service_account', ['service_account', 'serviceAccount']],
  ['client_email', ['client_email', 'clientEmail']],
  ['private_key', ['private_key', 'privateKey']],
  ['location', ['location']],
  ['vertex_location', ['vertex_location', 'vertexLocation']],
  ['aws_access_key_id', ['aws_access_key_id', 'awsAccessKeyId']],
  ['aws_secret_access_key', ['aws_secret_access_key', 'awsSecretAccessKey']],
  ['aws_session_token', ['aws_session_token', 'awsSessionToken']],
  ['aws_region', ['aws_region', 'awsRegion']],
  ['aws_force_global', ['aws_force_global', 'awsForceGlobal']],
  ['antigravity_project_id', ['antigravity_project_id', 'antigravityProjectId']],
]

// Export is intentionally allow-listed. Source records can contain account-management
// settings and personal profile data that do not belong in a credential conversion.
const PORTABLE_CREDENTIAL_KEYS = [
  'access_token',
  'refresh_token',
  'id_token',
  'session_token',
  'api_key',
  'base_url',
  'setup_token',
  'client_id',
  'client_secret',
  'project_id',
  'service_account_json',
  'service_account',
  'oauth_type',
  'tier_id',
  'token_type',
  'user_agent',
  'model_mapping',
  'auth_mode',
  'client_email',
  'private_key',
  'location',
  'vertex_location',
  'aws_access_key_id',
  'aws_secret_access_key',
  'aws_session_token',
  'aws_region',
  'aws_force_global',
  'antigravity_project_id',
  'custom_headers',
  'header_overrides',
  'header_override_enabled',
  'grok_custom_base_url_enabled',
] as const

const CREDENTIAL_ALIAS_KEYS = new Set(CREDENTIAL_KEY_ALIASES.flatMap(([, aliases]) => aliases))
const NORMALIZED_ACCOUNT_CREDENTIAL_KEYS = new Set([
  'access_token',
  'refresh_token',
  'id_token',
  'session_token',
  'api_key',
  'email',
  'chatgpt_account_id',
  'chatgpt_user_id',
  'plan_type',
  'organization_id',
  'expires_at',
])
const ACCOUNT_STRUCTURE_KEYS = new Set([
  'platform',
  'account_type',
  'accountType',
  'type',
  'name',
  'notes',
  'credentials',
  'raw_credentials',
  'rawCredentials',
  'credential_extras',
  'credentialExtras',
  'raw_account',
  'rawAccount',
  'extra',
  'concurrency',
  'priority',
  'proxy_key',
  'proxyKey',
  'rate_multiplier',
  'rateMultiplier',
  'auto_pause_on_expired',
  'autoPauseOnExpired',
  'last_refresh',
  'lastRefresh',
  'expires',
  'expires_in',
  'expiresIn',
  'user',
  'account',
  'tokens',
  'OPENAI_API_KEY',
])

function isRecord(value: unknown): value is UnknownRecord {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function getPath(record: UnknownRecord, path: string[]): unknown {
  let value: unknown = record
  for (const key of path) {
    if (!isRecord(value)) return undefined
    value = value[key]
  }
  return value
}

function firstString(record: UnknownRecord, ...paths: string[][]): string | undefined {
  for (const path of paths) {
    const value = getPath(record, path)
    if (typeof value === 'string' && value.trim()) return value.trim()
  }
  return undefined
}

function firstNumber(record: UnknownRecord, ...paths: string[][]): number | undefined {
  for (const path of paths) {
    const value = getPath(record, path)
    if (typeof value === 'number' && Number.isFinite(value)) return value
    if (typeof value === 'string' && value.trim()) {
      const parsed = Number(value)
      if (Number.isFinite(parsed)) return parsed
    }
  }
  return undefined
}

function normalizeTime(value: unknown, now: Date, expiresIn = false): string | undefined {
  if (value === undefined || value === null || value === '') return undefined
  if (expiresIn) {
    const seconds = typeof value === 'number' ? value : Number(value)
    if (!Number.isFinite(seconds) || seconds < 0) return undefined
    return new Date(now.getTime() + seconds * 1000).toISOString()
  }
  if (typeof value === 'number') {
    const millis = value > 10_000_000_000 ? value : value * 1000
    const date = new Date(millis)
    return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
  }
  if (typeof value === 'string') {
    const numeric = Number(value)
    if (value.trim() && Number.isFinite(numeric)) return normalizeTime(numeric, now)
    const date = new Date(value)
    return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
  }
  return undefined
}

function decodeJwtClaims(token: string | undefined): UnknownRecord | undefined {
  if (!token) return undefined
  const parts = token.split('.')
  if (parts.length !== 3 || !parts[1]) return undefined
  try {
    const normalized = parts[1].replace(/-/g, '+').replace(/_/g, '/')
    const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, '=')
    const binary = atob(padded)
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
    const parsed = JSON.parse(new TextDecoder().decode(bytes))
    return isRecord(parsed) ? parsed : undefined
  } catch {
    return undefined
  }
}

function enrichFromJwt(account: NormalizedTokenAccount): void {
  const accessClaims = decodeJwtClaims(account.accessToken)
  const idClaims = decodeJwtClaims(account.idToken)
  const claims = accessClaims ?? idClaims
  if (!claims) return

  const authClaims = getPath(claims, ['https://api.openai.com/auth'])
  const auth = isRecord(authClaims) ? authClaims : undefined
  const profileClaims = getPath(claims, ['https://api.openai.com/profile'])
  const profile = isRecord(profileClaims) ? profileClaims : undefined

  account.email ||= firstString(claims, ['email']) ?? (profile ? firstString(profile, ['email']) : undefined)
  account.userId ||=
    (auth ? firstString(auth, ['chatgpt_user_id'], ['user_id']) : undefined) ??
    firstString(claims, ['sub'])
  account.accountId ||= auth ? firstString(auth, ['chatgpt_account_id']) : undefined
  account.planType ||= auth ? firstString(auth, ['chatgpt_plan_type']) : undefined
  account.organizationId ||= auth ? firstString(auth, ['poid']) : undefined
  if (!account.expiresAt) {
    account.expiresAt = normalizeTime(claims.exp, new Date())
  }
}

function canonicalizeCredentialRecord(record: UnknownRecord, omitStructure = false): UnknownRecord {
  const normalized: UnknownRecord = {}
  for (const [key, value] of Object.entries(record)) {
    if (CREDENTIAL_ALIAS_KEYS.has(key) || (omitStructure && ACCOUNT_STRUCTURE_KEYS.has(key))) continue
    normalized[key] = value
  }
  for (const [canonicalKey, aliases] of CREDENTIAL_KEY_ALIASES) {
    for (const alias of aliases) {
      const value = record[alias]
      if (value !== undefined && value !== '') {
        normalized[canonicalKey] = value
        break
      }
    }
  }
  return normalized
}

function credentialExtrasFrom(record: UnknownRecord, omitStructure = false): UnknownRecord | undefined {
  const normalized = canonicalizeCredentialRecord(record, omitStructure)
  for (const key of NORMALIZED_ACCOUNT_CREDENTIAL_KEYS) delete normalized[key]
  return Object.keys(normalized).length ? normalized : undefined
}

function mergeCredentialExtras(...records: Array<UnknownRecord | undefined>): UnknownRecord | undefined {
  const merged: UnknownRecord = {}
  for (const record of records) {
    if (!record) continue
    Object.assign(merged, credentialExtrasFrom(record))
  }
  return Object.keys(merged).length ? merged : undefined
}

function normalizePlatform(value: string | undefined): string {
  const platform = value?.trim() || 'openai'
  switch (platform.toLowerCase()) {
    case 'openai':
      return 'openai'
    case 'anthropic':
    case 'claude':
      return 'anthropic'
    case 'gemini':
      return 'gemini'
    case 'antigravity':
      return 'antigravity'
    case 'grok':
      return 'grok'
    default:
      return platform
  }
}

function normalizeAccountType(value: string | undefined, credentials: UnknownRecord): string {
  const accountType = value?.trim()
  if (!accountType) return typeof credentials.api_key === 'string' && credentials.api_key.trim() ? 'apikey' : 'oauth'
  switch (accountType.toLowerCase().replace(/_/g, '-')) {
    case 'api-key':
    case 'apikey':
      return 'apikey'
    case 'setup-token':
      return 'setup-token'
    case 'service-account':
      return 'service_account'
    default:
      return accountType.toLowerCase()
  }
}

function isXiassAccountRecord(record: UnknownRecord): boolean {
  return typeof record.platform === 'string' && typeof record.type === 'string' && isRecord(record.credentials)
}

function isOpenAIOAuth(account: NormalizedTokenAccount): boolean {
  return account.platform.toLowerCase() === 'openai' && account.accountType.toLowerCase() === 'oauth'
}

export function isTokenOutputFormatCompatible(
  account: NormalizedTokenAccount,
  format: TokenOutputFormat,
): boolean {
  return format === 'xiass' || format === 'canonical' || isOpenAIOAuth(account)
}

export function areTokenAccountsCompatibleWithFormat(
  accounts: NormalizedTokenAccount[],
  format: TokenOutputFormat,
): boolean {
  return accounts.every((account) => isTokenOutputFormatCompatible(account, format))
}

function detectRecordFormat(record: UnknownRecord): TokenSourceFormat | undefined {
  const nestedCredentials = isRecord(record.credentials) ? record.credentials : undefined
  if (record.type === 'xiass-token-converter') return 'canonical'
  if (isXiassAccountRecord(record)) return 'xiass'
  if (isRecord(record.tokens)) return 'codex-auth'
  if (
    typeof record.accessToken === 'string' ||
    typeof record.refreshToken === 'string' ||
    typeof record.idToken === 'string' ||
    typeof record.sessionToken === 'string' ||
    isRecord(record.user) ||
    isRecord(record.account)
  ) {
    return 'session'
  }
  if (
    typeof record.access_token === 'string' ||
    typeof record.refresh_token === 'string' ||
    typeof record.id_token === 'string' ||
    typeof record.session_token === 'string' ||
    typeof record.api_key === 'string' ||
    typeof record.apiKey === 'string' ||
    typeof record.apikey === 'string' ||
    (nestedCredentials && CREDENTIAL_KEY_ALIASES.some(([, aliases]) => aliases.some((key) => key in nestedCredentials)))
  ) {
    return 'oauth'
  }
  return undefined
}

function accountFromRecord(
  record: UnknownRecord,
  index: number,
  now: Date,
  forcedFormat?: TokenSourceFormat,
): NormalizedTokenAccount | undefined {
  const sourceFormat = forcedFormat ?? detectRecordFormat(record)
  if (!sourceFormat) return undefined

  const canonicalCredentials = isRecord(record.raw_credentials)
    ? record.raw_credentials
    : isRecord(record.credentials)
      ? record.credentials
      : undefined
  const tokenRoot = sourceFormat === 'codex-auth' && isRecord(record.tokens)
    ? record.tokens
    : canonicalCredentials
      ? canonicalCredentials
      : record
  const normalizedCredentials = canonicalizeCredentialRecord(tokenRoot, tokenRoot === record)
  const platform = normalizePlatform(firstString(record, ['platform']))
  const explicitType = firstString(record, ['account_type'], ['accountType']) ??
    (sourceFormat !== 'canonical' ? firstString(record, ['type']) : undefined)
  const accountType = normalizeAccountType(explicitType, normalizedCredentials)
  const account: NormalizedTokenAccount = {
    index,
    sourceFormat,
    platform,
    accountType,
    name: firstString(record, ['name'], ['user', 'name']) ?? `${platform} ${accountType} ${index}`,
    email: firstString(record, ['email'], ['user', 'email']) ?? firstString(normalizedCredentials, ['email']),
    accountId: firstString(
      normalizedCredentials,
      ['chatgpt_account_id'],
    ) ?? firstString(tokenRoot, ['account', 'id']),
    userId: firstString(
      normalizedCredentials,
      ['chatgpt_user_id'],
    ) ?? firstString(tokenRoot, ['user', 'id']),
    planType: firstString(normalizedCredentials, ['plan_type']) ?? firstString(tokenRoot, ['account', 'plan_type'], ['account', 'planType']),
    organizationId: firstString(normalizedCredentials, ['organization_id']),
    accessToken: firstString(normalizedCredentials, ['access_token']),
    refreshToken: firstString(normalizedCredentials, ['refresh_token']),
    idToken: firstString(normalizedCredentials, ['id_token']),
    sessionToken: firstString(normalizedCredentials, ['session_token']),
    apiKey: firstString(normalizedCredentials, ['api_key']),
    expiresAt:
      normalizeTime(normalizedCredentials.expires_at, now) ??
      normalizeTime(record.expires, now) ??
      normalizeTime(tokenRoot.expires_in ?? tokenRoot.expiresIn, now, true),
    lastRefresh: normalizeTime(record.last_refresh ?? record.lastRefresh, now),
    rawCredentials: { ...tokenRoot },
  }

  const derivedExtras = credentialExtrasFrom(tokenRoot, tokenRoot === record)
  if (sourceFormat === 'canonical') {
    account.credentialExtras = mergeCredentialExtras(
      derivedExtras,
      isRecord(record.credential_extras) ? record.credential_extras : undefined,
    )
    account.extra = isRecord(record.extra) ? { ...record.extra } : undefined
    account.rawAccount = isRecord(record.raw_account) ? { ...record.raw_account } : undefined
  } else {
    account.credentialExtras = derivedExtras
  }

  if (isOpenAIOAuth(account)) {
    enrichFromJwt(account)
  }
  return account
}

function accountFromXiass(record: UnknownRecord, index: number, now: Date): NormalizedTokenAccount | undefined {
  if (!isRecord(record.credentials)) return undefined
  const credentials = record.credentials
  const account = accountFromRecord(
    {
      ...credentials,
      name: record.name,
      email: firstString(credentials, ['email']),
      platform: record.platform,
      account_type: record.type,
    },
    index,
    now,
    'xiass',
  )
  if (!account) return undefined
  account.platform = normalizePlatform(firstString(record, ['platform']))
  account.accountType = normalizeAccountType(firstString(record, ['type']), canonicalizeCredentialRecord(credentials))
  account.name = firstString(record, ['name']) ?? account.name
  account.notes = typeof record.notes === 'string' || record.notes === null ? record.notes : undefined
  account.concurrency = firstNumber(record, ['concurrency'])
  account.priority = firstNumber(record, ['priority'])
  account.proxyKey = typeof record.proxy_key === 'string' || record.proxy_key === null ? record.proxy_key : undefined
  account.rateMultiplier = record.rate_multiplier === null ? null : firstNumber(record, ['rate_multiplier'])
  account.accountExpiresAt = record.expires_at === null ? null : firstNumber(record, ['expires_at'])
  account.autoPauseOnExpired = typeof record.auto_pause_on_expired === 'boolean'
    ? record.auto_pause_on_expired
    : undefined
  account.credentialExtras = credentialExtrasFrom(credentials)
  account.extra = isRecord(record.extra) ? { ...record.extra } : undefined
  account.rawCredentials = { ...credentials }
  account.rawAccount = { ...record }
  if (isOpenAIOAuth(account)) {
    enrichFromJwt(account)
    account.name = firstString(record, ['name']) ?? account.name
  }
  return account
}

function extractJsonValues(content: string): unknown[] | undefined {
  try {
    return [JSON.parse(content)]
  } catch {
    // Continue with a small JSON-stream scanner for JSONL and adjacent objects.
  }

  const values: unknown[] = []
  let start = -1
  let depth = 0
  let inString = false
  let escaped = false

  for (let index = 0; index < content.length; index += 1) {
    const character = content[index]
    if (inString) {
      if (escaped) escaped = false
      else if (character === '\\') escaped = true
      else if (character === '"') inString = false
      continue
    }
    if (character === '"' && start >= 0) {
      inString = true
      continue
    }
    if (character === '{' || character === '[') {
      if (depth === 0) start = index
      depth += 1
      continue
    }
    if (character === '}' || character === ']') {
      if (depth === 0) return undefined
      depth -= 1
      if (depth === 0 && start >= 0) {
        try {
          values.push(JSON.parse(content.slice(start, index + 1)))
        } catch {
          return undefined
        }
        start = -1
      }
      continue
    }
    if (depth === 0 && start < 0 && !/[\s,]/.test(character)) return undefined
  }
  return depth === 0 && values.length ? values : undefined
}

function looksLikeRawToken(value: string): boolean {
  const token = value.trim()
  return token.length >= 20
    && !/\s/.test(token)
    && /^[A-Za-z0-9._~+/=-]+$/.test(token)
}

function flattenValues(value: unknown): unknown[] {
  if (Array.isArray(value)) return value.flatMap(flattenValues)
  return [value]
}

export function parseTokenInput(content: string, now = new Date()): TokenParseResult {
  const trimmed = content.trim()
  if (!trimmed) {
    return { accounts: [], sourceFormats: [], warnings: [], skipped: 0, proxies: [] }
  }

  const parsedValues = extractJsonValues(trimmed)
  if (!parsedValues && (trimmed.startsWith('{') || trimmed.startsWith('['))) {
    return {
      accounts: [],
      sourceFormats: [],
      warnings: [{ code: 'invalid-json', index: 1 }],
      skipped: 1,
      proxies: [],
    }
  }
  const rawValues = parsedValues ?? trimmed.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)
  const accounts: NormalizedTokenAccount[] = []
  const warnings: TokenWarning[] = []
  const sourceFormats = new Set<TokenSourceFormat>()
  const proxies: unknown[] = []
  let skipped = 0

  const append = (account: NormalizedTokenAccount | undefined) => {
    if (!account) {
      skipped += 1
      warnings.push({ code: 'unsupported-entry', index: accounts.length + skipped })
      return
    }
    account.index = accounts.length + 1
    accounts.push(account)
    sourceFormats.add(account.sourceFormat)
    if (isOpenAIOAuth(account) && account.sessionToken) {
      warnings.push({ code: 'session-token-not-convertible', index: account.index })
    }
    if (isOpenAIOAuth(account) && !account.accessToken) {
      warnings.push({ code: 'missing-access-token', index: account.index })
    }
    if (isOpenAIOAuth(account) && !account.refreshToken) {
      warnings.push({ code: 'missing-refresh-token', index: account.index })
    }
  }

  const visit = (value: unknown) => {
    if (Array.isArray(value)) {
      value.forEach(visit)
      return
    }
    if (typeof value === 'string') {
      const token = value.trim()
      if (!token) return
      if (!looksLikeRawToken(token)) {
        append(undefined)
        return
      }
      const account: NormalizedTokenAccount = {
        index: accounts.length + 1,
        sourceFormat: 'raw-token',
        platform: 'openai',
        accountType: 'oauth',
        name: `OpenAI OAuth ${accounts.length + 1}`,
        accessToken: token,
        rawCredentials: { access_token: token },
      }
      enrichFromJwt(account)
      append(account)
      warnings.push({ code: 'raw-token-assumed-access', index: account.index })
      return
    }
    if (!isRecord(value)) {
      append(undefined)
      return
    }

    if (Array.isArray(value.accounts)) {
      if (Array.isArray(value.proxies)) proxies.push(...value.proxies)
      const isCanonical = value.type === 'xiass-token-converter'
      for (const item of value.accounts) {
        if (!isRecord(item)) {
          append(undefined)
          continue
        }
        append(
          isXiassAccountRecord(item) && !isCanonical
            ? accountFromXiass(item, accounts.length + 1, now)
            : accountFromRecord(item, accounts.length + 1, now, isCanonical ? 'canonical' : undefined),
        )
      }
      return
    }
    if (Array.isArray(value.sessions)) {
      value.sessions.forEach(visit)
      return
    }
    append(
      isXiassAccountRecord(value)
        ? accountFromXiass(value, accounts.length + 1, now)
        : accountFromRecord(value, accounts.length + 1, now),
    )
  }

  rawValues.flatMap(flattenValues).forEach(visit)
  return { accounts, sourceFormats: [...sourceFormats], warnings, skipped, proxies }
}

function compactRecord(record: UnknownRecord): UnknownRecord {
  return Object.fromEntries(
    Object.entries(record).filter(([, value]) => value !== undefined && value !== ''),
  )
}

function portableCredentials(
  account: NormalizedTokenAccount,
  includeOpenAIAccountRouting: boolean,
): UnknownRecord {
  const source = canonicalizeCredentialRecord({
    ...account.rawCredentials,
    ...(account.credentialExtras ?? {}),
  }, true)
  const credentials: UnknownRecord = {}
  for (const key of PORTABLE_CREDENTIAL_KEYS) {
    const value = source[key]
    if (value !== undefined && value !== '') credentials[key] = value
  }

  const assignCanonical = (key: string, value: unknown) => {
    if (value !== undefined && value !== '') credentials[key] = value
  }
  assignCanonical('access_token', account.accessToken)
  assignCanonical('refresh_token', account.refreshToken)
  assignCanonical('id_token', account.idToken)
  assignCanonical('api_key', account.apiKey)
  assignCanonical('expires_at', account.expiresAt)
  if (includeOpenAIAccountRouting && account.platform === 'openai' && account.accountType === 'oauth') {
    assignCanonical('chatgpt_account_id', account.accountId)
    assignCanonical('organization_id', account.organizationId)
  }
  return compactRecord(credentials)
}

function asSession(account: NormalizedTokenAccount): UnknownRecord {
  return compactRecord({
    accessToken: account.accessToken,
    refreshToken: account.refreshToken,
    idToken: account.idToken,
  })
}

function asCodexAuth(account: NormalizedTokenAccount, now: Date): UnknownRecord {
  return {
    OPENAI_API_KEY: null,
    tokens: compactRecord({
      id_token: account.idToken,
      access_token: account.accessToken,
      refresh_token: account.refreshToken,
      account_id: account.accountId,
    }),
    last_refresh: account.lastRefresh ?? now.toISOString(),
  }
}

function asOAuth(account: NormalizedTokenAccount): UnknownRecord {
  const source = portableCredentials(account, false)
  return compactRecord({
    access_token: account.accessToken,
    refresh_token: account.refreshToken,
    id_token: account.idToken,
    token_type: source.token_type ?? 'Bearer',
    expires_at: account.expiresAt,
  })
}

function asCanonical(account: NormalizedTokenAccount): UnknownRecord {
  return {
    platform: account.platform,
    account_type: account.accountType,
    credentials: portableCredentials(account, true),
  }
}

function asXiassAccount(account: NormalizedTokenAccount, options: TokenExportOptions): UnknownRecord {
  return compactRecord({
    name: account.name,
    platform: account.platform,
    type: account.accountType,
    credentials: portableCredentials(account, true),
    notes: account.notes,
    extra: account.extra,
    proxy_key: account.proxyKey,
    concurrency: account.concurrency ?? options.concurrency ?? 3,
    priority: account.priority ?? options.priority ?? 50,
    rate_multiplier: account.rateMultiplier,
    expires_at: account.accountExpiresAt,
    auto_pause_on_expired: account.autoPauseOnExpired,
  })
}

export function exportTokenAccounts(
  accounts: NormalizedTokenAccount[],
  format: TokenOutputFormat,
  options: TokenExportOptions = {},
): TokenExportResult {
  const now = options.now ?? new Date()
  const incompatible = accounts.filter((account) => !isTokenOutputFormatCompatible(account, format))
  if (incompatible.length) {
    const identities = incompatible
      .map((account) => `#${account.index} ${account.platform}/${account.accountType}`)
      .join(', ')
    throw new Error(`Output format ${format} only supports OpenAI OAuth accounts: ${identities}`)
  }
  let value: unknown
  let fileName: string

  if (format === 'xiass') {
    value = {
      type: 'sub2api-data',
      version: 1,
      exported_at: now.toISOString(),
      proxies: options.proxies ?? [],
      accounts: accounts.map((account) => asXiassAccount(account, options)),
    }
    fileName = `xiass-accounts-${now.toISOString().slice(0, 10)}.json`
  } else if (format === 'session') {
    const sessions = accounts.map(asSession)
    value = sessions.length === 1 ? sessions[0] : sessions
    fileName = `openai-sessions-${now.toISOString().slice(0, 10)}.json`
  } else if (format === 'codex-auth') {
    const authFiles = accounts.map((account) => asCodexAuth(account, now))
    value = authFiles.length === 1 ? authFiles[0] : authFiles
    fileName = accounts.length === 1 ? 'auth.json' : `codex-auth-${now.toISOString().slice(0, 10)}.json`
  } else if (format === 'oauth') {
    const tokens = accounts.map(asOAuth)
    value = tokens.length === 1 ? tokens[0] : tokens
    fileName = `openai-oauth-tokens-${now.toISOString().slice(0, 10)}.json`
  } else {
    value = {
      type: 'xiass-token-converter',
      version: 3,
      accounts: accounts.map(asCanonical),
    }
    fileName = `xiass-token-json-${now.toISOString().slice(0, 10)}.json`
  }

  return {
    text: JSON.stringify(value, null, 2),
    fileName,
    accountCount: accounts.length,
  }
}

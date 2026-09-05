import type { ApiKey } from '@/types'

export interface CodexHelperConnection {
  callback: URL
  state: string
}

export interface CodexHelperPayload {
  base_url: string
  api_key: string
  key_name: string
  model?: string
  review_model?: string
}

export interface CodexHelperModelSelection {
  model?: string
  review_model?: string
}

// Keep discovery on the current XIASS-compatible Codex protocol. The server
// accepts this version and uses it to return the live OAuth manifest.
const codexModelDiscoveryClientVersion = '0.146.0'

export function parseCodexHelperConnection(callbackValue: unknown, stateValue: unknown): CodexHelperConnection {
  if (typeof callbackValue !== 'string' || typeof stateValue !== 'string') {
    throw new Error('缺少配置助手连接参数')
  }
  if (!/^[A-Za-z0-9_-]{32,128}$/.test(stateValue)) {
    throw new Error('配置助手会话无效')
  }

  let callback: URL
  try {
    callback = new URL(callbackValue)
  } catch {
    throw new Error('配置助手回调地址无效')
  }
  const isLoopback = callback.hostname === '127.0.0.1' || callback.hostname === '[::1]' || callback.hostname === '::1'
  const port = Number(callback.port)
  if (
    callback.protocol !== 'http:' ||
    !isLoopback ||
    !Number.isInteger(port) ||
    port < 1024 ||
    port > 65535 ||
    callback.pathname !== '/callback' ||
    callback.username !== '' ||
    callback.password !== ''
  ) {
    throw new Error('配置助手只能使用本机回环地址')
  }
  return { callback, state: stateValue }
}

export function buildCodexHelperCallback(
  connection: CodexHelperConnection,
  baseUrl: string,
  apiKey: Pick<ApiKey, 'key' | 'name'>,
  selection: CodexHelperModelSelection = {}
): string {
  const payload: CodexHelperPayload = {
    base_url: normalizeBaseUrl(baseUrl),
    api_key: apiKey.key,
    key_name: apiKey.name
  }
  const model = selection.model?.trim()
  const reviewModel = selection.review_model?.trim()
  if (model) payload.model = model
  if (reviewModel) payload.review_model = reviewModel
  const encodedPayload = encodeBase64Url(JSON.stringify(payload))
  const callback = new URL(connection.callback)
  callback.search = ''
  callback.hash = new URLSearchParams({
    state: connection.state,
    payload: encodedPayload
  }).toString()
  return callback.toString()
}

export async function fetchCodexCompatibleModels(
  baseUrl: string,
  apiKey: string,
  signal?: AbortSignal
): Promise<string[]> {
  const endpoint = new URL(`${normalizeBaseUrl(baseUrl)}/models`)
  endpoint.searchParams.set('client_version', codexModelDiscoveryClientVersion)
  const response = await fetch(endpoint, {
    method: 'GET',
    headers: {
      Accept: 'application/json',
      Authorization: `Bearer ${apiKey}`
    },
    credentials: 'omit',
    cache: 'no-store',
    signal
  })
  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new Error(`读取模型列表失败 (${response.status})`)
  }
  if (!response.ok) {
    const message = body && typeof body === 'object' && 'message' in body
      ? String((body as { message?: unknown }).message || '')
      : ''
    throw new Error(message || `读取模型列表失败 (${response.status})`)
  }

  const models: string[] = []
  if (body && typeof body === 'object') {
    const envelope = body as { data?: unknown; models?: unknown }
    // Prefer the Codex manifest when both shapes are present. It is the
    // authoritative per-account catalog for XIASS; data[].id remains the
    // fallback for ordinary OpenAI-compatible providers.
    collectModelIDs(envelope.models, models)
    collectModelIDs(envelope.data, models)
  }
  const unique = [...new Set(models)]
  if (unique.length === 0) {
    throw new Error('服务端没有返回可用模型')
  }
  return unique
}

export function isCodexCompatibleKey(key: ApiKey): boolean {
  return key.status === 'active' && key.group?.platform === 'openai'
}

function normalizeBaseUrl(value: string): string {
  const parsed = new URL(value)
  if (parsed.protocol !== 'https:' || !parsed.hostname || parsed.username || parsed.password) {
    throw new Error('XIASS API 地址无效')
  }
	parsed.search = ''
	parsed.hash = ''
	const path = parsed.pathname.replace(/\/+$/, '')
	parsed.pathname = /\/v1$/i.test(path) ? path : `${path}/v1`
  return parsed.toString().replace(/\/$/, '')
}

function collectModelIDs(value: unknown, target: string[]): void {
  if (!Array.isArray(value)) return
  for (const item of value) {
    const model = typeof item === 'string'
      ? item.trim()
      : item && typeof item === 'object'
        ? String((item as { id?: unknown; slug?: unknown }).id ?? (item as { slug?: unknown }).slug ?? '').trim()
        : ''
    if (!model || model.length > 200 || hasUnsafeModelCharacters(model)) continue
    target.push(model)
    if (target.length >= 256) break
  }
}

function hasUnsafeModelCharacters(value: string): boolean {
  return Array.from(value).some((character) => {
    const code = character.charCodeAt(0)
    return code <= 0x1f || (code >= 0x7f && code <= 0x9f)
  })
}

function encodeBase64Url(value: string): string {
  const bytes = new TextEncoder().encode(value)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

import crypto from 'node:crypto'
import fs from 'node:fs'
import http from 'node:http'
import path from 'node:path'

import { chromium } from '@playwright/test'

const port = Number(process.env.PORT || 8090)
const cdpURL = process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222'
const membersURL = process.env.MEMBERS_URL || 'https://chatgpt.com/admin/members'
const operationTimeout = Number(process.env.OPERATION_TIMEOUT_MS || 30000)
const confirmationTimeout = Number(process.env.CONFIRMATION_TIMEOUT_MS || 12000)
const memberRenderTimeout = boundedDuration(process.env.MEMBER_RENDER_TIMEOUT_MS, 15000, 5000, 60000)
const pendingInviteRenderTimeout = boundedDuration(process.env.PENDING_INVITE_RENDER_TIMEOUT_MS, 15000, 3000, 60000)
const requestBodyLimit = Number(process.env.REQUEST_BODY_LIMIT_BYTES || 32768)
const serviceToken = String(process.env.TEAM_CHILD_AUTOMATION_TOKEN || '').trim()
const browserConnectTimeout = boundedDuration(process.env.BROWSER_CONNECT_TIMEOUT_MS, 30000, 1000, 120000)
const browserConnectRetryDelay = boundedDuration(process.env.BROWSER_CONNECT_RETRY_DELAY_MS, 750, 100, 5000)
const workflowTTL = boundedDuration(process.env.WORKFLOW_TTL_MS, 45 * 60 * 1000, 5 * 60 * 1000, 2 * 60 * 60 * 1000)
const workflowStateFile = process.env.NODE_ENV === 'test'
  ? ''
  : String(process.env.WORKFLOW_STATE_FILE || '/app/data/workflows.enc').trim()
const oauthPageTimeout = boundedDuration(process.env.OAUTH_PAGE_TIMEOUT_MS, 45000, 10000, 120000)
const memberRefreshAttempts = 3
const inviteAttempts = 3
const workflowProtocolVersion = 2
const officialOpenAIClientID = 'app_EMoamEEZ73f0CkXaXp7hrann'
const officialOpenAIRedirectURI = 'http://localhost:1455/auth/callback'
const officialOpenAIScope = 'openid profile email offline_access'
const protectedMemberEmails = new Set(
  String(process.env.TEAM_CHILD_PROTECTED_MEMBER_EMAILS || '')
    .split(/[\s,;]+/)
    .map((value) => normalizeEmail(value))
    .filter(Boolean)
)

let browserPromise
let operation = Promise.resolve()
let activeWorkflowID = ''
const workflows = new Map()
// All automated Team member navigation stays in one dedicated browser tab.
// Route changes (Members -> Pending invites) and the official OAuth navigation
// are serialized by the operation queue and stay in this same managed tab.
let managedBrowserPage

function workflowStateEncryptionKey() {
  if (!serviceToken) return undefined
  return crypto.createHash('sha256').update(`xiass-team-child-workflow:${serviceToken}`).digest()
}

function encryptWorkflowState(payload) {
  const key = workflowStateEncryptionKey()
  if (!key) throw new Error('workflow state encryption is unavailable')
  const iv = crypto.randomBytes(12)
  const cipher = crypto.createCipheriv('aes-256-gcm', key, iv)
  const ciphertext = Buffer.concat([cipher.update(payload, 'utf8'), cipher.final()])
  return JSON.stringify({
    version: 1,
    iv: iv.toString('base64url'),
    tag: cipher.getAuthTag().toString('base64url'),
    ciphertext: ciphertext.toString('base64url')
  })
}

function decryptWorkflowState(payload) {
  const key = workflowStateEncryptionKey()
  if (!key) throw new Error('workflow state encryption is unavailable')
  const envelope = JSON.parse(payload)
  if (envelope?.version !== 1) throw new Error('workflow state version is unsupported')
  const decipher = crypto.createDecipheriv('aes-256-gcm', key, Buffer.from(envelope.iv, 'base64url'))
  decipher.setAuthTag(Buffer.from(envelope.tag, 'base64url'))
  return Buffer.concat([
    decipher.update(Buffer.from(envelope.ciphertext, 'base64url')),
    decipher.final()
  ]).toString('utf8')
}

function persistWorkflowState() {
  if (!workflowStateFile || !serviceToken) return
  try {
    const directory = path.dirname(workflowStateFile)
    fs.mkdirSync(directory, { recursive: true, mode: 0o700 })
    const temporary = `${workflowStateFile}.${process.pid}.tmp`
    const encoded = encryptWorkflowState(JSON.stringify({
      schema_version: workflowProtocolVersion,
      active_workflow_id: activeWorkflowID,
      workflows: Array.from(workflows.values())
    }))
    fs.writeFileSync(temporary, encoded, { encoding: 'utf8', mode: 0o600 })
    fs.renameSync(temporary, workflowStateFile)
  } catch {
    console.error('team-child workflow state could not be persisted')
  }
}

function restoreWorkflowState() {
  if (!workflowStateFile || !serviceToken || !fs.existsSync(workflowStateFile)) return
  try {
    const decoded = JSON.parse(decryptWorkflowState(fs.readFileSync(workflowStateFile, 'utf8')))
    if (decoded?.schema_version !== workflowProtocolVersion || !Array.isArray(decoded.workflows)) return
    const now = Date.now()
    for (const candidate of decoded.workflows) {
      if (!candidate || typeof candidate.id !== 'string' || candidate.expiresAt <= now) continue
      if (!Array.isArray(candidate.nodes) || candidate.nodes.length !== workflowNodeDefinitions.length) continue
      const workflow = candidate
      if (workflow.status === 'running') {
        workflow.status = 'failed'
        workflow.failedNodeKey = workflow.currentNodeKey || 'oauth'
        workflow.error = '自动化组件已恢复，请核对服务器浏览器当前页面后继续'
        const activeNode = workflow.nodes.find((node) => node.key === workflow.failedNodeKey)
        if (activeNode) {
          activeNode.status = 'failed'
          activeNode.message = workflow.error
        }
      }
      workflows.set(workflow.id, workflow)
    }
    const restoredActiveID = String(decoded.active_workflow_id || '')
    const restoredActive = workflows.get(restoredActiveID)
    if (restoredActive && ['running', 'manual_required', 'callback_ready', 'failed'].includes(restoredActive.status)) {
      activeWorkflowID = restoredActiveID
    }
    persistWorkflowState()
  } catch {
    console.error('team-child workflow state could not be restored')
  }
}

function json(res, status, payload) {
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    'x-xiass-team-child-protocol': String(workflowProtocolVersion)
  })
  res.end(JSON.stringify(payload))
}

function authorized(req) {
  if (!serviceToken) return false
  const supplied = String(req.headers['x-xiass-team-child-token'] || '').trim()
  const expected = Buffer.from(serviceToken)
  const actual = Buffer.from(supplied)
  return expected.length === actual.length && crypto.timingSafeEqual(expected, actual)
}

function normalizedPath(req) {
  try {
    return new URL(req.url || '/', 'http://127.0.0.1').pathname
  } catch {
    return ''
  }
}

function boundedDuration(value, fallback, minimum, maximum) {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed >= minimum && parsed <= maximum ? parsed : fallback
}

function sleep(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

async function connectPersistentBrowser() {
  const deadline = Date.now() + browserConnectTimeout
  let lastError

  while (true) {
    try {
      return await chromium.connectOverCDP(cdpURL)
    } catch (error) {
      lastError = error
    }

    const remaining = deadline - Date.now()
    if (remaining <= 0) {
      const detail = lastError instanceof Error ? lastError.message : String(lastError || 'unknown error')
      throw new Error(`无法连接持久化 Chromium：${detail}`)
    }
    await sleep(Math.min(browserConnectRetryDelay, remaining))
  }
}

async function browser() {
  if (!browserPromise) {
    browserPromise = connectPersistentBrowser().catch((error) => {
      browserPromise = undefined
      throw error
    })
  }
  const connected = await browserPromise
  if (!connected.isConnected()) {
    browserPromise = undefined
    return browser()
  }
  return connected
}

async function releaseCacheSession(cdpSession) {
  if (!cdpSession) return
  await cdpSession.send('Network.setCacheDisabled', { cacheDisabled: false }).catch(() => undefined)
  await cdpSession.detach().catch(() => undefined)
}

async function reloadMemberPage(active, targetURL, forceRefresh) {
  // A normal read is deliberately DOM-only. The managed tab may already have
  // a fully authenticated Members SPA, and re-running goto here resets its
  // React state before member or invitation rows finish rendering.
  if (!forceRefresh && isTeamMembersPage(active)) return undefined

  let cdpSession
  if (forceRefresh) {
    try {
      cdpSession = await active.context().newCDPSession(active)
      await cdpSession.send('Network.enable')
      await cdpSession.send('Network.setCacheDisabled', { cacheDisabled: true })
    } catch {
      await releaseCacheSession(cdpSession)
      cdpSession = undefined
      // Navigation still invalidates the SPA route when a CDP cache toggle is
      // unavailable. Do not fail a member operation just because Chromium has
      // temporarily lost its DevTools session.
    }
  }
  try {
    await active.goto(targetURL, { waitUntil: 'domcontentloaded', timeout: operationTimeout })
    return cdpSession
  } catch (error) {
    await releaseCacheSession(cdpSession)
    throw error
  }
}

function isTeamMembersPage(page) {
  try {
    const parsed = new URL(page.url())
    return parsed.hostname.toLowerCase() === 'chatgpt.com' && parsed.pathname.toLowerCase() === '/admin/members'
  } catch {
    return false
  }
}

function reusableTeamPage(context, { allowNonMembers = false } = {}) {
  if (managedBrowserPage && !managedBrowserPage.isClosed() && (allowNonMembers || isTeamMembersPage(managedBrowserPage))) return managedBrowserPage
  return context.pages().find((page) => isTeamMembersPage(page) && !page.isClosed())
}

async function membersPage({ forceRefresh = false, targetURL = membersURL, pending = false } = {}) {
  const connected = await browser()
  const context = connected.contexts()[0]
  if (!context) throw new Error('Chromium 尚未创建浏览器上下文')
  // Never reuse an arbitrary visible tab. Only the single dedicated Members
  // page owned by this service is eligible; OAuth/CAPTCHA/manual tabs remain
  // untouched until this workflow explicitly takes over that same page.
  let active = reusableTeamPage(context)
  if (!active && managedBrowserPage && !managedBrowserPage.isClosed()) {
    if (!forceRefresh) throw new Error('服务器浏览器当前正在 OpenAI 授权页，成员检查不会打断授权流程')
    // An explicit reset/refresh is allowed to return the dedicated workflow tab
    // from OAuth to Members. Ordinary background reads still never interrupt it.
    active = managedBrowserPage
  }
  if (!active) active = await context.newPage()
  managedBrowserPage = active
  let cdpSession
  try {
    cdpSession = await reloadMemberPage(active, targetURL, forceRefresh)
  } catch (error) {
    // A Chromium restart invalidates Playwright page objects without always
    // marking them closed. Recreate the dedicated tab once; never fall back to
    // another user-visible tab.
    if (!active.isClosed()) throw error
    active = await context.newPage()
    managedBrowserPage = active
    cdpSession = await reloadMemberPage(active, targetURL, forceRefresh)
  }
  try {
    if (pending) await waitForPendingInvitesPageReady(active, { allowRecoveryNavigation: forceRefresh })
    else await waitForMemberPageReady(active)
    return active
  } finally {
    // Keep cache disabled through the SPA's first render/API requests, then
    // restore the browser's normal caching for the operator's manual session.
    await releaseCacheSession(cdpSession)
  }
}

async function pendingInvitesPage({ forceRefresh = false } = {}) {
  // Start from the real Members route and let the page select its own Pending
  // invites tab. Some hosted builds keep `?tab=invites` in the URL while still
  // rendering the Members panel, which made a stale member row look like a
  // confirmed invitation.
  const current = await membersPage({ forceRefresh, targetURL: membersURL, pending: true })
  return current
}

// Keep the official OAuth handoff in the same persistent Chromium tab that
// the operator sees in the XIASS iframe. This deliberately navigates an
// existing page instead of calling context.newPage(), so clicking the XIASS
// button cannot leak the flow into a local browser or create a second tab.
async function navigatePersistentBrowser(value) {
  const targetURL = validateOpenAIAuthURL(value)
  const connected = await browser()
  const context = connected.contexts()[0]
  if (!context) throw new Error('Chromium 尚未创建浏览器上下文')

  let active = managedBrowserPage && !managedBrowserPage.isClosed()
    ? managedBrowserPage
    : context.pages().find((page) => !page.isClosed())
  if (!active) throw new Error('服务器浏览器没有可复用的现有标签页')
  managedBrowserPage = active

  await active.goto(targetURL, {
    waitUntil: 'domcontentloaded',
    timeout: operationTimeout
  })
  return { ok: true, url: active.url() }
}

async function workflowBrowserPage(workflow) {
  if (managedBrowserPage && !managedBrowserPage.isClosed()) return managedBrowserPage
  const connected = await browser()
  const context = connected.contexts()[0]
  if (!context) throw new Error('Chromium 尚未创建浏览器上下文')
  const pages = context.pages().filter((page) => !page.isClosed())
  const oauthPage = pages.find((page) => {
    try {
      const parsed = new URL(page.url())
      return parsed.hostname === 'auth.openai.com' || parsed.hostname === 'openai.com' || parsed.hostname === 'localhost'
    } catch {
      return false
    }
  })
  const active = oauthPage || pages.find((page) => isTeamMembersPage(page))
  if (!active) throw new Error('服务器浏览器没有可恢复的工作流标签页')
  managedBrowserPage = active
  return active
}

async function rowsFor(current) {
  const tableRows = current.locator('table tbody tr')
  if (await tableRows.count()) return tableRows
  return current.locator('[role="row"]')
}

function extractEmail(text) {
  return text.match(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/i)?.[0] || ''
}

function normalizeEmail(email) {
  return String(email || '')
    .normalize('NFKC')
    .replace(/[\u200B-\u200D\uFEFF]/g, '')
    .trim()
    .toLowerCase()
}

function normalizeWorkflowEmail(value) {
  const normalized = normalizeEmail(value)
  const embedded = extractEmail(normalized)
  return embedded || normalized
}

function isValidWorkflowEmail(value) {
  return Boolean(value) && value.length <= 320 && /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(value)
}

function displayRole(value) {
  const normalized = String(value || '').trim().toLowerCase()
  if (/^(owner|所有者)$/.test(normalized)) return 'owner'
  if (/^(admin|administrator|管理员)$/.test(normalized)) return 'admin'
  if (/^(member|成员)$/.test(normalized)) return 'member'
  return ''
}

function seatTypeFromLines(lines) {
  const candidate = lines.find((line) => /^(standard|flexible|enterprise|business|标准|灵活|企业)$/i.test(line))
  return candidate || ''
}

function roleFromLines(lines) {
  const exact = lines.find((line) => /^(owner|admin|member|成员|管理员|所有者)$/i.test(line))
  if (exact) return displayRole(exact)
  const candidate = lines.find((line) => line.length <= 32 && /owner|admin|member|成员|管理员|所有者/i.test(line))
  return candidate ? displayRole(candidate) || 'unknown' : 'unknown'
}

function normalizedRole(role) {
  const normalized = String(role || '').trim().toLowerCase()
  if (['admin', 'administrator', '管理员'].includes(normalized)) return 'admin'
  if (['member', '成员'].includes(normalized)) return 'member'
  throw new Error('成员角色无效')
}

function isProtectedTeamMember(member) {
  const role = displayRole(member?.role)
  return role === 'owner' || role === 'admin' || protectedMemberEmails.has(normalizeEmail(member?.email))
}

function parsePendingInvites(text) {
  const normalized = String(text || '').replace(/\s+/g, ' ').trim()
  const patterns = [
    /(?:pending invitations?|pending invites?)\s*(?:[(:·-]\s*)?(\d{1,5})\b/i,
    /\b(\d{1,5})\s+(?:pending invitations?|pending invites?)\b/i,
    /(?:待处理邀请|待接受邀请)\s*(?:[（(：:]\s*)?(\d{1,5})\s*(?:个|条)?/i,
    /(\d{1,5})\s*(?:个|条)\s*(?:待处理邀请|待接受邀请)/i
  ]
  for (const pattern of patterns) {
    const match = normalized.match(pattern)
    if (match) return Number(match[1])
  }
  return undefined
}

async function readMembers(current) {
  await waitForMemberPageReady(current)
  const body = await current.locator('body').innerText()
  if (/log in|登录|sign in/i.test(body) && !/成员|members/i.test(body)) {
    throw new Error('服务器浏览器尚未登录 ChatGPT 管理员页面')
  }

  const rows = await rowsFor(current)
  const count = await rows.count()
  const members = []
  for (let index = 0; index < count; index += 1) {
    const text = (await rows.nth(index).innerText()).trim()
    const email = extractEmail(text)
    if (!email) continue
    const lines = text.split(/[\n\t]+/).map((line) => line.trim()).filter(Boolean)
    const role = roleFromLines(lines)
    const member = {
      // Email is the stable identity. A row index is unsafe after sorting or
      // pagination changes and must never be used for a destructive action.
      id: normalizeEmail(email),
      email,
      name: lines.find((line) => line !== email && line !== role) || '',
      role,
      seat_type: seatTypeFromLines(lines),
      status: 'active'
    }
    member.protected = isProtectedTeamMember(member)
    members.push(member)
  }

  const pageTitle = (await current.locator('h1,h2').allTextContents()).join(' ').trim()
  const pendingInvites = parsePendingInvites(body)
  // The Team owner is the current logged-in account in the upstream UI. For
  // this workflow the useful "seat" is the first non-owner account that can
  // actually be replaced, not the owner row marked "You".
  const replaceable = members.find((member) => member.role === 'member' && !member.protected)
  const seatEmail = replaceable?.email || ''
  return {
    ready: true,
    url: current.url(),
    members,
    pending_invites: pendingInvites ?? 0,
    seat_email: seatEmail,
    workspace_name: pageTitle
  }
}

async function listMembers({ forceRefresh = false, requireEmails = false } = {}) {
  let result
  const attempts = forceRefresh && requireEmails ? memberRefreshAttempts : 1
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    const current = await membersPage({ forceRefresh })
    result = await readMembers(current)
    if (!requireEmails || result.members.some((member) => Boolean(normalizeEmail(member.email)))) return result
    if (attempt + 1 < attempts) await sleep(750)
  }
  if (requireEmails) throw new Error('未能刷新到成员邮箱列表，请在服务器浏览器确认成员页面后点击继续')
  return result
}

function pendingInvitesURL() {
  const target = new URL(membersURL)
  target.searchParams.set('tab', 'invites')
  return target.toString()
}

async function pendingInvitesRouteSelected(current) {
  try {
    const parsed = new URL(current.url())
    if (parsed.pathname.toLowerCase().includes('/invites')) return true
    // ChatGPT's current Members page keeps the selected tab in the query
    // string (`/admin/members?tab=invites`). The page body and exact target
    // email are still checked by the caller, so honoring this selected route
    // does not turn a stale Members shell into a pending invitation.
    if ((parsed.searchParams.get('tab') || '').toLowerCase() === 'invites') return true
  } catch {
    // The selected tab or active panel below is enough for SPA builds that use
    // a non-URL route.
  }
  const selectedControls = current.locator('[role="tab"][aria-selected="true"], [aria-current="page"], [aria-pressed="true"]')
  const selectedText = (await selectedControls.allTextContents().catch(() => [])).join(' ')
  if (/pending invitations?|pending invites?|待处理邀请|待接受邀请/i.test(selectedText)) return true

  // Do not scan the whole page here: the Members shell normally contains a
  // navigation label for Pending invites even when that panel is not selected.
  const headings = current.locator('h1:visible, h2:visible, h3:visible, [role="heading"]:visible, [role="tabpanel"]:visible')
  const headingText = (await headings.allTextContents().catch(() => [])).join(' ')
  return /pending invitations?|pending invites?|待处理邀请|待接受邀请/i.test(headingText)
}

async function pendingInvitesControl(current) {
  const pattern = /pending invitations?|pending invites?|待处理邀请|待接受邀请/i
  for (const role of ['tab', 'button', 'link']) {
    const controls = current.getByRole(role, { name: pattern })
    const count = await controls.count().catch(() => 0)
    for (let index = 0; index < count; index += 1) {
      const control = controls.nth(index)
      if (await control.isVisible().catch(() => false)) return control
    }
  }
  return null
}

async function membersControl(current) {
  const pattern = /^members$|^成员$/i
  for (const role of ['tab', 'button', 'link']) {
    const controls = current.getByRole(role, { name: pattern })
    const count = await controls.count().catch(() => 0)
    for (let index = 0; index < count; index += 1) {
      const control = controls.nth(index)
      if (await control.isVisible().catch(() => false)) return control
    }
  }
  return null
}

async function pendingInviteSnapshot({ forceRefresh = false, expectedEmail = '', waitForExpectedEmail = false } = {}) {
  const current = await pendingInvitesPage({ forceRefresh })
  const wanted = normalizeEmail(expectedEmail)
  if (wanted && waitForExpectedEmail) {
    await waitForVisiblePendingInviteEmail(current, wanted)
  }

  // Prefer the visible invitation table. When it exists with no body rows, the
  // result is authoritatively empty; never fall back to broad page selectors
  // that can capture an administrator email or a stale Members row.
  const visibleTables = current.locator('table:visible')
  const hasVisibleTable = await visibleTables.count() > 0
  const visiblePanels = current.locator('[role="tabpanel"]:visible')
  const recordScope = await visiblePanels.count() > 0
    ? visiblePanels.last()
    : current.locator('main:visible').first()
  const pendingRows = hasVisibleTable
    ? visibleTables.locator('tbody tr, [role="row"]')
    : recordScope.locator('[role="row"], [role="listitem"], article, [data-testid*="invite" i], [data-testid*="pending" i]')
  try {
    await pendingRows.first().waitFor({ state: 'visible', timeout: 2500 })
  } catch {
    // An empty pending-invites page is a valid result.
  }

  const rows = pendingRows
  const count = await rows.count()
  const recordTexts = []
  for (let index = 0; index < count; index += 1) {
    recordTexts.push(await rows.nth(index).innerText())
  }
  const emails = pendingInviteEmailsFromTexts(recordTexts)
  if (wanted && await visiblePendingInviteEmail(current, wanted)) {
    // A few hosted builds render the invitation as an unstructured text card.
    // Only the exact requested email is accepted in that fallback; arbitrary
    // emails from the page shell are never treated as pending invitations.
    emails.add(wanted)
  }
  const body = await recordScope.innerText().catch(() => '')
  const explicitlyEmpty = /\bno results\b|\bno pending (?:invites?|invitations?)\b|暂无.*邀请|没有.*邀请|还没有.*邀请/i.test(body)
  return {
    emails: explicitlyEmpty && emails.size === 0 ? new Set() : emails,
    pendingInvites: explicitlyEmpty && emails.size === 0 ? 0 : (parsePendingInvites(body) ?? emails.size)
  }
}

function pendingInviteEmailsFromTexts(texts) {
  const emails = new Set()
  for (const text of texts) {
    const email = normalizeEmail(extractEmail(String(text || '')))
    if (email) emails.add(email)
  }
  return emails
}

async function waitForVisiblePendingInviteEmail(current, wanted) {
  const deadline = Date.now() + pendingInviteRenderTimeout
  while (Date.now() < deadline) {
    if (await visiblePendingInviteEmail(current, wanted)) return true
    // Keep the same SPA page alive while its pending-invite data arrives. A
    // navigation/reload on every poll can reset the tab before React commits
    // the invitation row, which previously caused successful invites to look
    // like failures.
    await sleep(300)
  }
  return false
}

async function visiblePendingInviteEmail(current, wanted) {
  if (!await pendingInvitesRouteSelected(current)) return false
  const escapedWanted = wanted.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const exactMatches = current.getByText(new RegExp(`^${escapedWanted}$`, 'i'))
  const count = await exactMatches.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const candidate = exactMatches.nth(index)
    if (!(await candidate.isVisible().catch(() => false))) continue
    const isRecord = await candidate.evaluate((element) => {
      let node = element
      for (let depth = 0; node && depth < 7; depth += 1) {
        const tag = node.tagName.toLowerCase()
        const role = node.getAttribute('role') || ''
        const testID = (node.getAttribute('data-testid') || '').toLowerCase()
        if (tag === 'tr' || tag === 'article' || role === 'row' || role === 'listitem' || /pending|invite/.test(testID)) return true
        node = node.parentElement
      }
      return false
    }).catch(() => false)
    if (isRecord) return true
  }

  // Last-resort support for a page that has no semantic row/card wrapper. The
  // exact text is still required, and the route has already been verified as
  // the selected Pending invites panel.
  return count > 0
}

async function waitForPendingInviteEmail(email) {
  const wanted = normalizeEmail(email)
  const latest = await pendingInviteSnapshot({
    forceRefresh: false,
    expectedEmail: wanted,
    waitForExpectedEmail: true
  })
  if (latest.emails.has(wanted)) return latest

  // Some hosted builds immediately move the accepted invite into Members.
  // Treat that as success too, but never infer success from a count alone.
  const members = await listMembers({ forceRefresh: false })
  if (members.members.some((member) => normalizeEmail(member.email) === wanted)) return latest
  throw new Error('邀请操作已提交但待处理邀请中未出现该邮箱，未在规定时间内确认页面状态')
}

// The ChatGPT members page is a client-rendered application. DOMContentLoaded
// fires before the member rows exist, which previously produced an apparently
// valid but empty result on slower servers. Wait for the stable toolbar first,
// then allow the table a short window to populate. An actually empty workspace
// still returns normally once the toolbar is visible.
async function waitForMemberPageReady(current) {
  const deadline = Date.now() + Math.min(operationTimeout, memberRenderTimeout)
  const inviteButton = current.getByRole('button', { name: /invite member|邀请成员/i }).first()
  const rows = current.locator('table tbody tr, [role="row"]')
  let toolbarVisibleAt = 0
  let emptyStateVisibleAt = 0
  while (Date.now() < deadline) {
    if (await pendingInvitesRouteSelected(current)) {
      const control = await membersControl(current)
      if (control) {
        await control.click().catch(() => undefined)
        await sleep(500)
        continue
      }
      await sleep(400)
      continue
    }
    if (await rows.first().isVisible().catch(() => false)) return
    if (await inviteButton.isVisible().catch(() => false)) {
      // The Members toolbar commits before the table data. Returning as soon
      // as Invite member appears makes an owner-only workspace look empty on
      // slower sessions. Give the row query time to observe the same render;
      // only accept an empty result after a short stable toolbar window.
      toolbarVisibleAt ||= Date.now()
      const body = await current.locator('body').innerText().catch(() => '')
      const explicitEmpty = /no members|no users|暂无成员|没有成员|还没有成员/i.test(body)
      if (explicitEmpty) {
        emptyStateVisibleAt ||= Date.now()
        if (Date.now() - emptyStateVisibleAt >= 1500) return
      } else {
        emptyStateVisibleAt = 0
      }
      if (Date.now() - toolbarVisibleAt >= 5000) return
    }
    await sleep(250)
  }
  if (await pendingInvitesRouteSelected(current)) {
    throw new Error('成员页面仍停留在待处理邀请页，请刷新成员页面后重试')
  }
  // Preserve the previous behavior for a genuinely empty workspace: readMembers
  // will produce the useful login/page error, while a stale Pending invites tab
  // is rejected explicitly above instead of being parsed as members.
}

async function waitForPendingInvitesPageReady(current, { allowRecoveryNavigation = false } = {}) {
  const pendingPattern = /pending invitations?|pending invites?|待处理邀请|待接受邀请/i
  const emptyPattern = /no pending|no invitations|暂无.*邀请|没有.*邀请|还没有.*邀请/i
  const deadline = Date.now() + Math.min(
    operationTimeout,
    Math.max(memberRenderTimeout, pendingInviteRenderTimeout)
  )
  let attemptedTab = false
  let attemptedDirectRoute = false
  let selectedSince = 0

  while (Date.now() < deadline) {
    const body = await current.locator('body').innerText().catch(() => '')
    const routeSelected = await pendingInvitesRouteSelected(current)
    const selectedControls = current.locator('[role="tab"][aria-selected="true"], [aria-current="page"], [aria-pressed="true"]')
    const selectedText = (await selectedControls.allTextContents().catch(() => [])).join(' ')
    const selectedControl = pendingPattern.test(selectedText)
    const visiblePanels = current.locator('[role="tabpanel"]:visible, main:visible, section:visible')
    const panelText = (await visiblePanels.allTextContents().catch(() => [])).join(' ')
    const pendingContent = pendingPattern.test(panelText) || emptyPattern.test(panelText)
    const pendingSelected = selectedControl || (routeSelected && pendingContent)
    if (pendingSelected && pendingContent) {
      selectedSince ||= Date.now()
      if (Date.now() - selectedSince < 750) {
        await sleep(250)
        continue
      }
      // Give the SPA one short render tick after the route/tab selection. This
      // prevents a previous Members table from being mistaken for Pending
      // invites when the page reuses its shell.
      await sleep(500)
      return
    }
    selectedSince = 0

    if (!attemptedTab) {
      attemptedTab = true
      const control = await pendingInvitesControl(current)
      if (control) {
        await control.click().catch(() => undefined)
        await sleep(300)
        continue
      }
    }

    // Query-string routing is only a fallback. The next loop still requires an
    // actual selected panel/heading before the page is accepted.
    if (allowRecoveryNavigation && !attemptedDirectRoute) {
      attemptedDirectRoute = true
      await current.goto(pendingInvitesURL(), { waitUntil: 'domcontentloaded', timeout: operationTimeout }).catch(() => undefined)
      await sleep(300)
      continue
    }
    await sleep(250)
  }

  throw new Error('待处理邀请页面未完成加载，请刷新 ChatGPT 成员页面后重试')
}

async function clickText(current, pattern, options = {}) {
  const locator = current.getByRole('button', { name: pattern }).first()
  if (await locator.count()) {
    await locator.click(options)
    return
  }
  const textLocator = current.getByText(pattern).first()
  if (!(await textLocator.count())) throw new Error(`页面中找不到操作：${pattern}`)
  await textLocator.click(options)
}

async function visibleInviteDialog(current) {
  const dialog = current.locator('[data-testid="modal-invite-users-to-workspace"]').last()
  if ((await dialog.count()) > 0 && await dialog.isVisible().catch(() => false)) return dialog

  const roleDialog = current.getByRole('dialog').last()
  if ((await roleDialog.count()) > 0 && await roleDialog.isVisible().catch(() => false)) return roleDialog
  return null
}

async function openInviteDialog(current) {
  const existing = await visibleInviteDialog(current)
  if (existing) return existing

  const inviteButton = await firstVisibleInviteButton(current)
  if (!inviteButton) {
    throw new Error('成员页面中找不到邀请成员按钮')
  }
  await inviteButton.click()

  await waitUntil('邀请成员弹窗未出现', async () => Boolean(await visibleInviteDialog(current)))
  const dialog = await visibleInviteDialog(current)
  if (!dialog) throw new Error('邀请成员弹窗未出现')
  return dialog
}

async function firstVisibleInviteButton(current) {
  // Prefer the actual action label. A broad `/invite/` selector can pick the
  // Pending invites tab before it reaches the real Invite members button.
  for (const pattern of [/^invite members?$/i, /^invite$/i, /^邀请成员$/i, /^邀请$/i]) {
    const buttons = current.getByRole('button', { name: pattern })
    const count = await buttons.count().catch(() => 0)
    for (let index = 0; index < count; index += 1) {
      const button = buttons.nth(index)
      if (await button.isVisible().catch(() => false)) return button
    }
  }

  const buttons = current.locator('button')
  const count = await buttons.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const button = buttons.nth(index)
    if (!(await button.isVisible().catch(() => false))) continue
    const metadata = [
      await button.innerText().catch(() => ''),
      await button.getAttribute('aria-label'),
      await button.getAttribute('title'),
      await button.getAttribute('data-testid')
    ].filter(Boolean).join(' ').replace(/\s+/g, ' ').trim()
    if (!/invite|邀请/i.test(metadata) || /pending|待处理|待接受/i.test(metadata)) continue
    return button
  }
  return null
}

async function firstVisibleDialogButton(scope, pattern) {
  const exactPatterns = pattern.test('continue') || pattern.test('继续')
    ? [/^continue$/i, /^继续$/i]
    : [pattern]
  for (const exactPattern of exactPatterns) {
    const buttons = scope.getByRole('button', { name: exactPattern })
    const count = await buttons.count()
    for (let index = 0; index < count; index += 1) {
      const button = buttons.nth(index)
      if (await button.isVisible().catch(() => false)) return button
    }
  }

  const buttons = scope.getByRole('button', { name: pattern })
  const count = await buttons.count()
  for (let index = 0; index < count; index += 1) {
    const button = buttons.nth(index)
    if (!(await button.isVisible().catch(() => false))) continue
    const label = (await button.innerText().catch(() => '')).replace(/\s+/g, ' ').trim()
    if (/continue\s+(with|to)|继续使用|继续前往/i.test(label)) continue
    return button
  }
  return null
}

async function submitInviteDialog(scope, email) {
  const inputs = scope.locator('input')
  let input
  const count = await inputs.count()
  for (let index = 0; index < count; index += 1) {
    const candidate = inputs.nth(index)
    if (!(await candidate.isVisible().catch(() => false))) continue
    const type = (await candidate.getAttribute('type') || 'text').toLowerCase()
    const metadata = [
      type,
      await candidate.getAttribute('autocomplete'),
      await candidate.getAttribute('name'),
      await candidate.getAttribute('id'),
      await candidate.getAttribute('aria-label'),
      await candidate.getAttribute('placeholder')
    ].filter(Boolean).join(' ').toLowerCase()
    if (type === 'email' || /email|邮箱/.test(metadata)) {
      input = candidate
      break
    }
  }
  if (!input) throw new Error('邀请成员弹窗中找不到邮箱输入框')
  await input.fill(email)

  // This is the only supported invitation path. Fill the native Email field,
  // then click Send invites. Continue remains a compatibility fallback for an
  // older hosted build, but must never take priority over the real submit label.
  let submitButton
  await waitUntil('邀请成员弹窗中找不到可用的提交按钮', async () => {
    for (const pattern of [/^send invites?$/i, /^发送邀请$/i, /^continue$/i, /^继续$/i]) {
      const button = await firstVisibleDialogButton(scope, pattern)
      if (!button || await button.isDisabled().catch(() => true)) continue
      submitButton = button
      return true
    }
    return false
  })
  if (!submitButton) throw new Error('邀请成员弹窗中找不到可用的提交按钮')
  await submitButton.click()
}

async function memberRecord(current, email, required = true) {
  const wanted = normalizeEmail(email)
  const rows = await rowsFor(current)
  const count = await rows.count()
  for (let index = 0; index < count; index += 1) {
    const row = rows.nth(index)
    const actual = normalizeEmail(extractEmail(await row.innerText()))
    if (actual !== wanted) continue
    const lines = (await row.innerText()).split(/[\n\t]+/).map((line) => line.trim()).filter(Boolean)
    const member = {
      id: actual,
      email: extractEmail(await row.innerText()),
      role: roleFromLines(lines)
    }
    member.protected = isProtectedTeamMember(member)
    return { row, member }
  }
  if (required) throw new Error('成员列表中找不到该邮箱，请先刷新后重试')
  return null
}

async function memberRow(current, email, required = true) {
  const record = await memberRecord(current, email, required)
  return record?.row || null
}

function assertRemovableMember(member) {
  if (isProtectedTeamMember(member)) throw new Error('受保护的管理员账号不可移除或替换')
  if (displayRole(member.role) !== 'member') throw new Error('只能替换普通成员席位')
}

async function waitUntil(description, predicate) {
  const deadline = Date.now() + confirmationTimeout
  let lastError
  while (Date.now() < deadline) {
    try {
      if (await predicate()) return
    } catch (error) {
      lastError = error
    }
    await new Promise((resolve) => setTimeout(resolve, 300))
  }
  if (lastError instanceof Error) throw new Error(`${description}：${lastError.message}`)
  throw new Error(`${description}，未在规定时间内确认页面状态`)
}

async function clickMemberMenu(current, email) {
  const row = await memberRow(current, email)
  await row.scrollIntoViewIfNeeded()
  const buttons = row.locator('button')
  const buttonCount = await buttons.count()
  if (!buttonCount) throw new Error('成员行中找不到操作菜单')
  const trigger = buttons.nth(buttonCount - 1)

  // The current ChatGPT table renders the three-dot trigger as an icon-only
  // Radix menu. A default center click can land on the nested SVG <use>
  // element and leave the menu closed even though the button is interactive.
  // Click a stable button edge first, then use the keyboard activation path as
  // a fallback for builds that ignore pointer activation from the icon area.
  const openMenu = async () => {
    if (await trigger.getAttribute('aria-expanded').catch(() => '') === 'true') return true
    if (await current.locator('[role="menu"]:visible').count().catch(() => 0)) return true
    return false
  }

  await trigger.click({ position: { x: 4, y: 18 } }).catch(() => undefined)
  if (await openMenu()) return

  await trigger.press('Enter').catch(() => undefined)
  await waitUntil('成员操作菜单未展开', openMenu)
}

async function inviteMember(email) {
  const normalized = normalizeWorkflowEmail(email)
  if (!isValidWorkflowEmail(normalized)) {
    throw new Error('邀请邮箱格式无效')
  }
  let lastError
  for (let attempt = 1; attempt <= inviteAttempts; attempt += 1) {
    try {
      const existing = await listMembers({ forceRefresh: false, requireEmails: true })
      if (existing.members.some((member) => normalizeEmail(member.email) === normalized)) {
        return {
          ...existing,
          pending_invites: existing.pending_invites,
          operation: { type: 'invite', email: normalized, confirmed: true }
        }
      }

      // Only retries may perform a best-effort duplicate check. The first
      // attempt must always execute the native flow in this exact order:
      // Invite member -> Email -> Send invites -> Pending invites.
      if (attempt > 1) {
        const pendingBefore = await pendingInviteSnapshot({
          forceRefresh: false,
          expectedEmail: normalized,
          waitForExpectedEmail: true
        }).catch(() => null)
        if (pendingBefore?.emails.has(normalized)) {
          return {
            ...existing,
            pending_invites: pendingBefore.pendingInvites,
            operation: { type: 'invite', email: normalized, confirmed: true }
          }
        }
      }

      const current = await membersPage({ forceRefresh: false })
      const scope = await openInviteDialog(current)
      await submitInviteDialog(scope, normalized)

      const confirmedPending = await waitForPendingInviteEmail(normalized)
      const latest = await listMembers({ forceRefresh: false, requireEmails: true })
      return {
        ...latest,
        pending_invites: confirmedPending?.pendingInvites || 1,
        operation: { type: 'invite', email: normalized, confirmed: true }
      }
    } catch (error) {
      lastError = error
      if (attempt >= inviteAttempts) break
      // The requested recovery action is a fresh embedded-browser page load;
      // never change the dialog state by pressing Enter or selecting a second
      // submit action. The next attempt rechecks the live invitation first.
      await membersPage({ forceRefresh: true }).catch(() => undefined)
      await sleep(500)
    }
  }
  const detail = lastError instanceof Error ? `：${lastError.message}` : ''
  throw new Error(`邀请失败，已刷新重试 ${inviteAttempts} 次${detail}`)
}

async function removeMember(email) {
  const normalized = normalizeEmail(email)
  const current = await membersPage({ forceRefresh: false })
  const record = await memberRecord(current, normalized)
  assertRemovableMember(record.member)
  await clickMemberMenu(current, normalized)
  await clickText(current, /remove member|移除成员/i)
  await clickText(current, /remove from workspace|从工作空间移除|remove/i)

  await waitUntil('移除操作已提交但成员仍存在', async () => !(await memberRow(current, normalized, false)))
  const result = await readMembers(current)
  return { ...result, operation: { type: 'remove', email: normalized, confirmed: true } }
}

async function updateMember(email, role) {
  const normalized = normalizeEmail(email)
  const normalizedTargetRole = normalizedRole(role)
  if (!['admin', 'member'].includes(normalizedTargetRole)) throw new Error('成员角色无效')
  const current = await membersPage({ forceRefresh: false })
  const record = await memberRecord(current, normalized)
  assertRemovableMember(record.member)
  await clickMemberMenu(current, normalized)
  await clickText(current, /edit|编辑/i)
  const dialog = current.getByRole('dialog').last()
  const scope = (await dialog.count()) ? dialog : current
  const select = scope.locator('select').first()
  if (await select.count()) {
    try {
      await select.selectOption({ value: normalizedTargetRole })
    } catch {
      const labels = normalizedTargetRole === 'admin' ? ['Admin', '管理员'] : ['Member', '成员']
      let selected = false
      let lastError
      for (const label of labels) {
        try {
          await select.selectOption({ label })
          selected = true
          break
        } catch (error) {
          lastError = error
        }
      }
      if (!selected) throw lastError || new Error('页面中找不到目标角色')
    }
  } else {
    await clickText(scope, normalizedTargetRole === 'admin' ? /admin|管理员/i : /member|成员/i)
  }
  await clickText(scope, /save|保存|update|更新/i)

  await waitUntil('角色更新已提交但页面未显示目标角色', async () => {
    const row = await memberRow(current, normalized, false)
    if (!row) return false
    const lines = (await row.innerText()).split(/[\n\t]+/).map((line) => line.trim()).filter(Boolean)
    try {
      return normalizedRole(roleFromLines(lines)) === normalizedTargetRole
    } catch {
      return false
    }
  })
  const result = await readMembers(current)
  return { ...result, operation: { type: 'update', email: normalized, role: normalizedTargetRole, confirmed: true } }
}

function validateWorkflowEmail(value, label) {
  const email = normalizeWorkflowEmail(value)
  if (!isValidWorkflowEmail(email)) {
    throw new Error(`${label}格式无效`)
  }
  return email
}

function validateOpenAIAuthURL(value) {
  const raw = String(value || '').trim()
  if (!raw || raw.length > 8192) throw new Error('授权链接无效')
  let parsed
  try {
    parsed = new URL(raw)
  } catch {
    throw new Error('授权链接无效')
  }
  const query = parsed.searchParams
  const isOfficialPKCE = parsed.protocol === 'https:'
    && !parsed.username
    && !parsed.password
    && parsed.hostname.toLowerCase() === 'auth.openai.com'
    && parsed.pathname === '/oauth/authorize'
    && query.get('response_type') === 'code'
    && query.get('client_id') === officialOpenAIClientID
    && query.get('redirect_uri') === officialOpenAIRedirectURI
    && query.get('scope') === officialOpenAIScope
    && Boolean(query.get('state'))
    && Boolean(query.get('code_challenge'))
    && query.get('code_challenge_method') === 'S256'
    && query.get('codex_cli_simplified_flow') === 'true'
    && query.get('id_token_add_organizations') === 'true'
  if (!isOfficialPKCE) throw new Error('授权链接必须使用 XIASS 内置 OpenAI PKCE 登录流程')
  return parsed.toString()
}

function validateOAuthSessionID(value) {
  const sessionID = String(value || '').trim()
  if (!/^[A-Za-z0-9_-]{16,128}$/.test(sessionID)) throw new Error('XIASS OAuth 会话无效')
  return sessionID
}

function generateWorkflowPassword() {
  const groups = [
    'ABCDEFGHJKLMNPQRSTUVWXYZ',
    'abcdefghijkmnopqrstuvwxyz',
    '23456789',
    '!@#$%&*?'
  ]
  const pick = (source) => source[crypto.randomInt(0, source.length)]
  const password = groups.map(pick)
  const alphabet = groups.join('')
  while (password.length < 13) password.push(pick(alphabet))
  for (let index = password.length - 1; index > 0; index -= 1) {
    const swapIndex = crypto.randomInt(0, index + 1)
    ;[password[index], password[swapIndex]] = [password[swapIndex], password[index]]
  }
  return password.join('')
}

async function waitForOAuthPage(description, predicate, timeout = oauthPageTimeout) {
  const deadline = Date.now() + timeout
  let lastError
  while (Date.now() < deadline) {
    try {
      const result = await predicate()
      if (result) return result
    } catch (error) {
      lastError = error
    }
    await sleep(250)
  }
  if (lastError instanceof Error) throw new Error(`${description}：${lastError.message}`)
  throw new Error(`${description}，未在规定时间内识别页面状态`)
}

async function firstVisible(locator) {
  const count = await locator.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const candidate = locator.nth(index)
    if (await candidate.isVisible().catch(() => false)) return candidate
  }
  return null
}

async function firstVisibleRole(current, role, patterns) {
  for (const pattern of patterns) {
    const match = await firstVisible(current.getByRole(role, { name: pattern }))
    if (match) return match
  }
  return null
}

async function firstVisibleInput(current, matcher) {
  const inputs = current.locator('input')
  const count = await inputs.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const input = inputs.nth(index)
    if (!(await input.isVisible().catch(() => false))) continue
    const metadata = [
      await input.getAttribute('type'),
      await input.getAttribute('name'),
      await input.getAttribute('id'),
      await input.getAttribute('autocomplete'),
      await input.getAttribute('aria-label'),
      await input.getAttribute('placeholder')
    ].filter(Boolean).join(' ').toLowerCase()
    if (matcher(metadata, input)) return input
  }
  return null
}

async function clickOAuthContinue(current) {
  const button = await firstVisibleRole(current, 'button', [/^continue$/i, /^next$/i, /^继续$/i, /^下一步$/i])
  if (!button) throw new Error('OpenAI 页面中找不到继续按钮')
  await button.click()
}

async function oauthBody(current) {
  return (await current.locator('body').innerText().catch(() => '')).replace(/\s+/g, ' ').trim()
}

async function refreshOpenAIErrorPage(current) {
  const body = await oauthBody(current)
  if (!/(?:error\s*400|400\s*(?:error|bad request)|请求错误|出了点问题)/i.test(body)) return false
  await current.reload({ waitUntil: 'domcontentloaded', timeout: operationTimeout })
  return true
}

async function selectSignUp(current) {
  await refreshOpenAIErrorPage(current)
  const currentPath = (() => {
    try {
      return new URL(current.url()).pathname.toLowerCase()
    } catch {
      return ''
    }
  })()
  const body = await oauthBody(current)
  const emailInput = await firstVisibleInput(current, (metadata) => /email/.test(metadata))
  const alreadyOnSignUpPage = Boolean(emailInput) && (
    /create[-_]?account|sign[-_]?up|register/.test(currentPath)
      || (/(?:create an account|create your account|sign up|注册账号)/i.test(body)
        && !/(?:welcome back|log in|登录)/i.test(body))
  )
  if (alreadyOnSignUpPage) return

  const signUp = await waitForOAuthPage('OpenAI 页面中找不到 Sign up', async () => (
    await firstVisibleRole(current, 'button', [/^sign up$/i, /^create account$/i, /^注册$/i])
      || await firstVisibleRole(current, 'link', [/^sign up$/i, /^create account$/i, /^注册$/i])
  ))
  await signUp.click()
}

async function selectLoginForAnotherAccount(current) {
  await refreshOpenAIErrorPage(current)
  let actionClicks = 0
  let lastActionKey = ''
  return waitForOAuthPage('OpenAI 登录页中找不到邮箱输入框', async () => {
    const emailInput = await firstVisibleInput(current, (metadata) => /email/.test(metadata))
    if (emailInput) return emailInput
    if (actionClicks >= 3) return null
    const otherAccount = await firstVisibleRole(current, 'button', [/log in (?:with|to) another account|use another account|登录.*(?:其他|另一个)账号/i])
      || await firstVisibleRole(current, 'link', [/log in (?:with|to) another account|use another account|登录.*(?:其他|另一个)账号/i])
    const login = otherAccount
      || await firstVisibleRole(current, 'button', [/^log in$/i, /^sign in$/i, /^登录$/i])
      || await firstVisibleRole(current, 'link', [/^log in$/i, /^sign in$/i, /^登录$/i])
    if (!login) return null
    const actionKey = `${current.url()}|${await login.innerText().catch(() => '')}`
    if (actionKey === lastActionKey) return null
    lastActionKey = actionKey
    actionClicks += 1
    await login.click()
    return null
  })
}

async function fillWorkflowEmail(current, email) {
  const input = await waitForOAuthPage('OpenAI 注册页中找不到邮箱输入框', () => (
    firstVisibleInput(current, (metadata) => /email/.test(metadata))
  ))
  await input.fill(email)
  await clickOAuthContinue(current)
}

async function fillWorkflowPassword(current, password) {
  const input = await waitForOAuthPage('OpenAI 注册页中找不到密码输入框', () => (
    firstVisibleInput(current, (metadata) => /password/.test(metadata))
  ))
  await input.fill(password)
  await clickOAuthContinue(current)
  await waitForOAuthPage('OpenAI 未进入邮箱验证页面', async () => {
    const body = await oauthBody(current)
    return /check your inbox|verify your email|verification code|检查.*邮箱|验证.*邮箱|验证码/i.test(body)
      || (await verificationInputs(current)).length > 0
  })
}

async function fillLoginPassword(current, password) {
  const input = await waitForOAuthPage('OpenAI 登录页中找不到密码输入框', () => (
    firstVisibleInput(current, (metadata) => /password/.test(metadata))
  ))
  await input.fill(password)
  await clickOAuthContinue(current)
}

async function verificationInputs(current) {
  const candidates = current.locator('input[autocomplete="one-time-code"], input[inputmode="numeric"], input[name*="code" i], input[id*="code" i]')
  const visible = []
  const count = await candidates.count().catch(() => 0)
  for (let index = 0; index < count; index += 1) {
    const candidate = candidates.nth(index)
    if (await candidate.isVisible().catch(() => false)) visible.push(candidate)
  }
  return visible
}

async function fillVerificationCode(current, rawCode) {
  const code = String(rawCode || '').replace(/\s+/g, '')
  if (!/^\d{4,10}$/.test(code)) throw new Error('验证码格式无效')
  const inputs = await waitForOAuthPage('OpenAI 页面中找不到验证码输入框', () => verificationInputs(current))
  if (inputs.length === 1) {
    await inputs[0].fill(code)
  } else {
    if (inputs.length < code.length) throw new Error('OpenAI 验证码输入框数量与验证码不一致')
    for (let index = 0; index < code.length; index += 1) await inputs[index].fill(code[index])
  }
  // Some OpenAI verification pages submit as soon as the final digit is
  // entered. In that case the Continue button disappears before Playwright
  // can resolve it; accept the navigation once the code fields are gone.
  const continueButton = await firstVisibleRole(current, 'button', [/^continue$/i, /^next$/i, /^继续$/i, /^下一步$/i])
  if (continueButton) {
    await continueButton.click()
    return
  }
  await waitForOAuthPage('OpenAI 验证码已填写但页面未继续', async () => (
    (await verificationInputs(current)).length === 0
  ))
}

function isPhoneInputMetadata(metadata) {
  return /tel|phone|mobile|手机号/.test(metadata)
    && !/code|otp|one-time|验证码/.test(metadata)
}

function isEmailInputMetadata(metadata) {
  return /email|邮箱/.test(metadata)
    && !/code|otp|one-time|验证码/.test(metadata)
}

async function waitForPhonePage(current) {
  await waitForOAuthPage('OpenAI 未进入手机号验证页面', async () => {
    const body = await oauthBody(current)
    return /phone number|required.*phone|手机号|电话号码/i.test(body)
      && Boolean(await firstVisibleInput(current, isPhoneInputMetadata))
  })
}

async function openAIPhoneRecoveryState(current) {
  const phoneInput = await firstVisibleInput(current, isPhoneInputMetadata)
  if (phoneInput) return 'phone'

  const passwordInput = await firstVisibleInput(current, (metadata) => /password/.test(metadata))
  if (passwordInput) return 'password'

  const emailInput = await firstVisibleInput(current, isEmailInputMetadata)
  if (emailInput) return 'email'

  const body = await oauthBody(current)
  const codeInputs = await verificationInputs(current)
  if (codeInputs.length > 0 && /phone|text message|sms|mobile|短信|手机/i.test(body)) return 'sms_code'
  if (codeInputs.length > 0 && /email|inbox|邮箱|验证邮件/i.test(body)) return 'email_code'
  if (/localhost:1455\/auth\/callback/.test(current.url())) return 'callback'
  if (/workspace|工作空间|空间/i.test(body)) return 'workspace'
  return 'unknown'
}

async function recoverOpenAIPhoneEntry(current, workflow) {
  const password = String(workflow.generatedPassword || workflow.loginPassword || '')
  const email = normalizeWorkflowEmail(workflow.inviteEmail)
  let backAttempts = 0
  let restartedOAuth = false

  for (let attempt = 0; attempt < 8; attempt += 1) {
    const state = await openAIPhoneRecoveryState(current)
    if (state === 'phone') return 'phone'
    if (state === 'email_code') return 'email_code'
    if (state === 'callback' || state === 'workspace') {
      throw new Error('OpenAI 重新授权已跳过手机号页面，请在内嵌浏览器核对后继续')
    }
    if (state === 'password') {
      if (!password) throw new Error('重新进入手机号步骤需要登录密码，但本次工作流未保存密码')
      await fillLoginPassword(current, password)
      await sleep(500)
      continue
    }
    if (state === 'email') {
      if (!email) throw new Error('重新进入手机号步骤时找不到本次注册邮箱')
      await fillWorkflowEmail(current, email)
      await sleep(500)
      continue
    }

    if (backAttempts < 2) {
      backAttempts += 1
      await current.goBack({ waitUntil: 'domcontentloaded', timeout: operationTimeout }).catch(() => null)
      await sleep(500)
      continue
    }
    if (!restartedOAuth) {
      restartedOAuth = true
      backAttempts = 0
      await current.goto(workflow.authURL, { waitUntil: 'domcontentloaded', timeout: operationTimeout })
      await sleep(500)
      continue
    }
    throw new Error('OpenAI 无法返回手机号输入页，请在内嵌浏览器退回手机号步骤后继续')
  }
  throw new Error('OpenAI 重新进入手机号步骤超时')
}

async function submitPhoneOnOpenAI(current, rawPhone) {
  const phone = String(rawPhone || '').replace(/[\s()-]/g, '')
  if (!/^\+[1-9]\d{6,14}$/.test(phone)) throw new Error('手机号必须是完整国际格式')
  const input = await waitForOAuthPage('OpenAI 手机号页面中找不到输入框', () => (
    firstVisibleInput(current, isPhoneInputMetadata)
  ))
  await input.fill(phone)

  const textMessage = await firstVisibleRole(current, 'radio', [/text message|短信/i])
    || await firstVisibleRole(current, 'button', [/text message|短信/i])
    || await firstVisibleRole(current, 'option', [/text message|短信/i])
  if (textMessage) await textMessage.click().catch(() => undefined)

  const submit = await firstVisibleRole(current, 'button', [/send (?:a )?code|send sms|text me|continue|next|发送.*验证码|发送短信|继续|下一步/i])
  if (!submit) throw new Error('OpenAI 手机号页面中找不到发送短信按钮')
  await submit.click()

  const verificationState = await waitForOAuthPage('OpenAI 未进入短信验证码页面', async () => {
    const body = await oauthBody(current)
    if (/phone.*(?:invalid|unavailable|used too many)|too many.*phone|无法使用.*号码|手机号.*(?:不可用|次数过多)/i.test(body)) {
      return 'phone_rejected'
    }
    return (await verificationInputs(current)).length > 0
      && /phone|text message|sms|mobile|短信|手机/i.test(body)
  })
  if (verificationState === 'phone_rejected') {
    throw new Error('当前手机号不可用或使用次数过多，请确认换号后继续')
  }
}

async function fillProfile(current) {
  await sleep(5000)
  await waitForOAuthPage('OpenAI 未进入姓名和年龄页面', async () => {
    const body = await oauthBody(current)
    return /name|yourself|about you|姓名|年龄|介绍.*自己/i.test(body)
      && (await current.locator('input:visible').count().catch(() => 0)) > 0
  })

  const nameInput = await firstVisibleInput(current, (metadata) => /name|姓名/.test(metadata))
    || await firstVisible(current.locator('input:visible'))
  if (!nameInput) throw new Error('OpenAI 资料页中找不到姓名输入框')
  await nameInput.fill('black')

  const ageInput = await firstVisibleInput(current, (metadata) => /age|birth|年龄|出生/.test(metadata))
  if (ageInput) {
    await ageInput.fill('26')
  } else {
    const visibleInputs = current.locator('input:visible')
    if (await visibleInputs.count() > 1) {
      await visibleInputs.nth(1).fill('26')
    } else {
      throw new Error('OpenAI 资料页中找不到年龄输入框')
    }
  }
  await clickOAuthContinue(current)
}

async function chooseDefaultWorkspace(current) {
  await sleep(10000)
  await waitForOAuthPage('OpenAI 未进入默认工作空间页面', async () => {
    const body = await oauthBody(current)
    return /workspace|工作空间|空间/i.test(body)
      || /localhost:1455\/auth\/callback/.test(current.url())
  })
  if (/localhost:1455\/auth\/callback/.test(current.url())) return

  const defaultChoice = await firstVisibleRole(current, 'radio', [/default|workspace|默认|工作空间/i])
    || await firstVisibleRole(current, 'button', [/default workspace|默认工作空间/i])
  if (defaultChoice) await defaultChoice.click().catch(() => undefined)
  await clickOAuthContinue(current)
}

function callbackURLFromNavigationEntries(entries, expectedState) {
  for (const entry of [...(Array.isArray(entries) ? entries : [])].reverse()) {
    try {
      const parsed = new URL(String(entry?.url || ''))
      if (
        ['http:', 'https:'].includes(parsed.protocol)
        && parsed.hostname === 'localhost'
        && parsed.pathname === '/auth/callback'
        && Boolean(parsed.searchParams.get('code'))
        && parsed.searchParams.get('state') === expectedState
      ) return parsed.toString()
    } catch {
      // Ignore unrelated or browser-internal history entries.
    }
  }
  return ''
}

async function workflowCallbackURLFromPage(current, workflow) {
  const expectedState = new URL(workflow.authURL).searchParams.get('state') || ''
  const direct = callbackURLFromNavigationEntries([{ url: current.url() }], expectedState)
  if (direct) return direct

  let cdpSession
  try {
    // Chromium replaces page.url() with chrome-error://chromewebdata when the
    // localhost receiver is absent, while the address bar keeps code/state.
    cdpSession = await current.context().newCDPSession(current)
    const history = await cdpSession.send('Page.getNavigationHistory')
    return callbackURLFromNavigationEntries(history?.entries, expectedState)
  } catch {
    return ''
  } finally {
    await cdpSession?.detach().catch(() => undefined)
  }
}

async function captureWorkflowCallback(current, workflow) {
  const raw = await waitForOAuthPage('浏览器未出现 OAuth 回调 URL', () => (
    workflowCallbackURLFromPage(current, workflow)
  ))
  return validateWorkflowCallbackURL(raw, workflow)
}

async function runOAuthRegistrationUntilMailbox(workflow) {
  setWorkflowNode(workflow, 'oauth', 'running', '正在当前服务器浏览器标签页打开官方 PKCE URL')
  await navigatePersistentBrowser(workflow.authURL)
  const current = managedBrowserPage
  if (!current || current.isClosed()) throw new Error('服务器浏览器授权标签页不可用')
  completeWorkflowNode(workflow, 'oauth', 'XIASS 官方 OAuth 页面已打开')

  setWorkflowNode(workflow, 'signup', 'running', '正在选择 Sign up')
  await selectSignUp(current)
  completeWorkflowNode(workflow, 'signup', '已进入 OpenAI 注册路径')

  setWorkflowNode(workflow, 'email', 'running', '正在填入本次临时邮箱')
  await fillWorkflowEmail(current, workflow.inviteEmail)
  completeWorkflowNode(workflow, 'email', '临时邮箱已提交')

  setWorkflowNode(workflow, 'password', 'running', '正在生成并填入 13 位随机密码')
  workflow.generatedPassword = generateWorkflowPassword()
  persistWorkflowState()
  await fillWorkflowPassword(current, workflow.generatedPassword)
  completeWorkflowNode(workflow, 'password', '13 位随机密码已生成并填入')
  completeWorkflowNode(workflow, 'mail', 'OpenAI 验证邮件已发送')
  setWorkflowNode(workflow, 'mailbox', 'waiting', '正在轮询本次临时邮箱')
  workflow.status = 'manual_required'
  activeWorkflowID = workflow.id
  persistWorkflowState()
}

function completeReauthorizationOnlyNodes(workflow) {
  for (const key of ['phone', 'sms_confirm', 'phone_submit', 'sms_poll', 'sms_code', 'profile_wait', 'profile']) {
    completeWorkflowNode(workflow, key, '已有账号重新授权无需执行此步骤')
  }
}

async function finishOAuthReauthorization(workflow) {
  const current = await workflowBrowserPage(workflow)
  completeReauthorizationOnlyNodes(workflow)
  setWorkflowNode(workflow, 'workspace_wait', 'running', '正在等待默认工作空间页面')
  await chooseDefaultWorkspace(current)
  completeWorkflowNode(workflow, 'workspace_wait', '默认工作空间页面已出现')
  completeWorkflowNode(workflow, 'workspace', '已选择默认工作空间并继续')
  setWorkflowNode(workflow, 'callback', 'running', '正在读取浏览器地址栏中的 OAuth 回调')
  workflow.callbackURL = await captureWorkflowCallback(current, workflow)
  completeWorkflowNode(workflow, 'callback', 'OAuth 回调 code/state 已捕获并校验')
  setWorkflowNode(workflow, 'import', 'waiting', '等待将新 OAuth 凭据覆盖导入原 Team 账号')
  workflow.currentNodeKey = 'import'
  workflow.status = 'callback_ready'
  persistWorkflowState()
}

async function reauthorizationPostPasswordState(current, workflow) {
  return waitForOAuthPage('OpenAI 登录后未进入验证码或工作空间页面', async () => {
    const body = await oauthBody(current)
    const inputs = await verificationInputs(current)
    if (inputs.length > 0 && /verification|verify|code|check your inbox|验证码|验证|邮箱/i.test(body)) return 'email_code'
    if (await workflowCallbackURLFromPage(current, workflow)) return 'callback'
    if (/workspace|organization|continue to codex|authorize codex|工作空间|组织|授权/i.test(body)) return 'workspace'
    return null
  })
}

async function runOAuthReauthorization(workflow) {
  setWorkflowNode(workflow, 'oauth', 'running', '正在当前服务器浏览器标签页打开官方 PKCE URL')
  await navigatePersistentBrowser(workflow.authURL)
  const current = managedBrowserPage
  if (!current || current.isClosed()) throw new Error('服务器浏览器授权标签页不可用')
  completeWorkflowNode(workflow, 'oauth', 'XIASS 官方 OAuth 页面已打开')

  setWorkflowNode(workflow, 'signup', 'running', '正在切换到已有账号登录')
  const emailInput = await selectLoginForAnotherAccount(current)
  completeWorkflowNode(workflow, 'signup', '已进入 OpenAI 已有账号登录路径')

  setWorkflowNode(workflow, 'email', 'running', '正在填入历史 Team 邮箱')
  await emailInput.fill(workflow.inviteEmail)
  await clickOAuthContinue(current)
  completeWorkflowNode(workflow, 'email', '历史 Team 邮箱已提交')

  setWorkflowNode(workflow, 'password', 'running', '正在填入服务器保存的登录密码')
  await fillLoginPassword(current, workflow.loginPassword)
  completeWorkflowNode(workflow, 'password', '登录密码已自动填入并提交')

  const nextState = await reauthorizationPostPasswordState(current, workflow)
  if (nextState === 'email_code') {
    completeWorkflowNode(workflow, 'mail', 'OpenAI 已发送重新登录验证码')
    setWorkflowNode(workflow, 'mailbox', 'waiting', '正在轮询该历史 Team 邮箱')
    workflow.status = 'manual_required'
    activeWorkflowID = workflow.id
    persistWorkflowState()
    return
  }
  completeWorkflowNode(workflow, 'mail', '本次重新授权不需要邮箱验证码')
  completeWorkflowNode(workflow, 'mailbox', '已跳过邮箱轮询')
  completeWorkflowNode(workflow, 'email_code', '已跳过邮箱验证码填入')
  await finishOAuthReauthorization(workflow)
}

async function continueWorkflowWithEmailCode(workflow, code) {
  const current = await workflowBrowserPage(workflow)
  completeWorkflowNode(workflow, 'mailbox', 'Cloudflare 已读取 OpenAI 验证邮件')
  setWorkflowNode(workflow, 'email_code', 'running', '正在将邮箱验证码填入 OpenAI')
  await fillVerificationCode(current, code)
  completeWorkflowNode(workflow, 'email_code', '邮箱验证码已自动填入并提交')

  if (workflow.mode === 'reauthorization') {
    await finishOAuthReauthorization(workflow)
    return
  }
  await waitForPhonePage(current)
  completeWorkflowNode(workflow, 'phone', '已进入 OpenAI 手机号验证页面')
  setWorkflowNode(workflow, 'sms_confirm', 'waiting', '等待 XIASS Team 自动化领取手机号')
  workflow.status = 'manual_required'
  activeWorkflowID = workflow.id
  persistWorkflowState()
}

function resetPhoneReplacementNodes(workflow) {
  for (const key of ['phone_submit', 'sms_poll', 'sms_code']) {
    const node = workflowNodeState(workflow, key)
    if (!node) continue
    node.status = 'pending'
    node.message = ''
  }
  workflow.failedNodeKey = ''
  workflow.error = ''
  workflow.currentNodeKey = 'phone_submit'
  persistWorkflowState()
}

async function continueWorkflowWithPhone(workflow, phone, replacing = false) {
  const current = await workflowBrowserPage(workflow)
  if (replacing) {
    setWorkflowNode(workflow, 'phone_submit', 'running', '旧号码已释放，正在返回 OpenAI 手机号步骤')
    const recoveryState = await recoverOpenAIPhoneEntry(current, workflow)
    completeWorkflowNode(workflow, 'sms_confirm', 'XIASS Team 自动化已更换手机号')
    if (recoveryState === 'email_code') {
      completeWorkflowNode(workflow, 'mail', '重新登录已发送新的邮箱验证码')
      setWorkflowNode(workflow, 'mailbox', 'waiting', '正在轮询新的 OpenAI 邮箱验证码')
      workflow.status = 'manual_required'
      activeWorkflowID = workflow.id
      persistWorkflowState()
      return
    }
    setWorkflowNode(workflow, 'phone_submit', 'running', '已返回手机号页，正在填入新号码并选择 Text message')
  } else {
    completeWorkflowNode(workflow, 'sms_confirm', 'XIASS Team 自动化已领取手机号')
    setWorkflowNode(workflow, 'phone_submit', 'running', '正在填入完整号码并选择 Text message')
  }
  await submitPhoneOnOpenAI(current, phone)
  workflow.lastSubmittedPhone = phone
  completeWorkflowNode(workflow, 'phone_submit', replacing
    ? '新号码已提交并选择 Text message'
    : '号码已提交并选择 Text message')
  setWorkflowNode(workflow, 'sms_poll', 'waiting', '正在通过 XIASS SMS 服务轮询验证码')
  workflow.status = 'manual_required'
  activeWorkflowID = workflow.id
  persistWorkflowState()
}

async function continueWorkflowWithSMSCode(workflow, code) {
  const current = await workflowBrowserPage(workflow)
  completeWorkflowNode(workflow, 'sms_poll', 'XIASS SMS 服务已读取验证码')
  setWorkflowNode(workflow, 'sms_code', 'running', '正在填入短信验证码并继续')
  await fillVerificationCode(current, code)
  completeWorkflowNode(workflow, 'sms_code', '短信验证码已自动填入并提交')

  setWorkflowNode(workflow, 'profile_wait', 'running', '等待 5 秒进入资料页面')
  await fillProfile(current)
  completeWorkflowNode(workflow, 'profile_wait', '资料页面已出现')
  completeWorkflowNode(workflow, 'profile', '已填写姓名 black 和年龄 26 并继续')

  setWorkflowNode(workflow, 'workspace_wait', 'running', '等待 10 秒进入默认工作空间')
  await chooseDefaultWorkspace(current)
  completeWorkflowNode(workflow, 'workspace_wait', '默认工作空间页面已出现')
  completeWorkflowNode(workflow, 'workspace', '已选择默认工作空间并继续')

  setWorkflowNode(workflow, 'callback', 'running', '正在读取浏览器地址栏中的 OAuth 回调')
  workflow.callbackURL = await captureWorkflowCallback(current, workflow)
  completeWorkflowNode(workflow, 'callback', 'OAuth 回调 code/state 已捕获并校验')
  workflow.status = 'callback_ready'
  workflow.currentNodeKey = 'import'
  setWorkflowNode(workflow, 'import', 'waiting', '等待按已勾选配置导入 XIASS')
}

function workflowNode(key, number, label) {
  return { key, number, label, status: 'pending', message: '' }
}

const workflowNodeDefinitions = [
  ['members', '读取成员席位'],
  ['remove', '移除已选成员'],
  ['invite', '提交成员邀请'],
  ['invite_confirm', '确认 Pending invites'],
  ['oauth', '打开 XIASS 官方 OAuth'],
  ['signup', '选择 Sign up'],
  ['email', '填入临时邮箱'],
  ['password', '创建 13 位随机密码'],
  ['mail', '提交并发送邮箱验证码'],
  ['mailbox', 'Cloudflare 读取验证邮件'],
  ['email_code', '自动填入邮箱验证码'],
  ['phone', '进入手机号页面'],
  ['sms_confirm', '自动领取手机号'],
  ['phone_submit', '填入号码并选择 Text message'],
  ['sms_poll', '轮询短信验证码'],
  ['sms_code', '自动填入短信验证码'],
  ['profile_wait', '等待资料页 5 秒'],
  ['profile', '填写 black / 26'],
  ['workspace_wait', '等待工作空间 10 秒'],
  ['workspace', '默认工作空间继续'],
  ['callback', '捕获 OAuth 回调'],
  ['import', '按勾选配置导入 XIASS']
]

function createWorkflow(seatEmail, inviteEmail, authURL, oauthSessionID, seatAlreadyRemoved) {
  const id = crypto.randomBytes(24).toString('base64url')
  const now = Date.now()
  return {
    id,
    seatEmail,
    seatAlreadyRemoved,
    inviteEmail,
    authURL,
    oauthSessionID,
    createdAt: now,
    expiresAt: now + workflowTTL,
    status: 'running',
    error: '',
    failedNodeKey: '',
    inviteConfirmed: false,
    inviteConfirmedAt: 0,
    callbackURL: '',
    generatedPassword: '',
    lastSubmittedPhone: '',
    pauseRequested: false,
    pausedFromStatus: '',
    pausedNodeKey: '',
    currentNodeKey: '',
    nodes: workflowNodeDefinitions.map(([key, label], index) => workflowNode(key, index + 1, label))
  }
}

function createReauthorizationWorkflow(accountID, email, password, authURL, oauthSessionID) {
  const workflow = createWorkflow('', email, authURL, oauthSessionID, true)
  workflow.mode = 'reauthorization'
  workflow.targetAccountID = accountID
  workflow.loginPassword = password
  workflow.inviteConfirmed = true
  completeWorkflowNode(workflow, 'members', '已有 Team 账号重新授权，无需读取成员席位')
  completeWorkflowNode(workflow, 'remove', '已有 Team 账号重新授权，不移除成员')
  completeWorkflowNode(workflow, 'invite', '已有 Team 账号重新授权，不重复发送邀请')
  completeWorkflowNode(workflow, 'invite_confirm', '历史 Team 邮箱已绑定原账号')
  return workflow
}

function workflowSummary(workflow) {
  const oauthState = new URL(workflow.authURL).searchParams.get('state') || ''
  const summary = {
    schema_version: workflowProtocolVersion,
    id: workflow.id,
    status: workflow.status,
    expires_at: new Date(workflow.expiresAt).toISOString(),
    manual_required: workflow.status === 'manual_required',
    pause_requested: Boolean(workflow.pauseRequested),
    seat_already_removed: Boolean(workflow.seatAlreadyRemoved),
    oauth_session_id: workflow.oauthSessionID,
    oauth_state: oauthState,
    current_node: workflow.currentNodeKey || '',
    password_available: Boolean(workflow.generatedPassword),
    mode: workflow.mode === 'reauthorization' ? 'reauthorization' : 'registration',
    nodes: workflow.nodes.map(({ key, number, label, status, message }) => ({ key, number, label, status, ...(message ? { message } : {}) }))
  }
  if (workflow.mode === 'reauthorization' && Number.isSafeInteger(workflow.targetAccountID)) {
    summary.target_account_id = workflow.targetAccountID
  }
  if (workflow.error) summary.error = workflow.error
  // The callback is deliberately held only in process memory. It is returned
  // to the authenticated XIASS admin caller after the operator pastes it,
  // so the existing state-validated import endpoint can consume it.
  if (['callback_ready', 'completed'].includes(workflow.status) && workflow.callbackURL) summary.callback_url = workflow.callbackURL
  return summary
}

function setWorkflowNode(workflow, key, status, message = '') {
  if (workflow.cancelRequested && status !== 'cancelled') throw new Error('工作流已由管理员停止')
  if (workflow.pauseRequested && ['running', 'waiting'].includes(status)) {
    workflow.status = 'paused'
    workflow.pausedNodeKey = key
    workflow.currentNodeKey = key
    persistWorkflowState()
    throw new Error('工作流已暂停')
  }
  const node = workflow.nodes.find((item) => item.key === key)
  if (!node) return
  node.status = status
  node.message = message
  if (['running', 'waiting', 'failed'].includes(status)) workflow.currentNodeKey = key
  persistWorkflowState()
}

function completeWorkflowNode(workflow, key, message) {
  setWorkflowNode(workflow, key, 'completed', message)
}

function workflowNodeState(workflow, key) {
  return workflow.nodes.find((node) => node.key === key)
}

function workflowFailureNodeKey(workflow, fallbackKey) {
  return workflow.nodes.find((node) => node.status === 'running')?.key
    || workflow.currentNodeKey
    || fallbackKey
}

function completeInviteStep(workflow) {
  workflow.inviteConfirmed = true
  workflow.inviteConfirmedAt = Date.now()
}

function completeInviteNodes(workflow, submissionMessage, confirmationMessage) {
  completeWorkflowNode(workflow, 'invite', submissionMessage)
  completeWorkflowNode(workflow, 'invite_confirm', confirmationMessage)
}

async function confirmInviteNode(workflow) {
  setWorkflowNode(workflow, 'invite_confirm', 'running', '正在核对 Members 和 Pending invites')
  const latestMembers = await listMembers({ forceRefresh: false })
  if (latestMembers.members.some((member) => normalizeEmail(member.email) === workflow.inviteEmail)) {
    completeInviteStep(workflow)
    completeWorkflowNode(workflow, 'invite_confirm', '临时邮箱已在实时成员列表中确认')
    return
  }
  const pending = await pendingInviteSnapshot({ forceRefresh: false, expectedEmail: workflow.inviteEmail, waitForExpectedEmail: true })
  if (!pending.emails.has(normalizeEmail(workflow.inviteEmail))) {
    throw new Error('Pending invites 中未找到目标临时邮箱，请在内嵌浏览器完成邀请后继续')
  }
  completeInviteStep(workflow)
  completeWorkflowNode(workflow, 'invite_confirm', '已在 Pending invites 精确匹配临时邮箱')
}

async function resumeFineWorkflowFromNextNode(workflow, requestedNextKey = '') {
  let nextKey = requestedNextKey
  if (nextKey) {
    const requestedNode = workflowNodeState(workflow, nextKey)
    if (!requestedNode) throw new Error('暂停节点已失效，请重新开始')
    requestedNode.status = 'pending'
    requestedNode.message = ''
    workflow.failedNodeKey = ''
    workflow.error = ''
    workflow.status = 'running'
  } else {
    const failedKey = workflow.failedNodeKey
    const failedIndex = workflow.nodes.findIndex((node) => node.key === failedKey && node.status === 'failed')
    if (failedIndex < 0) throw new Error('当前工作流没有可继续的失败节点')
    const failedNode = workflow.nodes[failedIndex]
    failedNode.status = 'completed'
    failedNode.message = '该节点已由人工处理，自动化从下一节点继续'
    workflow.failedNodeKey = ''
    workflow.error = ''
    workflow.status = 'running'
    nextKey = workflow.nodes[failedIndex + 1]?.key || 'import'
  }

  const current = await workflowBrowserPage(workflow)

  try {
    // Fine-node recovery must preserve the complete 1-3 prefix. If reading,
    // removing, or inviting failed and the operator fixed that page manually,
    // continue with the next real member action instead of falling through to
    // OAuth callback capture.
    if (nextKey === 'remove') {
      if (workflow.seatAlreadyRemoved) {
        completeWorkflowNode(workflow, 'remove', '成员席位已由人工腾出，继续邀请')
      } else {
        setWorkflowNode(workflow, 'remove', 'running', '正在提交成员移除')
        await removeMember(workflow.seatEmail)
        completeWorkflowNode(workflow, 'remove', '成员已从工作区移除')
      }
      nextKey = 'invite'
    }
    if (nextKey === 'invite') {
      setWorkflowNode(workflow, 'invite', 'running', '正在原生邀请弹窗提交临时邮箱')
      await inviteMember(workflow.inviteEmail)
      completeWorkflowNode(workflow, 'invite', '已在原生页面提交邀请')
      await confirmInviteNode(workflow)
      nextKey = 'oauth'
    }
    if (nextKey === 'invite_confirm') {
      await confirmInviteNode(workflow)
      nextKey = 'oauth'
    }
    if (nextKey === 'oauth') {
      await runOAuthRegistrationUntilMailbox(workflow)
      return
    }
    if (nextKey === 'signup') {
      setWorkflowNode(workflow, 'signup', 'running', '正在选择 Sign up')
      await selectSignUp(current)
      completeWorkflowNode(workflow, 'signup', '已进入 OpenAI 注册路径')
    }
    if (['signup', 'email'].includes(nextKey)) {
      setWorkflowNode(workflow, 'email', 'running', '正在填入本次临时邮箱')
      await fillWorkflowEmail(current, workflow.inviteEmail)
      completeWorkflowNode(workflow, 'email', '临时邮箱已提交')
    }
    if (['signup', 'email', 'password'].includes(nextKey)) {
      setWorkflowNode(workflow, 'password', 'running', '正在生成并填入 13 位随机密码')
      workflow.generatedPassword = generateWorkflowPassword()
      persistWorkflowState()
      await fillWorkflowPassword(current, workflow.generatedPassword)
      completeWorkflowNode(workflow, 'password', '13 位随机密码已生成并填入')
      completeWorkflowNode(workflow, 'mail', 'OpenAI 验证邮件已发送')
      setWorkflowNode(workflow, 'mailbox', 'waiting', '正在轮询本次临时邮箱')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (['mail', 'mailbox'].includes(nextKey)) {
      await waitForOAuthPage('OpenAI 未停留在邮箱验证页面', async () => {
        const body = await oauthBody(current)
        return /check your inbox|verify your email|verification code|检查.*邮箱|验证.*邮箱|验证码/i.test(body)
          || (await verificationInputs(current)).length > 0
      })
      completeWorkflowNode(workflow, 'mail', '已确认 OpenAI 验证邮件页面')
      setWorkflowNode(workflow, 'mailbox', 'waiting', '正在轮询本次临时邮箱')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (nextKey === 'email_code') {
      setWorkflowNode(workflow, 'email_code', 'waiting', '等待 XIASS 将当前邮箱验证码填入 OpenAI')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (nextKey === 'phone') {
      await waitForPhonePage(current)
      completeWorkflowNode(workflow, 'email_code', '已确认邮箱验证码由人工提交')
      completeWorkflowNode(workflow, 'phone', '已进入 OpenAI 手机号验证页面')
      setWorkflowNode(workflow, 'sms_confirm', 'waiting', '等待 XIASS Team 自动化领取手机号')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (nextKey === 'sms_confirm') {
      setWorkflowNode(workflow, 'sms_confirm', 'waiting', '等待 XIASS Team 自动化领取手机号')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (nextKey === 'phone_submit') {
      setWorkflowNode(workflow, 'phone_submit', 'waiting', '等待 XIASS 将已确认的完整国际号码填入 OpenAI')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (nextKey === 'sms_poll') {
      await waitForOAuthPage('OpenAI 未停留在短信验证码页面', async () => (await verificationInputs(current)).length > 0)
      completeWorkflowNode(workflow, 'phone_submit', '已确认号码由人工提交')
      setWorkflowNode(workflow, 'sms_poll', 'waiting', '正在通过 XIASS SMS 服务轮询验证码')
      workflow.status = 'manual_required'
      persistWorkflowState()
      return
    }
    if (['sms_code', 'profile_wait', 'profile'].includes(nextKey)) {
      if (nextKey === 'sms_code') {
        setWorkflowNode(workflow, 'sms_code', 'waiting', '等待 XIASS 将当前短信验证码填入 OpenAI')
        workflow.status = 'manual_required'
        persistWorkflowState()
        return
      }
      setWorkflowNode(workflow, 'profile_wait', 'running', '等待 5 秒进入资料页面')
      await fillProfile(current)
      completeWorkflowNode(workflow, 'profile_wait', '资料页面已出现')
      completeWorkflowNode(workflow, 'profile', '已填写姓名 black 和年龄 26 并继续')
    }
    if (['profile_wait', 'profile', 'workspace_wait', 'workspace'].includes(nextKey)) {
      setWorkflowNode(workflow, 'workspace_wait', 'running', '等待 10 秒进入默认工作空间')
      await chooseDefaultWorkspace(current)
      completeWorkflowNode(workflow, 'workspace_wait', '默认工作空间页面已出现')
      completeWorkflowNode(workflow, 'workspace', '已选择默认工作空间并继续')
    }
    setWorkflowNode(workflow, 'callback', 'running', '正在读取浏览器地址栏中的 OAuth 回调')
    workflow.callbackURL = await captureWorkflowCallback(current, workflow)
    completeWorkflowNode(workflow, 'callback', 'OAuth 回调 code/state 已捕获并校验')
    setWorkflowNode(workflow, 'import', 'waiting', '等待按已勾选配置导入 XIASS')
    workflow.currentNodeKey = 'import'
    workflow.status = 'callback_ready'
  } catch (error) {
    const active = workflow.nodes.find((node) => node.status === 'running')
    markWorkflowNodeFailed(workflow, active?.key || nextKey, error)
  }
}

function redactWorkflowError(error) {
  const raw = error instanceof Error ? error.message : String(error || 'unknown error')
  return raw
    .replace(/https?:\/\/[^\s]+/gi, '外部页面')
    .replace(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi, '邮箱')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 240) || '自动化步骤未完成'
}

function pruneWorkflows() {
  const now = Date.now()
  let changed = false
  for (const [id, workflow] of workflows.entries()) {
    if (workflow.expiresAt > now) continue
    if (activeWorkflowID === id) activeWorkflowID = ''
    workflows.delete(id)
    changed = true
  }
  if (changed) persistWorkflowState()
}

function activeWorkflow() {
  pruneWorkflows()
  if (!activeWorkflowID) return undefined
  const workflow = workflows.get(activeWorkflowID)
  if (!workflow || !['running', 'manual_required', 'callback_ready', 'failed', 'paused'].includes(workflow.status)) {
    activeWorkflowID = ''
    persistWorkflowState()
    return undefined
  }
  return workflow
}

async function activeWorkflowStatus() {
  const workflow = activeWorkflow()
  if (!workflow) return { schema_version: workflowProtocolVersion, active: false }
  return { schema_version: workflowProtocolVersion, active: true, workflow: await workflowStatus(workflow.id) }
}

function validateWorkflowCallbackURL(value, workflow) {
  const raw = String(value || '').trim()
  if (!raw || raw.length > 8192) throw new Error('回调 URL 无效')
  let parsed
  try {
    parsed = new URL(raw)
  } catch {
    throw new Error('回调 URL 无效')
  }
  const code = parsed.searchParams.get('code')?.trim() || ''
  const state = parsed.searchParams.get('state')?.trim() || ''
  const expectedState = new URL(workflow.authURL).searchParams.get('state')?.trim() || ''
  if (!code || !state || !expectedState || state !== expectedState) {
    throw new Error('回调 state 与当前 XIASS OAuth 会话不匹配')
  }
  if (!['http:', 'https:'].includes(parsed.protocol)) throw new Error('回调 URL 协议无效')
  return parsed.toString()
}

async function submitWorkflowCallback(id, value) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status === 'cancelled') throw new Error('当前工作流已停止，请重新开始')
  if (!workflow.inviteConfirmed) throw new Error('临时邮箱邀请尚未完成，暂不能提交 OAuth 回调')

  workflow.callbackURL = validateWorkflowCallbackURL(value, workflow)
  workflow.error = ''
  workflow.status = 'callback_ready'
  workflow.currentNodeKey = 'import'
  setWorkflowNode(workflow, 'callback', 'completed', 'OAuth 回调 code/state 已捕获并校验')
  setWorkflowNode(workflow, 'import', 'waiting', '等待按已勾选配置导入 XIASS')
  return workflowSummary(workflow)
}

function restartableOAuthWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (!['manual_required', 'failed'].includes(workflow.status)) {
    throw new Error('当前工作流尚未进入可重新授权的状态')
  }
  if (!workflow.inviteConfirmed) {
    throw new Error('临时邮箱邀请尚未完成，暂不能只重启 OAuth')
  }
  return workflow
}

function resetOAuthWorkflowSteps(workflow) {
  const startIndex = workflow.nodes.findIndex((node) => node.key === 'oauth')
  for (let index = Math.max(0, startIndex); index < workflow.nodes.length; index += 1) {
    workflow.nodes[index].status = 'pending'
    workflow.nodes[index].message = ''
  }
  workflow.callbackURL = ''
  workflow.generatedPassword = ''
  workflow.currentNodeKey = 'oauth'
  workflow.error = ''
  persistWorkflowState()
}

async function restartOAuthWorkflow(id, value, oauthSessionIDValue) {
  const workflow = restartableOAuthWorkflow(id)
  const authURL = validateOpenAIAuthURL(value)
  const oauthSessionID = validateOAuthSessionID(oauthSessionIDValue)
  resetOAuthWorkflowSteps(workflow)
  workflow.authURL = authURL
  workflow.oauthSessionID = oauthSessionID
  const action = workflow.mode === 'reauthorization'
    ? () => runOAuthReauthorization(workflow)
    : () => runOAuthRegistrationUntilMailbox(workflow)
  return scheduleWorkflowNodeAction(workflow, 'oauth', action)
}

async function executeWorkflow(workflow) {
  try {
    setWorkflowNode(workflow, 'members', 'running', '正在刷新并读取实时成员页面')
    // A workflow reads the live managed tab and waits for real member rows. A
    // reload is reserved for an explicit refresh or a failed operation retry;
    // normal SPA reads must not reset the page while its data is arriving.
    const initial = await listMembers({ forceRefresh: false, requireEmails: true })
    const selected = initial.members.find((member) => normalizeEmail(member.email) === workflow.seatEmail)

    if (workflow.seatAlreadyRemoved) {
      const replaceable = initial.members.find((member) => displayRole(member.role) === 'member' && !isProtectedTeamMember(member))
      if (replaceable) {
        throw new Error('实时成员列表中仍有可替换的普通成员，请选择该成员后按常规流程操作')
      }
      const unverified = initial.members.find((member) => !isProtectedTeamMember(member))
      if (unverified) {
        throw new Error('成员角色无法确认受保护状态，请在浏览器中核对后重新刷新成员列表')
      }
      completeWorkflowNode(workflow, 'members', '已确认当前成员席位状态')
      completeWorkflowNode(workflow, 'remove', '普通成员席位已由人工腾出，未执行移除')
    } else if (selected) {
      assertRemovableMember(selected)
      completeWorkflowNode(workflow, 'members', '已读取并确认可替换的普通成员席位')
      setWorkflowNode(workflow, 'remove', 'running', '正在通过成员行菜单移除已选普通成员')
      await removeMember(workflow.seatEmail)
      completeWorkflowNode(workflow, 'remove', '已从实时成员页面确认成员移除')
    } else {
      throw new Error('已选成员不在实时成员列表中，请刷新成员页确认后点击继续')
    }

    setWorkflowNode(workflow, 'invite', 'running', '正在原生 Invite member 弹窗提交临时邮箱')
    const currentMembers = await listMembers({ forceRefresh: false })
    const invitationAccepted = currentMembers.members.some((member) => normalizeEmail(member.email) === workflow.inviteEmail)
    if (invitationAccepted) {
      completeInviteStep(workflow)
      completeInviteNodes(workflow, '无需重复提交邀请', '临时邮箱已出现在成员列表中')
    } else {
      // The first attempt must submit the native invitation before reading
      // Pending invites. Preloading the pending tab here used to fail on an
      // empty workspace and prevented Send invites from ever being clicked.
      await inviteMember(workflow.inviteEmail)
      completeWorkflowNode(workflow, 'invite', '已在原生页面提交邀请')
      await confirmInviteNode(workflow)
    }

    await runOAuthRegistrationUntilMailbox(workflow)
  } catch (error) {
    const activeNode = workflow.nodes.find((node) => node.status === 'running')
    markWorkflowNodeFailed(workflow, activeNode?.key || workflow.currentNodeKey || 'members', error)
  }
}

function markWorkflowNodeFailed(workflow, nodeKey, error) {
  if (workflow.cancelRequested || workflow.status === 'cancelled') return '工作流已由管理员停止'
  if (workflow.pauseRequested || workflow.status === 'paused') {
    workflow.status = 'paused'
    workflow.pausedNodeKey = workflow.pausedNodeKey || nodeKey
    workflow.currentNodeKey = workflow.pausedNodeKey
    persistWorkflowState()
    return '工作流已暂停'
  }
  const message = redactWorkflowError(error)
  workflow.status = 'failed'
  workflow.error = message
  workflow.failedNodeKey = nodeKey
  workflow.currentNodeKey = nodeKey
  setWorkflowNode(workflow, nodeKey, 'failed', message)
  activeWorkflowID = workflow.id
  return message
}

function validateWorkflowCode(value) {
  const code = String(value || '').replace(/\s+/g, '')
  if (!/^\d{4,10}$/.test(code)) throw new Error('验证码格式无效')
  return code
}

function workflowForAutomationInput(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status === 'cancelled') throw new Error('当前工作流已停止，请重新开始')
  if (workflow.status === 'paused' || workflow.pauseRequested) throw new Error('当前工作流已暂停，请先继续自动化')
  if (workflow.status === 'running') throw new Error('当前自动化节点仍在执行')
  return workflow
}

function scheduleWorkflowNodeAction(workflow, nodeKey, action) {
  workflow.status = 'running'
  workflow.error = ''
  workflow.failedNodeKey = ''
  setWorkflowNode(workflow, nodeKey, 'running', workflowNodeState(workflow, nodeKey)?.message || '正在执行')
  activeWorkflowID = workflow.id
  void runExclusive(async () => {
    try {
      await action()
    } catch (error) {
      markWorkflowNodeFailed(workflow, workflowFailureNodeKey(workflow, nodeKey), error)
    }
  })
  return workflowSummary(workflow)
}

function submitWorkflowEmailCode(id, value) {
  const workflow = workflowForAutomationInput(id)
  if (!['mailbox', 'email_code'].includes(workflow.currentNodeKey)) throw new Error('当前页面尚未等待邮箱验证码')
  const code = validateWorkflowCode(value)
  return scheduleWorkflowNodeAction(workflow, 'email_code', () => continueWorkflowWithEmailCode(workflow, code))
}

function submitWorkflowPhone(id, value) {
  const workflow = workflowForAutomationInput(id)
  const previousNode = workflow.currentNodeKey
  const previousStatus = workflow.status
  if (!['sms_confirm', 'phone_submit', 'sms_poll', 'sms_code'].includes(previousNode)) {
    throw new Error('当前页面尚未等待手机号')
  }
  const phone = String(value || '').replace(/[\s()-]/g, '')
  if (!/^\+[1-9]\d{6,14}$/.test(phone)) throw new Error('手机号必须是完整国际格式')
  const replacing = ['sms_poll', 'sms_code'].includes(previousNode)
    || (previousStatus === 'failed' && previousNode === 'phone_submit')
    || (Boolean(workflow.lastSubmittedPhone) && workflow.lastSubmittedPhone !== phone)
  if (replacing) resetPhoneReplacementNodes(workflow)
  return scheduleWorkflowNodeAction(workflow, 'phone_submit', () => continueWorkflowWithPhone(workflow, phone, replacing))
}

function submitWorkflowSMSCode(id, value) {
  const workflow = workflowForAutomationInput(id)
  if (!['sms_poll', 'sms_code'].includes(workflow.currentNodeKey)) throw new Error('当前页面尚未等待短信验证码')
  const code = validateWorkflowCode(value)
  return scheduleWorkflowNodeAction(workflow, 'sms_code', () => continueWorkflowWithSMSCode(workflow, code))
}

function completeWorkflowImport(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status !== 'callback_ready' || !workflow.callbackURL) throw new Error('OAuth 回调尚未就绪')
  completeWorkflowNode(workflow, 'import', workflow.mode === 'reauthorization'
    ? '新 OAuth 凭据已覆盖导入原 Team 账号'
    : '已按勾选的分组、优先级和并发导入 XIASS')
  workflow.status = 'completed'
  workflow.currentNodeKey = 'import'
  workflow.generatedPassword = ''
  workflow.loginPassword = ''
  if (activeWorkflowID === workflow.id) activeWorkflowID = ''
  persistWorkflowState()
  return workflowSummary(workflow)
}

async function startWorkflow(payload) {
  const seatAlreadyRemoved = payload?.seat_already_removed === true
  const rawSeatEmail = normalizeWorkflowEmail(payload?.seat_email)
  if (seatAlreadyRemoved && rawSeatEmail) throw new Error('人工腾位工作流不能携带待移除成员')
  const seatEmail = seatAlreadyRemoved ? '' : validateWorkflowEmail(rawSeatEmail, '成员邮箱')
  const inviteEmail = validateWorkflowEmail(payload?.invite_email, '临时邮箱')
  if (seatEmail && seatEmail === inviteEmail) throw new Error('临时邮箱不能与待移除成员相同')
  if (payload?.confirmed !== true) throw new Error('需要确认移除成员和发送邀请后才能开始')
  const authURL = validateOpenAIAuthURL(payload?.auth_url)
  const oauthSessionID = validateOAuthSessionID(payload?.oauth_session_id)
  if (activeWorkflow()) throw new Error('已有 Team 子号工作流正在进行，请先完成或取消当前工作流')

  const workflow = createWorkflow(seatEmail, inviteEmail, authURL, oauthSessionID, seatAlreadyRemoved)
  workflows.set(workflow.id, workflow)
  activeWorkflowID = workflow.id
  persistWorkflowState()
  // Return immediately so the UI can show the operation timeline while the
  // shared Chromium service serially performs the destructive actions.
  void runExclusive(() => executeWorkflow(workflow))
  return workflowSummary(workflow)
}

async function startReauthorizationWorkflow(payload) {
  const accountID = Number(payload?.account_id)
  if (!Number.isSafeInteger(accountID) || accountID <= 0) throw new Error('Team 子号账号 ID 无效')
  const email = validateWorkflowEmail(payload?.email, 'Team 子号邮箱')
  const password = String(payload?.password || '')
  if (password.length < 8 || password.length > 256) throw new Error('Team 子号登录密码无效')
  const authURL = validateOpenAIAuthURL(payload?.auth_url)
  const oauthSessionID = validateOAuthSessionID(payload?.oauth_session_id)
  if (activeWorkflow()) throw new Error('已有 Team 子号工作流正在进行，请先完成或取消当前工作流')

  const workflow = createReauthorizationWorkflow(accountID, email, password, authURL, oauthSessionID)
  workflows.set(workflow.id, workflow)
  activeWorkflowID = workflow.id
  persistWorkflowState()
  void runExclusive(() => runOAuthReauthorization(workflow).catch((error) => {
    const activeNode = workflow.nodes.find((node) => node.status === 'running')
    markWorkflowNodeFailed(workflow, activeNode?.key || workflow.currentNodeKey || 'oauth', error)
  }))
  return workflowSummary(workflow)
}

async function continueWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status === 'paused' || workflow.pauseRequested) return resumePausedWorkflow(workflow)
  if (workflow.status !== 'failed') throw new Error('当前工作流无需继续或仍在执行中')
  const current = activeWorkflow()
  if (current && current.id !== workflow.id) throw new Error('已有其他 Team 子号工作流正在进行')

  if (workflow.failedNodeKey) {
    workflow.status = 'running'
    workflow.error = ''
    activeWorkflowID = workflow.id
    void runExclusive(async () => {
      if (await recoverWorkflowCallback(workflow)) return
      if (workflow.mode === 'reauthorization') {
        resetOAuthWorkflowSteps(workflow)
        await runOAuthReauthorization(workflow)
        return
      }
      await resumeFineWorkflowFromNextNode(workflow)
    })
    return workflowSummary(workflow)
  }
  throw new Error('当前工作流没有可继续的新版自动化节点，请重新开始')
}

async function recoverWorkflowCallback(workflow) {
  const current = await workflowBrowserPage(workflow)
  const callbackURL = await workflowCallbackURLFromPage(current, workflow)
  if (!callbackURL) return false

  const failedNode = workflowNodeState(workflow, workflow.failedNodeKey)
  if (failedNode?.status === 'failed') {
    completeWorkflowNode(workflow, failedNode.key, '该节点已完成，已从浏览器地址栏恢复 OAuth 回调')
  }
  workflow.callbackURL = validateWorkflowCallbackURL(callbackURL, workflow)
  workflow.error = ''
  workflow.failedNodeKey = ''
  completeWorkflowNode(workflow, 'callback', 'OAuth 回调 code/state 已从浏览器地址栏捕获并校验')
  setWorkflowNode(workflow, 'import', 'waiting', '等待按已勾选配置导入 XIASS')
  workflow.currentNodeKey = 'import'
  workflow.status = 'callback_ready'
  persistWorkflowState()
  return true
}

function workflowStatus(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  return workflowSummary(workflow)
}

function workflowSecret(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (!workflow.generatedPassword) throw new Error('当前工作流尚未生成登录密码')
  return { email: workflow.inviteEmail, password: workflow.generatedPassword }
}

function cancelWorkflowState(workflow) {
  if (!workflow || !['running', 'manual_required', 'callback_ready', 'failed', 'paused'].includes(workflow.status)) return workflow
  const activeNode = workflow.nodes.find((node) => ['running', 'waiting', 'failed'].includes(node.status))
    || workflow.nodes.find((node) => node.status === 'pending')
  if (activeNode) {
    activeNode.status = 'cancelled'
    activeNode.message = '工作流已由管理员停止，可从第一步重新开始'
  }
  workflow.cancelRequested = true
  workflow.status = 'cancelled'
  workflow.error = ''
  workflow.failedNodeKey = ''
  workflow.generatedPassword = ''
  workflow.loginPassword = ''
  workflow.pauseRequested = false
  workflow.pausedFromStatus = ''
  workflow.pausedNodeKey = ''
  return workflow
}

function pauseWorkflowState(workflow) {
  if (!workflow || !['running', 'manual_required'].includes(workflow.status)) return workflow
  if (workflow.pauseRequested) return workflow
  workflow.pauseRequested = true
  workflow.pausedFromStatus = workflow.status
  workflow.pausedNodeKey = workflow.currentNodeKey || workflow.nodes.find((node) => ['running', 'waiting'].includes(node.status))?.key || ''
  if (workflow.status === 'manual_required') workflow.status = 'paused'
  persistWorkflowState()
  return workflow
}

function pauseWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  pauseWorkflowState(workflow)
  return workflowSummary(workflow)
}

function resumePausedWorkflow(workflow) {
  if (!workflow.pauseRequested && workflow.status !== 'paused') throw new Error('当前工作流未暂停')
  const previousStatus = workflow.pausedFromStatus
  const resumeNodeKey = workflow.pausedNodeKey || workflow.currentNodeKey
  workflow.pauseRequested = false
  workflow.pausedFromStatus = ''
  workflow.pausedNodeKey = ''

  const currentNode = workflowNodeState(workflow, resumeNodeKey)
  if (previousStatus === 'manual_required' || currentNode?.status === 'waiting') {
    workflow.status = 'manual_required'
    workflow.currentNodeKey = resumeNodeKey
    persistWorkflowState()
    return workflowSummary(workflow)
  }

  workflow.status = 'running'
  activeWorkflowID = workflow.id
  void runExclusive(async () => {
    try {
      if (workflow.mode === 'reauthorization') {
        resetOAuthWorkflowSteps(workflow)
        await runOAuthReauthorization(workflow)
      } else if (!resumeNodeKey || resumeNodeKey === 'members') {
        await executeWorkflow(workflow)
      } else {
        await resumeFineWorkflowFromNextNode(workflow, resumeNodeKey)
      }
    } catch (error) {
      markWorkflowNodeFailed(workflow, workflowFailureNodeKey(workflow, resumeNodeKey || 'members'), error)
    }
  })
  persistWorkflowState()
  return workflowSummary(workflow)
}

function cancelWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  cancelWorkflowState(workflow)
  if (activeWorkflowID === workflow.id) activeWorkflowID = ''
  persistWorkflowState()
  return workflowSummary(workflow)
}

async function runExclusive(task) {
  const next = operation.then(task, task)
  operation = next.catch(() => undefined)
  return next
}

async function readBody(req) {
  let body = ''
  for await (const chunk of req) {
    body += chunk
    if (Buffer.byteLength(body) > requestBodyLimit) throw new Error('请求体过大')
  }
  try {
    return body ? JSON.parse(body) : {}
  } catch {
    throw new Error('请求体不是有效 JSON')
  }
}

async function handle(req, res) {
  const path = normalizedPath(req)
  if (req.method === 'GET' && path === '/healthz') {
    return json(res, 200, { ok: true, workflow_schema_version: workflowProtocolVersion })
  }
  if (req.method === 'GET' && path === '/readyz') {
    try {
      const connected = await browser()
      return json(res, connected.isConnected() ? 200 : 503, {
        ok: connected.isConnected(),
        workflow_schema_version: workflowProtocolVersion
      })
    } catch {
      return json(res, 503, { ok: false, workflow_schema_version: workflowProtocolVersion })
    }
  }
  if (!authorized(req)) return json(res, 401, { error: 'automation service authentication required' })

  if (req.method === 'GET' && path === '/workflows/active') {
    try {
      return json(res, 200, await activeWorkflowStatus())
    } catch (error) {
      return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
    }
  }

  const workflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})$/)
  const pauseWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/pause$/)
  const workflowSecretMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/secret$/)
  // Progress inspection is intentionally outside the serialized browser queue.
  // A remove/invite workflow can take several seconds; queuing this read behind
  // it would leave the XIASS page stuck at "running" until all browser actions
  // finished, defeating the step-by-step workspace. This path only reads the
  // in-memory workflow state and visible OAuth controls; it never navigates or
  // submits an external page.
  if (workflowMatch && req.method === 'GET') {
    try {
      return json(res, 200, await workflowStatus(workflowMatch[1]))
    } catch (error) {
      return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
    }
  }

  // Cancellation must bypass the serialized browser queue so it can stop a
  // long-running page wait. The active action observes cancelRequested at the
  // next node boundary and cannot advance to a later external operation.
  if (workflowMatch && req.method === 'DELETE') {
    try {
      return json(res, 200, cancelWorkflow(workflowMatch[1]))
    } catch (error) {
      return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
    }
  }

  // Pausing is also out-of-band. A running page action may finish, but the
  // workflow cannot enter its next browser node until the operator resumes it.
  if (pauseWorkflowMatch && req.method === 'POST') {
    try {
      return json(res, 200, pauseWorkflow(pauseWorkflowMatch[1]))
    } catch (error) {
      return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
    }
  }

  try {
    const result = await runExclusive(async () => {
      if (req.method === 'GET' && path === '/members') return listMembers()
      if (req.method === 'POST' && path === '/members/refresh') return listMembers({ forceRefresh: true })
      if (req.method === 'POST' && path === '/members/inspect') {
        const result = await listMembers({ forceRefresh: true })
        return { ...result, operation: { type: 'inspect', confirmed: true } }
      }
      if (req.method === 'POST' && path === '/members/invite') {
        const body = await readBody(req)
        return inviteMember(String(body.email || '').trim())
      }
      if (req.method === 'DELETE' && path === '/members') {
        const body = await readBody(req)
        return removeMember(String(body.email || '').trim())
      }
      if (req.method === 'PATCH' && path === '/members') {
        const body = await readBody(req)
        return updateMember(String(body.email || '').trim(), String(body.role || 'member'))
      }
      if (req.method === 'POST' && path === '/workflows') {
        const body = await readBody(req)
        return startWorkflow(body)
      }
      if (req.method === 'POST' && path === '/workflows/reauthorize') {
        const body = await readBody(req)
        return startReauthorizationWorkflow(body)
      }
      if (workflowSecretMatch && req.method === 'GET') return workflowSecret(workflowSecretMatch[1])
      const continueWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/continue$/)
      if (continueWorkflowMatch && req.method === 'POST') return continueWorkflow(continueWorkflowMatch[1])
      const emailCodeWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/email-code$/)
      if (emailCodeWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return submitWorkflowEmailCode(emailCodeWorkflowMatch[1], body?.code)
      }
      const phoneWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/phone$/)
      if (phoneWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return submitWorkflowPhone(phoneWorkflowMatch[1], body?.phone)
      }
      const smsCodeWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/sms-code$/)
      if (smsCodeWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return submitWorkflowSMSCode(smsCodeWorkflowMatch[1], body?.code)
      }
      const completeWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/complete$/)
      if (completeWorkflowMatch && req.method === 'POST') return completeWorkflowImport(completeWorkflowMatch[1])
      const callbackWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/callback$/)
      if (callbackWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return submitWorkflowCallback(callbackWorkflowMatch[1], body?.callback_url)
      }
      const restartOAuthWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/restart-oauth$/)
      if (restartOAuthWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return restartOAuthWorkflow(restartOAuthWorkflowMatch[1], body?.auth_url, body?.oauth_session_id)
      }
      return null
    })
    if (result === null) return json(res, 404, { error: 'not found' })
    return json(res, 200, result)
  } catch (error) {
    return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
  }
}

if (process.env.NODE_ENV !== 'test') {
  restoreWorkflowState()
  http.createServer(handle).listen(port, '0.0.0.0', () => {
    console.log(`team-child-automation listening on ${port}`)
  })
}

export {
  callbackURLFromNavigationEntries,
  cancelWorkflowState,
  completeWorkflowNode,
  createWorkflow,
  decryptWorkflowState,
  encryptWorkflowState,
  fillVerificationCode,
  generateWorkflowPassword,
  pauseWorkflowState,
  pendingInviteEmailsFromTexts,
  recoverOpenAIPhoneEntry,
  resumePausedWorkflow,
  setWorkflowNode,
  submitInviteDialog,
  validateOAuthSessionID,
  validateWorkflowCode,
  workflowProtocolVersion,
  workflowNodeDefinitions,
  workflowFailureNodeKey,
  workflowSummary
}

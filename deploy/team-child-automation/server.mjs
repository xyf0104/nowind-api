import crypto from 'node:crypto'
import http from 'node:http'

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
const oauthPageTimeout = boundedDuration(process.env.OAUTH_PAGE_TIMEOUT_MS, 45000, 10000, 120000)
const memberRefreshAttempts = 3
const inviteAttempts = 3
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

function json(res, status, payload) {
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store'
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
    throw new Error('服务器浏览器当前正在 OpenAI 授权页，成员检查不会打断授权流程')
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
  const pendingRows = current.locator(
    'table tbody tr, [role="row"], [role="listitem"], article, [data-testid*="invite" i], [data-testid*="pending" i]'
  )
  try {
    await pendingRows.first().waitFor({ state: 'visible', timeout: 2500 })
  } catch {
    // An empty pending-invites page is a valid result.
  }

  const rows = pendingRows
  const count = await rows.count()
  const emails = new Set()
  for (let index = 0; index < count; index += 1) {
    const email = normalizeEmail(extractEmail(await rows.nth(index).innerText()))
    if (email) emails.add(email)
  }
  if (wanted && await visiblePendingInviteEmail(current, wanted)) {
    // A few hosted builds render the invitation as an unstructured text card.
    // Only the exact requested email is accepted in that fallback; arbitrary
    // emails from the page shell are never treated as pending invitations.
    emails.add(wanted)
  }
  const body = await current.locator('body').innerText().catch(() => '')
  return {
    emails,
    pendingInvites: parsePendingInvites(body) ?? emails.size
  }
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

  // This is the only supported invitation path. Do not press Enter, add an
  // address chip, or look for a second send/purchase action: the embedded
  // browser's Continue button submits the invitation directly. The caller
  // verifies the result in the live pending/member list afterwards.
  let submitButton
  await waitUntil('邀请成员弹窗中找不到可用的提交按钮', async () => {
    // Hosted ChatGPT builds use both labels for the same invitation submit
    // action. Keep the exact Continue path preferred, while accepting the
    // current Send invites variant after the email field is populated.
    for (const pattern of [/continue|继续/i, /^send invites?$/i, /^发送邀请$/i]) {
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
      // A previous click may have succeeded while the SPA response was slow.
      // Check the live member and pending-invite views before submitting again.
      const existing = await listMembers({ forceRefresh: false, requireEmails: true })
      // The same slow SPA render can happen before a retry or a manually
      // completed invitation. Wait for the exact target before deciding that
      // a second native invite is necessary.
      const pendingBefore = await pendingInviteSnapshot({ forceRefresh: false, expectedEmail: normalized, waitForExpectedEmail: true })
      if (existing.members.some((member) => normalizeEmail(member.email) === normalized) || pendingBefore.emails.has(normalized)) {
        return {
          ...existing,
          pending_invites: pendingBefore.pendingInvites,
          operation: { type: 'invite', email: normalized, confirmed: true }
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
  const inputs = await waitForOAuthPage('OpenAI 页面中找不到验证码输入框', verificationInputs)
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

async function waitForPhonePage(current) {
  await waitForOAuthPage('OpenAI 未进入手机号验证页面', async () => {
    const body = await oauthBody(current)
    return /phone number|required.*phone|手机号|电话号码/i.test(body)
      && Boolean(await firstVisibleInput(current, (metadata) => /tel|phone|mobile|手机号/.test(metadata)))
  })
}

async function submitPhoneOnOpenAI(current, rawPhone) {
  const phone = String(rawPhone || '').replace(/[\s()-]/g, '')
  if (!/^\+[1-9]\d{6,14}$/.test(phone)) throw new Error('手机号必须是完整国际格式')
  const input = await waitForOAuthPage('OpenAI 手机号页面中找不到输入框', () => (
    firstVisibleInput(current, (metadata) => /tel|phone|mobile|手机号/.test(metadata))
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

async function captureWorkflowCallback(current, workflow) {
  const raw = await waitForOAuthPage('浏览器未出现 OAuth 回调 URL', async () => {
    const currentURL = current.url()
    try {
      const parsed = new URL(currentURL)
      return parsed.searchParams.get('code') && parsed.searchParams.get('state') ? currentURL : ''
    } catch {
      return ''
    }
  })
  return validateWorkflowCallbackURL(raw, workflow)
}

async function runOAuthRegistrationUntilMailbox(workflow) {
  setWorkflowStep(workflow, 'oauth', 'running', '正在打开 XIASS 官方 OpenAI OAuth 页面')
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
  await fillWorkflowPassword(current, workflow.generatedPassword)
  completeWorkflowNode(workflow, 'password', '13 位随机密码已生成并填入')
  completeWorkflowNode(workflow, 'mail', 'OpenAI 验证邮件已发送')
  setWorkflowStep(workflow, 'oauth', 'completed', '官方注册页已提交，正在等待邮箱验证码')
  setWorkflowStep(workflow, 'verify', 'waiting', '正在通过 Cloudflare 邮箱读取 OpenAI 验证码')
  setWorkflowNode(workflow, 'mailbox', 'waiting', '正在轮询本次临时邮箱')
  workflow.status = 'manual_required'
  activeWorkflowID = workflow.id
}

async function continueWorkflowWithEmailCode(workflow, code) {
  const current = managedBrowserPage
  if (!current || current.isClosed()) throw new Error('服务器浏览器授权标签页不可用')
  completeWorkflowNode(workflow, 'mailbox', 'Cloudflare 已读取 OpenAI 验证邮件')
  setWorkflowNode(workflow, 'email_code', 'running', '正在将邮箱验证码填入 OpenAI')
  await fillVerificationCode(current, code)
  completeWorkflowNode(workflow, 'email_code', '邮箱验证码已自动填入并提交')
  await waitForPhonePage(current)
  completeWorkflowNode(workflow, 'phone', '已进入 OpenAI 手机号验证页面')
  setWorkflowNode(workflow, 'sms_confirm', 'waiting', '请在 XIASS 站内弹窗确认领取手机号')
  workflow.status = 'manual_required'
  activeWorkflowID = workflow.id
}

async function continueWorkflowWithPhone(workflow, phone) {
  const current = managedBrowserPage
  if (!current || current.isClosed()) throw new Error('服务器浏览器授权标签页不可用')
  completeWorkflowNode(workflow, 'sms_confirm', '已通过 XIASS 站内确认领取手机号')
  setWorkflowNode(workflow, 'phone_submit', 'running', '正在填入完整号码并选择 Text message')
  await submitPhoneOnOpenAI(current, phone)
  completeWorkflowNode(workflow, 'phone_submit', '号码已提交并选择 Text message')
  setWorkflowNode(workflow, 'sms_poll', 'waiting', '正在通过 XIASS SMS 服务轮询验证码')
  workflow.status = 'manual_required'
  activeWorkflowID = workflow.id
}

async function continueWorkflowWithSMSCode(workflow, code) {
  const current = managedBrowserPage
  if (!current || current.isClosed()) throw new Error('服务器浏览器授权标签页不可用')
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
  setWorkflowStep(workflow, 'verify', 'completed', '外部验证已完成，回调 URL 已校验')
  workflow.status = 'callback_ready'
  workflow.currentNodeKey = 'import'
  setWorkflowNode(workflow, 'import', 'waiting', '等待按已勾选配置导入 XIASS')
}

function workflowStep(key, number, label) {
  return { key, number, label, status: 'pending', message: '' }
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
  ['sms_confirm', '确认领取手机号'],
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
    startStep: 'members',
    runOnlyStep: false,
    createdAt: now,
    expiresAt: now + workflowTTL,
    status: 'running',
    error: '',
    resumeRequested: false,
    resumeNextStepIndex: -1,
    failedStepKey: '',
    failedNodeKey: '',
    seatObserved: false,
    inviteConfirmed: false,
    inviteConfirmedAt: 0,
    callbackURL: '',
    generatedPassword: '',
    currentNodeKey: '',
    steps: [
      workflowStep('members', 1, seatAlreadyRemoved ? '确认已腾出席位' : '读取成员席位'),
      workflowStep('remove', 2, seatAlreadyRemoved ? '跳过成员移除' : '移除已选成员'),
      workflowStep('invite', 3, '邀请临时邮箱'),
      workflowStep('oauth', 4, '准备 OpenAI 授权链接'),
      workflowStep('verify', 5, '完成外部授权并提交回调')
    ],
    nodes: workflowNodeDefinitions.map(([key, label], index) => workflowNode(key, index + 1, label))
  }
}

const workflowStepKeys = new Set(['members', 'remove', 'invite', 'oauth', 'verify'])

function validateWorkflowStartStep(value) {
  const step = String(value || '').trim().toLowerCase() || 'members'
  if (!workflowStepKeys.has(step)) throw new Error('工作流起始步骤无效')
  return step
}

function validateWorkflowStep(value) {
  const step = String(value || '').trim().toLowerCase()
  if (!step || !workflowStepKeys.has(step)) throw new Error('步骤无效')
  return step
}

function workflowStepIndex(stepKey) {
  const index = ['members', 'remove', 'invite', 'oauth', 'verify'].indexOf(stepKey)
  if (index < 0) throw new Error('步骤无效')
  return index
}

function workflowSummary(workflow) {
  const oauthState = new URL(workflow.authURL).searchParams.get('state') || ''
  const summary = {
    id: workflow.id,
    status: workflow.status,
    expires_at: new Date(workflow.expiresAt).toISOString(),
    manual_required: workflow.status === 'manual_required',
    seat_already_removed: Boolean(workflow.seatAlreadyRemoved),
    oauth_session_id: workflow.oauthSessionID,
    oauth_state: oauthState,
    current_node: workflow.currentNodeKey || '',
    steps: workflow.steps.map(({ key, number, label, status, message }) => ({ key, number, label, status, ...(message ? { message } : {}) })),
    nodes: workflow.nodes.map(({ key, number, label, status, message }) => ({ key, number, label, status, ...(message ? { message } : {}) }))
  }
  if (workflow.error) summary.error = workflow.error
  // The callback is deliberately held only in process memory. It is returned
  // to the authenticated XIASS admin caller after the operator pastes it,
  // so the existing state-validated import endpoint can consume it.
  if (['callback_ready', 'completed'].includes(workflow.status) && workflow.callbackURL) summary.callback_url = workflow.callbackURL
  return summary
}

function setWorkflowStep(workflow, key, status, message = '') {
  const step = workflow.steps.find((item) => item.key === key)
  if (!step) return
  step.status = status
  step.message = message
}

function setWorkflowNode(workflow, key, status, message = '') {
  const node = workflow.nodes.find((item) => item.key === key)
  if (!node) return
  node.status = status
  node.message = message
  if (['running', 'waiting', 'failed'].includes(status)) workflow.currentNodeKey = key
}

function completeWorkflowNode(workflow, key, message) {
  setWorkflowNode(workflow, key, 'completed', message)
}

function workflowNodeState(workflow, key) {
  return workflow.nodes.find((node) => node.key === key)
}

function workflowStepState(workflow, key) {
  return workflow.steps.find((step) => step.key === key)
}

function isWorkflowStepCompleted(workflow, key) {
  return workflowStepState(workflow, key)?.status === 'completed' || (key === 'invite' && workflow.inviteConfirmed === true)
}

function completeInviteStep(workflow, message) {
  workflow.inviteConfirmed = true
  workflow.inviteConfirmedAt = Date.now()
  setWorkflowStep(workflow, 'invite', 'completed', message)
}

function completeInviteNodes(workflow, submissionMessage, confirmationMessage) {
  completeWorkflowNode(workflow, 'invite', submissionMessage)
  completeWorkflowNode(workflow, 'invite_confirm', confirmationMessage)
}

async function confirmInviteNode(workflow) {
  setWorkflowNode(workflow, 'invite_confirm', 'running', '正在核对 Members 和 Pending invites')
  const latestMembers = await listMembers({ forceRefresh: false })
  if (latestMembers.members.some((member) => normalizeEmail(member.email) === workflow.inviteEmail)) {
    completeInviteStep(workflow, '临时邮箱已在实时成员列表中确认')
    completeWorkflowNode(workflow, 'invite_confirm', '临时邮箱已在实时成员列表中确认')
    return
  }
  const pending = await pendingInviteSnapshot({ forceRefresh: false, expectedEmail: workflow.inviteEmail, waitForExpectedEmail: true })
  if (!pending.emails.has(normalizeEmail(workflow.inviteEmail))) {
    throw new Error('Pending invites 中未找到目标临时邮箱，请在内嵌浏览器完成邀请后继续')
  }
  completeInviteStep(workflow, '临时邮箱已在 Pending invites 中确认')
  completeWorkflowNode(workflow, 'invite_confirm', '已在 Pending invites 精确匹配临时邮箱')
}

async function confirmManualStepCompletion(workflow, step) {
  if (step.key === 'members') {
    const latest = await listMembers({ forceRefresh: false, requireEmails: true })
    if (workflow.seatAlreadyRemoved) {
      const unexpected = latest.members.find((member) => !isProtectedTeamMember(member))
      if (unexpected) throw new Error('成员列表仍有未确认的普通成员，请先在浏览器完成席位处理')
      return '成员页面已刷新并确认当前席位状态'
    }
    const selected = latest.members.find((member) => normalizeEmail(member.email) === workflow.seatEmail)
    if (selected) {
      assertRemovableMember(selected)
      workflow.seatObserved = true
      return '成员页面已刷新，下一步将处理已选普通成员席位'
    }
    if (workflow.seatObserved) return '已确认已选成员不再出现在实时列表中'
    throw new Error('未在实时成员页面确认已选席位，请先登录并刷新成员列表')
  }

  if (step.key === 'remove') {
    const latest = await listMembers({ forceRefresh: false, requireEmails: true })
    if (latest.members.some((member) => normalizeEmail(member.email) === workflow.seatEmail)) {
      throw new Error('待替换成员仍在实时成员列表中，请先在内嵌浏览器完成移除')
    }
    return '已在实时成员列表确认席位已腾出'
  }

  if (step.key === 'invite') {
    // The ordinary seat has already been removed by this point, so a valid
    // workspace may contain no member rows at all. Pending invites is the
    // authoritative confirmation for this step; an empty Members table must
    // not block continuation.
    const latestMembers = await listMembers({ forceRefresh: false })
    if (latestMembers.members.some((member) => normalizeEmail(member.email) === workflow.inviteEmail)) {
      completeInviteStep(workflow, '临时邮箱已在实时成员列表中确认')
      return '临时邮箱已在实时成员列表中确认'
    }
    const pending = await pendingInviteSnapshot({ forceRefresh: false, expectedEmail: workflow.inviteEmail, waitForExpectedEmail: true })
    if (!pending.emails.has(normalizeEmail(workflow.inviteEmail))) {
      throw new Error('Pending invites 中未找到目标临时邮箱，请在内嵌浏览器中点击邀请并确认后再继续')
    }
    completeInviteStep(workflow, '临时邮箱已在 Pending invites 中确认')
    return '临时邮箱已在 Pending invites 中确认'
  }

  if (step.key === 'oauth') {
    return 'XIASS 官方 OAuth 链接已生成，请在内嵌服务器浏览器完成授权'
  }
  return '该步骤已由人工处理，自动化将直接执行下一步'
}

async function completeFailedStepForManualContinuation(workflow) {
  const failedIndex = workflow.failedStepKey
    ? workflow.steps.findIndex((step) => step.key === workflow.failedStepKey && step.status === 'failed')
    : workflow.steps.findIndex((step) => step.status === 'failed')
  if (failedIndex < 0) throw new Error('当前工作流没有可继续的失败步骤')
  const failed = workflow.steps[failedIndex]
  const confirmationMessage = await confirmManualStepCompletion(workflow, failed)
  failed.status = 'completed'
  failed.message = confirmationMessage
  if (failed.key === 'invite') {
    workflow.inviteConfirmed = true
    workflow.inviteConfirmedAt = Date.now()
  }
  workflow.resumeNextStepIndex = failedIndex + 1
  workflow.failedStepKey = ''
}

async function resumeFineWorkflowFromNextNode(workflow) {
  const failedKey = workflow.failedNodeKey
  const failedIndex = workflow.nodes.findIndex((node) => node.key === failedKey && node.status === 'failed')
  if (failedIndex < 0) throw new Error('当前工作流没有可继续的失败节点')
  const failedNode = workflow.nodes[failedIndex]
  failedNode.status = 'completed'
  failedNode.message = '该节点已由人工处理，自动化从下一节点继续'
  workflow.failedNodeKey = ''
  workflow.failedStepKey = ''
  workflow.error = ''
  workflow.status = 'running'

  const current = managedBrowserPage
  if (!current || current.isClosed()) throw new Error('服务器浏览器授权标签页不可用')
  let nextKey = workflow.nodes[failedIndex + 1]?.key || 'import'

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
      await fillWorkflowPassword(current, workflow.generatedPassword)
      completeWorkflowNode(workflow, 'password', '13 位随机密码已生成并填入')
      completeWorkflowNode(workflow, 'mail', 'OpenAI 验证邮件已发送')
      setWorkflowStep(workflow, 'oauth', 'completed', '官方注册页已提交，正在等待邮箱验证码')
      setWorkflowStep(workflow, 'verify', 'waiting', '正在通过 Cloudflare 邮箱读取 OpenAI 验证码')
      setWorkflowNode(workflow, 'mailbox', 'waiting', '正在轮询本次临时邮箱')
      workflow.status = 'manual_required'
      return
    }
    if (['mail', 'mailbox'].includes(nextKey)) {
      await waitForOAuthPage('OpenAI 未停留在邮箱验证页面', async () => {
        const body = await oauthBody(current)
        return /check your inbox|verify your email|verification code|检查.*邮箱|验证.*邮箱|验证码/i.test(body)
          || (await verificationInputs(current)).length > 0
      })
      completeWorkflowNode(workflow, 'mail', '已确认 OpenAI 验证邮件页面')
      setWorkflowStep(workflow, 'oauth', 'completed', '已确认注册提交完成')
      setWorkflowStep(workflow, 'verify', 'waiting', '正在通过 Cloudflare 邮箱读取 OpenAI 验证码')
      setWorkflowNode(workflow, 'mailbox', 'waiting', '正在轮询本次临时邮箱')
      workflow.status = 'manual_required'
      return
    }
    if (nextKey === 'email_code') {
      setWorkflowNode(workflow, 'email_code', 'waiting', '等待 XIASS 将当前邮箱验证码填入 OpenAI')
      workflow.status = 'manual_required'
      return
    }
    if (nextKey === 'phone') {
      await waitForPhonePage(current)
      completeWorkflowNode(workflow, 'email_code', '已确认邮箱验证码由人工提交')
      completeWorkflowNode(workflow, 'phone', '已进入 OpenAI 手机号验证页面')
      setWorkflowNode(workflow, 'sms_confirm', 'waiting', '请在 XIASS 站内弹窗确认领取手机号')
      workflow.status = 'manual_required'
      return
    }
    if (nextKey === 'sms_confirm') {
      setWorkflowNode(workflow, 'sms_confirm', 'waiting', '请在 XIASS 站内弹窗确认领取手机号')
      workflow.status = 'manual_required'
      return
    }
    if (nextKey === 'phone_submit') {
      setWorkflowNode(workflow, 'phone_submit', 'waiting', '等待 XIASS 将已确认的完整国际号码填入 OpenAI')
      workflow.status = 'manual_required'
      return
    }
    if (nextKey === 'sms_poll') {
      await waitForOAuthPage('OpenAI 未停留在短信验证码页面', async () => (await verificationInputs(current)).length > 0)
      completeWorkflowNode(workflow, 'phone_submit', '已确认号码由人工提交')
      setWorkflowNode(workflow, 'sms_poll', 'waiting', '正在通过 XIASS SMS 服务轮询验证码')
      workflow.status = 'manual_required'
      return
    }
    if (['sms_code', 'profile_wait', 'profile'].includes(nextKey)) {
      if (nextKey === 'sms_code') {
        setWorkflowNode(workflow, 'sms_code', 'waiting', '等待 XIASS 将当前短信验证码填入 OpenAI')
        workflow.status = 'manual_required'
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
    setWorkflowStep(workflow, 'verify', 'completed', '外部验证已完成，回调 URL 已校验')
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
  for (const [id, workflow] of workflows.entries()) {
    if (workflow.expiresAt > now) continue
    if (activeWorkflowID === id) activeWorkflowID = ''
    workflows.delete(id)
  }
}

function activeWorkflow() {
  pruneWorkflows()
  if (!activeWorkflowID) return undefined
  const workflow = workflows.get(activeWorkflowID)
  if (!workflow || !['running', 'manual_required', 'callback_ready', 'failed'].includes(workflow.status)) {
    activeWorkflowID = ''
    return undefined
  }
  return workflow
}

async function activeWorkflowStatus() {
  const workflow = activeWorkflow()
  if (!workflow) return { active: false }
  return { active: true, workflow: await workflowStatus(workflow.id) }
}

function waitForWorkflowCallback(workflow, message) {
  setWorkflowStep(workflow, 'verify', 'waiting', message)
  workflow.status = 'manual_required'
  activeWorkflowID = workflow.id
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
  if (!isWorkflowStepCompleted(workflow, 'invite')) throw new Error('临时邮箱邀请尚未完成，暂不能提交 OAuth 回调')

  workflow.callbackURL = validateWorkflowCallbackURL(value, workflow)
  workflow.error = ''
  setWorkflowStep(workflow, 'oauth', 'completed', '官方授权已由内嵌服务器浏览器完成')
  setWorkflowStep(workflow, 'verify', 'completed', '已校验回调 URL 的 code 和 state')
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
  if (!isWorkflowStepCompleted(workflow, 'invite')) {
    throw new Error('临时邮箱邀请尚未完成，暂不能只重启 OAuth')
  }
  return workflow
}

function markWorkflowActionFailed(workflow, error) {
  const message = redactWorkflowError(error)
  workflow.status = 'failed'
  workflow.error = message
  const active = workflow.steps.find((step) => step.status === 'running')
  const failedKey = active?.key || 'verify'
  workflow.failedStepKey = failedKey
  setWorkflowStep(workflow, failedKey, 'failed', message)
  return message
}

function resetOAuthWorkflowSteps(workflow) {
  setWorkflowStep(workflow, 'oauth', 'pending', '')
  setWorkflowStep(workflow, 'verify', 'pending', '')
  const startIndex = workflow.nodes.findIndex((node) => node.key === 'oauth')
  for (let index = Math.max(0, startIndex); index < workflow.nodes.length; index += 1) {
    workflow.nodes[index].status = 'pending'
    workflow.nodes[index].message = ''
  }
  workflow.callbackURL = ''
  workflow.generatedPassword = ''
  workflow.currentNodeKey = 'oauth'
  workflow.error = ''
}

async function restartOAuthWorkflow(id, value, oauthSessionIDValue) {
  const workflow = restartableOAuthWorkflow(id)
  const authURL = validateOpenAIAuthURL(value)
  const oauthSessionID = validateOAuthSessionID(oauthSessionIDValue)
  resetOAuthWorkflowSteps(workflow)
  workflow.authURL = authURL
  workflow.oauthSessionID = oauthSessionID
  return scheduleWorkflowNodeAction(workflow, 'oauth', () => runOAuthRegistrationUntilMailbox(workflow))
}

// After an operator handles a failed stage, continue starts at the following
// stage by design. It never refreshes, revalidates, or replays the failed
// action. A later failure can be handled the same way, one stage at a time.
async function resumeWorkflowFromNextStep(workflow) {
  const startIndex = workflow.resumeNextStepIndex
  if (!Number.isInteger(startIndex) || startIndex < 0 || startIndex > workflow.steps.length) {
    throw new Error('继续位置无效，请重新开始工作流')
  }
  workflow.resumeRequested = false
  workflow.resumeNextStepIndex = -1

  for (let index = startIndex; index < workflow.steps.length; index += 1) {
    const step = workflow.steps[index]
    if (step.status === 'completed') continue

    if (step.key === 'members') {
      const message = await confirmManualStepCompletion(workflow, step)
      setWorkflowStep(workflow, 'members', 'completed', message)
      completeWorkflowNode(workflow, 'members', message)
      continue
    }
    if (step.key === 'remove') {
      if (workflow.seatAlreadyRemoved) {
        setWorkflowStep(workflow, 'remove', 'completed', '成员席位已由人工腾出，跳过移除')
        completeWorkflowNode(workflow, 'remove', '成员席位已由人工腾出，跳过移除')
        continue
      }
      // If the operator also completed removal while recovering the previous
      // Members step, recognize the live absence instead of submitting a
      // duplicate destructive action.
      const currentMembers = await listMembers({ forceRefresh: false, requireEmails: true })
      if (!currentMembers.members.some((member) => normalizeEmail(member.email) === workflow.seatEmail)) {
        setWorkflowStep(workflow, 'remove', 'completed', '已在实时成员列表确认成员已由人工移除')
        completeWorkflowNode(workflow, 'remove', '已在实时成员列表确认成员已由人工移除')
        continue
      }
      setWorkflowStep(workflow, 'remove', 'running', '正在提交成员移除')
      await removeMember(workflow.seatEmail)
      setWorkflowStep(workflow, 'remove', 'completed', '成员已从工作区移除')
      completeWorkflowNode(workflow, 'remove', '成员已从工作区移除')
      continue
    }
    if (step.key === 'invite') {
      setWorkflowStep(workflow, 'invite', 'running', '正在邀请临时邮箱')
      setWorkflowNode(workflow, 'invite', 'running', '正在原生邀请弹窗提交临时邮箱')
      await inviteMember(workflow.inviteEmail)
      completeWorkflowNode(workflow, 'invite', '已在原生页面提交邀请')
      await confirmInviteNode(workflow)
      continue
    }
    if (step.key === 'oauth') {
      await runOAuthRegistrationUntilMailbox(workflow)
      return
    }
    if (step.key === 'verify') {
      await waitForWorkflowCallback(workflow, '完成授权后，请把完整回调 URL 粘贴到工作区')
      return
    }
  }

  await waitForWorkflowCallback(workflow, '等待授权回调地址')
}

async function executeSingleWorkflowStep(workflow, index) {
  for (let previous = 0; previous < index; previous += 1) {
    const step = workflow.steps[previous]
    if (step.status === 'completed') continue
    const message = await confirmManualStepCompletion(workflow, step)
    setWorkflowStep(workflow, step.key, 'completed', message)
    completeWorkflowNode(workflow, step.key, message)
  }

  const step = workflow.steps[index]
  if (!step || step.status === 'completed') {
    workflow.status = 'manual_required'
    activeWorkflowID = workflow.id
    return
  }

  if (step.key === 'members') {
    const message = await confirmManualStepCompletion(workflow, step)
    setWorkflowStep(workflow, 'members', 'completed', message)
    completeWorkflowNode(workflow, 'members', message)
  } else if (step.key === 'remove') {
    if (workflow.seatAlreadyRemoved) {
      setWorkflowStep(workflow, 'remove', 'completed', '成员席位已由人工腾出，跳过移除')
      completeWorkflowNode(workflow, 'remove', '成员席位已由人工腾出，跳过移除')
    } else {
      const currentMembers = await listMembers({ forceRefresh: false, requireEmails: true })
      if (!currentMembers.members.some((member) => normalizeEmail(member.email) === workflow.seatEmail)) {
        setWorkflowStep(workflow, 'remove', 'completed', '已在实时成员列表确认成员已由人工移除')
        completeWorkflowNode(workflow, 'remove', '已在实时成员列表确认成员已由人工移除')
      } else {
        setWorkflowStep(workflow, 'remove', 'running', '正在提交成员移除')
        await removeMember(workflow.seatEmail)
        setWorkflowStep(workflow, 'remove', 'completed', '成员已从工作区移除')
        completeWorkflowNode(workflow, 'remove', '成员已从工作区移除')
      }
    }
  } else if (step.key === 'invite') {
    setWorkflowStep(workflow, 'invite', 'running', '正在邀请临时邮箱')
    setWorkflowNode(workflow, 'invite', 'running', '正在原生邀请弹窗提交临时邮箱')
    await inviteMember(workflow.inviteEmail)
    completeWorkflowNode(workflow, 'invite', '已在原生页面提交邀请')
    await confirmInviteNode(workflow)
  } else if (step.key === 'oauth') {
    await runOAuthRegistrationUntilMailbox(workflow)
    return
  } else if (step.key === 'verify') {
    await waitForWorkflowCallback(workflow, '完成授权后，请把完整回调 URL 粘贴到工作区')
    return
  }

  workflow.status = 'manual_required'
  workflow.error = ''
  activeWorkflowID = workflow.id
}

async function executeWorkflow(workflow) {
  try {
    if (workflow.resumeRequested) {
      await resumeWorkflowFromNextStep(workflow)
      return
    }

    const startIndex = workflowStepIndex(workflow.startStep || 'members')
    if (startIndex > 0) {
      if (workflow.runOnlyStep) {
        await executeSingleWorkflowStep(workflow, startIndex)
        return
      }
      // A manually completed prefix is accepted only after the live browser
      // confirms every earlier stage. This allows starting at OAuth after an
      // operator handled the member page without replaying destructive actions.
      for (let index = 0; index < startIndex; index += 1) {
        const step = workflow.steps[index]
        if (step.status === 'completed') continue
        const message = await confirmManualStepCompletion(workflow, step)
        setWorkflowStep(workflow, step.key, 'completed', message)
        completeWorkflowNode(workflow, step.key, message)
      }
      workflow.resumeNextStepIndex = startIndex
      await resumeWorkflowFromNextStep(workflow)
      return
    }

    setWorkflowStep(workflow, 'members', 'running', '正在读取成员管理页')
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
      setWorkflowStep(workflow, 'members', 'completed', '已刷新成员页，确认仅剩受保护成员')
      setWorkflowStep(workflow, 'remove', 'completed', '普通成员席位已由人工腾出，跳过移除')
      completeWorkflowNode(workflow, 'members', '已确认当前成员席位状态')
      completeWorkflowNode(workflow, 'remove', '普通成员席位已由人工腾出，未执行移除')
    } else if (selected) {
      assertRemovableMember(selected)
      workflow.seatObserved = true
      setWorkflowStep(workflow, 'members', 'completed', '已刷新并确认可替换的普通成员席位')
      completeWorkflowNode(workflow, 'members', '已读取并确认可替换的普通成员席位')
      if (isWorkflowStepCompleted(workflow, 'remove')) {
        throw new Error('已完成的移除步骤刷新后仍显示该成员，请重新确认成员状态后开始新的工作流')
      }
      setWorkflowStep(workflow, 'remove', 'running', '正在提交成员移除')
      setWorkflowNode(workflow, 'remove', 'running', '正在通过成员行菜单移除已选普通成员')
      await removeMember(workflow.seatEmail)
      setWorkflowStep(workflow, 'remove', 'completed', '成员已从工作区移除')
      completeWorkflowNode(workflow, 'remove', '已从实时成员页面确认成员移除')
    } else if (isWorkflowStepCompleted(workflow, 'remove')) {
      setWorkflowStep(workflow, 'members', 'completed', '已刷新成员页，原成员仍保持已移除状态')
      completeWorkflowNode(workflow, 'members', '已确认原成员不在实时列表中')
      completeWorkflowNode(workflow, 'remove', '已确认成员席位保持腾出状态')
    } else {
      throw new Error('已选成员不在实时成员列表中，请刷新成员页确认后点击继续')
    }

    setWorkflowStep(workflow, 'invite', 'running', '正在确认临时邮箱邀请状态')
    setWorkflowNode(workflow, 'invite', 'running', '正在原生 Invite member 弹窗提交临时邮箱')
    const currentMembers = await listMembers({ forceRefresh: false })
    const invitationAccepted = currentMembers.members.some((member) => normalizeEmail(member.email) === workflow.inviteEmail)
    if (invitationAccepted) {
      completeInviteStep(workflow, '临时邮箱已出现在成员列表中')
      completeInviteNodes(workflow, '无需重复提交邀请', '临时邮箱已出现在成员列表中')
    } else {
      const pendingSnapshot = await pendingInviteSnapshot({ forceRefresh: false, expectedEmail: workflow.inviteEmail, waitForExpectedEmail: true })
      if (pendingSnapshot.emails.has(normalizeEmail(workflow.inviteEmail))) {
        completeInviteStep(workflow, '临时邮箱已出现在待处理邀请中')
        completeInviteNodes(workflow, '无需重复提交邀请', '已在 Pending invites 精确匹配临时邮箱')
      } else if (workflow.seatAlreadyRemoved && pendingSnapshot.emails.size > 0) {
        throw new Error('当前工作区已有其他待处理邀请。为避免占用已腾出的唯一席位，请恢复对应临时邮箱或先在浏览器中取消旧邀请后重试')
      } else {
        await inviteMember(workflow.inviteEmail)
        completeWorkflowNode(workflow, 'invite', '已在原生页面提交邀请')
        await confirmInviteNode(workflow)
      }
    }

    await runOAuthRegistrationUntilMailbox(workflow)
  } catch (error) {
    const message = redactWorkflowError(error)
    workflow.status = 'failed'
    workflow.error = message
    const activeNode = workflow.nodes.find((node) => node.status === 'running')
    if (activeNode) {
      markWorkflowNodeFailed(workflow, activeNode.key, error)
      return
    }
    const active = workflow.steps.find((step) => step.status === 'running')
    const failedKey = active?.key || 'verify'
    workflow.failedStepKey = failedKey
    setWorkflowStep(workflow, failedKey, 'failed', message)
  }
}

function markSpecificWorkflowStepFailed(workflow, stepKey, error) {
  const message = redactWorkflowError(error)
  workflow.status = 'failed'
  workflow.error = message
  workflow.failedStepKey = stepKey
  setWorkflowStep(workflow, stepKey, 'failed', message)
  return message
}

function markWorkflowNodeFailed(workflow, nodeKey, error) {
  const message = redactWorkflowError(error)
  workflow.status = 'failed'
  workflow.error = message
  workflow.failedNodeKey = nodeKey
  workflow.currentNodeKey = nodeKey
  setWorkflowNode(workflow, nodeKey, 'failed', message)
  const coarseKey = nodeKey === 'members'
    ? 'members'
    : nodeKey === 'remove'
      ? 'remove'
      : ['invite', 'invite_confirm'].includes(nodeKey)
        ? 'invite'
        : ['oauth', 'signup', 'email', 'password', 'mail'].includes(nodeKey)
          ? 'oauth'
          : 'verify'
  workflow.failedStepKey = coarseKey
  setWorkflowStep(workflow, coarseKey, 'failed', message)
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
  if (workflow.status === 'running') throw new Error('当前自动化节点仍在执行')
  return workflow
}

function scheduleWorkflowNodeAction(workflow, nodeKey, action) {
  workflow.status = 'running'
  workflow.error = ''
  workflow.failedNodeKey = ''
  workflow.failedStepKey = ''
  setWorkflowNode(workflow, nodeKey, 'running', workflowNodeState(workflow, nodeKey)?.message || '正在执行')
  activeWorkflowID = workflow.id
  void runExclusive(async () => {
    try {
      await action()
    } catch (error) {
      markWorkflowNodeFailed(workflow, nodeKey, error)
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
  if (!['sms_confirm', 'phone_submit'].includes(workflow.currentNodeKey)) throw new Error('当前页面尚未等待手机号')
  const phone = String(value || '').replace(/[\s()-]/g, '')
  if (!/^\+[1-9]\d{6,14}$/.test(phone)) throw new Error('手机号必须是完整国际格式')
  return scheduleWorkflowNodeAction(workflow, 'phone_submit', () => continueWorkflowWithPhone(workflow, phone))
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
  completeWorkflowNode(workflow, 'import', '已按勾选的分组、优先级和并发导入 XIASS')
  workflow.status = 'completed'
  workflow.currentNodeKey = 'import'
  workflow.generatedPassword = ''
  if (activeWorkflowID === workflow.id) activeWorkflowID = ''
  return workflowSummary(workflow)
}

async function runWorkflowStep(id, stepKey) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (!workflowStepKeys.has(stepKey)) throw new Error('步骤无效')
  if (workflow.status === 'running') throw new Error('当前工作流正在执行，请等待当前步骤完成')
  if (workflow.status === 'cancelled') throw new Error('当前工作流已停止，请重新开始')

  const index = workflowStepIndex(stepKey)
  const selected = workflow.steps[index]
  if (selected.status === 'completed') return workflowSummary(workflow)

  workflow.status = 'running'
  workflow.error = ''
  workflow.failedStepKey = ''
  try {
    await executeSingleWorkflowStep(workflow, index)
    return workflowSummary(workflow)
  } catch (error) {
    markSpecificWorkflowStepFailed(workflow, stepKey, error)
    throw error
  }
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
  const startStep = validateWorkflowStartStep(payload?.start_step)
  if (activeWorkflow()) throw new Error('已有 Team 子号工作流正在进行，请先完成或取消当前工作流')

  const workflow = createWorkflow(seatEmail, inviteEmail, authURL, oauthSessionID, seatAlreadyRemoved)
  workflow.startStep = startStep
  workflow.runOnlyStep = payload?.run_only_step === true
  workflows.set(workflow.id, workflow)
  activeWorkflowID = workflow.id
  // Return immediately so the UI can show the operation timeline while the
  // shared Chromium service serially performs the destructive actions.
  void runExclusive(() => executeWorkflow(workflow))
  return workflowSummary(workflow)
}

async function continueWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status !== 'failed') throw new Error('当前工作流无需继续或仍在执行中')
  const current = activeWorkflow()
  if (current && current.id !== workflow.id) throw new Error('已有其他 Team 子号工作流正在进行')

  if (workflow.failedNodeKey) {
    workflow.status = 'running'
    workflow.error = ''
    activeWorkflowID = workflow.id
    void runExclusive(() => resumeFineWorkflowFromNextNode(workflow))
    return workflowSummary(workflow)
  }

  await completeFailedStepForManualContinuation(workflow)
  workflow.status = 'running'
  workflow.error = ''
  workflow.resumeRequested = true
  activeWorkflowID = workflow.id
  void runExclusive(() => executeWorkflow(workflow))
  return workflowSummary(workflow)
}

function workflowStatus(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status === 'manual_required') {
    const verify = workflowStepState(workflow, 'verify')
    setWorkflowStep(
      workflow,
      'verify',
      'waiting',
      verify?.message || '完成官方 OAuth 后，把地址栏中的完整回调 URL 粘贴到工作区'
    )
  }
  return workflowSummary(workflow)
}

function cancelWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status === 'running') throw new Error('当前步骤正在执行，暂不能取消')
  if (workflow.status === 'manual_required' || workflow.status === 'callback_ready' || workflow.status === 'failed') {
    workflow.status = 'cancelled'
    const active = workflow.steps.find((step) => step.status === 'running' || step.status === 'failed')
    if (active) {
      setWorkflowStep(workflow, active.key, 'cancelled', '已停止自动化；已完成的成员操作不会自动回滚')
    } else {
      setWorkflowStep(workflow, 'verify', 'cancelled', '已停止自动检查，成员移除和邀请操作不会自动回滚')
      const pendingNode = workflow.nodes.find((node) => ['waiting', 'pending'].includes(node.status))
      if (pendingNode) setWorkflowNode(workflow, pendingNode.key, 'cancelled', '工作流已由管理员停止')
    }
    workflow.generatedPassword = ''
  }
  if (activeWorkflowID === workflow.id) activeWorkflowID = ''
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
  if (req.method === 'GET' && path === '/healthz') return json(res, 200, { ok: true })
  if (req.method === 'GET' && path === '/readyz') {
    try {
      const connected = await browser()
      return json(res, connected.isConnected() ? 200 : 503, { ok: connected.isConnected() })
    } catch {
      return json(res, 503, { ok: false })
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
      if (req.method === 'POST' && path === '/browser/navigate') {
        const body = await readBody(req)
        return navigatePersistentBrowser(body?.url)
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
      const continueWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/continue$/)
      if (continueWorkflowMatch && req.method === 'POST') return continueWorkflow(continueWorkflowMatch[1])
      const runStepMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/run-step$/)
      if (runStepMatch && req.method === 'POST') {
        const body = await readBody(req)
        return runWorkflowStep(runStepMatch[1], validateWorkflowStep(body?.step))
      }
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
      if (workflowMatch && req.method === 'DELETE') return cancelWorkflow(workflowMatch[1])
      return null
    })
    if (result === null) return json(res, 404, { error: 'not found' })
    return json(res, 200, result)
  } catch (error) {
    return json(res, 409, { error: error instanceof Error ? error.message : String(error) })
  }
}

if (process.env.NODE_ENV !== 'test') {
  http.createServer(handle).listen(port, '0.0.0.0', () => {
    console.log(`team-child-automation listening on ${port}`)
  })
}

export {
  completeWorkflowNode,
  createWorkflow,
  generateWorkflowPassword,
  setWorkflowNode,
  validateOAuthSessionID,
  validateWorkflowCode,
  workflowNodeDefinitions,
  workflowSummary
}

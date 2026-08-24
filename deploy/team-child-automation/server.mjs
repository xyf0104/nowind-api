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
// Route changes (Members -> Pending invites) are serialized by the operation
// queue; official OAuth is handed off through the displayed PKCE URL.
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

function workflowStep(key, number, label) {
  return { key, number, label, status: 'pending', message: '' }
}

function createWorkflow(seatEmail, inviteEmail, authURL, seatAlreadyRemoved) {
  const id = crypto.randomBytes(24).toString('base64url')
  const now = Date.now()
  return {
    id,
    seatEmail,
    seatAlreadyRemoved,
    inviteEmail,
    authURL,
    startStep: 'members',
    runOnlyStep: false,
    createdAt: now,
    expiresAt: now + workflowTTL,
    status: 'running',
    error: '',
    resumeRequested: false,
    resumeNextStepIndex: -1,
    failedStepKey: '',
    seatObserved: false,
    inviteConfirmed: false,
    inviteConfirmedAt: 0,
    callbackURL: '',
    steps: [
      workflowStep('members', 1, seatAlreadyRemoved ? '确认已腾出席位' : '读取成员席位'),
      workflowStep('remove', 2, seatAlreadyRemoved ? '跳过成员移除' : '移除已选成员'),
      workflowStep('invite', 3, '邀请临时邮箱'),
      workflowStep('oauth', 4, '准备 OpenAI 授权链接'),
      workflowStep('verify', 5, '完成外部授权并提交回调')
    ]
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
  const summary = {
    id: workflow.id,
    status: workflow.status,
    expires_at: new Date(workflow.expiresAt).toISOString(),
    manual_required: workflow.status === 'manual_required',
    seat_already_removed: Boolean(workflow.seatAlreadyRemoved),
    steps: workflow.steps.map(({ key, number, label, status, message }) => ({ key, number, label, status, ...(message ? { message } : {}) }))
  }
  if (workflow.error) summary.error = workflow.error
  // The callback is deliberately held only in process memory. It is returned
  // to the authenticated XIASS admin caller after the operator pastes it,
  // so the existing state-validated import endpoint can consume it.
  if (workflow.status === 'callback_ready' && workflow.callbackURL) summary.callback_url = workflow.callbackURL
  return summary
}

function setWorkflowStep(workflow, key, status, message = '') {
  const step = workflow.steps.find((item) => item.key === key)
  if (!step) return
  step.status = status
  step.message = message
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
    return 'XIASS 官方 OAuth 链接已生成，请在外部浏览器完成授权'
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
  if (!workflow || !['running', 'manual_required', 'failed'].includes(workflow.status)) {
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
  setWorkflowStep(workflow, 'oauth', 'completed', '官方授权已由外部浏览器完成')
  setWorkflowStep(workflow, 'verify', 'completed', '已校验回调 URL 的 code 和 state')
  workflow.status = 'callback_ready'
  if (activeWorkflowID === workflow.id) activeWorkflowID = ''
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
  workflow.callbackURL = ''
  workflow.error = ''
}

async function restartOAuthWorkflow(id, value) {
  const workflow = restartableOAuthWorkflow(id)
  const authURL = validateOpenAIAuthURL(value)
  workflow.status = 'running'
  resetOAuthWorkflowSteps(workflow)
  try {
    workflow.authURL = authURL
    setWorkflowStep(workflow, 'oauth', 'completed', '已生成新的 XIASS 官方 OAuth 链接，请在外部浏览器完成授权')
    await waitForWorkflowCallback(workflow, '完成外部授权后，请把完整回调 URL 粘贴到工作区')
    return workflowSummary(workflow)
  } catch (error) {
    markWorkflowActionFailed(workflow, error)
    throw error
  }
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
      continue
    }
    if (step.key === 'remove') {
      if (workflow.seatAlreadyRemoved) {
        setWorkflowStep(workflow, 'remove', 'completed', '成员席位已由人工腾出，跳过移除')
        continue
      }
      // If the operator also completed removal while recovering the previous
      // Members step, recognize the live absence instead of submitting a
      // duplicate destructive action.
      const currentMembers = await listMembers({ forceRefresh: false, requireEmails: true })
      if (!currentMembers.members.some((member) => normalizeEmail(member.email) === workflow.seatEmail)) {
        setWorkflowStep(workflow, 'remove', 'completed', '已在实时成员列表确认成员已由人工移除')
        continue
      }
      setWorkflowStep(workflow, 'remove', 'running', '正在提交成员移除')
      await removeMember(workflow.seatEmail)
      setWorkflowStep(workflow, 'remove', 'completed', '成员已从工作区移除')
      continue
    }
    if (step.key === 'invite') {
      setWorkflowStep(workflow, 'invite', 'running', '正在邀请临时邮箱')
      await inviteMember(workflow.inviteEmail)
      completeInviteStep(workflow, '邀请已发送并出现在待处理邀请中')
      continue
    }
    if (step.key === 'oauth') {
      setWorkflowStep(workflow, 'oauth', 'completed', '官方 OAuth 链接已准备好，请在外部浏览器完成授权')
      continue
    }
    if (step.key === 'verify') {
      await waitForWorkflowCallback(workflow, '完成外部授权后，请把完整回调 URL 粘贴到工作区')
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
  } else if (step.key === 'remove') {
    if (workflow.seatAlreadyRemoved) {
      setWorkflowStep(workflow, 'remove', 'completed', '成员席位已由人工腾出，跳过移除')
    } else {
      const currentMembers = await listMembers({ forceRefresh: false, requireEmails: true })
      if (!currentMembers.members.some((member) => normalizeEmail(member.email) === workflow.seatEmail)) {
        setWorkflowStep(workflow, 'remove', 'completed', '已在实时成员列表确认成员已由人工移除')
      } else {
        setWorkflowStep(workflow, 'remove', 'running', '正在提交成员移除')
        await removeMember(workflow.seatEmail)
        setWorkflowStep(workflow, 'remove', 'completed', '成员已从工作区移除')
      }
    }
  } else if (step.key === 'invite') {
    setWorkflowStep(workflow, 'invite', 'running', '正在邀请临时邮箱')
    await inviteMember(workflow.inviteEmail)
    completeInviteStep(workflow, '邀请已发送并出现在待处理邀请中')
  } else if (step.key === 'oauth') {
    setWorkflowStep(workflow, 'oauth', 'completed', '官方 OAuth 链接已准备好，请在外部浏览器完成授权')
  } else if (step.key === 'verify') {
    await waitForWorkflowCallback(workflow, '完成外部授权后，请把完整回调 URL 粘贴到工作区')
    return
  }

  workflow.status = 'manual_required'
  workflow.error = ''
  activeWorkflowID = workflow.id
  if (step.key === 'oauth') {
    setWorkflowStep(workflow, 'verify', 'waiting', '官方 OAuth 链接已准备，请完成外部授权后粘贴回调 URL')
  }
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
      }
      workflow.resumeNextStepIndex = startIndex
      await resumeWorkflowFromNextStep(workflow)
      return
    }

    setWorkflowStep(workflow, 'members', 'running', '正在读取成员管理页')
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
    } else if (selected) {
      assertRemovableMember(selected)
      workflow.seatObserved = true
      setWorkflowStep(workflow, 'members', 'completed', '已刷新并确认可替换的普通成员席位')
      if (isWorkflowStepCompleted(workflow, 'remove')) {
        throw new Error('已完成的移除步骤刷新后仍显示该成员，请重新确认成员状态后开始新的工作流')
      }
      setWorkflowStep(workflow, 'remove', 'running', '正在提交成员移除')
      await removeMember(workflow.seatEmail)
      setWorkflowStep(workflow, 'remove', 'completed', '成员已从工作区移除')
    } else if (isWorkflowStepCompleted(workflow, 'remove')) {
      setWorkflowStep(workflow, 'members', 'completed', '已刷新成员页，原成员仍保持已移除状态')
    } else {
      throw new Error('已选成员不在实时成员列表中，请刷新成员页确认后点击继续')
    }

    setWorkflowStep(workflow, 'invite', 'running', '正在确认临时邮箱邀请状态')
    const currentMembers = await listMembers({ forceRefresh: false })
    const invitationAccepted = currentMembers.members.some((member) => normalizeEmail(member.email) === workflow.inviteEmail)
    if (invitationAccepted) {
      completeInviteStep(workflow, '临时邮箱已出现在成员列表中')
    } else {
      const pendingSnapshot = await pendingInviteSnapshot({ forceRefresh: false, expectedEmail: workflow.inviteEmail, waitForExpectedEmail: true })
      if (pendingSnapshot.emails.has(normalizeEmail(workflow.inviteEmail))) {
        completeInviteStep(workflow, '临时邮箱已出现在待处理邀请中')
      } else if (workflow.seatAlreadyRemoved && pendingSnapshot.emails.size > 0) {
        throw new Error('当前工作区已有其他待处理邀请。为避免占用已腾出的唯一席位，请恢复对应临时邮箱或先在浏览器中取消旧邀请后重试')
      } else {
        await inviteMember(workflow.inviteEmail)
        completeInviteStep(workflow, '邀请已发送并出现在待处理邀请中')
      }
    }

    if (!isWorkflowStepCompleted(workflow, 'oauth')) {
      setWorkflowStep(workflow, 'oauth', 'completed', '官方 OAuth 链接已准备好，请在外部浏览器完成授权')
    }
    setWorkflowStep(workflow, 'verify', 'waiting', '请在官方浏览器完成外部授权，然后把完整回调 URL 粘贴到工作区')
    workflow.status = 'manual_required'
  } catch (error) {
    const message = redactWorkflowError(error)
    workflow.status = 'failed'
    workflow.error = message
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
  const startStep = validateWorkflowStartStep(payload?.start_step)
  if (activeWorkflow()) throw new Error('已有 Team 子号工作流正在进行，请先完成或取消当前工作流')

  const workflow = createWorkflow(seatEmail, inviteEmail, authURL, seatAlreadyRemoved)
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
  if (workflow.status === 'manual_required' || workflow.status === 'failed') {
    workflow.status = 'cancelled'
    const active = workflow.steps.find((step) => step.status === 'running' || step.status === 'failed')
    if (active) {
      setWorkflowStep(workflow, active.key, 'cancelled', '已停止自动化；已完成的成员操作不会自动回滚')
    } else {
      setWorkflowStep(workflow, 'verify', 'cancelled', '已停止自动检查，成员移除和邀请操作不会自动回滚')
    }
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
      const callbackWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/callback$/)
      if (callbackWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return submitWorkflowCallback(callbackWorkflowMatch[1], body?.callback_url)
      }
      const restartOAuthWorkflowMatch = path.match(/^\/workflows\/([A-Za-z0-9_-]{16,128})\/restart-oauth$/)
      if (restartOAuthWorkflowMatch && req.method === 'POST') {
        const body = await readBody(req)
        return restartOAuthWorkflow(restartOAuthWorkflowMatch[1], body?.auth_url)
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

http.createServer(handle).listen(port, '0.0.0.0', () => {
  console.log(`team-child-automation listening on ${port}`)
})

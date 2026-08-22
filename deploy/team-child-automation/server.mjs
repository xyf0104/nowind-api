import crypto from 'node:crypto'
import http from 'node:http'

import { chromium } from '@playwright/test'

const port = Number(process.env.PORT || 8090)
const cdpURL = process.env.BROWSER_CDP_URL || 'http://127.0.0.1:9222'
const membersURL = process.env.MEMBERS_URL || 'https://chatgpt.com/admin/members'
const operationTimeout = Number(process.env.OPERATION_TIMEOUT_MS || 30000)
const confirmationTimeout = Number(process.env.CONFIRMATION_TIMEOUT_MS || 12000)
const requestBodyLimit = Number(process.env.REQUEST_BODY_LIMIT_BYTES || 32768)
const serviceToken = String(process.env.TEAM_CHILD_AUTOMATION_TOKEN || '').trim()
const browserConnectTimeout = boundedDuration(process.env.BROWSER_CONNECT_TIMEOUT_MS, 30000, 1000, 120000)
const browserConnectRetryDelay = boundedDuration(process.env.BROWSER_CONNECT_RETRY_DELAY_MS, 750, 100, 5000)
const workflowTTL = boundedDuration(process.env.WORKFLOW_TTL_MS, 45 * 60 * 1000, 5 * 60 * 1000, 2 * 60 * 60 * 1000)

let browserPromise
let operation = Promise.resolve()
let activeWorkflowID = ''
const workflows = new Map()
let managedMembersPage

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

async function membersPage() {
  const connected = await browser()
  const context = connected.contexts()[0]
  if (!context) throw new Error('Chromium 尚未创建浏览器上下文')
  // Never reuse an arbitrary visible tab. The manual browser may be on an OAuth
  // page, a CAPTCHA, or a human confirmation step; member automation must not
  // navigate it away. This service owns one separate members-page tab only.
  let active = managedMembersPage
  if (!active || active.isClosed()) {
    active = await context.newPage()
    managedMembersPage = active
  }
  try {
    await active.goto(membersURL, { waitUntil: 'domcontentloaded', timeout: operationTimeout })
  } catch (error) {
    // A Chromium restart invalidates Playwright page objects without always
    // marking them closed. Recreate the dedicated tab once; never fall back to
    // another user-visible tab.
    if (!active.isClosed()) throw error
    active = await context.newPage()
    managedMembersPage = active
    await active.goto(membersURL, { waitUntil: 'domcontentloaded', timeout: operationTimeout })
  }
  await waitForMemberPageReady(active)
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
  return String(email || '').trim().toLowerCase()
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

function isCurrentSeatRow(text) {
  const normalized = String(text || '').replace(/\s+/g, ' ').trim()
  return /(?:^|[\s([{:：])(?:you|your account|current account|current seat|当前账号|当前账户|当前席位|你)(?=$|[\s)\]},，。：:])/i.test(normalized)
}

function parsePendingInvites(text) {
  const normalized = String(text || '').replace(/\s+/g, ' ').trim()
  const patterns = [
    /(?:pending invitations?|pending invites?|待处理邀请|待接受邀请)[^\d]{0,24}(\d{1,5})/i,
    /(\d{1,5})[^\d]{0,24}(?:pending invitations?|pending invites?|待处理邀请|待接受邀请)/i
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
  const rowRecords = []
  for (let index = 0; index < count; index += 1) {
    const text = (await rows.nth(index).innerText()).trim()
    const email = extractEmail(text)
    if (!email) continue
    const lines = text.split(/[\n\t]+/).map((line) => line.trim()).filter(Boolean)
    const role = roleFromLines(lines)
    members.push({
      // Email is the stable identity. A row index is unsafe after sorting or
      // pagination changes and must never be used for a destructive action.
      id: normalizeEmail(email),
      email,
      name: lines.find((line) => line !== email && line !== role) || '',
      role,
      seat_type: seatTypeFromLines(lines),
      status: 'active'
    })
    rowRecords.push({ text, email })
  }

  const pageTitle = (await current.locator('h1,h2').allTextContents()).join(' ').trim()
  const pendingInvites = parsePendingInvites(body)
  // The Team owner is the current logged-in account in the upstream UI. For
  // this workflow the useful "seat" is the first non-owner account that can
  // actually be replaced, not the owner row marked "You".
  const replaceable = members.find((member) => ['member', 'admin'].includes(String(member.role || '').toLowerCase()))
  const seatEmail = replaceable?.email || rowRecords.find((row) => isCurrentSeatRow(row.text))?.email || ''
  return {
    ready: true,
    url: current.url(),
    members,
    ...(pendingInvites === undefined ? {} : { pending_invites: pendingInvites }),
    seat_email: seatEmail,
    workspace_name: pageTitle
  }
}

async function listMembers() {
  const current = await membersPage()
  return readMembers(current)
}

// The ChatGPT members page is a client-rendered application. DOMContentLoaded
// fires before the member rows exist, which previously produced an apparently
// valid but empty result on slower servers. Wait for the stable toolbar first,
// then allow the table a short window to populate. An actually empty workspace
// still returns normally once the toolbar is visible.
async function waitForMemberPageReady(current) {
  const inviteButton = current.getByRole('button', { name: /invite member|邀请成员/i }).first()
  try {
    await inviteButton.waitFor({ state: 'visible', timeout: Math.min(operationTimeout, 12000) })
  } catch {
    return
  }

  const rows = current.locator('table tbody tr')
  try {
    await rows.first().waitFor({ state: 'visible', timeout: 3500 })
  } catch {
    // A workspace may genuinely have no member rows. The visible toolbar above
    // is the readiness signal in that case.
  }
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

async function memberRow(current, email, required = true) {
  const wanted = normalizeEmail(email)
  const rows = await rowsFor(current)
  const count = await rows.count()
  for (let index = 0; index < count; index += 1) {
    const row = rows.nth(index)
    const actual = normalizeEmail(extractEmail(await row.innerText()))
    if (actual === wanted) return row
  }
  if (required) throw new Error('成员列表中找不到该邮箱，请先刷新后重试')
  return null
}

async function hasVisibleDialog(current) {
  return (await current.locator('[role="dialog"]:visible').count()) > 0
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
  await buttons.nth(buttonCount - 1).click()
}

async function inviteMember(email) {
  const normalized = normalizeEmail(email)
  if (!normalized || !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(normalized)) {
    throw new Error('成员邮箱格式无效')
  }
  const current = await membersPage()
  const bodyBefore = await current.locator('body').innerText()
  await clickText(current, /invite member|邀请成员/i)
  const dialog = current.getByRole('dialog').last()
  const scope = (await dialog.count()) ? dialog : current
  const input = scope.locator('input[type="email"], input').first()
  await input.fill(normalized)
  await clickText(scope, /invite|邀请/i)

  await waitUntil('邀请操作已提交但页面没有成功反馈', async () => {
    if (await hasVisibleDialog(current)) return false
    const body = await current.locator('body').innerText()
    const feedback = /invited|invitation sent|invitation pending|邀请已发送|邀请成功|已邀请/i.test(body)
    return feedback || (!normalizeEmail(bodyBefore).includes(normalized) && normalizeEmail(body).includes(normalized))
  })
  const result = await readMembers(current)
  return { ...result, operation: { type: 'invite', email: normalized, confirmed: true } }
}

async function removeMember(email) {
  const normalized = normalizeEmail(email)
  const current = await membersPage()
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
  const current = await membersPage()
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
  const email = normalizeEmail(value)
  if (!email || !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email) || email.length > 320) {
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
  const allowedHosts = new Set(['auth.openai.com', 'login.openai.com', 'chatgpt.com'])
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password || !allowedHosts.has(parsed.hostname.toLowerCase())) {
    throw new Error('授权链接不是受支持的 OpenAI 地址')
  }
  return parsed.toString()
}

function isOAuthCallbackURL(value) {
  try {
    const parsed = new URL(String(value || ''))
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && Boolean(parsed.searchParams.get('code')?.trim()) && Boolean(parsed.searchParams.get('state')?.trim())
  } catch {
    return false
  }
}

function workflowStep(key, number, label) {
  return { key, number, label, status: 'pending', message: '' }
}

function createWorkflow(seatEmail, inviteEmail, authURL) {
  const id = crypto.randomBytes(24).toString('base64url')
  const now = Date.now()
  return {
    id,
    seatEmail,
    inviteEmail,
    authURL,
    createdAt: now,
    expiresAt: now + workflowTTL,
    status: 'running',
    error: '',
    oauthPage: undefined,
    callbackURL: '',
    steps: [
      workflowStep('members', 1, '读取成员席位'),
      workflowStep('remove', 2, '移除已选成员'),
      workflowStep('invite', 3, '邀请临时邮箱'),
      workflowStep('oauth', 4, '打开 OpenAI 授权页'),
      workflowStep('verify', 5, '完成外部验证并捕获回调')
    ]
  }
}

function workflowSummary(workflow) {
  const summary = {
    id: workflow.id,
    status: workflow.status,
    expires_at: new Date(workflow.expiresAt).toISOString(),
    manual_required: workflow.status === 'manual_required',
    steps: workflow.steps.map(({ key, number, label, status, message }) => ({ key, number, label, status, ...(message ? { message } : {}) }))
  }
  if (workflow.error) summary.error = workflow.error
  // The callback is deliberately held only in process memory. It is returned
  // to the authenticated XIASS admin caller after the browser has reached it,
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
  if (!workflow || !['running', 'manual_required'].includes(workflow.status)) {
    activeWorkflowID = ''
    return undefined
  }
  return workflow
}

async function openOAuthAuthorizationPage(authURL) {
  const connected = await browser()
  const context = connected.contexts()[0]
  if (!context) throw new Error('Chromium 尚未创建浏览器上下文')
  // Keep the members tab intact. The manually controlled browser can switch to
  // this tab only after the seat replacement and invitation both finish.
  const oauthPage = await context.newPage()
  await oauthPage.goto(authURL, { waitUntil: 'domcontentloaded', timeout: operationTimeout })
  return oauthPage
}

async function isVisible(locator) {
  try {
    return await locator.first().isVisible({ timeout: 400 })
  } catch {
    return false
  }
}

// Only inspect semantic controls that are already visible to the person using
// the browser. We intentionally do not extract page text, form values, email
// codes, phone numbers, or credentials into workflow state or logs.
async function describeOAuthNextStep(oauthPage) {
  if (!oauthPage || oauthPage.isClosed()) return '授权标签已关闭；请接管浏览器后重新开始授权。'
  if (await isVisible(oauthPage.getByRole('button', { name: /use another account|使用其他账号/i }))) {
    return '已打开登录页，请在服务器浏览器中选择“使用其他账号”。'
  }
  if (await isVisible(oauthPage.locator('input[autocomplete="one-time-code"], input[name*="code" i], input[id*="code" i]'))) {
    return '正在等待验证码输入；邮箱和短信验证码仍由现有确认式接码流程提供。'
  }
  if (await isVisible(oauthPage.locator('input[type="tel"], input[autocomplete="tel"], input[name*="phone" i]'))) {
    return '页面正在请求手机号；请在接码服务中明确确认领取号码后再填写。'
  }
  if (await isVisible(oauthPage.locator('input[type="email"], input[autocomplete="email"]'))) {
    return '页面正在请求邮箱；请使用当前工作流中的临时邮箱。'
  }
  if (await isVisible(oauthPage.locator('input[type="password"], input[autocomplete="new-password"]'))) {
    return '页面正在请求密码；请在服务器浏览器中完成该外部账号步骤。'
  }
  if (await isVisible(oauthPage.locator('input[autocomplete="name"], input[name*="name" i], input[name*="age" i]'))) {
    return '页面正在请求资料信息；请在服务器浏览器中按外部页面要求完成。'
  }
  if (await isVisible(oauthPage.getByRole('button', { name: /continue|next|继续|下一步/i }))) {
    return '外部页面等待人工确认；请核对当前内容后在服务器浏览器中继续。'
  }
  return '外部授权页面等待人工处理；完成后系统会自动识别回调地址。'
}

async function executeWorkflow(workflow) {
  try {
    setWorkflowStep(workflow, 'members', 'running', '正在读取成员管理页')
    const initial = await listMembers()
    const selected = initial.members.find((member) => normalizeEmail(member.email) === workflow.seatEmail)
    if (!selected) throw new Error('已选成员已不在当前工作区，请刷新后重新选择')
    if (!['member', 'admin'].includes(String(selected.role || '').toLowerCase())) {
      throw new Error('只能替换普通成员或管理员席位，不能替换所有者')
    }
    setWorkflowStep(workflow, 'members', 'completed', '已确认可替换成员席位')

    setWorkflowStep(workflow, 'remove', 'running', '正在提交成员移除')
    await removeMember(workflow.seatEmail)
    setWorkflowStep(workflow, 'remove', 'completed', '成员已从工作区移除')

    setWorkflowStep(workflow, 'invite', 'running', '正在发送临时邮箱邀请')
    await inviteMember(workflow.inviteEmail)
    setWorkflowStep(workflow, 'invite', 'completed', '邀请已发送')

    setWorkflowStep(workflow, 'oauth', 'running', '正在打开授权页')
    workflow.oauthPage = await openOAuthAuthorizationPage(workflow.authURL)
    setWorkflowStep(workflow, 'oauth', 'completed', '授权页已在服务器浏览器的新标签中打开')
    setWorkflowStep(workflow, 'verify', 'waiting', '请在需要时接管浏览器，完成外部验证；系统会自动识别回调地址')
    workflow.status = 'manual_required'
  } catch (error) {
    const message = redactWorkflowError(error)
    workflow.status = 'failed'
    workflow.error = message
    const active = workflow.steps.find((step) => step.status === 'running')
    if (active) setWorkflowStep(workflow, active.key, 'failed', message)
    if (activeWorkflowID === workflow.id) activeWorkflowID = ''
  }
}

async function startWorkflow(payload) {
  const seatEmail = validateWorkflowEmail(payload?.seat_email, '成员邮箱')
  const inviteEmail = validateWorkflowEmail(payload?.invite_email, '临时邮箱')
  if (seatEmail === inviteEmail) throw new Error('临时邮箱不能与待移除成员相同')
  if (payload?.confirmed !== true) throw new Error('需要确认移除成员和发送邀请后才能开始')
  const authURL = validateOpenAIAuthURL(payload?.auth_url)
  if (activeWorkflow()) throw new Error('已有 Team 子号工作流正在进行，请先完成或取消当前工作流')

  const workflow = createWorkflow(seatEmail, inviteEmail, authURL)
  workflows.set(workflow.id, workflow)
  activeWorkflowID = workflow.id
  // Return immediately so the UI can show the operation timeline while the
  // shared Chromium service serially performs the destructive actions.
  void runExclusive(() => executeWorkflow(workflow))
  return workflowSummary(workflow)
}

async function workflowStatus(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status === 'manual_required' && workflow.oauthPage && !workflow.oauthPage.isClosed()) {
    const currentURL = workflow.oauthPage.url()
    if (isOAuthCallbackURL(currentURL)) {
      workflow.callbackURL = currentURL
      workflow.status = 'callback_ready'
      setWorkflowStep(workflow, 'verify', 'completed', '已识别授权回调地址')
      if (activeWorkflowID === workflow.id) activeWorkflowID = ''
    } else {
      setWorkflowStep(workflow, 'verify', 'waiting', await describeOAuthNextStep(workflow.oauthPage))
    }
  } else if (workflow.status === 'manual_required') {
    setWorkflowStep(workflow, 'verify', 'waiting', await describeOAuthNextStep(workflow.oauthPage))
  }
  return workflowSummary(workflow)
}

function cancelWorkflow(id) {
  pruneWorkflows()
  const workflow = workflows.get(String(id || '').trim())
  if (!workflow) throw new Error('工作流不存在或已过期')
  if (workflow.status === 'running') throw new Error('当前步骤正在执行，暂不能取消')
  if (workflow.status === 'manual_required') {
    workflow.status = 'cancelled'
    setWorkflowStep(workflow, 'verify', 'cancelled', '已停止自动检查，成员移除和邀请操作不会自动回滚')
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
      if (req.method === 'POST' && path === '/members/refresh') return listMembers()
      if (req.method === 'POST' && path === '/members/inspect') {
        const result = await listMembers()
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

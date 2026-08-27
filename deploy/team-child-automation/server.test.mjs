import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

process.env.NODE_ENV = 'test'
process.env.TEAM_CHILD_AUTOMATION_TOKEN = 'unit-test-team-child-token'

const {
  activateBrowserPage,
  callbackURLFromNavigationEntries,
  cancelWorkflowState,
  confirmOfficialMemberRemoval,
  completeReauthorizationOnlyNodes,
  completeWorkflowNode,
  createReauthorizationWorkflow,
  createWorkflow,
  decryptWorkflowState,
  encryptWorkflowState,
  fillVerificationCode,
  generateWorkflowPassword,
  markWorkflowInviteSubmitted,
  pauseWorkflowState,
  pendingInviteEmailsFromTexts,
  recoverOpenAIPhoneEntry,
  reusableOAuthPage,
  resumePausedWorkflow,
  setWorkflowNode,
  submitInviteDialog,
  validateOAuthSessionID,
  validateWorkflowCode,
  workflowProtocolVersion,
  workflowNodeDefinitions,
  workflowFailureNodeKey,
  workflowSummary
} = await import('./server.mjs')

const authURL = 'https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=test-state'

describe('Team child OAuth automation state', () => {
  it('activates managed pages without requiring the noVNC viewer', async () => {
    const sent = []
    let broughtToFront = 0
    let detached = 0
    const cdpSession = {
      send: async (method, params) => { sent.push([method, params]) },
      detach: async () => { detached += 1 }
    }
    const page = {
      bringToFront: async () => { broughtToFront += 1 },
      context: () => ({ newCDPSession: async () => cdpSession })
    }

    await activateBrowserPage(page)

    assert.equal(broughtToFront, 1)
    assert.deepEqual(sent, [
      ['Page.bringToFront', undefined],
      ['Emulation.setFocusEmulationEnabled', { enabled: true }]
    ])
    assert.equal(detached, 1)
  })

  it('publishes only the current 22-node workflow protocol', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    const summary = workflowSummary(workflow)
    assert.equal(workflowProtocolVersion, 2)
    assert.equal(summary.schema_version, 2)
    assert.equal(summary.nodes.length, 22)
    assert.equal('startStep' in workflow, false)
    assert.equal('runOnlyStep' in workflow, false)
    assert.equal('resumeNextStepIndex' in workflow, false)
  })

  it('extracts invitations only from supplied pending-record text', () => {
    assert.deepEqual([...pendingInviteEmailsFromTexts(['No results'])], [])
    assert.deepEqual(
      [...pendingInviteEmailsFromTexts(['child@example.test\nInvited today\nMember'])],
      ['child@example.test']
    )
  })

  it('never selects the ChatGPT Members tab as the OAuth workflow tab', () => {
    const page = (url) => ({ url: () => url, isClosed: () => false })
    const members = page('https://chatgpt.com/admin/members')
    const oauth = page('https://auth.openai.com/oauth/authorize?state=test-state')

    assert.equal(reusableOAuthPage({ pages: () => [members] }), undefined)
    assert.equal(reusableOAuthPage({ pages: () => [members, oauth] }), oauth)
  })

  it('fills the native invite Email input and clicks Send invites', async () => {
    const observed = { email: '', clicked: 0 }
    const input = {
      isVisible: async () => true,
      getAttribute: async (name) => name === 'type' ? 'email' : null,
      fill: async (value) => { observed.email = value }
    }
    const button = {
      isVisible: async () => true,
      isDisabled: async () => false,
      innerText: async () => 'Send invites',
      click: async () => { observed.clicked += 1 }
    }
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const scope = {
      locator: (selector) => collection(selector === 'input' ? [input] : []),
      getByRole: (role, options) => collection(role === 'button' && options.name.test('Send invites') ? [button] : [])
    }

    await submitInviteDialog(scope, 'child@example.test')
    assert.equal(observed.email, 'child@example.test')
    assert.equal(observed.clicked, 1)
  })

  it('clicks the official Remove from workspace confirmation only inside its dialog', async () => {
    const clicked = []
    const button = (label) => ({
      isVisible: async () => true,
      isDisabled: async () => false,
      click: async () => { clicked.push(label) }
    })
    const removeMember = button('Remove member')
    const removeFromWorkspace = button('Remove from workspace')
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const dialog = {
      getByRole: (role, options) => collection(
        role === 'button'
          ? ['Remove member', 'Remove from workspace'].filter((label) => options.name.test(label)).map((label) => label === 'Remove member' ? removeMember : removeFromWorkspace)
          : []
      ),
      getByText: () => collection([])
    }
    const page = {
      locator: (selector) => {
        assert.equal(selector, '[role="dialog"]:visible, [role="alertdialog"]:visible')
        return collection([dialog])
      }
    }

    await confirmOfficialMemberRemoval(page)

    assert.deepEqual(clicked, ['Remove from workspace'])
  })

  it('records Send invites exactly once for workflow continuations', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    assert.equal(workflow.inviteSubmitted, false)
    assert.equal(markWorkflowInviteSubmitted(workflow), true)
    const submittedAt = workflow.inviteSubmittedAt
    assert.equal(workflow.inviteSubmitted, true)
    assert.ok(submittedAt > 0)
    assert.equal(markWorkflowInviteSubmitted(workflow), false)
    assert.equal(workflow.inviteSubmittedAt, submittedAt)
  })

  it('keeps existing-account reauthorization out of phone and profile registration', () => {
    const workflow = createReauthorizationWorkflow(
      317,
      'child@example.test',
      'SavedPassword!',
      authURL,
      'oauth-session-abcdefghijklmnop'
    )
    completeReauthorizationOnlyNodes(workflow)

    assert.equal(workflow.mode, 'reauthorization')
    assert.equal(workflow.targetAccountID, 317)
    for (const key of ['members', 'remove', 'invite', 'invite_confirm', 'phone', 'sms_confirm', 'phone_submit', 'sms_poll', 'sms_code', 'profile_wait', 'profile']) {
      assert.equal(workflow.nodes.find((node) => node.key === key)?.status, 'completed')
    }
    assert.equal(workflow.nodes.find((node) => node.key === 'workspace')?.status, 'pending')
  })

  it('prefers Send invites when an older Continue action is also present', async () => {
    const clicked = []
    const input = {
      isVisible: async () => true,
      getAttribute: async (name) => name === 'type' ? 'email' : null,
      fill: async () => undefined
    }
    const button = (label) => ({
      isVisible: async () => true,
      isDisabled: async () => false,
      innerText: async () => label,
      click: async () => { clicked.push(label) }
    })
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const scope = {
      locator: (selector) => collection(selector === 'input' ? [input] : []),
      getByRole: (role, options) => {
        if (role !== 'button') return collection([])
        const items = ['Continue', 'Send invites'].filter((label) => options.name.test(label)).map(button)
        return collection(items)
      }
    }

    await submitInviteDialog(scope, 'child@example.test')
    assert.deepEqual(clicked, ['Send invites'])
  })

  it('fills an OAuth verification code against the current browser page', async () => {
    const observed = { code: '', clicked: 0 }
    const input = {
      isVisible: async () => true,
      fill: async (value) => { observed.code = value }
    }
    const button = {
      isVisible: async () => true,
      click: async () => { observed.clicked += 1 }
    }
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const page = {
      locator: (selector) => collection(selector.includes('one-time-code') ? [input] : []),
      getByRole: (role, options) => collection(role === 'button' && options.name.test('Continue') ? [button] : [])
    }

    await fillVerificationCode(page, '123456')
    assert.equal(observed.code, '123456')
    assert.equal(observed.clicked, 1)
  })

  it('returns from an old SMS code page and reuses the saved password before replacing the phone', async () => {
    let state = 'sms_code'
    let password = ''
    let backClicks = 0
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const input = (type, autocomplete = '') => ({
      isVisible: async () => true,
      getAttribute: async (name) => ({ type, autocomplete }[name] || null),
      fill: async (value) => { if (type === 'password') password = value }
    })
    const page = {
      locator: (selector) => {
        if (selector === 'body') {
          return { innerText: async () => state === 'sms_code' ? 'Check your phone SMS code' : state === 'password' ? 'Enter your password' : 'Phone number' }
        }
        const currentInput = state === 'sms_code'
          ? input('text', 'one-time-code')
          : state === 'password' ? input('password') : input('tel')
        if (selector === 'input') return collection([currentInput])
        if (selector.includes('one-time-code')) return collection(state === 'sms_code' ? [currentInput] : [])
        return collection([])
      },
      getByRole: (role, options) => collection(
        role === 'button' && state === 'password' && options.name.test('Continue')
          ? [{ isVisible: async () => true, click: async () => { state = 'phone' } }]
          : []
      ),
      goBack: async () => { backClicks += 1; state = 'password' },
      goto: async () => undefined,
      url: () => 'https://auth.openai.com/phone-verification'
    }
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    workflow.generatedPassword = 'SavedPassword!'

    assert.equal(await recoverOpenAIPhoneEntry(page, workflow), 'phone')
    assert.equal(backClicks, 1)
    assert.equal(password, 'SavedPassword!')
  })

  it('restarts the XIASS official PKCE URL when OpenAI reports an invalid authorization step', async () => {
    let state = 'invalid_auth_step'
    let password = ''
    let backClicks = 0
    const visited = []
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const passwordInput = {
      isVisible: async () => true,
      getAttribute: async (name) => ({ type: 'password', autocomplete: 'current-password' }[name] || null),
      fill: async (value) => { password = value }
    }
    const phoneInput = {
      isVisible: async () => true,
      getAttribute: async (name) => ({ type: 'tel', autocomplete: 'tel' }[name] || null),
      fill: async () => undefined
    }
    const page = {
      locator: (selector) => {
        if (selector === 'body') {
          return { innerText: async () => state === 'invalid_auth_step'
            ? 'Invalid authorization step. error_code: invalid_auth_step'
            : state === 'password' ? 'Enter your password' : 'Phone number' }
        }
        if (selector === 'input') {
          if (state === 'password') return collection([passwordInput])
          if (state === 'phone') return collection([phoneInput])
        }
        return collection([])
      },
      getByRole: (role, options) => collection(
        role === 'button' && state === 'password' && options.name.test('Continue')
          ? [{ isVisible: async () => true, click: async () => { state = 'phone' } }]
          : []
      ),
      goBack: async () => { backClicks += 1 },
      goto: async (url) => { visited.push(url); state = 'password' },
      url: () => 'https://auth.openai.com/add-phone'
    }
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    workflow.generatedPassword = 'SavedPassword!'

    assert.equal(await recoverOpenAIPhoneEntry(page, workflow), 'phone')
    assert.deepEqual(visited, [authURL])
    assert.equal(backClicks, 0)
    assert.equal(password, 'SavedPassword!')
  })

  it('recovers the matching localhost callback from Chromium navigation history', () => {
    const matching = 'http://localhost:1455/auth/callback?code=secret-code&state=current-state'
    const result = callbackURLFromNavigationEntries([
      { url: 'https://auth.openai.com/sign-in-with-chatgpt/codex/consent' },
      { url: 'http://localhost:1455/auth/callback?code=old-code&state=old-state' },
      { url: matching }
    ], 'current-state')
    assert.equal(result, matching)
    assert.equal(callbackURLFromNavigationEntries([{ url: matching }], 'other-state'), '')
  })

  it('attributes a long-running action failure to its actual active node', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    completeWorkflowNode(workflow, 'sms_code', '短信验证码已提交')
    setWorkflowNode(workflow, 'callback', 'running', '正在捕获回调')
    assert.equal(workflowFailureNodeKey(workflow, 'sms_code'), 'callback')
  })

  it('cancels a running workflow and clears its short-lived login secrets', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    workflow.generatedPassword = 'Generated123!'
    workflow.loginPassword = 'SavedPassword!'
    setWorkflowNode(workflow, 'invite', 'running', '正在提交邀请')

    cancelWorkflowState(workflow)

    assert.equal(workflow.status, 'cancelled')
    assert.equal(workflow.cancelRequested, true)
    assert.equal(workflow.generatedPassword, '')
    assert.equal(workflow.loginPassword, '')
    assert.equal(workflow.nodes.find((node) => node.key === 'invite').status, 'cancelled')
  })

  it('persists a manual SMS pause and resumes without clearing workflow secrets', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    workflow.generatedPassword = 'SavedPassword!'
    completeWorkflowNode(workflow, 'phone_submit', '手机号已提交')
    setWorkflowNode(workflow, 'sms_poll', 'waiting', '等待短信')
    workflow.status = 'manual_required'

    pauseWorkflowState(workflow)
    assert.equal(workflow.status, 'paused')
    assert.equal(workflowSummary(workflow).pause_requested, true)
    assert.equal(workflow.generatedPassword, 'SavedPassword!')

    const resumed = resumePausedWorkflow(workflow)
    assert.equal(resumed.status, 'manual_required')
    assert.equal(resumed.pause_requested, false)
    assert.equal(workflow.generatedPassword, 'SavedPassword!')
  })

  it('marks a running browser node paused immediately and blocks its next node', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    setWorkflowNode(workflow, 'invite', 'running', '正在邀请')

    pauseWorkflowState(workflow)

    assert.equal(workflow.status, 'paused')
    assert.equal(workflow.pauseRequested, true)
    assert.throws(() => setWorkflowNode(workflow, 'invite_confirm', 'running', '正在确认'), /工作流已暂停/)
    assert.equal(workflow.currentNodeKey, 'invite_confirm')
  })

  it('keeps the complete node order stable', () => {
    assert.deepEqual(workflowNodeDefinitions.map(([key]) => key), [
      'members', 'remove', 'invite', 'invite_confirm', 'oauth', 'signup', 'email', 'password',
      'mail', 'mailbox', 'email_code', 'phone', 'sms_confirm', 'phone_submit',
      'sms_poll', 'sms_code', 'profile_wait', 'profile', 'workspace_wait',
      'workspace', 'callback', 'import'
    ])
  })

  it('generates a fresh 13-character mixed password without exposing it in summaries', () => {
    const observed = new Set()
    for (let index = 0; index < 64; index += 1) {
      const password = generateWorkflowPassword()
      assert.equal(password.length, 13)
      assert.match(password, /[A-Z]/)
      assert.match(password, /[a-z]/)
      assert.match(password, /[0-9]/)
      assert.match(password, /[!@#$%&*?]/)
      observed.add(password)
    }
    assert.ok(observed.size > 60)

    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    workflow.generatedPassword = generateWorkflowPassword()
    const encoded = JSON.stringify(workflowSummary(workflow))
    assert.equal(encoded.includes(workflow.generatedPassword), false)
    assert.equal(workflowSummary(workflow).password_available, true)
    assert.equal(workflowSummary(workflow).nodes.length, 22)
  })

  it('encrypts persisted workflow passwords with authenticated ciphertext', () => {
    const password = generateWorkflowPassword()
    const plaintext = JSON.stringify({ schema_version: 2, workflows: [{ generatedPassword: password }] })
    const encrypted = encryptWorkflowState(plaintext)
    assert.equal(encrypted.includes(password), false)
    assert.equal(decryptWorkflowState(encrypted), plaintext)

    const tampered = JSON.parse(encrypted)
    tampered.ciphertext = `${tampered.ciphertext.slice(0, -1)}${tampered.ciphertext.endsWith('A') ? 'B' : 'A'}`
    assert.throws(() => decryptWorkflowState(JSON.stringify(tampered)))
  })

  it('reports only the current node and never completes the SMS gate implicitly', () => {
    const workflow = createWorkflow('member@example.test', 'child@example.test', authURL, 'oauth-session-abcdefghijklmnop', false)
    completeWorkflowNode(workflow, 'phone', '手机号页面已出现')
    setWorkflowNode(workflow, 'sms_confirm', 'waiting', '等待站内确认')
    workflow.status = 'manual_required'
    const summary = workflowSummary(workflow)
    assert.equal(summary.current_node, 'sms_confirm')
    assert.equal(summary.nodes.find((node) => node.key === 'sms_confirm').status, 'waiting')
    assert.equal(summary.nodes.find((node) => node.key === 'phone_submit').status, 'pending')
  })

  it('validates OAuth session and code inputs without retaining them', () => {
    assert.equal(validateOAuthSessionID('oauth-session-abcdefghijklmnop'), 'oauth-session-abcdefghijklmnop')
    assert.equal(validateWorkflowCode(' 123456 '), '123456')
    assert.throws(() => validateOAuthSessionID('short'))
    assert.throws(() => validateWorkflowCode('12'))
    assert.throws(() => validateWorkflowCode('secret-code'))
  })
})

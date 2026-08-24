import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

process.env.NODE_ENV = 'test'
process.env.TEAM_CHILD_AUTOMATION_TOKEN = 'unit-test-team-child-token'

const {
  callbackURLFromNavigationEntries,
  completeWorkflowNode,
  createWorkflow,
  decryptWorkflowState,
  encryptWorkflowState,
  fillVerificationCode,
  generateWorkflowPassword,
  pendingInviteEmailsFromTexts,
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

  it('fills the native invite Email input and clicks its Continue action', async () => {
    const observed = { email: '', clicked: 0 }
    const input = {
      isVisible: async () => true,
      getAttribute: async (name) => name === 'type' ? 'email' : null,
      fill: async (value) => { observed.email = value }
    }
    const button = {
      isVisible: async () => true,
      isDisabled: async () => false,
      innerText: async () => 'Continue',
      click: async () => { observed.clicked += 1 }
    }
    const collection = (items) => ({
      count: async () => items.length,
      nth: (index) => items[index]
    })
    const scope = {
      locator: (selector) => collection(selector === 'input' ? [input] : []),
      getByRole: (role, options) => collection(role === 'button' && options.name.test('Continue') ? [button] : [])
    }

    await submitInviteDialog(scope, 'child@example.test')
    assert.equal(observed.email, 'child@example.test')
    assert.equal(observed.clicked, 1)
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

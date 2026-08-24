import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

process.env.NODE_ENV = 'test'

const {
  completeWorkflowNode,
  createWorkflow,
  generateWorkflowPassword,
  setWorkflowNode,
  validateOAuthSessionID,
  validateWorkflowCode,
  workflowNodeDefinitions,
  workflowSummary
} = await import('./server.mjs')

const authURL = 'https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=test-state'

describe('Team child OAuth automation state', () => {
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
    assert.equal(workflowSummary(workflow).nodes.length, 22)
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

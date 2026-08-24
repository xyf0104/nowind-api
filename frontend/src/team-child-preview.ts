import { createApp, defineComponent, h, ref } from 'vue'
import { createPinia } from 'pinia'
import TeamChildOAuthWorkspace from '@/components/admin/account/TeamChildOAuthWorkspace.vue'
import { teamChildAPI, type TeamChildWorkflow, type TeamChildWorkflowNode, type TeamChildWorkflowNodeKey } from '@/api/admin/teamChild'
import { Icon } from '@/components/icons'
import i18n, { initI18n } from '@/i18n'
import './style.css'

const nodeDefinitions: Array<[TeamChildWorkflowNodeKey, string]> = [
  ['members', '读取成员席位'], ['remove', '移除已选成员'], ['invite', '提交成员邀请'],
  ['invite_confirm', '确认 Pending invites'], ['oauth', '打开 XIASS 官方 OAuth'], ['signup', '选择 Sign up'],
  ['email', '填入临时邮箱'], ['password', '创建 13 位随机密码'], ['mail', '提交并发送邮箱验证码'],
  ['mailbox', 'Cloudflare 读取验证邮件'], ['email_code', '自动填入邮箱验证码'], ['phone', '进入手机号页面'],
  ['sms_confirm', '确认领取手机号'], ['phone_submit', '填入号码并选择 Text message'], ['sms_poll', '轮询短信验证码'],
  ['sms_code', '自动填入短信验证码'], ['profile_wait', '等待资料页 5 秒'], ['profile', '填写 black / 26'],
  ['workspace_wait', '等待工作空间 10 秒'], ['workspace', '默认工作空间继续'], ['callback', '捕获 OAuth 回调'],
  ['import', '按勾选配置导入 XIASS']
]

const nodes: TeamChildWorkflowNode[] = nodeDefinitions.map(([key, label], index) => ({
  key,
  label,
  number: index + 1,
  status: index < 14 ? 'completed' : index === 14 ? 'waiting' : 'pending',
  ...(index === 14 ? { message: '正在通过 XIASS SMS 服务轮询验证码，最长等待 2 分钟。' } : {})
}))

const workflow: TeamChildWorkflow = {
  schema_version: 2,
  id: 'preview-workflow-abcdefghijklmnop',
  status: 'manual_required',
  expires_at: '2026-08-24T17:00:00.000Z',
  manual_required: true,
  current_node: 'sms_poll',
  password_available: true,
  nodes
}

const Preview = defineComponent({
  setup() {
    const mailboxConfigFileInput = ref<HTMLInputElement | null>(null)
    const configImporting = ref(false)
    const configMessage = ref('')

    async function handleMailboxConfigChange(event: Event) {
      const input = event.target as HTMLInputElement
      const file = input.files?.[0]
      input.value = ''
      if (!file) return

      configImporting.value = true
      configMessage.value = '正在导入邮箱配置...'
      try {
        const result = await teamChildAPI.importMailboxConfig(file)
        configMessage.value = result.configured ? '邮箱配置已就绪' : '邮箱配置未通过校验'
      } catch {
        configMessage.value = '导入失败，请检查配置文件'
      } finally {
        configImporting.value = false
      }
    }

    return () => h('main', { class: 'min-h-screen bg-dark-950 px-4 py-6 text-gray-100 sm:px-6 lg:px-8' }, [
      h('div', { class: 'mx-auto max-w-[1480px]' }, [
        h('header', { class: 'mb-5 flex min-w-0 flex-col gap-4 border-b border-dark-700 pb-5 lg:flex-row lg:items-end lg:justify-between' }, [
          h('div', { class: 'min-w-0' }, [
            h('p', { class: 'text-xs font-semibold uppercase text-primary-400' }, '管理员工作流'),
            h('h1', { class: 'mt-2 text-2xl font-semibold' }, 'Team 子号创建'),
            h('p', { class: 'mt-1 text-sm text-gray-400' }, '悬浮进度卡持续显示当前节点，实际操作集中在下方工作区。')
          ]),
          h('div', { class: 'flex min-w-0 flex-wrap items-center justify-end gap-2' }, [
            h('input', {
              ref: mailboxConfigFileInput,
              type: 'file',
              class: 'hidden',
              accept: '.json,.env,.txt,application/json,text/plain',
              onChange: handleMailboxConfigChange
            }),
            h('span', { class: 'max-w-full truncate text-xs text-gray-400', 'aria-live': 'polite' }, configMessage.value),
            h('button', {
              type: 'button',
              class: 'btn btn-secondary flex max-w-full items-center justify-center gap-2 whitespace-nowrap',
              disabled: configImporting.value,
              title: '导入 Cloudflare 邮箱配置',
              onClick: () => mailboxConfigFileInput.value?.click()
            }, [
              h(Icon, { name: configImporting.value ? 'refresh' : 'upload', size: 'sm', class: configImporting.value ? 'animate-spin' : '', 'stroke-width': 2 }),
              h('span', configImporting.value ? '正在导入...' : '导入邮箱配置')
            ])
          ])
        ]),
        h(TeamChildOAuthWorkspace, {
          workflow,
          mailboxEmail: 'team1004@example.test',
          revealedPassword: '',
          passwordLoading: false,
          showOneClick: true,
          historyMailboxes: ['team1001@example.test', 'team1003@example.test', 'team1004@example.test'],
          selectedMailboxEmail: 'team1004@example.test',
          mailboxCode: '847219',
          mailboxCodeLoading: false,
          mailboxConfigured: true,
          browserVisible: false,
          browserConfigured: true
        })
      ])
    ])
  }
})

async function mountPreview() {
  const app = createApp(Preview)
  app.use(createPinia())
  app.use(i18n)
  await initI18n()
  app.mount('#app')
}

void mountPreview()

import { createApp, defineComponent, h } from 'vue'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import TeamChildCreationView from '@/views/admin/TeamChildCreationView.vue'
import { teamChildAPI } from '@/api/admin/teamChild'
import { groupsAPI } from '@/api/admin/groups'
import { accountsAPI } from '@/api/admin/accounts'
import i18n, { initI18n } from '@/i18n'
import './style.css'

Object.assign(teamChildAPI, {
  getMailboxStatus: async () => ({ configured: true, browser_configured: true }),
  listMailboxes: async () => [
    'team1001@example.test',
    'team1003@example.test',
    'team1004@example.test'
  ],
  getActiveMailbox: async () => null,
  getActiveTeamChildWorkflow: async () => null,
  listTeamChildMembers: async () => ({
    ready: true,
    pending_invites: 0,
    workspace_name: 'XIASS Research',
    seat_email: 'researcher@example.test',
    members: [
      {
        id: 'owner@example.test',
        email: 'owner@example.test',
        name: 'Workspace Owner',
        role: 'owner',
        status: 'active',
        protected: true
      },
      {
        id: 'researcher@example.test',
        email: 'researcher@example.test',
        name: 'Research Member',
        role: 'member',
        status: 'active'
      }
    ]
  })
})

Object.assign(groupsAPI, {
  getAll: async () => [
    { id: 1, name: 'OpenAI Free', platform: 'openai', type: 'standard' },
    { id: 2, name: 'Team Research', platform: 'openai', type: 'standard' }
  ]
})

Object.assign(accountsAPI, {
  list: async () => ({
    items: [
      {
        id: 317,
        name: 'team1003@example.test',
        platform: 'openai',
        type: 'oauth',
        status: 'error',
        error_message: 'OpenAI upstream returned HTTP 401 Unauthorized',
        schedulable: false,
        proxy_id: null,
        concurrency: 10,
        priority: 1,
        group_ids: [1],
        credentials_status: { has_xiass_team_child_password_encrypted: true },
        extra: { xiass_team_child: true, xiass_team_child_email: 'team1003@example.test' }
      },
      {
        id: 318,
        name: 'team1004@example.test',
        platform: 'openai',
        type: 'oauth',
        status: 'active',
        error_message: '',
        schedulable: true,
        proxy_id: null,
        concurrency: 10,
        priority: 1,
        group_ids: [1, 2],
        credentials_status: { has_xiass_team_child_password_encrypted: true },
        extra: { xiass_team_child: true, xiass_team_child_email: 'team1004@example.test' }
      }
    ],
    total: 2
  }),
  getUsage: async (accountID: number) => accountID === 317
    ? { needs_reauth: true, error_code: 'unauthenticated', error: 'HTTP 401 Unauthorized' }
    : {
        five_hour: { utilization: 18 },
        seven_day: { utilization: 42, weekly_estimate_usd: 12.34 }
      }
})

const Preview = defineComponent({
  setup() {
    return () => h('main', {
      class: 'min-h-screen bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-gray-100'
    }, h(TeamChildCreationView))
  }
})

const previewRouter = createRouter({
  history: createMemoryHistory(),
  routes: [
    {
      path: '/',
      name: 'TeamChildPreview',
      component: defineComponent({ render: () => null })
    },
    {
      path: '/admin/accounts',
      name: 'AdminAccounts',
      component: defineComponent({ render: () => null })
    }
  ]
})

async function mountPreview() {
  const app = createApp(Preview)
  app.use(createPinia())
  app.use(previewRouter)
  app.use(i18n)
  await initI18n()
  await previewRouter.isReady()
  app.mount('#app')
}

void mountPreview()

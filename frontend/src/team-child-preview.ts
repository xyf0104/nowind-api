import { createApp, defineComponent, h } from 'vue'
import { createPinia } from 'pinia'
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
  list: async () => ({ items: [], total: 0 })
})

const Preview = defineComponent({
  setup() {
    return () => h('main', {
      class: 'min-h-screen bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-gray-100'
    }, h(TeamChildCreationView))
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

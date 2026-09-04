<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.title') }}</h1>
          <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.description') }}</p>
        </div>
        <button type="button" class="btn btn-secondary" :disabled="loading || saving || pairingSaving" :title="t('admin.executionNodes.refresh')" @click="refreshAll">
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          <span>{{ t('admin.executionNodes.refresh') }}</span>
        </button>
      </div>

      <div v-if="loading && !status" class="flex justify-center py-16">
        <span class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" />
      </div>

      <template v-else-if="status">
        <section class="card overflow-hidden">
          <div class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <div class="flex min-w-0 items-start gap-3">
              <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg" :class="status.balancing_enabled ? 'bg-emerald-100 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'">
                <Icon name="server" size="lg" />
              </div>
              <div class="min-w-0">
                <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.enabled') }}</h2>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.enabledHint') }}</p>
              </div>
            </div>
            <div class="flex items-center gap-3">
              <span class="text-sm font-medium" :class="status.balancing_enabled ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500 dark:text-gray-400'">
                {{ status.balancing_enabled ? t('admin.executionNodes.on') : t('admin.executionNodes.off') }}
              </span>
              <Toggle :model-value="draftEnabled" :disabled="saving" :aria-label="t('admin.executionNodes.enabled')" :title="t('admin.executionNodes.enabled')" data-testid="execution-node-balancing-toggle" @update:model-value="requestToggle" />
            </div>
          </div>
          <div class="grid gap-4 p-5 sm:grid-cols-2 lg:grid-cols-4 sm:px-6">
            <StatusTile :label="t('admin.executionNodes.localNode')" :value="status.runtime.node_id || t('admin.executionNodes.notConfigured')" :tone="status.runtime.node_id ? 'ok' : 'bad'" />
            <StatusTile :label="t('admin.executionNodes.runtimeEnabled')" :value="status.runtime.enabled ? t('admin.executionNodes.configured') : t('admin.executionNodes.notConfigured')" :tone="status.runtime.enabled ? 'ok' : 'bad'" />
            <StatusTile :label="t('admin.executionNodes.database')" :value="status.database_reachable ? t('admin.executionNodes.healthy') : t('admin.executionNodes.unavailable')" :tone="status.database_reachable ? 'ok' : 'bad'" />
            <StatusTile :label="t('admin.executionNodes.redis')" :value="status.heartbeat_store_reachable ? t('admin.executionNodes.healthy') : t('admin.executionNodes.unavailable')" :tone="status.heartbeat_store_reachable ? 'ok' : 'warn'" />
          </div>
        </section>

        <section class="card overflow-hidden">
          <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.runtimeTitle') }}</h2>
          </div>
          <div class="grid gap-x-8 gap-y-4 p-5 sm:grid-cols-2 lg:grid-cols-4 sm:px-6">
            <RuntimeValue :label="t('admin.executionNodes.localNode')" :value="status.runtime.node_id || '-'" />
            <RuntimeValue :label="t('admin.executionNodes.controlPlane')" :value="status.runtime.control_plane ? t('admin.executionNodes.yes') : t('admin.executionNodes.no')" />
            <RuntimeValue :label="t('admin.executionNodes.emergencyEgress')" :value="status.runtime.emergency_local_egress ? t('admin.executionNodes.yes') : t('admin.executionNodes.no')" />
            <RuntimeValue :label="t('admin.executionNodes.egress')" :value="status.runtime.default_proxy_id > 0 ? `#${status.runtime.default_proxy_id}` : t('admin.executionNodes.proxyMissing')" />
          </div>
        </section>

        <section class="card overflow-hidden" data-testid="execution-node-pairing">
          <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.pairingTitle') }}</h2>
              <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.pairingDescription') }}</p>
            </div>
            <div class="flex items-center gap-2 text-sm font-medium" :class="pairingStatus?.production_ready ? 'text-emerald-600 dark:text-emerald-400' : pairingStatus?.paired ? 'text-amber-600 dark:text-amber-400' : 'text-gray-500 dark:text-gray-400'">
              <span class="h-2.5 w-2.5 rounded-full" :class="pairingStatus?.production_ready ? 'bg-emerald-500' : pairingStatus?.paired ? 'bg-amber-500' : 'bg-gray-400'" />
              {{ pairingStatus?.production_ready ? t('admin.executionNodes.pairingReady') : pairingStatus?.paired ? t('admin.executionNodes.pairingIncomplete') : t('admin.executionNodes.pairingNotPaired') }}
            </div>
          </div>
          <div class="grid gap-5 p-5 lg:grid-cols-2 sm:px-6">
            <div class="space-y-3">
              <div class="flex items-center justify-between gap-3">
                <div>
                  <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.inviteTitle') }}</h3>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.inviteHint') }}</p>
                </div>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="pairingSaving" data-testid="execution-node-generate-invite" @click="generateInvite">
                  <Icon name="key" size="sm" />
                  <span>{{ pairingInvite ? t('admin.executionNodes.regenerateInvite') : t('admin.executionNodes.generateInvite') }}</span>
                </button>
              </div>
              <div class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/20 dark:text-amber-300">
                {{ t('admin.executionNodes.inviteAuthorityNotice') }}
              </div>
              <div v-if="pairingInvite" class="rounded-lg border border-amber-200 bg-amber-50 p-3 dark:border-amber-900/60 dark:bg-amber-950/20">
                <div class="flex gap-2">
                  <input :value="pairingInvite.token" readonly class="input min-w-0 flex-1 bg-white font-mono text-xs dark:bg-dark-800" data-testid="execution-node-invite-token" />
                  <button type="button" class="btn btn-secondary btn-sm shrink-0" :title="t('admin.executionNodes.copyInvite')" @click="copyInvite">
                    <Icon name="copy" size="sm" />
                    <span>{{ t('admin.executionNodes.copyInvite') }}</span>
                  </button>
                </div>
                <p class="mt-2 text-xs text-amber-800 dark:text-amber-300">{{ t('admin.executionNodes.inviteExpiry', { time: formatPairingTime(pairingInvite.expires_at) }) }}</p>
              </div>
              <p v-else class="rounded-lg border border-dashed border-gray-200 px-3 py-4 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">{{ t('admin.executionNodes.noInvite') }}</p>
            </div>
            <div class="space-y-3">
              <div>
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.joinTitle') }}</h3>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.joinHint') }}</p>
              </div>
              <div class="rounded-lg border border-sky-200 bg-sky-50 px-3 py-2 text-xs leading-5 text-sky-800 dark:border-sky-900/60 dark:bg-sky-950/20 dark:text-sky-300">
                {{ t('admin.executionNodes.joinAuthorityNotice') }}
              </div>
              <label class="block" for="execution-node-peer-url">
                <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.executionNodes.peerURL') }}</span>
                <input id="execution-node-peer-url" v-model.trim="pairingPeerURL" class="input mt-1 w-full" type="url" autocomplete="url" placeholder="https://api2.example.com" data-testid="execution-node-peer-url" />
              </label>
              <label class="block" for="execution-node-peer-token">
                <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.executionNodes.inviteToken') }}</span>
                <input id="execution-node-peer-token" v-model.trim="pairingToken" class="input mt-1 w-full font-mono text-xs" type="password" autocomplete="one-time-code" data-testid="execution-node-peer-token" />
              </label>
              <div class="flex flex-wrap gap-2">
                <button type="button" class="btn btn-primary btn-sm" :disabled="pairingSaving || !pairingPeerURL || !pairingToken" data-testid="execution-node-pair" @click="pairNode">
                  <Icon :name="pairingSaving ? 'refresh' : 'link'" size="sm" :class="pairingSaving ? 'animate-spin' : ''" />
                  <span>{{ pairingSaving ? t('admin.executionNodes.pairing') : t('admin.executionNodes.pair') }}</span>
                </button>
                <button v-if="pairingStatus?.paired" type="button" class="btn btn-secondary btn-sm text-red-600 dark:text-red-400" :disabled="pairingSaving" data-testid="execution-node-unpair" @click="showUnpairConfirm = true">
                  <Icon name="x" size="sm" />
                  <span>{{ t('admin.executionNodes.unpair') }}</span>
                </button>
              </div>
            </div>
          </div>
          <div v-if="pairingStatus?.paired" class="border-t border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <div class="grid gap-2 sm:grid-cols-2 lg:grid-cols-5">
              <PairingCheck :label="t('admin.executionNodes.protocolCheck')" :ok="pairingStatus.protocol_compatible" />
              <PairingCheck :label="t('admin.executionNodes.databaseCheck')" :ok="pairingStatus.database_shared" />
              <PairingCheck :label="t('admin.executionNodes.redisCheck')" :ok="pairingStatus.redis_shared" />
              <PairingCheck :label="t('admin.executionNodes.authCheck')" :ok="pairingStatus.auth_compatible" />
              <PairingCheck :label="t('admin.executionNodes.productionCheck')" :ok="pairingStatus.production_ready" />
            </div>
            <p class="mt-3 text-xs leading-5" :class="pairingStatus.production_ready ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-300'">
              {{ pairingStatus.production_ready ? t('admin.executionNodes.pairingReadyHint') : pairingStatus.state_error || t('admin.executionNodes.pairingMismatchHint') }}
            </p>
          </div>
        </section>

        <section class="card overflow-hidden">
          <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.nodesTitle') }}</h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.weightHint') }}</p>
              <p class="mt-1 max-w-3xl text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.egressHint') }}</p>
            </div>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="draftNodes.length >= 32 || saving || mappingLocked" @click="addNode">
              <Icon name="plus" size="sm" />
              <span>{{ t('admin.executionNodes.addNode') }}</span>
            </button>
          </div>
          <div class="space-y-3 p-5 sm:px-6">
            <div v-for="(node, index) in draftNodes" :key="node.key" class="rounded-lg border border-gray-200 p-4 dark:border-dark-600" :data-testid="`execution-node-row-${index}`">
              <div class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_9rem_minmax(0,1.2fr)_auto] lg:items-end">
                <label class="block min-w-0" :for="`execution-node-id-${index}`">
                  <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.executionNodes.node') }}</span>
                  <input :id="`execution-node-id-${index}`" v-model.trim="node.nodeID" class="input mt-1 w-full" maxlength="64" type="text" :disabled="saving || mappingLocked" :data-testid="`execution-node-id-${index}`" />
                </label>
                <label class="block min-w-0" :for="`execution-node-weight-${index}`">
                  <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.executionNodes.weight') }}</span>
                  <input :id="`execution-node-weight-${index}`" v-model.number="node.weight" class="input mt-1 w-full" min="0" max="1000000" step="0.1" inputmode="decimal" type="number" :disabled="saving" :data-testid="`execution-node-weight-${index}`" />
                </label>
                <label class="block min-w-0" :for="`execution-node-proxy-${index}`">
                  <span class="text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.executionNodes.egress') }}</span>
                  <select :id="`execution-node-proxy-${index}`" v-model.number="node.proxyID" class="input mt-1 w-full" :disabled="saving || mappingLocked" :data-testid="`execution-node-proxy-${index}`">
                    <option :value="0">{{ t('admin.executionNodes.proxyMissing') }}</option>
                    <option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">#{{ proxy.id }} · {{ proxy.name }}</option>
                  </select>
                </label>
                <button v-if="draftNodes.length > 1" type="button" class="btn btn-secondary h-10 px-3 text-red-600 dark:text-red-400" :title="t('admin.executionNodes.removeNode')" :disabled="saving || mappingLocked" @click="removeNode(index)">
                  <Icon name="trash" size="sm" />
                </button>
              </div>
              <div v-if="nodeStatus(node.nodeID)" class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
                <span class="inline-flex items-center gap-1.5" :class="nodeStatus(node.nodeID)!.online ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500 dark:text-gray-400'">
                  <span class="h-2 w-2 rounded-full" :class="nodeStatus(node.nodeID)!.online ? 'bg-emerald-500' : 'bg-gray-400'" />
                  {{ nodeStatus(node.nodeID)!.online ? t('admin.executionNodes.online') : t('admin.executionNodes.offline') }}
                  <span v-if="nodeStatus(node.nodeID)!.is_local">· {{ t('admin.executionNodes.local') }}</span>
                </span>
                <span class="text-gray-500 dark:text-gray-400">{{ t('admin.executionNodes.accounts') }} {{ nodeStatus(node.nodeID)!.account_stats.total }} / {{ t('admin.executionNodes.activeAccounts') }} {{ nodeStatus(node.nodeID)!.account_stats.active }} / {{ t('admin.executionNodes.schedulableAccounts') }} {{ nodeStatus(node.nodeID)!.account_stats.schedulable }}</span>
                <span class="text-gray-500 dark:text-gray-400">{{ nodeStatus(node.nodeID)!.proxy_valid ? (nodeStatus(node.nodeID)!.proxy_name || `#${nodeStatus(node.nodeID)!.proxy_id}`) : t('admin.executionNodes.proxyInvalid') }}</span>
              </div>
            </div>
          </div>
        </section>

        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="text-sm" :class="status.can_enable ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
            {{ status.can_enable ? t('admin.executionNodes.canEnable') : t('admin.executionNodes.cannotEnable') }}
          </div>
          <button type="button" class="btn btn-primary" :disabled="saving || !hasValidDraft" data-testid="execution-node-save" @click="save">
            <Icon :name="saving ? 'refresh' : 'check'" size="sm" :class="saving ? 'animate-spin' : ''" />
            <span>{{ saving ? t('admin.executionNodes.saving') : t('admin.executionNodes.save') }}</span>
          </button>
        </div>

        <section class="rounded-lg border border-sky-200 bg-sky-50 p-5 dark:border-sky-900/60 dark:bg-sky-950/20">
          <h2 class="text-sm font-semibold text-sky-900 dark:text-sky-200">{{ t('admin.executionNodes.ingressTitle') }}</h2>
          <p class="mt-1 text-sm leading-6 text-sky-800 dark:text-sky-300">{{ t('admin.executionNodes.ingressDescription') }}</p>
        </section>

        <section class="card overflow-hidden">
          <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.executionNodes.issueTitle') }}</h2>
          </div>
          <div v-if="status.issues.length" class="divide-y divide-gray-100 dark:divide-dark-700">
            <div v-for="issue in status.issues" :key="`${issue.code}-${issue.message}`" class="flex gap-3 px-5 py-3 text-sm sm:px-6">
              <Icon :name="issue.severity === 'error' ? 'exclamationTriangle' : 'infoCircle'" size="sm" :class="issue.severity === 'error' ? 'text-red-500' : 'text-amber-500'" />
              <div class="min-w-0"><span class="font-medium text-gray-800 dark:text-gray-200">{{ issueText(issue) }}</span><span class="ml-2 font-mono text-xs text-gray-400 dark:text-gray-500">{{ issue.code }}</span></div>
            </div>
          </div>
          <p v-else class="px-5 py-4 text-sm text-gray-500 dark:text-gray-400 sm:px-6">{{ t('admin.executionNodes.noIssues') }}</p>
        </section>
      </template>
    </div>

    <ConfirmDialog
      :show="showEnableConfirm"
      :title="t('admin.executionNodes.confirmEnableTitle')"
      :message="t('admin.executionNodes.confirmEnableMessage')"
      :confirm-text="t('admin.executionNodes.confirmEnable')"
      :cancel-text="t('common.cancel')"
      @confirm="confirmEnable"
      @cancel="showEnableConfirm = false"
    />
    <ConfirmDialog
      :show="showUnpairConfirm"
      :title="t('admin.executionNodes.confirmUnpairTitle')"
      :message="t('admin.executionNodes.confirmUnpairMessage')"
      :confirm-text="t('admin.executionNodes.confirmUnpair')"
      :cancel-text="t('common.cancel')"
      @confirm="unpairNode"
      @cancel="showUnpairConfirm = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { ExecutionNodeAdminNode, ExecutionNodeAdminStatus, ExecutionNodePairingInvite, ExecutionNodePairingStatus } from '@/api/admin/executionNodes'
import type { Proxy } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()
const status = ref<ExecutionNodeAdminStatus | null>(null)
const pairingStatus = ref<ExecutionNodePairingStatus | null>(null)
const pairingInvite = ref<ExecutionNodePairingInvite | null>(null)
const pairingPeerURL = ref('')
const pairingToken = ref('')
const proxies = ref<Proxy[]>([])
const loading = ref(false)
const saving = ref(false)
const pairingSaving = ref(false)
const showEnableConfirm = ref(false)
const showUnpairConfirm = ref(false)
const draftEnabled = ref(false)
let nodeSequence = 0

interface NodeDraft { key: number; nodeID: string; weight: number; proxyID: number }
const draftNodes = ref<NodeDraft[]>([])
const mappingLocked = computed(() => Boolean(status.value?.balancing_enabled))

const StatusTile = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true }, tone: { type: String, default: 'ok' } },
  setup(props) {
    return () => h('div', { class: 'rounded-lg border border-gray-200 p-4 dark:border-dark-600' }, [
      h('div', { class: 'text-xs text-gray-500 dark:text-gray-400' }, props.label),
      h('div', { class: ['mt-1 text-sm font-semibold', props.tone === 'ok' ? 'text-emerald-600 dark:text-emerald-400' : props.tone === 'warn' ? 'text-amber-600 dark:text-amber-400' : 'text-red-600 dark:text-red-400'] }, props.value)
    ])
  }
})

const RuntimeValue = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true } },
  setup(props) {
    return () => h('div', [h('div', { class: 'text-xs text-gray-500 dark:text-gray-400' }, props.label), h('div', { class: 'mt-1 text-sm font-medium text-gray-900 dark:text-white' }, props.value)])
  }
})

const PairingCheck = defineComponent({
  props: { label: { type: String, required: true }, ok: { type: Boolean, required: true } },
  setup(props) {
    return () => h('div', { class: 'rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-600' }, [
      h('div', { class: 'text-xs text-gray-500 dark:text-gray-400' }, props.label),
      h('div', { class: ['mt-1 text-sm font-semibold', props.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'] }, props.ok ? t('admin.executionNodes.passed') : t('admin.executionNodes.failed'))
    ])
  }
})

const hasValidDraft = computed(() => {
  const ids = new Set<string>()
  const proxyIDs = new Set<number>()
  let positive = false
  for (const node of draftNodes.value) {
    const id = node.nodeID.trim()
    const weight = Number(node.weight)
    if (!/^[A-Za-z0-9._-]{1,64}$/.test(id) || ids.has(id) || !Number.isFinite(weight) || weight < 0 || weight > 1_000_000) return false
    ids.add(id)
    positive ||= weight > 0
    if (draftEnabled.value && (!Number.isInteger(node.proxyID) || node.proxyID <= 0 || proxyIDs.has(node.proxyID))) return false
    if (node.proxyID > 0) proxyIDs.add(node.proxyID)
  }
  return ids.size > 0 && positive
})

function nodeStatus(nodeID: string): ExecutionNodeAdminNode | undefined {
  return status.value?.nodes.find((node) => node.node_id === nodeID.trim())
}

function issueText(issue: ExecutionNodeAdminStatus['issues'][number]): string {
  const key = `admin.executionNodes.issues.${issue.code}`
  const translated = t(key)
  return translated === key ? issue.message : translated
}

function syncDraft(next: ExecutionNodeAdminStatus): void {
  draftEnabled.value = next.balancing_enabled
  draftNodes.value = next.nodes.map((node) => ({ key: ++nodeSequence, nodeID: node.node_id, weight: node.weight, proxyID: node.proxy_id || 0 }))
  if (!draftNodes.value.length) draftNodes.value = [{ key: ++nodeSequence, nodeID: 'api', weight: 1, proxyID: 0 }]
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [nextStatus, nextProxies] = await Promise.all([adminAPI.executionNodes.getStatus(), adminAPI.proxies.getAllWithCount()])
    status.value = nextStatus
    proxies.value = nextProxies
    syncDraft(nextStatus)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.executionNodes.reloadFailed')))
  } finally {
    loading.value = false
  }
}

async function loadPairing(): Promise<void> {
  try {
    pairingStatus.value = await adminAPI.executionNodes.getPairingStatus()
  } catch (error) {
    pairingStatus.value = null
    appStore.showError(extractApiErrorMessage(error, t('admin.executionNodes.pairingLoadFailed')))
  }
}

async function refreshAll(): Promise<void> {
  await Promise.all([load(), loadPairing()])
}

async function generateInvite(): Promise<void> {
  pairingSaving.value = true
  try {
    pairingInvite.value = await adminAPI.executionNodes.generatePairingInvite()
    appStore.showSuccess(t('admin.executionNodes.inviteGenerated'))
    await loadPairing()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.executionNodes.pairingFailed')))
  } finally {
    pairingSaving.value = false
  }
}

async function copyInvite(): Promise<void> {
  if (!pairingInvite.value) return
  try {
    await navigator.clipboard.writeText(pairingInvite.value.token)
    appStore.showSuccess(t('admin.executionNodes.inviteCopied'))
  } catch {
    appStore.showError(t('admin.executionNodes.inviteCopyFailed'))
  }
}

async function pairNode(): Promise<void> {
  pairingSaving.value = true
  try {
    pairingStatus.value = await adminAPI.executionNodes.pairExecutionNode(pairingPeerURL.value, pairingToken.value)
    pairingToken.value = ''
    appStore.showSuccess(t('admin.executionNodes.pairSuccess'))
    await load()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.executionNodes.pairingFailed')))
  } finally {
    pairingSaving.value = false
  }
}

async function unpairNode(): Promise<void> {
  showUnpairConfirm.value = false
  pairingSaving.value = true
  try {
    await adminAPI.executionNodes.unpairExecutionNode()
    pairingStatus.value = await adminAPI.executionNodes.getPairingStatus()
    appStore.showSuccess(t('admin.executionNodes.unpairSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.executionNodes.pairingFailed')))
  } finally {
    pairingSaving.value = false
  }
}

function formatPairingTime(value: string): string {
  return new Date(value).toLocaleString()
}

function addNode(): void {
  const used = new Set(draftNodes.value.map((node) => node.nodeID))
  let suffix = draftNodes.value.length + 1
  let nodeID = `node${suffix}`
  while (used.has(nodeID)) nodeID = `node${++suffix}`
  draftNodes.value.push({ key: ++nodeSequence, nodeID, weight: 1, proxyID: 0 })
}

function removeNode(index: number): void {
  if (draftNodes.value.length > 1) draftNodes.value.splice(index, 1)
}

function requestToggle(next: boolean): void {
  if (next) {
    if (!status.value?.can_enable) {
      appStore.showError(status.value?.issues.filter((issue) => issue.severity === 'error').map((issue) => issue.message).join('；') || t('admin.executionNodes.cannotEnable'))
      return
    }
    showEnableConfirm.value = true
    return
  }
  draftEnabled.value = false
}

function confirmEnable(): void {
  showEnableConfirm.value = false
  draftEnabled.value = true
}

async function save(): Promise<void> {
  if (!hasValidDraft.value) {
    appStore.showError(draftEnabled.value ? t('admin.executionNodes.invalidProxy') : t('admin.executionNodes.invalidWeights'))
    return
  }
  const weights: Record<string, number> = {}
  const proxyIDs: Record<string, number> = {}
  for (const node of draftNodes.value) {
    const nodeID = node.nodeID.trim()
    weights[nodeID] = Number(node.weight)
    if (node.proxyID > 0) proxyIDs[nodeID] = Math.floor(node.proxyID)
  }
  saving.value = true
  try {
    await adminAPI.settings.updateSettings({ execution_node_balancing_enabled: draftEnabled.value, execution_node_weights: weights, execution_node_proxy_ids: proxyIDs })
    appStore.showSuccess(t('admin.executionNodes.saveSuccess'))
    await load()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.executionNodes.saveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(() => { void refreshAll() })
</script>

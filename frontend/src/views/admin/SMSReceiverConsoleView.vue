<template>
  <main class="sms-console" :class="{ 'is-preview': isLocalPreview }">
    <DarkVideoBackground blurred force-theme="dark" />
    <div class="sms-console__body">
      <div v-if="isLocalPreview" class="sms-preview-banner" role="status">
        <Icon name="beaker" size="sm" />
        <span>本地设计预览：展示数据不会领取号码、保存卡密或影响服务器队列。</span>
      </div>

      <section class="sms-intro" aria-labelledby="sms-page-title">
        <div class="sms-intro__content">
          <p class="sms-eyebrow">XIASS API OPERATIONS</p>
          <h1 id="sms-page-title">Codex 授权接码工作台</h1>
          <p class="sms-intro__summary">领取临时号码，实时接收 OAuth 验证码。</p>
          <p class="sms-intro__purpose"><Icon name="shield" size="xs" />仅限 Codex 登录接码使用</p>
          <div v-if="authStore.isAuthenticated" class="sms-workbench-actions" aria-label="接码工作台操作">
            <span class="sms-connection" :class="connectionClass">
              <span class="sms-connection__dot" aria-hidden="true"></span>
              {{ connectionLabel }}
            </span>
            <button
              type="button"
              class="sms-header-button"
              title="刷新接码服务状态"
              :disabled="isLoadingStatus"
              @click="loadStatus"
            >
              <Icon name="refresh" size="sm" :class="{ 'animate-spin': isLoadingStatus }" />
              <span>刷新状态</span>
            </button>
          </div>
        </div>
        <div class="sms-intro-auth" aria-label="账户入口">
          <template v-if="authStore.isAuthenticated">
            <button
              type="button"
              class="sms-header-button sms-header-button--quiet"
              title="退出当前账户"
              @click="logout"
            >
              <Icon name="login" size="sm" />
              <span>退出登录</span>
            </button>
          </template>
          <template v-else>
            <button type="button" class="sms-header-button sms-header-button--quiet" @click="goToLogin">
              <Icon name="login" size="sm" />
              <span>登录</span>
            </button>
            <button type="button" class="sms-header-button sms-header-button--primary" @click="goToRegister">
              <Icon name="userPlus" size="sm" />
              <span>注册</span>
            </button>
          </template>
        </div>
      </section>

      <section class="sms-workspace" aria-label="授权接码操作区">
        <article class="sms-panel sms-panel--receiver">
          <div class="sms-panel__head">
            <div class="sms-panel__title-group">
              <span class="sms-panel__icon sms-panel__icon--primary"><Icon name="chat" size="md" /></span>
              <div>
                <h2>当前接码会话</h2>
                <p>号码与验证码均可一键复制</p>
              </div>
            </div>
            <span class="sms-status" :class="statusToneClass">
              <span class="sms-status__dot" aria-hidden="true"></span>
              {{ statusText }}
            </span>
          </div>

          <div class="sms-receiver-content">
            <section class="sms-number-card" aria-live="polite">
              <div class="sms-field-heading">
                <span>临时手机号</span>
                <span class="sms-field-heading__hint">领取后自动监听</span>
              </div>
              <button
                type="button"
                class="sms-copy-value sms-copy-value--phone"
                :class="{ 'is-empty': !phoneForCopy }"
                :disabled="!phoneForCopy"
                :title="phoneForCopy ? '复制完整国际号码' : '尚未领取号码'"
                @click="copyPhone"
              >
                <strong class="sms-phone-number">
                  <span v-if="countryCallingCode" class="sms-phone-number__code">+{{ countryCallingCode }}</span>
                  <span class="sms-phone-number__local">{{ localPhoneNumber }}</span>
                </strong>
                <Icon :name="phoneCopied ? 'check' : 'copy'" size="md" />
              </button>
              <div class="sms-number-meta">
                <span class="sms-number-meta__label">地区</span>
                <span class="sms-region">
                  <span v-if="countryFlag" class="sms-region__flag" aria-hidden="true">{{ countryFlag }}</span>
                  {{ region }}
                </span>
              </div>
            </section>

            <section class="sms-code-card" :class="{ 'has-code': hasCode }" aria-live="polite">
              <div class="sms-field-heading">
                <span>验证码</span>
                <span v-if="hasCode" class="sms-code-card__received"><Icon name="checkCircle" size="xs" /> 已接收</span>
                <span v-else class="sms-field-heading__hint">每 5 秒自动查询</span>
              </div>
              <button
                type="button"
                class="sms-copy-value sms-copy-value--code"
                :class="{ 'is-empty': !hasCode }"
                :disabled="!hasCode"
                :title="hasCode ? '复制验证码' : '等待验证码'"
                @click="copyCode"
              >
                <strong>{{ code }}</strong>
                <Icon :name="codeCopied ? 'check' : 'copy'" size="md" />
              </button>
              <p class="sms-code-card__hint">
                <Icon name="shield" size="xs" />
                {{ hasCode ? '验证码已送达，可直接复制到授权页面。' : '请在授权页面完成手机号验证，验证码到达后会显示在这里。' }}
              </p>
            </section>
          </div>

          <div class="sms-action-area">
            <button
              v-if="canStart"
              type="button"
              class="sms-start-button"
              :disabled="isStarting"
              @click="begin"
            >
              <Icon name="bolt" size="sm" :class="{ 'animate-pulse': isStarting }" />
              <span>{{ isStarting ? '正在领取号码…' : (authStore.isAuthenticated ? startButtonLabel : '登录后领取号码') }}</span>
            </button>
            <div v-else class="sms-action-grid">
              <button
              type="button"
              class="sms-action"
              title="立即查询当前号码是否收到验证码"
              :disabled="!canRefresh && !isLocalPreview"
                @click="refreshCode"
              >
                <Icon name="refresh" size="md" :class="{ 'animate-spin': isRefreshing }" />
                <span>刷新</span>
                <small>查询验证码</small>
              </button>
              <button
              type="button"
              class="sms-action"
              title="释放当前号码并领取新的号码"
              :disabled="!canChangeNumber && !isLocalPreview"
                @click="changeNumber"
              >
                <Icon name="swap" size="md" :class="{ 'animate-spin': isChangingNumber }" />
                <span>换号</span>
                <small>{{ changeNumberHint }}</small>
              </button>
              <button
              type="button"
              class="sms-action sms-action--danger"
              title="取消当前号码并将未使用卡密退回队列"
              :disabled="!canCancel && !isLocalPreview"
                @click="cancelSession"
              >
                <Icon name="x" size="md" :class="{ 'animate-spin': isCancelling }" />
                <span>取消</span>
                <small>{{ cancelHint }}</small>
              </button>
            </div>
            <p class="sms-action-help">
              <Icon name="infoCircle" size="xs" />
              {{ isAdmin
                ? '换号、取消、超时或临时错误都会释放卡密；仅收到实际验证码后才会消耗。'
                : `刷新和复制不收费；领取时预扣 ${formatMoney(currentFeeAmount)}，实际收到验证码后才最终结算。` }}
            </p>
          </div>

          <a :href="mainSiteHomeURL" class="sms-brand-promo" aria-label="访问 XIASS API 官方中转站">
            <img src="/brand/xiass-mark-dark.png" alt="" class="sms-brand-promo__logo" />
            <span class="sms-brand-promo__copy">
              <strong>XIASS <b>API</b></strong>
              <span>官方 API 中转站</span>
              <small>全满血账号</small>
            </span>
            <span class="sms-brand-promo__visit">访问官网 <Icon name="arrowRight" size="sm" /></span>
          </a>
        </article>

        <div class="sms-workspace__side">
          <div v-if="isAdmin" class="sms-overview" aria-label="接码服务概览">
            <div class="sms-overview__item">
              <span>待用卡密</span>
              <strong>{{ queuedKeyCount }}</strong>
            </div>
            <div class="sms-overview__divider" aria-hidden="true"></div>
            <div class="sms-overview__item">
              <span>监听中</span>
              <strong>{{ activeCount }}</strong>
            </div>
          </div>
          <div v-else class="sms-overview sms-overview--member" aria-label="接码费用与余额">
            <div class="sms-overview__item">
              <span>当前余额</span>
              <strong>{{ formatMoney(memberBalance) }}</strong>
            </div>
            <div class="sms-overview__divider" aria-hidden="true"></div>
            <div class="sms-overview__item">
              <span>接码费用</span>
              <strong>{{ formatMoney(currentFeeAmount) }}<small>/ 次</small></strong>
            </div>
          </div>

        <aside v-if="isAdmin" class="sms-panel sms-panel--queue">
          <div class="sms-panel__head">
            <div class="sms-panel__title-group">
              <span class="sms-panel__icon sms-panel__icon--violet"><Icon name="key" size="md" /></span>
              <div>
                <h2>接码卡密队列</h2>
                <p>批量保存，服务端加密管理</p>
              </div>
            </div>
          </div>

          <div class="sms-queue-stats" aria-label="队列状态">
            <div>
              <span>待用</span>
              <strong>{{ queuedKeyCount }}</strong>
            </div>
            <div>
              <span>使用中</span>
              <strong>{{ activeCount }}</strong>
            </div>
          </div>

          <section class="sms-member-fee" aria-labelledby="sms-member-fee-title">
            <div class="sms-member-fee__heading">
              <div>
                <span id="sms-member-fee-title">会员接码价格</span>
                <small>仅影响之后新领取的号码</small>
              </div>
              <span class="sms-member-fee__current">当前 {{ formatMoney(currentFeeAmount) }}</span>
            </div>
            <div class="sms-member-fee__form">
              <label class="sms-member-fee__input" for="sms-member-fee-input">
                <span aria-hidden="true">¥</span>
                <input
                  id="sms-member-fee-input"
                  v-model="memberFeeInput"
                  type="number"
                  min="0.01"
                  max="10000"
                  step="0.01"
                  inputmode="decimal"
                  autocomplete="off"
                  aria-label="会员单次接码价格"
                  @keydown.enter.prevent="saveMemberFee"
                />
              </label>
              <button
                type="button"
                class="sms-member-fee__save"
                :disabled="isSavingMemberFee"
                @click="saveMemberFee"
              >
                <Icon name="check" size="sm" />
                {{ isSavingMemberFee ? '保存中…' : '保存价格' }}
              </button>
            </div>
            <p>进行中的会话会保留领取时的价格，收到验证码、取消或退款都按原价结算。</p>
          </section>

          <label class="sms-textarea-label" for="sms-card-keys">添加卡密</label>
          <textarea
            id="sms-card-keys"
            v-model="cardKeysInput"
            class="sms-textarea"
            rows="7"
            placeholder="每行一个卡密，也支持用空格或逗号分隔"
            spellcheck="false"
          />
          <p class="sms-field-note"><Icon name="lock" size="xs" /> 卡密会加密保存；明文、删除和排序请在系统设置的“授权接码卡密”中管理。</p>

          <div class="sms-queue-buttons">
            <button
              type="button"
              class="sms-save-button"
              :disabled="!cardKeysInput.trim() || isSavingKeys"
              @click="saveKeys"
            >
              <Icon name="plus" size="sm" />
              {{ isSavingKeys ? '加密保存中…' : '加密保存卡密' }}
            </button>
            <button
              type="button"
              class="sms-clear-button"
              :class="{ 'is-confirming': clearConfirmationActive }"
              :disabled="queuedKeyCount === 0 || isClearingKeys"
              @click="requestClearQueue"
            >
              <Icon name="trash" size="sm" />
              {{ clearConfirmationActive ? '再次点击确认清空' : (isClearingKeys ? '正在清空…' : '清空待用') }}
            </button>
          </div>

          <div class="sms-security-note">
            <span class="sms-security-note__icon"><Icon name="shield" size="sm" /></span>
            <p><b>卡密安全规则</b>：仅提交到 XIASS API 服务器并加密存储；原文仅在管理员系统设置的卡密管理区可见。</p>
          </div>
        </aside>

        <aside v-else class="sms-panel sms-panel--member">
          <div class="sms-panel__head">
            <div class="sms-panel__title-group">
              <span class="sms-panel__icon sms-panel__icon--violet"><Icon name="creditCard" size="md" /></span>
              <div>
                <h2>会员接码服务</h2>
                <p>仅对收到实际验证码的会话收费</p>
              </div>
            </div>
          </div>

          <div class="sms-member-balance">
            <span>可用余额</span>
            <strong>{{ formatMoney(memberBalance) }}</strong>
              <p>每次授权接码 {{ formatMoney(currentFeeAmount) }}，余额不足时无法领取号码。</p>
          </div>

          <div class="sms-member-rules">
            <div>
              <span class="sms-member-rules__icon"><Icon name="shield" size="sm" /></span>
              <p><b>领取号码</b><small>先校验余额并预扣本次费用。</small></p>
            </div>
            <div>
              <span class="sms-member-rules__icon"><Icon name="refresh" size="sm" /></span>
              <p><b>刷新与换号</b><small>刷新免费；未收到验证码的换号会释放旧会话再领取新号。</small></p>
            </div>
            <div>
              <span class="sms-member-rules__icon"><Icon name="checkCircle" size="sm" /></span>
              <p><b>收到验证码</b><small>服务端确认后按 {{ formatMoney(currentFeeAmount) }} 完成结算，该次服务不可退款。</small></p>
            </div>
          </div>

          <button type="button" class="sms-recharge-button" @click="goToRecharge">
            <Icon name="creditCard" size="sm" />
            充值余额
          </button>
          <p class="sms-recharge-note">充值、支付和到账回调均使用 XIASS API 原有支付链路。</p>
        </aside>
        </div>
      </section>

      <section class="sms-flow" aria-label="接码流程说明">
        <div class="sms-flow__heading">
          <span class="sms-flow__icon"><Icon name="arrowsUpDown" size="sm" /></span>
          <div>
            <h2>使用流程</h2>
            <p>每一步的状态都在当前页面可见</p>
          </div>
        </div>
        <ol class="sms-flow__steps">
          <li :class="{ 'is-active': queuedKeyCount > 0 }">
            <span class="sms-flow__number">1</span>
            <div><b>保存卡密</b><small>服务器加密入队</small></div>
          </li>
          <li :class="{ 'is-active': hasActiveSession || phase === 'received' }">
            <span class="sms-flow__number">2</span>
            <div><b>领取号码</b><small>复制到授权页面</small></div>
          </li>
          <li :class="{ 'is-active': hasCode }">
            <span class="sms-flow__number">3</span>
            <div><b>接收验证码</b><small>复制并完成授权</small></div>
          </li>
        </ol>
      </section>
    </div>
    <SMSRechargeDialog :open="rechargeDialogOpen" @close="rechargeDialogOpen = false" @success="handleRechargeSuccess" />
  </main>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'
import DarkVideoBackground from '@/components/common/DarkVideoBackground.vue'
import SMSRechargeDialog from '@/components/sms/SMSRechargeDialog.vue'
import { useClipboard } from '@/composables/useClipboard'
import { usePixlabSMSReceiver } from '@/composables/usePixlabSMSReceiver'
import { updateMemberFee } from '@/api/admin/smsReceiver'
import { useAppStore, useAuthStore } from '@/stores'

const router = useRouter()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const { copyToClipboard } = useClipboard()
const previewMode = computed(() => String(route.query.preview || ''))
const isLocalPreview = computed(() => import.meta.env.DEV && ['1', 'member'].includes(previewMode.value))
const isAdmin = computed(() => isLocalPreview.value ? previewMode.value !== 'member' : authStore.isAdmin)
const receiverScope = isAdmin.value ? 'admin' : 'member'
const receiver = usePixlabSMSReceiver(receiverScope)

const {
  phase,
  phoneForCopy,
  countryCallingCode,
  localPhoneNumber,
  region,
  countryFlag,
  code,
  queuedKeyCount,
  feeAmount,
  balance,
  hasActiveSession,
  statusText,
  canRefresh,
  canChangeNumber,
  canCancel,
  memberMutationRemainingSeconds,
  isRefreshing,
  isChangingNumber,
  isCancelling
} = receiver

const activeCount = ref(0)
const cardKeysInput = ref('')
const isLoadingStatus = ref(false)
const isStarting = ref(false)
const isSavingKeys = ref(false)
const isClearingKeys = ref(false)
const isSavingMemberFee = ref(false)
const clearConfirmationActive = ref(false)
const phoneCopied = ref(false)
const codeCopied = ref(false)
const memberFeeInput = ref('')
const rechargeDialogOpen = ref(false)
let clearConfirmationTimer: number | undefined
let previewNumberIndex = 0

const hasCode = computed(() => Boolean(code.value && code.value !== '--'))
const memberBalance = computed(() => balance.value ?? authStore.user?.balance ?? null)
const currentFeeAmount = computed(() => feeAmount.value > 0 ? feeAmount.value : 2)
const mainSiteHomeURL = computed(() => {
  const currentHost = window.location.host
  const mainHost = currentHost.replace(/^sms\./i, 'api.')
  return `${window.location.protocol}//${mainHost}/`
})
const canStart = computed(() => !hasActiveSession.value && !['waiting', 'starting'].includes(phase.value))
const startButtonLabel = computed(() => phase.value === 'received' ? '领取新的号码' : '领取一个号码')
const memberMutationCountdown = computed(() => {
  const seconds = memberMutationRemainingSeconds.value
  if (!seconds) return ''
  const minutes = Math.floor(seconds / 60)
  const remainder = seconds % 60
  return minutes > 0 ? `${minutes} 分 ${remainder.toString().padStart(2, '0')} 秒` : `${remainder} 秒`
})
const changeNumberHint = computed(() => {
  if (!isAdmin.value && hasCode.value) return '验证码已收到'
  if (!isAdmin.value && memberMutationCountdown.value) return `${memberMutationCountdown.value}后可换号`
  return '更换手机号'
})
const cancelHint = computed(() => {
  if (!isAdmin.value && hasCode.value) return '验证码已收到'
  if (!isAdmin.value && memberMutationCountdown.value) return `${memberMutationCountdown.value}后可取消`
  return '释放当前会话'
})
const connectionLabel = computed(() => isLocalPreview.value
  ? '本地预览'
  : !authStore.isAuthenticated
    ? '请先登录'
    : phase.value === 'error'
      ? '服务连接异常'
      : '服务已连接')
const connectionClass = computed(() => phase.value === 'error'
  ? 'is-error'
  : isLocalPreview.value
    ? 'is-preview'
    : !authStore.isAuthenticated
      ? 'is-guest'
      : 'is-online')
const statusToneClass = computed(() => {
  if (phase.value === 'received') return 'is-success'
  if (phase.value === 'error' || phase.value === 'expired') return 'is-error'
  if (phase.value === 'unavailable') return 'is-warning'
  return 'is-live'
})

function previewSession(number = '+1 816 215 0598', nextCode = '--'): void {
  const digits = number.replace(/\D/g, '')
  phase.value = nextCode === '--' ? 'waiting' : 'received'
  countryCallingCode.value = '1'
  localPhoneNumber.value = digits.slice(1)
  phoneForCopy.value = `+${digits}`
  region.value = '美国'
  countryFlag.value = '🇺🇸'
  code.value = nextCode
  queuedKeyCount.value = Math.max(0, queuedKeyCount.value || 8)
  activeCount.value = nextCode === '--' ? 1 : 0
}

function describeError(error: unknown, fallback: string): string {
  if (typeof error === 'object' && error !== null && 'message' in error) {
    const message = String((error as { message?: unknown }).message || '').trim()
    if (message) return message
  }
  return fallback
}

async function loadStatus(): Promise<void> {
  if (isLocalPreview.value) {
    previewSession()
    appStore.showInfo('本地预览状态已刷新。')
    return
  }

  if (!authStore.isAuthenticated) {
    appStore.showInfo('登录后可查看余额并领取接码号码。')
    return
  }

  isLoadingStatus.value = true
  try {
    const status = await receiver.refreshQueueStatus()
    activeCount.value = status.active_count
    if (typeof status.fee_amount === 'number' && document.activeElement?.id !== 'sms-member-fee-input') {
      memberFeeInput.value = status.fee_amount.toFixed(2)
    }
  } catch (error) {
    appStore.showError(describeError(error, '无法读取接码服务状态。'))
  } finally {
    isLoadingStatus.value = false
  }
}

async function saveMemberFee(): Promise<void> {
  const rawFee = Number(memberFeeInput.value)
  if (!Number.isFinite(rawFee) || rawFee < 0.01 || rawFee > 10000) {
    appStore.showError('会员接码价格必须在 ¥0.01 到 ¥10000.00 之间。')
    return
  }

  const normalizedFee = Math.round(rawFee * 100) / 100
  isSavingMemberFee.value = true
  try {
    if (isLocalPreview.value) {
      feeAmount.value = normalizedFee
    } else {
      const result = await updateMemberFee(normalizedFee)
      feeAmount.value = result.fee_amount
    }
    memberFeeInput.value = feeAmount.value.toFixed(2)
    appStore.showSuccess(`会员接码价格已更新为 ${formatMoney(feeAmount.value)}，仅影响后续新领取的号码。`)
  } catch (error) {
    appStore.showError(describeError(error, '保存会员接码价格失败，请稍后重试。'))
  } finally {
    isSavingMemberFee.value = false
  }
}

async function begin(): Promise<void> {
  if (isLocalPreview.value) {
    previewNumberIndex = (previewNumberIndex + 1) % 2
    previewSession(previewNumberIndex === 0 ? '+1 816 215 0598' : '+1 312 555 0184')
    appStore.showSuccess('本地预览已领取演示号码。')
    return
  }

  if (!authStore.isAuthenticated) {
    await router.push({ path: '/login', query: { redirect: '/sms' } })
    return
  }

  isStarting.value = true
  try {
    const outcome = await receiver.start()
    await loadStatus()
    if (outcome === 'unavailable') {
      appStore.showInfo(isAdmin.value ? '暂无待用卡密，请先在右侧添加卡密。' : '暂时没有可用号码，请稍后刷新重试。')
    } else if (outcome === 'received') {
      appStore.showSuccess('已收到验证码。')
    } else {
      appStore.showSuccess('号码已领取，正在实时监听验证码。')
    }
  } catch (error) {
    appStore.showError(describeError(error, '领取号码失败，请稍后重试。'))
  } finally {
    isStarting.value = false
  }
}

async function refreshCode(): Promise<void> {
  if (isLocalPreview.value) {
    previewSession(phoneForCopy.value || '+1 816 215 0598', '846 217')
    appStore.showSuccess('本地预览：已模拟收到验证码。')
    return
  }

  try {
    const outcome = await receiver.refresh()
    await loadStatus()
    appStore.showInfo(outcome === 'received' ? '已收到验证码。' : '已查询当前号码，暂未收到验证码。')
  } catch (error) {
    appStore.showError(describeError(error, '查询验证码失败，请稍后重试。'))
  }
}

async function changeNumber(): Promise<void> {
  if (isLocalPreview.value) {
    previewNumberIndex = (previewNumberIndex + 1) % 2
    previewSession(previewNumberIndex === 0 ? '+1 816 215 0598' : '+1 312 555 0184')
    appStore.showSuccess('本地预览：已更换演示号码。')
    return
  }

  try {
    const outcome = await receiver.changeNumber()
    await loadStatus()
    appStore.showSuccess(outcome === 'received' ? '新号码已收到验证码。' : '已更换号码，正在继续监听。')
  } catch (error) {
    appStore.showError(describeError(error, '更换号码失败，请稍后重试。'))
  }
}

async function cancelSession(): Promise<void> {
  if (isLocalPreview.value) {
    phase.value = 'idle'
    phoneForCopy.value = ''
    countryCallingCode.value = ''
    localPhoneNumber.value = '--'
    region.value = '--'
    countryFlag.value = ''
    code.value = '--'
    activeCount.value = 0
    appStore.showInfo('本地预览：已取消演示会话。')
    return
  }

  try {
    const outcome = await receiver.cancel()
    await loadStatus()
    appStore.showInfo(outcome === 'received' ? '验证码已收到，当前会话已按实际结果结算。' : '已取消当前会话，未使用卡密已退回队列。')
  } catch (error) {
    appStore.showError(describeError(error, '取消会话失败，请稍后重试。'))
  }
}

async function saveKeys(): Promise<void> {
  const rawKeys = cardKeysInput.value.trim()
  if (!rawKeys) return

  if (isLocalPreview.value) {
    const added = rawKeys.split(/[\s,]+/).filter(Boolean).length
    queuedKeyCount.value += added
    cardKeysInput.value = ''
    appStore.showSuccess(`本地预览：已模拟加密保存 ${added} 个卡密。`)
    return
  }

  isSavingKeys.value = true
  try {
    const added = await receiver.appendCardKeys(rawKeys)
    cardKeysInput.value = ''
    await loadStatus()
    appStore.showSuccess(added > 0 ? `已加密保存 ${added} 个接码卡密。` : '没有发现可添加的新卡密。')
  } catch (error) {
    appStore.showError(describeError(error, '保存卡密失败，请稍后重试。'))
  } finally {
    isSavingKeys.value = false
  }
}

function resetClearConfirmation(): void {
  clearConfirmationActive.value = false
  if (clearConfirmationTimer) window.clearTimeout(clearConfirmationTimer)
  clearConfirmationTimer = undefined
}

async function requestClearQueue(): Promise<void> {
  if (!clearConfirmationActive.value) {
    clearConfirmationActive.value = true
    appStore.showInfo('再次点击“确认清空”才会删除待用卡密。')
    clearConfirmationTimer = window.setTimeout(resetClearConfirmation, 5_000)
    return
  }

  resetClearConfirmation()
  if (isLocalPreview.value) {
    queuedKeyCount.value = 0
    appStore.showInfo('本地预览：已清空演示队列。')
    return
  }

  isClearingKeys.value = true
  try {
    const deleted = await receiver.clearQueuedCardKeys()
    await loadStatus()
    appStore.showInfo(deleted > 0 ? `已清空 ${deleted} 个待用接码卡密。` : '当前没有待用接码卡密。')
  } catch (error) {
    appStore.showError(describeError(error, '清空卡密失败，请稍后重试。'))
  } finally {
    isClearingKeys.value = false
  }
}

async function copyPhone(): Promise<void> {
  if (await copyToClipboard(phoneForCopy.value, '手机号已复制。')) {
    phoneCopied.value = true
    window.setTimeout(() => { phoneCopied.value = false }, 2_000)
  }
}

async function copyCode(): Promise<void> {
  if (await copyToClipboard(code.value, '验证码已复制。')) {
    codeCopied.value = true
    window.setTimeout(() => { codeCopied.value = false }, 2_000)
  }
}

function goToLogin(): void {
  void router.push({ path: '/login', query: { redirect: '/sms' } })
}

function goToRegister(): void {
  void router.push({ path: '/register', query: { redirect: '/sms' } })
}

function goToRecharge(): void {
  if (!authStore.isAuthenticated) {
    goToLogin()
    return
  }
  rechargeDialogOpen.value = true
}

async function handleRechargeSuccess(): Promise<void> {
  await loadStatus()
}

function formatMoney(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return '--'
  return `¥${value.toFixed(2)}`
}

async function logout(): Promise<void> {
  await authStore.logout()
  await router.push({ path: '/login', query: { redirect: '/sms' } })
}

onMounted(async () => {
  if (isLocalPreview.value) {
    queuedKeyCount.value = 8
    feeAmount.value = 2
    memberFeeInput.value = feeAmount.value.toFixed(2)
    previewSession()
    return
  }

  if (!authStore.isAuthenticated) return
  await loadStatus()
  if (hasActiveSession.value) {
    try {
      await receiver.start()
      await loadStatus()
    } catch (error) {
      appStore.showError(describeError(error, '无法恢复上次的接码会话。'))
    }
  }
})

onBeforeUnmount(() => {
  receiver.stop()
  resetClearConfirmation()
})
</script>

<style scoped>
.sms-console {
  position: relative;
  isolation: isolate;
  min-height: 100vh;
  color: #dbeafe;
  background: rgba(4, 20, 28, .48);
}

.sms-console__body {
  position: relative;
  z-index: 1;
}

.sms-console__body {
  width: min(1180px, calc(100% - 40px));
  margin: 0 auto;
}

.sms-connection,
.sms-panel__title-group,
.sms-region,
.sms-field-note,
.sms-security-note,
.sms-flow__heading,
.sms-flow__steps li,
.sms-preview-banner {
  display: flex;
  align-items: center;
}

.sms-panel__icon,
.sms-flow__icon,
.sms-security-note__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
}

.sms-connection { gap: 7px; color: #9ab0c9; font-size: 12px; white-space: nowrap; }
.sms-connection__dot,
.sms-status__dot { width: 7px; height: 7px; border-radius: 50%; background: currentColor; box-shadow: 0 0 0 3px color-mix(in srgb, currentColor 17%, transparent); }
.sms-connection.is-online { color: #43d5a3; }
.sms-connection.is-error { color: #fb7185; }
.sms-connection.is-preview { color: #a78bfa; }
.sms-connection.is-guest { color: #f2c56c; }

.sms-header-button,
.sms-icon-button,
.sms-action,
.sms-start-button,
.sms-save-button,
.sms-member-fee__save,
.sms-clear-button,
.sms-copy-value {
  font: inherit;
  border: 0;
  cursor: pointer;
  transition: border-color .18s ease, background-color .18s ease, color .18s ease, transform .18s ease, box-shadow .18s ease;
}

.sms-header-button {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-height: 34px;
  padding: 0 11px;
  border: 1px solid rgba(113, 159, 204, .24);
  border-radius: 7px;
  color: #c8d8eb;
  background: rgba(15, 38, 64, .56);
  font-size: 12px;
  font-weight: 600;
}

.sms-header-button:hover:not(:disabled), .sms-icon-button:hover { border-color: rgba(102, 208, 255, .58); color: #e4f8ff; background: rgba(23, 68, 104, .72); }
.sms-header-button--quiet { background: transparent; }
.sms-header-button--primary { border-color: rgba(93, 218, 255, .64); color: #032235; background: #76dfff; }
.sms-header-button--primary:hover:not(:disabled) { color: #021b2a; background: #a8edff; }
.sms-icon-button { display: inline-grid; place-items: center; width: 34px; height: 34px; border: 1px solid rgba(113, 159, 204, .24); border-radius: 7px; color: #9db0c8; background: transparent; }
.sms-header-button:disabled, .sms-action:disabled, .sms-start-button:disabled, .sms-save-button:disabled, .sms-member-fee__save:disabled, .sms-clear-button:disabled, .sms-copy-value:disabled { cursor: not-allowed; opacity: .45; }

.sms-console__body { padding: 42px 0 50px; }
.sms-preview-banner { gap: 8px; margin-bottom: 22px; padding: 10px 13px; border: 1px solid rgba(167, 139, 250, .36); border-radius: 8px; color: #dcd2ff; background: rgba(100, 77, 164, .18); font-size: 13px; }

.sms-intro { display: flex; align-items: flex-start; justify-content: space-between; gap: 30px; margin-bottom: 27px; }
.sms-intro__content { min-width: 0; }
.sms-eyebrow { margin: 0 0 9px; color: #54c7ff; font-size: 11px; font-weight: 750; letter-spacing: .16em; }
.sms-intro h1 { margin: 0; color: #f5fbff; font-size: 32px; line-height: 1.12; letter-spacing: 0; }
.sms-intro__summary { max-width: 625px; margin: 11px 0 0; color: #9fb1c8; font-size: 14px; line-height: 1.7; }
.sms-intro__purpose { display: inline-flex; align-items: center; gap: 7px; margin: 13px 0 0; padding: 7px 11px; border: 1px solid rgba(76, 218, 180, .42); border-radius: 6px; color: #9cf0d5; background: rgba(16, 121, 101, .18); font-size: 13px; font-weight: 750; letter-spacing: .02em; }
.sms-intro__purpose svg { color: #58e0ba; }
.sms-workbench-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 9px; width: fit-content; margin-top: 17px; padding: 8px 9px; border: 1px solid rgba(95, 156, 208, .23); border-radius: 8px; background: rgba(6, 26, 45, .56); box-shadow: inset 0 1px rgba(255, 255, 255, .025); }
.sms-intro-auth { display: flex; align-items: center; gap: 9px; min-height: 34px; }

.sms-overview { display: flex; align-items: stretch; width: 100%; min-width: 0; box-sizing: border-box; padding: 10px 16px; border: 1px solid rgba(94, 148, 200, .24); border-radius: 8px; background: rgba(9, 31, 54, .9); box-shadow: inset 0 1px rgba(255, 255, 255, .03), 0 12px 28px rgba(0, 0, 0, .14); }
.sms-overview__item { min-width: 0; flex: 1 1 0; }
.sms-overview__item span { display: block; color: #90a4bf; font-size: 11px; }
.sms-overview__item strong { display: block; margin-top: 2px; color: #ecf8ff; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 22px; line-height: 1; white-space: nowrap; }
.sms-overview__item strong small { color: #90a6bf; font-family: inherit; font-size: 11px; font-weight: 600; }
.sms-overview__divider { width: 1px; margin: 0 17px; background: rgba(124, 160, 197, .25); }

.sms-workspace { display: grid; grid-template-columns: minmax(0, 1.55fr) minmax(320px, .92fr); gap: 20px; }
.sms-workspace__side { position: relative; min-width: 0; }
.sms-workspace__side > .sms-overview { position: absolute; z-index: 2; left: 50%; bottom: calc(100% + 14px); transform: translateX(-50%); }
.sms-panel, .sms-flow { border: 1px solid rgba(97, 151, 205, .26); border-radius: 9px; background: rgba(8, 27, 48, .82); box-shadow: 0 22px 55px rgba(0, 0, 0, .16), inset 0 1px rgba(255, 255, 255, .027); }
.sms-panel { overflow: hidden; }
.sms-panel__head { display: flex; align-items: center; justify-content: space-between; gap: 14px; padding: 19px 21px; border-bottom: 1px solid rgba(100, 147, 194, .2); background: rgba(13, 39, 67, .5); }
.sms-panel__title-group { min-width: 0; gap: 12px; }
.sms-panel__title-group h2 { margin: 0; color: #f2f8ff; font-size: 15px; letter-spacing: 0; }
.sms-panel__title-group p { margin: 4px 0 0; color: #8fa5c0; font-size: 12px; }
.sms-panel__icon { width: 36px; height: 36px; border-radius: 8px; }
.sms-panel__icon--primary { color: #042237; background: #60d5ff; }
.sms-panel__icon--violet { color: #e4ddff; background: rgba(128, 105, 216, .72); }
.sms-status { display: inline-flex; align-items: center; gap: 7px; flex: 0 0 auto; padding: 6px 9px; border: 1px solid currentColor; border-radius: 999px; font-size: 11px; font-weight: 700; }
.sms-status.is-live { color: #49d8a6; background: rgba(38, 160, 119, .11); }
.sms-status.is-success { color: #76e6a6; background: rgba(45, 158, 90, .14); }
.sms-status.is-error { color: #fb8ca0; background: rgba(213, 63, 86, .13); }
.sms-status.is-warning { color: #ffd273; background: rgba(205, 143, 31, .13); }

.sms-receiver-content { display: grid; grid-template-columns: minmax(0, 1.1fr) minmax(0, .9fr); gap: 14px; padding: 20px 21px; }
.sms-number-card, .sms-code-card { min-width: 0; padding: 17px; border: 1px solid rgba(101, 156, 212, .24); border-radius: 8px; background: rgba(5, 22, 42, .66); }
.sms-code-card { border-color: rgba(110, 115, 202, .28); }
.sms-code-card.has-code { border-color: rgba(68, 203, 150, .52); background: rgba(13, 73, 65, .31); }
.sms-field-heading { display: flex; align-items: center; justify-content: space-between; gap: 10px; color: #adbed2; font-size: 12px; font-weight: 650; }
.sms-field-heading__hint { color: #657993; font-weight: 500; white-space: nowrap; }
.sms-copy-value { width: 100%; display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 13px 0 10px; color: #f2f9ff; background: transparent; text-align: left; }
.sms-copy-value:hover:not(:disabled) { color: #7cdeff; transform: translateX(2px); }
.sms-copy-value strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; letter-spacing: .015em; }
.sms-copy-value--phone strong { display: inline-flex; align-items: baseline; gap: 12px; white-space: nowrap; font-size: clamp(21px, 2.25vw, 28px); }
.sms-phone-number__code { color: #70d7f6; font-size: inherit; font-weight: 750; }
.sms-phone-number__local { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
.sms-copy-value--code strong { font-size: clamp(26px, 3.15vw, 38px); letter-spacing: .18em; }
.sms-copy-value.is-empty strong { color: #63758d; }
.sms-number-meta { display: flex; align-items: baseline; gap: 13px; padding-top: 14px; border-top: 1px solid rgba(104, 148, 193, .18); color: #91a6bf; font-size: clamp(21px, 2.25vw, 28px); line-height: 1.2; }
.sms-number-meta__label { flex: 0 0 auto; color: #91a6bf; font-weight: 650; }
.sms-region { min-width: 0; flex-wrap: nowrap; gap: 9px; color: #c8d9e9; font-weight: 700; white-space: nowrap; }
.sms-region__flag { flex: 0 0 auto; font-size: 1em; line-height: 1; }
.sms-code-card__received { display: inline-flex; align-items: center; gap: 4px; color: #62dfa2; font-weight: 700; }
.sms-code-card__hint { display: flex; align-items: flex-start; gap: 6px; margin: 5px 0 0; color: #7f95ad; font-size: 11px; line-height: 1.55; }
.sms-code-card__hint svg { flex: 0 0 auto; margin-top: 2px; color: #64c9ee; }

.sms-action-area { padding: 0 21px 20px; }
.sms-action-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; }
.sms-action { display: grid; grid-template-columns: 24px minmax(0, 1fr); column-gap: 9px; align-items: center; min-height: 66px; padding: 10px 11px; border: 1px solid rgba(99, 151, 207, .24); border-radius: 8px; color: #c8d9eb; background: rgba(17, 47, 77, .5); text-align: left; }
.sms-action:hover:not(:disabled) { border-color: rgba(91, 203, 255, .62); color: #f0fbff; background: rgba(20, 66, 104, .74); box-shadow: 0 8px 20px rgba(2, 42, 70, .32); }
.sms-action > svg { grid-row: span 2; color: #64d5ff; }
.sms-action span { min-width: 0; color: inherit; font-size: 13px; font-weight: 750; }
.sms-action small { min-width: 0; overflow: hidden; text-overflow: ellipsis; color: #7f96b0; font-size: 10px; white-space: nowrap; }
.sms-action--danger > svg { color: #fb869b; }
.sms-action--danger:hover:not(:disabled) { border-color: rgba(248, 113, 113, .55); background: rgba(104, 29, 46, .35); }
.sms-start-button, .sms-save-button { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 45px; padding: 0 18px; border-radius: 7px; color: #042033; background: #64d5ff; font-size: 13px; font-weight: 800; box-shadow: 0 9px 20px rgba(26, 162, 220, .19); }
.sms-start-button { width: 100%; }
.sms-start-button:hover:not(:disabled), .sms-save-button:hover:not(:disabled) { color: #021925; background: #99e6ff; transform: translateY(-1px); box-shadow: 0 12px 25px rgba(25, 170, 232, .26); }
.sms-action-help { display: flex; align-items: flex-start; gap: 6px; margin: 12px 1px 0; color: #758ca6; font-size: 11px; line-height: 1.5; }
.sms-action-help svg { flex: 0 0 auto; margin-top: 2px; }

.sms-brand-promo { display: flex; align-items: center; gap: 14px; min-height: 82px; margin: 0 21px 21px; padding: 13px 16px; overflow: hidden; border: 1px solid rgba(78, 204, 220, .32); border-radius: 8px; color: #e7f9ff; background: rgba(8, 48, 65, .74); box-shadow: inset 0 1px rgba(255, 255, 255, .035); text-decoration: none; transition: border-color .18s ease, background-color .18s ease, transform .18s ease, box-shadow .18s ease; }
.sms-brand-promo:hover { border-color: rgba(89, 223, 231, .72); background: rgba(10, 68, 84, .84); transform: translateY(-1px); box-shadow: 0 11px 25px rgba(2, 32, 46, .28), inset 0 1px rgba(255, 255, 255, .055); }
.sms-brand-promo__logo { width: 54px; height: 54px; flex: 0 0 54px; object-fit: contain; opacity: .94; filter: drop-shadow(0 4px 10px rgba(84, 223, 239, .22)); }
.sms-brand-promo__copy { display: grid; min-width: 0; gap: 3px; }
.sms-brand-promo__copy strong { color: #f3fdff; font-size: 15px; font-weight: 850; letter-spacing: .08em; }
.sms-brand-promo__copy strong b { color: #67def4; }
.sms-brand-promo__copy > span { color: #bdedf5; font-size: 12px; font-weight: 700; }
.sms-brand-promo__copy small { color: #78b1c0; font-size: 11px; }
.sms-brand-promo__visit { display: inline-flex; align-items: center; gap: 5px; margin-left: auto; color: #7be3ec; font-size: 11px; font-weight: 750; white-space: nowrap; }

.sms-panel--queue { display: flex; flex-direction: column; }
.sms-queue-stats { display: grid; grid-template-columns: 1fr 1fr; margin: 19px 21px 0; border: 1px solid rgba(107, 103, 201, .27); border-radius: 8px; overflow: hidden; background: rgba(51, 42, 113, .18); }
.sms-queue-stats div { padding: 12px 14px; }
.sms-queue-stats div + div { border-left: 1px solid rgba(128, 119, 205, .25); }
.sms-queue-stats span { display: block; color: #aaa9d7; font-size: 11px; }
.sms-queue-stats strong { display: block; margin-top: 4px; color: #f1efff; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 22px; }
.sms-member-fee { margin: 16px 21px 0; padding: 13px; border: 1px solid rgba(68, 202, 173, .3); border-radius: 8px; background: rgba(10, 72, 67, .24); }
.sms-member-fee__heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.sms-member-fee__heading > div > span { display: block; color: #d4f4ed; font-size: 12px; font-weight: 750; }
.sms-member-fee__heading small { display: block; margin-top: 3px; color: #7daaa1; font-size: 10px; line-height: 1.4; }
.sms-member-fee__current { flex: 0 0 auto; color: #7ce9c2; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 12px; font-weight: 800; }
.sms-member-fee__form { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 9px; margin-top: 12px; }
.sms-member-fee__input { display: flex; align-items: center; min-width: 0; min-height: 39px; padding: 0 10px; border: 1px solid rgba(99, 186, 169, .36); border-radius: 7px; color: #8ce9ca; background: rgba(2, 28, 31, .62); transition: border-color .18s ease, box-shadow .18s ease; }
.sms-member-fee__input:focus-within { border-color: #5ce0bd; box-shadow: 0 0 0 3px rgba(61, 213, 174, .13); }
.sms-member-fee__input span { margin-right: 5px; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 14px; font-weight: 800; }
.sms-member-fee__input input { min-width: 0; width: 100%; border: 0; outline: 0; color: #edfff9; background: transparent; font: 700 14px ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; }
.sms-member-fee__input input::-webkit-outer-spin-button, .sms-member-fee__input input::-webkit-inner-spin-button { margin: 0; }
.sms-member-fee__save { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 39px; padding: 0 11px; border: 1px solid rgba(85, 223, 189, .56); border-radius: 7px; color: #03231f; background: #76e8c6; font-size: 12px; font-weight: 800; white-space: nowrap; }
.sms-member-fee__save:hover:not(:disabled) { background: #a9f6dc; transform: translateY(-1px); box-shadow: 0 7px 16px rgba(19, 183, 143, .22); }
.sms-member-fee > p { margin: 10px 0 0; color: #719a93; font-size: 10px; line-height: 1.5; }
.sms-textarea-label { display: block; margin: 18px 21px 8px; color: #bdd0e3; font-size: 12px; font-weight: 700; }
.sms-textarea { display: block; width: calc(100% - 42px); min-height: 132px; margin: 0 21px; padding: 11px 12px; border: 1px solid rgba(108, 151, 196, .28); border-radius: 7px; outline: none; resize: vertical; color: #e8f4ff; background: rgba(4, 20, 38, .72); font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 12px; line-height: 1.6; transition: border-color .18s ease, box-shadow .18s ease; }
.sms-textarea::placeholder { color: #60748e; }
.sms-textarea:focus { border-color: #60d5ff; box-shadow: 0 0 0 3px rgba(66, 192, 246, .14); }
.sms-field-note { gap: 5px; margin: 8px 21px 0; color: #748ba4; font-size: 11px; line-height: 1.45; }
.sms-field-note svg { color: #9e9bdc; }
.sms-queue-buttons { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 10px; margin: 16px 21px 20px; }
.sms-save-button { width: 100%; }
.sms-clear-button { display: inline-flex; align-items: center; justify-content: center; gap: 7px; min-height: 45px; padding: 0 13px; border: 1px solid rgba(140, 163, 189, .25); border-radius: 7px; color: #aec1d3; background: rgba(15, 35, 59, .62); font-size: 12px; font-weight: 700; white-space: nowrap; }
.sms-clear-button:hover:not(:disabled), .sms-clear-button.is-confirming { border-color: rgba(251, 113, 133, .52); color: #ffd8df; background: rgba(105, 34, 52, .42); }
.sms-security-note { align-items: flex-start; gap: 10px; margin: auto 21px 21px; padding: 11px; border: 1px solid rgba(110, 170, 201, .2); border-radius: 7px; color: #8197ae; background: rgba(11, 37, 60, .58); }
.sms-security-note__icon { width: 25px; height: 25px; border-radius: 6px; color: #4ed2bc; background: rgba(31, 160, 130, .16); }
.sms-security-note p { margin: 0; font-size: 11px; line-height: 1.55; }
.sms-security-note b { color: #c4d8e8; }

.sms-panel--member { display: flex; flex-direction: column; }
.sms-member-balance { margin: 20px 21px 0; padding: 17px; border: 1px solid rgba(72, 202, 172, .29); border-radius: 8px; background: rgba(15, 83, 73, .24); }
.sms-member-balance > span { display: block; color: #88b6aa; font-size: 12px; }
.sms-member-balance strong { display: block; margin-top: 6px; color: #dffef1; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 31px; line-height: 1; }
.sms-member-balance p { margin: 10px 0 0; color: #8ea8a2; font-size: 11px; line-height: 1.5; }
.sms-member-rules { display: grid; gap: 1px; margin: 20px 21px; overflow: hidden; border: 1px solid rgba(96, 147, 190, .2); border-radius: 8px; background: rgba(102, 151, 195, .16); }
.sms-member-rules > div { display: flex; align-items: flex-start; gap: 10px; padding: 11px; background: rgba(8, 31, 53, .68); }
.sms-member-rules__icon { display: inline-grid; place-items: center; width: 27px; height: 27px; flex: 0 0 27px; border-radius: 6px; color: #6cddff; background: rgba(52, 170, 214, .16); }
.sms-member-rules p { margin: 0; }
.sms-member-rules b, .sms-member-rules small { display: block; }
.sms-member-rules b { color: #d9eafb; font-size: 12px; }
.sms-member-rules small { margin-top: 3px; color: #7f98b0; font-size: 11px; line-height: 1.45; }
.sms-recharge-button { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 45px; margin: auto 21px 0; border: 0; border-radius: 7px; color: #042033; background: #73e4c1; font: inherit; font-size: 13px; font-weight: 800; cursor: pointer; transition: transform .18s ease, background-color .18s ease, box-shadow .18s ease; box-shadow: 0 9px 20px rgba(28, 181, 141, .18); }
.sms-recharge-button:hover { background: #a4f3d7; transform: translateY(-1px); box-shadow: 0 12px 25px rgba(28, 181, 141, .24); }
.sms-recharge-note { margin: 10px 21px 21px; color: #728ba1; font-size: 10px; line-height: 1.5; text-align: center; }

.sms-flow { display: flex; align-items: center; gap: 28px; margin-top: 20px; padding: 16px 21px; }
.sms-flow__heading { flex: 0 0 auto; gap: 10px; }
.sms-flow__icon { width: 34px; height: 34px; border-radius: 8px; color: #f4d27e; background: rgba(201, 145, 48, .18); }
.sms-flow h2 { margin: 0; color: #ebf6ff; font-size: 13px; }
.sms-flow p { margin: 3px 0 0; color: #7890a8; font-size: 11px; }
.sms-flow__steps { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); flex: 1; gap: 11px; padding: 0; margin: 0; list-style: none; }
.sms-flow__steps li { min-width: 0; gap: 9px; padding: 8px 9px; border-radius: 7px; color: #8399af; background: rgba(17, 43, 69, .52); }
.sms-flow__steps li.is-active { color: #dcefff; background: rgba(32, 95, 129, .46); }
.sms-flow__number { display: inline-grid; place-items: center; width: 20px; height: 20px; flex: 0 0 20px; border: 1px solid currentColor; border-radius: 50%; font-size: 10px; font-weight: 800; }
.sms-flow__steps b, .sms-flow__steps small { display: block; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sms-flow__steps b { color: inherit; font-size: 12px; }
.sms-flow__steps small { margin-top: 2px; color: #7590a9; font-size: 10px; }

@media (max-width: 900px) {
  .sms-workspace { grid-template-columns: 1fr; }
  .sms-panel--queue { min-height: 0; }
  .sms-workspace__side > .sms-overview { position: static; width: 100%; box-sizing: border-box; margin-bottom: 12px; transform: none; }
  .sms-security-note { margin-top: 0; }
  .sms-flow { align-items: flex-start; flex-direction: column; gap: 14px; }
  .sms-flow__steps { width: 100%; }
}

@media (max-width: 660px) {
  .sms-console__body { width: min(100% - 28px, 1180px); }
  .sms-console__body { padding: 25px 0 32px; }
  .sms-intro { align-items: stretch; flex-direction: column; gap: 17px; margin-bottom: 19px; }
  .sms-intro-auth { justify-content: flex-end; }
  .sms-intro h1 { font-size: 27px; }
  .sms-intro__summary { font-size: 13px; line-height: 1.6; }
  .sms-workbench-actions { width: 100%; box-sizing: border-box; }
  .sms-overview { width: 100%; box-sizing: border-box; }
  .sms-overview__item { flex: 1; }
  .sms-panel__head, .sms-receiver-content, .sms-action-area { padding-left: 15px; padding-right: 15px; }
  .sms-panel__head { padding-top: 15px; padding-bottom: 15px; }
  .sms-receiver-content { grid-template-columns: 1fr; gap: 10px; padding-top: 15px; padding-bottom: 15px; }
  .sms-copy-value--phone strong { font-size: 23px; }
  .sms-copy-value--code strong { font-size: 33px; }
  .sms-action-grid { gap: 8px; }
  .sms-action { grid-template-columns: 1fr; justify-items: center; row-gap: 3px; min-height: 82px; padding: 9px 5px; text-align: center; }
  .sms-action > svg { grid-row: auto; }
  .sms-action span { font-size: 12px; }
  .sms-action small { max-width: 100%; font-size: 9px; }
  .sms-brand-promo { gap: 11px; min-height: 76px; margin-left: 15px; margin-right: 15px; margin-bottom: 15px; padding: 11px 12px; }
  .sms-brand-promo__logo { width: 46px; height: 46px; flex-basis: 46px; }
  .sms-brand-promo__visit { font-size: 10px; }
  .sms-queue-stats { margin-left: 15px; margin-right: 15px; }
  .sms-member-fee { margin-left: 15px; margin-right: 15px; }
  .sms-textarea-label { margin-left: 15px; margin-right: 15px; }
  .sms-textarea { width: calc(100% - 30px); margin-left: 15px; margin-right: 15px; }
  .sms-field-note { margin-left: 15px; margin-right: 15px; }
  .sms-queue-buttons { margin-left: 15px; margin-right: 15px; }
  .sms-security-note { margin-left: 15px; margin-right: 15px; margin-bottom: 15px; }
  .sms-flow { padding: 15px; }
  .sms-flow__steps { grid-template-columns: 1fr; gap: 6px; }
  .sms-flow__steps li { padding: 9px; }
}

@media (max-width: 380px) {
  .sms-intro h1 { font-size: 25px; }
  .sms-status { padding-left: 7px; padding-right: 7px; }
  .sms-panel__title-group { gap: 9px; }
  .sms-panel__icon { width: 32px; height: 32px; }
  .sms-panel__title-group p { max-width: 155px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sms-action small { display: none; }
  .sms-action { min-height: 68px; }
  .sms-queue-buttons { grid-template-columns: 1fr; }
  .sms-clear-button { width: 100%; }
  .sms-brand-promo__visit { display: none; }
}
</style>

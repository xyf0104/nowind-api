<template>
  <Teleport to="body">
    <Transition name="sms-recharge-fade">
      <div v-if="open" class="sms-recharge-overlay" @click.self="close">
        <section class="sms-recharge-dialog" role="dialog" aria-modal="true" aria-labelledby="sms-recharge-title">
          <button type="button" class="sms-recharge-dialog__close" aria-label="关闭充值" @click="close">
            <Icon name="x" size="md" />
          </button>

          <template v-if="phase === 'paying' && paymentState">
            <header class="sms-recharge-dialog__head">
              <p>XIASS API</p>
              <h2 id="sms-recharge-title">完成充值</h2>
              <span>支付完成后余额会自动刷新</span>
            </header>
            <PaymentStatusPanel
              :order-id="paymentState.orderId"
              :amount="paymentState.amount"
              :pay-amount="paymentState.payAmount"
              :qr-code="paymentState.qrCode"
              :expires-at="paymentState.expiresAt"
              :payment-type="paymentState.paymentType"
              :pay-url="paymentState.payUrl"
              :order-type="paymentState.orderType"
              :currency="paymentState.currency"
              :out-trade-no="paymentState.outTradeNo"
              :mobile-alipay-deep-link="paymentState.alipayMobilePrecreateDeepLink"
              @done="finishPayment"
              @success="handlePaymentSuccess"
            />
          </template>

          <template v-else>
            <header class="sms-recharge-dialog__head">
              <p>XIASS API</p>
              <h2 id="sms-recharge-title">充值余额</h2>
              <span>充值成功后可立即领取授权接码号码</span>
            </header>

            <div v-if="loading" class="sms-recharge-dialog__loading">
              <span></span>
              正在读取可用支付方式…
            </div>

            <template v-else>
              <div class="sms-recharge-tiers" aria-label="选择充值金额">
                <button
                  v-for="tier in tiers"
                  :key="tier"
                  type="button"
                  :class="{ 'is-selected': amount === tier }"
                  :disabled="!tierAvailable(tier)"
                  @click="amount = tier"
                >
                  ¥{{ tier }}
                </button>
              </div>

              <p v-if="errorMessage" class="sms-recharge-error">{{ errorMessage }}</p>
              <div v-else-if="methodOptions.length" class="sms-recharge-methods">
                <PaymentMethodSelector
                  :methods="methodOptions"
                  :selected="selectedMethod"
                  compact
                  @select="selectedMethod = $event"
                />
              </div>
              <div v-else class="sms-recharge-empty">
                暂无可用支付方式，请联系管理员后重试。
              </div>

              <div class="sms-recharge-summary">
                <span>本次充值</span>
                <strong>{{ formatMoney(amount) }}</strong>
                <small v-if="selectedFeeRate > 0">支付方式手续费 {{ selectedFeeRate }}%</small>
              </div>

              <button
                type="button"
                class="sms-recharge-submit"
                :disabled="!canCreateOrder || submitting"
                @click="createRechargeOrder"
              >
                <span v-if="submitting" class="sms-recharge-submit__spinner"></span>
                <Icon v-else name="creditCard" size="sm" />
                {{ submitting ? '正在创建订单…' : `去支付 ${formatMoney(amount)}` }}
              </button>
              <p class="sms-recharge-note">最低充值金额10元，该余额同时支持XIASS API中转站使用</p>
            </template>
          </template>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'
import PaymentMethodSelector, { type PaymentMethodOption } from '@/components/payment/PaymentMethodSelector.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import { getPaymentPopupFeatures } from '@/components/payment/providerConfig'
import { buildCreateOrderPayload, decidePaymentLaunch, getVisibleMethods, normalizeVisibleMethod, type PaymentRecoverySnapshot } from '@/components/payment/paymentFlow'
import { paymentAPI } from '@/api/payment'
import { useAppStore, useAuthStore } from '@/stores'
import { isMobileDevice } from '@/utils/device'
import type { CheckoutInfoResponse, CreateOrderResult } from '@/types/payment'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: []; success: [] }>()

const router = useRouter()
const authStore = useAuthStore()
const appStore = useAppStore()
const tiers = [10, 20, 50, 100] as const
const amount = ref<number>(10)
const selectedMethod = ref('')
const loading = ref(false)
const submitting = ref(false)
const errorMessage = ref('')
const phase = ref<'select' | 'paying'>('select')
const paymentState = ref<PaymentRecoverySnapshot | null>(null)
const checkout = ref<CheckoutInfoResponse>({
  methods: {}, global_min: 0, global_max: 0, plans: [], balance_disabled: false,
  balance_recharge_multiplier: 1, subscription_usd_to_cny_rate: 0, recharge_fee_rate: 0,
  help_text: '', help_image_url: '', stripe_publishable_key: '',
})

const visibleMethods = computed(() => getVisibleMethods(checkout.value.methods))
const selectedLimit = computed(() => visibleMethods.value[selectedMethod.value])
const selectedFeeRate = computed(() => selectedLimit.value?.fee_rate ?? 0)
const methodOptions = computed<PaymentMethodOption[]>(() => Object.entries(visibleMethods.value).map(([type, limit]) => ({
  type,
  display_name: limit.display_name,
  fee_rate: limit.fee_rate,
  available: limit.available !== false && amountFitsMethod(amount.value, type),
})))
const canCreateOrder = computed(() => Boolean(
  selectedMethod.value
  && selectedLimit.value?.available !== false
  && amountFitsMethod(amount.value, selectedMethod.value)
))

watch(() => props.open, (open) => {
  if (open) void loadCheckout()
})

function amountFitsMethod(value: number, method: string): boolean {
  const limit = visibleMethods.value[method]
  const globalMinimum = Math.max(10, Number(checkout.value.global_min) || 0)
  const globalMaximum = Number(checkout.value.global_max) || 0
  if (!limit || value < globalMinimum) return false
  if (globalMaximum > 0 && value > globalMaximum) return false
  if (limit.single_min > 0 && value < limit.single_min) return false
  if (limit.single_max > 0 && value > limit.single_max) return false
  return limit.available !== false
}

function tierAvailable(value: number): boolean {
  return Object.keys(visibleMethods.value).some((method) => amountFitsMethod(value, method))
}

async function loadCheckout(): Promise<void> {
  phase.value = 'select'
  paymentState.value = null
  errorMessage.value = ''
  loading.value = true
  try {
    const response = await paymentAPI.getCheckoutInfo()
    checkout.value = response.data
    const methods = Object.keys(visibleMethods.value).filter((method) => amountFitsMethod(amount.value, method))
    selectedMethod.value = methods[0] || ''
  } catch (error) {
    errorMessage.value = describeError(error, '无法读取支付方式，请稍后重试。')
  } finally {
    loading.value = false
  }
}

async function createRechargeOrder(): Promise<void> {
  if (!canCreateOrder.value || submitting.value) return
  const paymentType = normalizeVisibleMethod(selectedMethod.value) || selectedMethod.value
  submitting.value = true
  errorMessage.value = ''
  try {
    const payload = buildCreateOrderPayload({
      amount: amount.value,
      paymentType,
      orderType: 'balance',
      origin: window.location.origin,
      isMobile: isMobileDevice(),
      isWechatBrowser: /MicroMessenger/i.test(window.navigator.userAgent),
      forceQRCode: checkout.value.alipay_force_qrcode === true && paymentType === 'alipay',
      mobilePrecreateDeepLink: checkout.value.alipay_mobile_precreate_deep_link === true,
    })
    const response = await paymentAPI.createOrder(payload)
    const result = response.data as CreateOrderResult & { resume_token?: string }
    const stripeRouteUrl = result.client_secret
      ? router.resolve({
        path: '/payment/stripe',
        query: {
          order_id: String(result.order_id),
          client_secret: result.client_secret,
          method: paymentType === 'stripe' ? undefined : (paymentType === 'wxpay' ? 'wechat_pay' : 'alipay'),
          resume_token: result.resume_token || undefined,
        }
      }).href
      : ''
    const airwallexRouteUrl = result.client_secret && result.intent_id
      ? router.resolve({
        path: '/payment/airwallex',
        query: {
          order_id: String(result.order_id),
          out_trade_no: result.out_trade_no || undefined,
          resume_token: result.resume_token || undefined,
        }
      }).href
      : ''
    const decision = decidePaymentLaunch(result, {
      visibleMethod: paymentType,
      orderType: 'balance',
      isMobile: isMobileDevice(),
      isWechatBrowser: /MicroMessenger/i.test(window.navigator.userAgent),
      forceQRCode: checkout.value.alipay_force_qrcode === true && paymentType === 'alipay',
      mobilePrecreateDeepLink: checkout.value.alipay_mobile_precreate_deep_link === true,
      stripePopupUrl: stripeRouteUrl,
      stripeRouteUrl,
      airwallexRouteUrl,
    })
    if (decision.kind === 'unhandled') {
      throw new Error('当前支付方式未返回可用的支付页面，请更换支付方式后重试。')
    }
    if (decision.kind === 'wechat_oauth' && decision.oauth?.authorize_url) {
      window.location.assign(decision.oauth.authorize_url)
      return
    }

    paymentState.value = decision.paymentState
    phase.value = 'paying'
    if (decision.kind === 'stripe_popup') {
      openGatewayWindow(decision.paymentState.payUrl)
    } else if (decision.kind === 'stripe_route' || decision.kind === 'airwallex_route') {
      window.location.assign(decision.paymentState.payUrl)
    } else if (decision.kind === 'redirect_waiting' && decision.paymentState.payUrl) {
      if (isMobileDevice()) window.location.assign(decision.paymentState.payUrl)
      else openGatewayWindow(decision.paymentState.payUrl)
    } else if (decision.kind === 'wechat_jsapi') {
      throw new Error('当前微信内支付需要在完整支付页完成，请使用右上角“支付中心”继续。')
    }
  } catch (error) {
    errorMessage.value = describeError(error, '创建充值订单失败，请稍后重试。')
    appStore.showError(errorMessage.value)
    phase.value = 'select'
    paymentState.value = null
  } finally {
    submitting.value = false
  }
}

async function handlePaymentSuccess(): Promise<void> {
  await authStore.refreshUser().catch(() => undefined)
  emit('success')
  appStore.showSuccess('充值成功，余额已更新。')
}

function finishPayment(): void {
  phase.value = 'select'
  paymentState.value = null
  emit('close')
}

function openGatewayWindow(url: string): void {
  if (!url) return
  const popup = window.open(url, 'xiassSmsPayment', getPaymentPopupFeatures())
  if (!popup || popup.closed) window.location.assign(url)
}

function close(): void {
  if (submitting.value) return
  emit('close')
}

function formatMoney(value: number): string {
  return `¥${value.toFixed(2)}`
}

function describeError(error: unknown, fallback: string): string {
  if (typeof error === 'object' && error !== null && 'message' in error) {
    const message = String((error as { message?: unknown }).message || '').trim()
    if (message && !/^request failed with status code \d+$/i.test(message)) return message
  }
  return fallback
}
</script>

<style scoped>
.sms-recharge-overlay { position: fixed; inset: 0; z-index: 80; display: grid; place-items: center; padding: 20px; background: rgba(0, 11, 16, .76); backdrop-filter: blur(9px); }
.sms-recharge-dialog { position: relative; width: min(100%, 470px); max-height: min(760px, calc(100vh - 40px)); overflow: auto; padding: 25px; border: 1px solid rgba(103, 201, 216, .32); border-radius: 10px; color: #dcebf0; background: rgba(5, 27, 35, .97); box-shadow: 0 30px 85px rgba(0, 0, 0, .5), inset 0 1px rgba(255, 255, 255, .04); }
.sms-recharge-dialog__close { position: absolute; top: 14px; right: 14px; display: grid; width: 32px; height: 32px; place-items: center; border: 1px solid rgba(130, 181, 196, .25); border-radius: 7px; color: #98b6c2; background: rgba(14, 51, 62, .7); cursor: pointer; }
.sms-recharge-dialog__close:hover { color: #edfbff; border-color: rgba(112, 221, 237, .58); background: rgba(19, 78, 91, .85); }
.sms-recharge-dialog__head { padding-right: 38px; }
.sms-recharge-dialog__head p { margin: 0 0 7px; color: #5cd2ed; font-size: 10px; font-weight: 800; letter-spacing: .15em; }
.sms-recharge-dialog__head h2 { margin: 0; color: #f3fcff; font-size: 22px; line-height: 1.2; }
.sms-recharge-dialog__head span { display: block; margin-top: 7px; color: #86a3ae; font-size: 12px; line-height: 1.55; }
.sms-recharge-dialog__loading, .sms-recharge-empty { display: flex; min-height: 180px; align-items: center; justify-content: center; color: #8ba8b3; font-size: 13px; text-align: center; }
.sms-recharge-dialog__loading { gap: 9px; }
.sms-recharge-dialog__loading span, .sms-recharge-submit__spinner { width: 16px; height: 16px; border: 2px solid rgba(112, 229, 204, .26); border-top-color: #69e4c5; border-radius: 50%; animation: sms-recharge-spin .7s linear infinite; }
.sms-recharge-tiers { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; margin-top: 23px; }
.sms-recharge-tiers button { min-height: 48px; border: 1px solid rgba(103, 157, 170, .28); border-radius: 7px; color: #bfd1d7; background: rgba(11, 48, 58, .64); font: 800 14px ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; cursor: pointer; transition: .18s ease; }
.sms-recharge-tiers button:hover, .sms-recharge-tiers button.is-selected { border-color: rgba(92, 229, 195, .7); color: #072820; background: #70e4c5; box-shadow: 0 7px 18px rgba(25, 183, 146, .2); }
.sms-recharge-tiers button:disabled { cursor: not-allowed; opacity: .42; box-shadow: none; }
.sms-recharge-methods { margin-top: 22px; }
.sms-recharge-summary { display: grid; grid-template-columns: 1fr auto; gap: 4px 16px; align-items: end; margin-top: 20px; padding: 14px; border: 1px solid rgba(78, 194, 167, .24); border-radius: 8px; background: rgba(8, 75, 66, .22); }
.sms-recharge-summary span { color: #88afa5; font-size: 12px; }
.sms-recharge-summary strong { color: #dffff1; font: 800 24px ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; }
.sms-recharge-summary small { grid-column: 1 / -1; color: #7e9f97; font-size: 10px; }
.sms-recharge-error { margin: 13px 0 0; color: #ff9cae; font-size: 12px; line-height: 1.5; }
.sms-recharge-submit { display: inline-flex; width: 100%; min-height: 47px; align-items: center; justify-content: center; gap: 8px; margin-top: 20px; border: 0; border-radius: 7px; color: #052b24; background: #74e7c5; font: 800 14px inherit; cursor: pointer; box-shadow: 0 10px 24px rgba(23, 183, 143, .2); transition: .18s ease; }
.sms-recharge-submit:hover:not(:disabled) { background: #a8f5db; transform: translateY(-1px); }
.sms-recharge-submit:disabled { cursor: not-allowed; opacity: .48; }
.sms-recharge-note { margin: 10px 5px 0; color: #728e99; font-size: 10px; line-height: 1.55; text-align: center; }
.sms-recharge-dialog :deep(.card) { border: 1px solid rgba(96, 160, 175, .25); border-radius: 8px; color: #dcebf0; background: rgba(8, 40, 50, .92); }
.sms-recharge-dialog :deep(.btn) { border-radius: 7px; }
.sms-recharge-dialog :deep(.text-gray-900), .sms-recharge-dialog :deep(.text-gray-700) { color: #e9f8fc; }
.sms-recharge-dialog :deep(.text-gray-500), .sms-recharge-dialog :deep(.text-gray-400) { color: #8da8b2; }
.sms-recharge-fade-enter-active, .sms-recharge-fade-leave-active { transition: opacity .2s ease; }
.sms-recharge-fade-enter-from, .sms-recharge-fade-leave-to { opacity: 0; }
@keyframes sms-recharge-spin { to { transform: rotate(360deg); } }
@media (max-width: 480px) { .sms-recharge-overlay { padding: 12px; } .sms-recharge-dialog { max-height: calc(100vh - 24px); padding: 20px 16px; } .sms-recharge-tiers { gap: 6px; } .sms-recharge-tiers button { min-height: 44px; font-size: 13px; } }
</style>

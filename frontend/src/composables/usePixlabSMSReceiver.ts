import { computed, ref } from 'vue'
import * as smsReceiverAPI from '@/api/admin/smsReceiver'
import type { SMSReceiverSession } from '@/api/admin/smsReceiver'

type SMSPhase = 'idle' | 'starting' | 'waiting' | 'received' | 'expired' | 'unavailable' | 'error'
export type SMSReceiveOutcome = 'waiting' | 'received' | 'expired' | 'unavailable'
export type SMSReceiverScope = 'admin' | 'member'

const ACTIVE_SESSION_STORAGE_KEY = 'xiass-api:pixlab-sms:active-session-id'
const MEMBER_ACTIVE_SESSION_STORAGE_KEY = 'xiass-api:pixlab-sms:member-active-session-id'
const LEGACY_QUEUE_STORAGE_KEY = 'xiass-api:pixlab-sms:card-key-queue'
const LEGACY_ACTIVE_KEY_STORAGE_KEY = 'xiass-api:pixlab-sms:active-card-key'
const POLL_INTERVAL_MS = 5_000

const callingCodes = [
  ['1', '美国/加拿大'], ['7', '俄罗斯/哈萨克斯坦'], ['20', '埃及'], ['27', '南非'],
  ['30', '希腊'], ['31', '荷兰'], ['32', '比利时'], ['33', '法国'], ['34', '西班牙'],
  ['39', '意大利'], ['44', '英国'], ['49', '德国'], ['52', '墨西哥'], ['55', '巴西'],
  ['60', '马来西亚'], ['61', '澳大利亚'], ['62', '印度尼西亚'], ['63', '菲律宾'],
  ['65', '新加坡'], ['66', '泰国'], ['81', '日本'], ['82', '韩国'], ['84', '越南'],
  ['86', '中国'], ['90', '土耳其'], ['91', '印度'], ['92', '巴基斯坦'], ['93', '阿富汗'],
  ['94', '斯里兰卡'], ['95', '缅甸'], ['98', '伊朗'], ['212', '摩洛哥'], ['351', '葡萄牙'],
  ['352', '卢森堡'], ['353', '爱尔兰'], ['354', '冰岛'], ['358', '芬兰'], ['380', '乌克兰'],
  ['420', '捷克'], ['852', '香港'], ['853', '澳门'], ['855', '柬埔寨'], ['856', '老挝'],
  ['880', '孟加拉国'], ['886', '台湾'], ['960', '马尔代夫'], ['961', '黎巴嫩'], ['971', '阿联酋']
] as const

const countryFlags: Record<string, string> = {
  '美国': '🇺🇸', 'united states': '🇺🇸', 'us': '🇺🇸',
  '加拿大': '🇨🇦', 'canada': '🇨🇦',
  '英国': '🇬🇧', 'united kingdom': '🇬🇧', 'uk': '🇬🇧',
  '日本': '🇯🇵', 'japan': '🇯🇵',
  '韩国': '🇰🇷', 'south korea': '🇰🇷', 'korea': '🇰🇷',
  '中国': '🇨🇳', 'china': '🇨🇳',
  '香港': '🇭🇰', 'hong kong': '🇭🇰',
  '澳门': '🇲🇴', 'macau': '🇲🇴',
  '台湾': '🇹🇼', 'taiwan': '🇹🇼',
  '新加坡': '🇸🇬', 'singapore': '🇸🇬',
  '澳大利亚': '🇦🇺', 'australia': '🇦🇺',
  '马来西亚': '🇲🇾', 'malaysia': '🇲🇾',
  '泰国': '🇹🇭', 'thailand': '🇹🇭',
  '越南': '🇻🇳', 'vietnam': '🇻🇳',
  '菲律宾': '🇵🇭', 'philippines': '🇵🇭',
  '印度': '🇮🇳', 'india': '🇮🇳',
  '印度尼西亚': '🇮🇩', 'indonesia': '🇮🇩',
  '德国': '🇩🇪', 'germany': '🇩🇪',
  '法国': '🇫🇷', 'france': '🇫🇷',
  '西班牙': '🇪🇸', 'spain': '🇪🇸',
  '意大利': '🇮🇹', 'italy': '🇮🇹',
  '巴西': '🇧🇷', 'brazil': '🇧🇷',
  '墨西哥': '🇲🇽', 'mexico': '🇲🇽',
  '俄罗斯': '🇷🇺', 'russia': '🇷🇺',
  '土耳其': '🇹🇷', 'turkey': '🇹🇷',
  '阿联酋': '🇦🇪', 'united arab emirates': '🇦🇪',
}

const callingCodeFlags: Record<string, string> = {
  '1': '🇺🇸', '7': '🇷🇺', '20': '🇪🇬', '27': '🇿🇦', '30': '🇬🇷',
  '31': '🇳🇱', '32': '🇧🇪', '33': '🇫🇷', '34': '🇪🇸', '39': '🇮🇹',
  '44': '🇬🇧', '49': '🇩🇪', '52': '🇲🇽', '55': '🇧🇷', '60': '🇲🇾',
  '61': '🇦🇺', '62': '🇮🇩', '63': '🇵🇭', '65': '🇸🇬', '66': '🇹🇭',
  '81': '🇯🇵', '82': '🇰🇷', '84': '🇻🇳', '86': '🇨🇳', '90': '🇹🇷',
  '91': '🇮🇳', '92': '🇵🇰', '93': '🇦🇫', '94': '🇱🇰', '95': '🇲🇲',
  '98': '🇮🇷', '212': '🇲🇦', '351': '🇵🇹', '352': '🇱🇺', '353': '🇮🇪',
  '354': '🇮🇸', '358': '🇫🇮', '380': '🇺🇦', '420': '🇨🇿', '852': '🇭🇰',
  '853': '🇲🇴', '855': '🇰🇭', '856': '🇱🇦', '880': '🇧🇩', '886': '🇹🇼',
  '960': '🇲🇻', '961': '🇱🇧', '971': '🇦🇪',
}

function safeStorage(): Storage | null {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null
  } catch {
    return null
  }
}

// The browser stores only an opaque server session ID, never a card key.
function readActiveSession(scope: SMSReceiverScope): string {
	const key = scope === 'member' ? MEMBER_ACTIVE_SESSION_STORAGE_KEY : ACTIVE_SESSION_STORAGE_KEY
	return safeStorage()?.getItem(key)?.trim() ?? ''
}

function writeActiveSession(scope: SMSReceiverScope, value: string): void {
	const storage = safeStorage()
	if (!storage) return
	const key = scope === 'member' ? MEMBER_ACTIVE_SESSION_STORAGE_KEY : ACTIVE_SESSION_STORAGE_KEY
	if (value) storage.setItem(key, value)
	else storage.removeItem(key)
}

function readLegacyCardKeys(): string[] {
  const storage = safeStorage()
  if (!storage) return []
  const values: string[] = []
  const rawQueue = storage.getItem(LEGACY_QUEUE_STORAGE_KEY)
  if (rawQueue) {
    try {
      const parsed: unknown = JSON.parse(rawQueue)
      if (Array.isArray(parsed)) {
        values.push(...parsed.filter((item): item is string => typeof item === 'string' && item.trim().length > 0))
      }
    } catch {
      // A malformed legacy value is erased only after a successful migration attempt.
    }
  }
  const activeKey = storage.getItem(LEGACY_ACTIVE_KEY_STORAGE_KEY)?.trim()
  if (activeKey) values.push(activeKey)
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))]
}

function clearLegacyCardKeys(): void {
  const storage = safeStorage()
  storage?.removeItem(LEGACY_QUEUE_STORAGE_KEY)
  storage?.removeItem(LEGACY_ACTIVE_KEY_STORAGE_KEY)
}

function formatPhone(number: string, reportedRegion: string): {
  display: string
  copyValue: string
  region: string
  flag: string
} {
  const digits = number.replace(/\D/g, '')
  if (!digits) return { display: '--', copyValue: '', region: reportedRegion || '--', flag: '' }

  const normalizedRegion = reportedRegion.trim().toLowerCase()

  const match = [...callingCodes]
    .sort(([left], [right]) => right.length - left.length)
    .find(([callingCode]) => digits.startsWith(callingCode) && digits.length > callingCode.length)

  if (match) {
    const [callingCode, fallbackRegion] = match
    const localNumber = digits.slice(callingCode.length)
    return {
      display: `+${callingCode} ${localNumber}`,
      copyValue: localNumber,
      region: reportedRegion || fallbackRegion,
      flag: countryFlags[normalizedRegion] || callingCodeFlags[callingCode] || ''
    }
  }

  return {
    display: `+${digits}`,
    copyValue: digits,
    region: reportedRegion || '自动识别',
    flag: countryFlags[normalizedRegion] || ''
  }
}

function errorReason(error: unknown): string {
  if (typeof error === 'object' && error !== null && 'reason' in error) {
    return String((error as { reason?: unknown }).reason || '')
  }
  return ''
}

export function usePixlabSMSReceiver(scope: SMSReceiverScope = 'admin') {
	const isMember = scope === 'member'
	const api = isMember ? smsReceiverAPI.memberSMSReceiverAPI : smsReceiverAPI
	const phase = ref<SMSPhase>('idle')
  const phoneDisplay = ref('--')
  const phoneForCopy = ref('')
  const region = ref('--')
  const countryFlag = ref('')
  const code = ref('--')
  const queuedKeyCount = ref(0)
	const activeSessionID = ref(readActiveSession(scope))
	const available = ref(false)
	const feeAmount = ref(0)
	const balance = ref<number | null>(null)
	const chargeState = ref<'held' | 'captured' | 'released' | ''>('')
  const actionAvailableAt = ref('')
  const currentTime = ref(Date.now())
  const isRefreshing = ref(false)
  const isChangingNumber = ref(false)
  const isCancelling = ref(false)
  let pollingTimer: number | undefined
  let cooldownTimer: number | undefined

  const statusText = computed(() => {
    const labels: Record<SMSPhase, string> = {
      idle: '等待取号',
      starting: '正在取号',
      waiting: '实时监听',
      received: '接收成功',
      expired: '已超时',
      unavailable: '暂无卡密',
      error: '连接失败'
    }
    return labels[phase.value]
  })

  const statusClass = computed(() => {
    if (phase.value === 'received') return 'text-emerald-600 dark:text-emerald-300'
    if (phase.value === 'expired' || phase.value === 'error') return 'text-red-600 dark:text-red-300'
    if (phase.value === 'unavailable') return 'text-amber-600 dark:text-amber-300'
    return 'text-blue-600 dark:text-blue-300'
  })

  const hasActiveSession = computed(() => Boolean(activeSessionID.value))
  const canRefresh = computed(() => hasActiveSession.value && !isRefreshing.value && !isChangingNumber.value && !isCancelling.value && !['starting', 'received', 'expired'].includes(phase.value))
  const memberMutationRemainingSeconds = computed(() => {
    if (!isMember || !actionAvailableAt.value) return 0
    const availableAt = Date.parse(actionAvailableAt.value)
    if (!Number.isFinite(availableAt)) return 0
    return Math.max(0, Math.ceil((availableAt - currentTime.value) / 1_000))
  })
  const canMutateMemberSession = computed(() => !isMember || memberMutationRemainingSeconds.value === 0)
  const canChangeNumber = computed(() => hasActiveSession.value && canMutateMemberSession.value && !isRefreshing.value && !isChangingNumber.value && !isCancelling.value && !['starting', 'received'].includes(phase.value))
  const canCancel = computed(() => hasActiveSession.value && canMutateMemberSession.value && !isRefreshing.value && !isChangingNumber.value && !isCancelling.value && !['starting', 'received'].includes(phase.value))

  // Move queues produced by the short-lived browser-local implementation to
  // the server on first use. This is deliberately best-effort: a failed upload
  // leaves the legacy data intact so an operator can retry rather than lose it.
  async function migrateLegacyCardKeys(): Promise<number> {
    if (isMember) return 0
    const legacyKeys = readLegacyCardKeys()
    if (legacyKeys.length === 0) return 0
    const result = await smsReceiverAPI.addCardKeys(legacyKeys.join('\n'))
    queuedKeyCount.value = result.queued_count
    clearLegacyCardKeys()
    return result.added_count
  }

  async function refreshQueueStatus(): Promise<smsReceiverAPI.SMSReceiverQueueStatus> {
		const status = await api.getStatus()
		queuedKeyCount.value = status.queued_count
		available.value = status.available === true
		if (typeof status.fee_amount === 'number') feeAmount.value = status.fee_amount
		if (typeof status.balance === 'number') balance.value = status.balance
		return status
  }

  async function appendCardKeys(raw: string): Promise<number> {
    const result = await smsReceiverAPI.addCardKeys(raw)
    queuedKeyCount.value = result.queued_count
    return result.added_count
  }

  async function clearQueuedCardKeys(): Promise<number> {
    const result = await smsReceiverAPI.clearCardKeys()
    queuedKeyCount.value = result.queued_count
    return result.deleted_count
  }

  function stopPolling(): void {
    if (pollingTimer) clearInterval(pollingTimer)
    pollingTimer = undefined
  }

  function stop(): void {
    stopPolling()
    if (cooldownTimer) clearInterval(cooldownTimer)
    cooldownTimer = undefined
  }

  function startCooldownTicker(): void {
    if (!isMember || !actionAvailableAt.value || memberMutationRemainingSeconds.value <= 0) return
    if (cooldownTimer) return
    cooldownTimer = window.setInterval(() => {
      currentTime.value = Date.now()
      if (memberMutationRemainingSeconds.value <= 0 && cooldownTimer) {
        clearInterval(cooldownTimer)
        cooldownTimer = undefined
      }
    }, 1_000)
  }

  function clearActiveSession(): void {
    stop()
    activeSessionID.value = ''
		writeActiveSession(scope, '')
  }

  function resetDisplay(nextPhase: SMSPhase): void {
    stop()
    phase.value = nextPhase
    phoneDisplay.value = nextPhase === 'unavailable' ? '暂无待用卡密' : '--'
    phoneForCopy.value = ''
    region.value = '--'
    countryFlag.value = ''
    code.value = '--'
  }

	function applyPhone(result: SMSReceiverSession): void {
    const rawNumber = result.number?.trim() ?? ''
    if (!rawNumber) return
    const formatted = formatPhone(rawNumber, result.country?.trim() ?? '')
    phoneDisplay.value = formatted.display
    phoneForCopy.value = formatted.copyValue
    region.value = formatted.region
		countryFlag.value = formatted.flag
	}

	function applyBilling(result: SMSReceiverSession): void {
		if (typeof result.fee_amount === 'number') feeAmount.value = result.fee_amount
		if (typeof result.balance === 'number') balance.value = result.balance
		if (result.charge_state === 'held' || result.charge_state === 'captured' || result.charge_state === 'released') {
			chargeState.value = result.charge_state
		}
		actionAvailableAt.value = result.action_available_at || ''
		currentTime.value = Date.now()
		if (actionAvailableAt.value) {
			startCooldownTicker()
		} else if (cooldownTimer) {
			clearInterval(cooldownTimer)
			cooldownTimer = undefined
		}
	}

	function applyResponse(result: SMSReceiverSession): SMSReceiveOutcome {
		queuedKeyCount.value = result.queued_count
		applyBilling(result)
    applyPhone(result)
    const status = (result.status || '').trim().toUpperCase()
    const incomingCode = result.code?.trim() ?? ''

    if (incomingCode && incomingCode !== '--') {
      code.value = incomingCode
      clearActiveSession()
      phase.value = 'received'
      return 'received'
    }

    if (['EXPIRED', 'TIMEOUT', 'CANCELLED', 'CANCELED', 'FAILED', 'ERROR', 'RECEIVED', 'COMPLETED', 'USED'].includes(status)) {
      clearActiveSession()
      phase.value = 'expired'
      code.value = '--'
      return 'expired'
    }

    if (result.session_id) {
      activeSessionID.value = result.session_id
			writeActiveSession(scope, result.session_id)
    }
    phase.value = 'waiting'
    startPolling()
    return 'waiting'
  }

  function startPolling(): void {
    stopPolling()
    if (!activeSessionID.value) return
    pollingTimer = window.setInterval(() => {
      if (phase.value !== 'waiting') {
        stop()
        return
      }
      void refresh(true).catch(() => undefined)
    }, POLL_INTERVAL_MS)
  }

  async function resumeActive(): Promise<SMSReceiveOutcome> {
		const sessionID = activeSessionID.value || readActiveSession(scope)
    if (!sessionID) {
      resetDisplay('unavailable')
      return 'unavailable'
    }
    activeSessionID.value = sessionID
    phase.value = 'starting'
    try {
		return applyResponse(await api.resume(sessionID))
    } catch (error) {
      if (errorReason(error) === 'SMS_SESSION_NOT_FOUND') clearActiveSession()
      phase.value = 'error'
      throw error
    }
  }

  async function start(): Promise<SMSReceiveOutcome> {
    if (activeSessionID.value || readActiveSession(scope)) return resumeActive()

    await migrateLegacyCardKeys()
    await refreshQueueStatus()
		if ((!isMember && queuedKeyCount.value <= 0) || (isMember && !available.value)) {
      resetDisplay('unavailable')
      return 'unavailable'
    }

    phase.value = 'starting'
    phoneDisplay.value = '正在获取…'
    region.value = '--'
    code.value = '--'
    try {
		return applyResponse(await api.redeem())
    } catch (error) {
      phase.value = 'error'
      await refreshQueueStatus().catch(() => undefined)
      throw error
    }
  }

  async function refresh(silent = false): Promise<SMSReceiveOutcome> {
		const sessionID = activeSessionID.value || readActiveSession(scope)
    if (!sessionID) {
      resetDisplay('unavailable')
      return 'unavailable'
    }
    activeSessionID.value = sessionID
    if (!silent) isRefreshing.value = true
    try {
      const result = phase.value === 'error' || phase.value === 'starting'
			? await api.resume(sessionID)
			: await api.check(sessionID)
      return applyResponse(result)
    } catch (error) {
      if (errorReason(error) === 'SMS_SESSION_NOT_FOUND') clearActiveSession()
      phase.value = 'error'
      throw error
    } finally {
      if (!silent) isRefreshing.value = false
    }
  }

  async function changeNumber(): Promise<SMSReceiveOutcome> {
		const sessionID = activeSessionID.value || readActiveSession(scope)
    if (!sessionID) throw new Error('当前没有可更换的手机号。')
    isChangingNumber.value = true
    stop()
    try {
		return applyResponse(await api.change(sessionID))
    } catch (error) {
      if (errorReason(error) === 'SMS_SESSION_NOT_FOUND') clearActiveSession()
      phase.value = 'error'
      throw error
    } finally {
      isChangingNumber.value = false
      if (phase.value === 'waiting') startPolling()
    }
  }

  async function cancel(): Promise<SMSReceiveOutcome> {
		const sessionID = activeSessionID.value || readActiveSession(scope)
    if (!sessionID) {
      resetDisplay('idle')
      return 'unavailable'
    }
    isCancelling.value = true
    stop()
    try {
		const result = await api.cancel(sessionID)
		const outcome = applyResponse(result)
      if (outcome !== 'received') {
        clearActiveSession()
        resetDisplay('idle')
      }
      return outcome
    } finally {
      isCancelling.value = false
      if (phase.value === 'waiting') startPolling()
    }
  }

  return {
    phase,
    phoneDisplay,
    phoneForCopy,
    region,
		countryFlag,
    code,
		queuedKeyCount,
		available,
		feeAmount,
		balance,
		chargeState,
		actionAvailableAt,
		memberMutationRemainingSeconds,
    hasActiveSession,
    statusText,
    statusClass,
    canRefresh,
    canChangeNumber,
    canCancel,
    isRefreshing,
    isChangingNumber,
    isCancelling,
    refreshQueueStatus,
    appendCardKeys,
    clearQueuedCardKeys,
    migrateLegacyCardKeys,
    start,
    refresh,
    changeNumber,
    cancel,
    stop
  }
}

import { computed, ref } from 'vue'
import * as smsReceiverAPI from '@/api/admin/smsReceiver'
import type { SMSReceiverSession } from '@/api/admin/smsReceiver'

type SMSPhase = 'idle' | 'starting' | 'waiting' | 'received' | 'expired' | 'unavailable' | 'error'
export type SMSReceiveOutcome = 'waiting' | 'received' | 'expired' | 'unavailable'

const ACTIVE_SESSION_STORAGE_KEY = 'xiass-api:pixlab-sms:active-session-id'
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

function safeStorage(): Storage | null {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null
  } catch {
    return null
  }
}

// The browser stores only an opaque server session ID, never a card key.
function readActiveSession(): string {
  return safeStorage()?.getItem(ACTIVE_SESSION_STORAGE_KEY)?.trim() ?? ''
}

function writeActiveSession(value: string): void {
  const storage = safeStorage()
  if (!storage) return
  if (value) storage.setItem(ACTIVE_SESSION_STORAGE_KEY, value)
  else storage.removeItem(ACTIVE_SESSION_STORAGE_KEY)
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
} {
  const digits = number.replace(/\D/g, '')
  if (!digits) return { display: '--', copyValue: '', region: reportedRegion || '--' }

  const match = [...callingCodes]
    .sort(([left], [right]) => right.length - left.length)
    .find(([callingCode]) => digits.startsWith(callingCode) && digits.length > callingCode.length)

  if (match) {
    const [callingCode, fallbackRegion] = match
    const localNumber = digits.slice(callingCode.length)
    return {
      display: `+${callingCode} ${localNumber}`,
      copyValue: localNumber,
      region: reportedRegion || fallbackRegion
    }
  }

  return { display: `+${digits}`, copyValue: digits, region: reportedRegion || '自动识别' }
}

function errorReason(error: unknown): string {
  if (typeof error === 'object' && error !== null && 'reason' in error) {
    return String((error as { reason?: unknown }).reason || '')
  }
  return ''
}

export function usePixlabSMSReceiver() {
  const phase = ref<SMSPhase>('idle')
  const phoneDisplay = ref('--')
  const phoneForCopy = ref('')
  const region = ref('--')
  const code = ref('--')
  const queuedKeyCount = ref(0)
  const activeSessionID = ref(readActiveSession())
  const isRefreshing = ref(false)
  const isChangingNumber = ref(false)
  const isCancelling = ref(false)
  let pollingTimer: number | undefined

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
  const canChangeNumber = computed(() => hasActiveSession.value && !isRefreshing.value && !isChangingNumber.value && !isCancelling.value && !['starting', 'received'].includes(phase.value))
  const canCancel = computed(() => hasActiveSession.value && !isRefreshing.value && !isChangingNumber.value && !isCancelling.value && !['starting', 'received'].includes(phase.value))

  // Move queues produced by the short-lived browser-local implementation to
  // the server on first use. This is deliberately best-effort: a failed upload
  // leaves the legacy data intact so an operator can retry rather than lose it.
  async function migrateLegacyCardKeys(): Promise<number> {
    const legacyKeys = readLegacyCardKeys()
    if (legacyKeys.length === 0) return 0
    const result = await smsReceiverAPI.addCardKeys(legacyKeys.join('\n'))
    queuedKeyCount.value = result.queued_count
    clearLegacyCardKeys()
    return result.added_count
  }

  async function refreshQueueStatus(): Promise<void> {
    const status = await smsReceiverAPI.getStatus()
    queuedKeyCount.value = status.queued_count
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

  function stop(): void {
    if (pollingTimer) clearInterval(pollingTimer)
    pollingTimer = undefined
  }

  function clearActiveSession(): void {
    stop()
    activeSessionID.value = ''
    writeActiveSession('')
  }

  function resetDisplay(nextPhase: SMSPhase): void {
    stop()
    phase.value = nextPhase
    phoneDisplay.value = nextPhase === 'unavailable' ? '暂无待用卡密' : '--'
    phoneForCopy.value = ''
    region.value = '--'
    code.value = '--'
  }

  function applyPhone(result: SMSReceiverSession): void {
    const rawNumber = result.number?.trim() ?? ''
    if (!rawNumber) return
    const formatted = formatPhone(rawNumber, result.country?.trim() ?? '')
    phoneDisplay.value = formatted.display
    phoneForCopy.value = formatted.copyValue
    region.value = formatted.region
  }

  function applyResponse(result: SMSReceiverSession): SMSReceiveOutcome {
    queuedKeyCount.value = result.queued_count
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
      writeActiveSession(result.session_id)
    }
    phase.value = 'waiting'
    startPolling()
    return 'waiting'
  }

  function startPolling(): void {
    stop()
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
    const sessionID = activeSessionID.value || readActiveSession()
    if (!sessionID) {
      resetDisplay('unavailable')
      return 'unavailable'
    }
    activeSessionID.value = sessionID
    phase.value = 'starting'
    try {
      return applyResponse(await smsReceiverAPI.resume(sessionID))
    } catch (error) {
      if (errorReason(error) === 'SMS_SESSION_NOT_FOUND') clearActiveSession()
      phase.value = 'error'
      throw error
    }
  }

  async function start(): Promise<SMSReceiveOutcome> {
    if (activeSessionID.value || readActiveSession()) return resumeActive()

    await migrateLegacyCardKeys()
    await refreshQueueStatus()
    if (queuedKeyCount.value <= 0) {
      resetDisplay('unavailable')
      return 'unavailable'
    }

    phase.value = 'starting'
    phoneDisplay.value = '正在获取…'
    region.value = '--'
    code.value = '--'
    try {
      return applyResponse(await smsReceiverAPI.redeem())
    } catch (error) {
      phase.value = 'error'
      await refreshQueueStatus().catch(() => undefined)
      throw error
    }
  }

  async function refresh(silent = false): Promise<SMSReceiveOutcome> {
    const sessionID = activeSessionID.value || readActiveSession()
    if (!sessionID) {
      resetDisplay('unavailable')
      return 'unavailable'
    }
    activeSessionID.value = sessionID
    if (!silent) isRefreshing.value = true
    try {
      const result = phase.value === 'error' || phase.value === 'starting'
        ? await smsReceiverAPI.resume(sessionID)
        : await smsReceiverAPI.check(sessionID)
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
    const sessionID = activeSessionID.value || readActiveSession()
    if (!sessionID) throw new Error('当前没有可更换的手机号。')
    isChangingNumber.value = true
    stop()
    try {
      return applyResponse(await smsReceiverAPI.change(sessionID))
    } catch (error) {
      if (errorReason(error) === 'SMS_SESSION_NOT_FOUND') clearActiveSession()
      phase.value = 'error'
      throw error
    } finally {
      isChangingNumber.value = false
      if (phase.value === 'waiting') startPolling()
    }
  }

  async function cancel(): Promise<void> {
    const sessionID = activeSessionID.value || readActiveSession()
    if (!sessionID) {
      resetDisplay('idle')
      return
    }
    isCancelling.value = true
    stop()
    try {
      const result = await smsReceiverAPI.cancel(sessionID)
      queuedKeyCount.value = result.queued_count
      clearActiveSession()
      resetDisplay('idle')
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
    code,
    queuedKeyCount,
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

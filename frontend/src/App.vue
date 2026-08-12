<script setup lang="ts">
import { RouterView, useRouter, useRoute } from 'vue-router'
import { computed, onMounted, onBeforeUnmount, watch } from 'vue'
import Toast from '@/components/common/Toast.vue'
import NavigationProgress from '@/components/common/NavigationProgress.vue'
import AdminComplianceDialog from '@/components/admin/AdminComplianceDialog.vue'
import { resolveRouteDocumentTitle } from '@/router/title'
import AnnouncementPopup from '@/components/common/AnnouncementPopup.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore, useAuthStore, useSubscriptionStore, useAnnouncementStore, useAdminComplianceStore, useAdminSettingsStore } from '@/stores'
import { getSetupStatus } from '@/api/setup'
import { sanitizeUrl } from '@/utils/url'
import { getCurrentTheme } from '@/utils/theme'

const DEFAULT_LIGHT_FAVICON_URL = '/favicon-light.png?v=1.0.68'
const DEFAULT_DARK_FAVICON_URL = '/favicon-dark.png?v=1.0.68'
const DEFAULT_APPLE_TOUCH_ICON_URL = '/apple-touch-icon.png?v=1.0.68'

const router = useRouter()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const subscriptionStore = useSubscriptionStore()
const announcementStore = useAnnouncementStore()
const adminComplianceStore = useAdminComplianceStore()
const adminSettingsStore = useAdminSettingsStore()
let themeObserver: MutationObserver | null = null

// The SMS host is a standalone member workbench. Keep XIASS API's global
// announcements and admin acknowledgement flow scoped to the main domain.
const isStandaloneSMSHost = window.location.hostname.split('.')[0]?.toLowerCase() === 'sms'

// These views already use AppLayout. Keeping one layout instance above the
// routed content prevents a route change from recreating the video background,
// particle canvas, sidebar, header, onboarding setup, and version checks.
const APP_LAYOUT_ROUTE_NAMES = new Set([
  'Dashboard', 'Keys', 'CodexHelperConnect', 'BatchImageGuide', 'Usage',
  'Redeem', 'Affiliate', 'UserPricing', 'Profile', 'Subscriptions',
  'PurchaseSubscription', 'OrderList', 'PaymentQRCode', 'CustomPage',
  // AdminOps owns a real fullscreen mode, so it deliberately keeps its
  // existing self-contained layout instead of inheriting this shell.
  'ChannelStatus', 'AdminDashboard', 'AdminAuditLogs', 'AdminUsers',
  'AdminGroups', 'AdminChannels', 'AdminChannelMonitor', 'AdminSubscriptions',
  'AdminAccounts', 'AdminAnnouncements', 'AdminProxies', 'AdminRedeem',
  'AdminPromoCodes', 'AdminSettings', 'AdminRiskControl', 'AdminPromptAudit', 'AdminUsage',
  'AdminAffiliateInvites', 'AdminAffiliateRebates', 'AdminAffiliateTransfers',
  'AdminPaymentDashboard', 'AdminOrders', 'AdminPaymentPlans',
  'AirwallexPayment',
])

const usesPersistentAppLayout = computed(() => (
  !isStandaloneSMSHost
  && typeof route.name === 'string'
  && APP_LAYOUT_ROUTE_NAMES.has(route.name)
))

function updateDocumentTitle() {
  const customMenuItems = [
    ...(appStore.cachedPublicSettings?.custom_menu_items ?? []),
    ...(authStore.isAdmin ? adminSettingsStore.customMenuItems : []),
  ]
  document.title = resolveRouteDocumentTitle(route, appStore.siteName, customMenuItems)
}

function imageMimeType(imageUrl: string): string | null {
  const dataMimeType = imageUrl.match(/^data:(image\/[a-z0-9.+-]+)[;,]/i)?.[1]
  if (dataMimeType) {
    return dataMimeType.toLowerCase()
  }

  let pathname = ''
  try {
    pathname = new URL(imageUrl, window.location.href).pathname.toLowerCase()
  } catch {
    return null
  }

  if (pathname.endsWith('.svg')) return 'image/svg+xml'
  if (pathname.endsWith('.png')) return 'image/png'
  if (pathname.endsWith('.jpg') || pathname.endsWith('.jpeg')) return 'image/jpeg'
  if (pathname.endsWith('.gif')) return 'image/gif'
  if (pathname.endsWith('.webp')) return 'image/webp'
  if (pathname.endsWith('.avif')) return 'image/avif'
  if (pathname.endsWith('.ico')) return 'image/x-icon'
  return null
}

function updateIconLink(rel: 'icon' | 'apple-touch-icon', imageUrl: string) {
  let link = document.querySelector<HTMLLinkElement>(`link[rel="${rel}"]`)
  if (!link) {
    link = document.createElement('link')
    link.rel = rel
    document.head.appendChild(link)
  }

  const mimeType = imageMimeType(imageUrl)
  if (mimeType) {
    link.type = mimeType
  } else {
    link.removeAttribute('type')
  }
  link.href = imageUrl
}

function updateSiteIcons(logoUrl: string) {
  const customLogo = sanitizeUrl(logoUrl, { allowRelative: true, allowDataUrl: true })
  const defaultFavicon = getCurrentTheme() === 'dark'
    ? DEFAULT_DARK_FAVICON_URL
    : DEFAULT_LIGHT_FAVICON_URL
  updateIconLink('icon', customLogo || defaultFavicon)
  updateIconLink('apple-touch-icon', customLogo || DEFAULT_APPLE_TOUCH_ICON_URL)
}

// Watch for site settings changes and update favicon/title
watch(
  () => appStore.siteLogo,
  (newLogo) => {
    updateSiteIcons(newLogo)
  },
  { immediate: true }
)

watch(
  [
    () => route.fullPath,
    () => route.meta.title,
    () => route.meta.titleKey,
    () => appStore.siteName,
    () => appStore.cachedPublicSettings?.custom_menu_items,
    () => authStore.isAdmin,
    () => adminSettingsStore.customMenuItems,
  ],
  updateDocumentTitle,
  { deep: true }
)

// Watch for authentication state and manage subscription data + announcements
function onVisibilityChange() {
  if (!isStandaloneSMSHost && document.visibilityState === 'visible' && authStore.isAuthenticated) {
    announcementStore.fetchAnnouncements()
  }
}

function onAdminComplianceRequired(event: Event) {
  const detail = (event as CustomEvent<Record<string, string>>).detail || {}
  adminComplianceStore.requireAcknowledgement(detail)
}

watch(
  () => authStore.isAuthenticated,
  (isAuthenticated, oldValue) => {
    if (isAuthenticated) {
      if (authStore.isAdmin && !isStandaloneSMSHost) {
        adminComplianceStore.fetchStatus().catch((error) => {
          console.error('Failed to fetch admin compliance status:', error)
        })
      }

      // User logged in: preload subscriptions and start polling
      subscriptionStore.fetchActiveSubscriptions().catch((error) => {
        console.error('Failed to preload subscriptions:', error)
      })
      subscriptionStore.startPolling()

      // Announcements: new login vs page refresh restore
      if (!isStandaloneSMSHost && oldValue === false) {
        // New login: delay 3s then force fetch
        setTimeout(() => announcementStore.fetchAnnouncements(true), 3000)
      } else if (!isStandaloneSMSHost) {
        // Page refresh restore (oldValue was undefined)
        announcementStore.fetchAnnouncements()
      }

      // Register visibility change listener
      document.addEventListener('visibilitychange', onVisibilityChange)
    } else {
      // User logged out: clear data and stop polling
      subscriptionStore.clear()
      announcementStore.reset()
      adminComplianceStore.reset()
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  },
  { immediate: true }
)

// Route change trigger (throttled by store)
router.afterEach(() => {
  if (!isStandaloneSMSHost && authStore.isAuthenticated) {
    announcementStore.fetchAnnouncements()
  }
})

onBeforeUnmount(() => {
  themeObserver?.disconnect()
  document.removeEventListener('visibilitychange', onVisibilityChange)
  window.removeEventListener('admin-compliance-required', onAdminComplianceRequired)
})

onMounted(async () => {
  window.addEventListener('admin-compliance-required', onAdminComplianceRequired)
  themeObserver = new MutationObserver(() => updateSiteIcons(appStore.siteLogo))
  themeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['class']
  })

  // Check if setup is needed
  try {
    const status = await getSetupStatus()
    if (status.needs_setup && route.path !== '/setup' && !isStandaloneSMSHost) {
      router.replace('/setup')
      return
    }
  } catch {
    // If setup endpoint fails, assume normal mode and continue
  }

  // Load public settings into appStore (will be cached for other components)
  await appStore.fetchPublicSettings()

  // Re-resolve document title now that site settings are available
  updateDocumentTitle()
})
</script>

<template>
  <NavigationProgress />
  <RouterView v-slot="{ Component }">
    <AppLayout v-if="usesPersistentAppLayout">
      <component :is="Component" />
    </AppLayout>
    <component :is="Component" v-else />
  </RouterView>
  <Toast />
  <AnnouncementPopup v-if="!isStandaloneSMSHost" />
  <AdminComplianceDialog v-if="!isStandaloneSMSHost" />
</template>

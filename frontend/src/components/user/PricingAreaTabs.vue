<template>
  <nav
    class="custom-scrollbar flex max-w-full gap-1 overflow-x-auto border-b border-gray-200 dark:border-dark-700"
    :aria-label="t('pricingAreaTabs.label')"
    data-testid="pricing-area-tabs"
  >
    <RouterLink
      v-for="tab in tabs"
      :key="tab.id"
      :to="tab.to"
      :data-testid="`pricing-area-tab-${tab.id}`"
      :aria-current="tab.id === activeTab ? 'page' : undefined"
      :class="[
        'relative inline-flex min-h-10 shrink-0 items-center border-b-2 px-4 py-2 text-sm font-medium transition-colors',
        tab.id === activeTab
          ? 'border-primary-500 text-primary-600 dark:text-primary-400'
          : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-900 dark:text-gray-400 dark:hover:border-dark-500 dark:hover:text-gray-100',
      ]"
    >
      {{ tab.label }}
    </RouterLink>
  </nav>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'

type PricingAreaTab = 'pricing' | 'monitor'

interface PricingAreaTabItem {
  id: PricingAreaTab
  to: string
  label: string
}

const { t } = useI18n()

const { activeTab } = defineProps<{
  activeTab: PricingAreaTab
}>()

const monitorEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.channelMonitor))
const pricingEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPricing))

const tabs = computed(() => {
  const result: PricingAreaTabItem[] = []

  if (pricingEnabled.value) {
    result.push({
      id: 'pricing',
      to: '/pricing',
      label: t('pricingAreaTabs.pricing'),
    })
  }

  if (monitorEnabled.value) {
    result.push({
      id: 'monitor',
      to: '/pricing/monitor',
      label: t('pricingAreaTabs.monitor'),
    })
  }

  return result
})
</script>

<template>
  <div class="relative min-h-screen overflow-x-hidden bg-[#cdd8df] text-gray-900 transition-colors duration-300 dark:bg-[#061720] dark:text-white">
    <DarkVideoBackground blurred />

    <header class="relative z-20 border-b border-white/30 bg-white/35 backdrop-blur-xl dark:border-white/10 dark:bg-[#061720]/45">
      <nav class="mx-auto flex h-16 max-w-[1480px] items-center justify-between gap-3 px-4 sm:px-6 lg:px-8">
        <RouterLink to="/home" class="flex min-w-0 items-center gap-3">
          <img :src="logoUrl" :alt="siteName" class="h-9 w-9 flex-none object-contain" />
          <span class="truncate text-base font-semibold text-gray-950 dark:text-white">{{ siteName }}</span>
        </RouterLink>

        <div class="flex flex-none items-center gap-1.5 sm:gap-2">
          <LocaleSwitcher />
          <button
            type="button"
            class="btn-ghost btn-icon"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="handleThemeToggle"
          >
            <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
          </button>
          <RouterLink to="/login" class="btn btn-secondary btn-sm hidden sm:inline-flex">
            {{ t('home.login') }}
          </RouterLink>
          <RouterLink to="/register" class="btn btn-primary btn-sm">
            {{ t('auth.signUp') }}
          </RouterLink>
        </div>
      </nav>
    </header>

    <main class="relative z-10 mx-auto max-w-[1480px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
      <slot />
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import DarkVideoBackground from '@/components/common/DarkVideoBackground.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import { getCurrentTheme, toggleTheme } from '@/utils/theme'
import { sanitizeUrl } from '@/utils/url'

const { t } = useI18n()
const appStore = useAppStore()
const isDark = ref(getCurrentTheme() === 'dark')

const siteName = computed(() => appStore.siteName || 'XIASS API')
const logoUrl = computed(() => sanitizeUrl(appStore.siteLogo, {
  allowRelative: true,
  allowDataUrl: true,
}) || (isDark.value ? '/brand/xiass-mark-dark.png' : '/brand/xiass-mark-light.png'))

function handleThemeToggle(): void {
  isDark.value = toggleTheme() === 'dark'
}
</script>

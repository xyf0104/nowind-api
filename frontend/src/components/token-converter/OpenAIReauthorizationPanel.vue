<template>
  <section class="card overflow-hidden" aria-labelledby="openai-reauthorization-title">
    <div class="flex flex-col gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between sm:px-5">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <h2 id="openai-reauthorization-title" class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('tokenConverter.reauthorization.title') }}
          </h2>
          <span class="rounded bg-emerald-50 px-2 py-1 text-xs font-semibold text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300">
            {{ t('tokenConverter.reauthorization.free') }}
          </span>
        </div>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {{ t('tokenConverter.reauthorization.description') }}
        </p>
      </div>
      <button
        type="button"
        class="btn btn-primary btn-sm w-full justify-center sm:w-auto"
        :disabled="starting"
        @click="startAuthorization"
      >
        <span v-if="starting" class="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
        <Icon v-else name="key" size="sm" />
        {{ starting ? t('tokenConverter.reauthorization.generating') : t('tokenConverter.reauthorization.generate') }}
      </button>
    </div>

    <div
      v-if="errorMessage"
      class="border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/25 dark:text-red-300 sm:px-5"
      role="alert"
    >
      {{ errorMessage }}
    </div>

    <div v-if="sessionId" class="grid gap-4 px-4 py-4 sm:px-5 lg:grid-cols-[minmax(0,0.78fr)_minmax(0,1.22fr)]">
      <div class="min-w-0 rounded-md border border-primary-200 bg-primary-50/70 p-4 dark:border-primary-900/60 dark:bg-primary-950/25">
        <div class="flex items-start gap-3">
          <span class="flex h-7 w-7 flex-none items-center justify-center rounded bg-primary-600 text-sm font-semibold text-white">1</span>
          <div class="min-w-0 flex-1">
            <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('tokenConverter.reauthorization.openLogin') }}</p>
            <p class="mt-1 text-xs leading-5 text-gray-600 dark:text-gray-400">{{ t('tokenConverter.reauthorization.openLoginHint') }}</p>
            <a
              :href="authUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="btn btn-secondary btn-sm mt-3 w-full justify-center"
            >
              <Icon name="externalLink" size="sm" />
              {{ t('tokenConverter.reauthorization.openOpenAI') }}
            </a>
          </div>
        </div>
      </div>

      <div class="min-w-0 rounded-md border border-gray-200 p-4 dark:border-dark-600">
        <div class="flex items-start gap-3">
          <span class="flex h-7 w-7 flex-none items-center justify-center rounded bg-primary-600 text-sm font-semibold text-white">2</span>
          <div class="min-w-0 flex-1">
            <label for="openai-oauth-callback" class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('tokenConverter.reauthorization.pasteCallback') }}
            </label>
            <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('tokenConverter.reauthorization.pasteCallbackHint') }}</p>
            <div class="mt-3 flex min-w-0 flex-col gap-2 sm:flex-row">
              <input
                id="openai-oauth-callback"
                v-model="callbackInput"
                type="text"
                class="input h-10 min-w-0 flex-1 font-mono text-xs"
                :placeholder="t('tokenConverter.reauthorization.callbackPlaceholder')"
                autocomplete="off"
                spellcheck="false"
                maxlength="10000"
                @keydown.enter="exchangeCode"
              />
              <button
                type="button"
                class="btn btn-primary btn-sm h-10 w-full justify-center sm:w-auto"
                :disabled="exchanging || !callbackInput.trim()"
                @click="exchangeCode"
              >
                <span v-if="exchanging" class="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
                <Icon v-else name="download" size="sm" />
                {{ exchanging ? t('tokenConverter.reauthorization.exchanging') : t('tokenConverter.reauthorization.getToken') }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { exchangeOpenAIReauthorization, startOpenAIReauthorization } from '@/api/tokenTools'

const emit = defineEmits<{
  (event: 'tokens', tokenJson: string): void
}>()

const { t } = useI18n()
const starting = ref(false)
const exchanging = ref(false)
const authUrl = ref('')
const sessionId = ref('')
const expectedState = ref('')
const callbackInput = ref('')
const errorMessage = ref('')

function stateFromAuthorizationURL(value: string): string {
  try {
    return new URL(value).searchParams.get('state')?.trim() ?? ''
  } catch {
    return ''
  }
}

function parseCallbackInput(value: string): { code: string; state: string; error?: string } {
  const trimmed = value.trim()
  const looksLikeURL = /^https?:\/\//i.test(trimmed)
  if (!looksLikeURL) {
    return { code: trimmed, state: expectedState.value }
  }

  try {
    const callbackURL = new URL(trimmed)
    const validCallback = callbackURL.protocol === 'http:'
      && callbackURL.hostname === 'localhost'
      && callbackURL.port === '1455'
      && callbackURL.pathname === '/auth/callback'
      && !callbackURL.username
      && !callbackURL.password
      && !callbackURL.hash
    if (!validCallback) {
      return { code: '', state: '', error: t('tokenConverter.reauthorization.invalidCallback') }
    }
    const oauthError = callbackURL.searchParams.get('error_description') || callbackURL.searchParams.get('error')
    if (oauthError) return { code: '', state: '', error: oauthError }
    return {
      code: callbackURL.searchParams.get('code')?.trim() ?? '',
      state: callbackURL.searchParams.get('state')?.trim() ?? '',
    }
  } catch {
    return { code: '', state: '', error: t('tokenConverter.reauthorization.invalidCallback') }
  }
}

async function startAuthorization(): Promise<void> {
  if (starting.value) return
  starting.value = true
  errorMessage.value = ''
  callbackInput.value = ''
  authUrl.value = ''
  sessionId.value = ''
  expectedState.value = ''
  try {
    const result = await startOpenAIReauthorization()
    authUrl.value = result.auth_url
    sessionId.value = result.session_id
    expectedState.value = stateFromAuthorizationURL(result.auth_url)
  } catch (error) {
    const message = (error as { message?: string })?.message
    errorMessage.value = message || t('tokenConverter.reauthorization.generateFailed')
  } finally {
    starting.value = false
  }
}

async function exchangeCode(): Promise<void> {
  if (exchanging.value || !sessionId.value) return
  errorMessage.value = ''
  const parsed = parseCallbackInput(callbackInput.value)
  if (parsed.error) {
    errorMessage.value = parsed.error
    return
  }
  if (!parsed.code || !parsed.state) {
    errorMessage.value = t('tokenConverter.reauthorization.missingCodeOrState')
    return
  }

  exchanging.value = true
  try {
    const token = await exchangeOpenAIReauthorization({
      session_id: sessionId.value,
      code: parsed.code,
      state: parsed.state,
    })
    emit('tokens', JSON.stringify(token, null, 2))
    callbackInput.value = ''
    authUrl.value = ''
    sessionId.value = ''
    expectedState.value = ''
  } catch (error) {
    const message = (error as { message?: string })?.message
    errorMessage.value = message || t('tokenConverter.reauthorization.exchangeFailed')
  } finally {
    exchanging.value = false
  }
}
</script>

<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-3xl px-4 py-8 sm:px-6">
      <div class="mb-6">
        <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
          {{ t('codexHelper.title') }}
        </h1>
        <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">
          {{ t('codexHelper.description') }}
        </p>
      </div>

      <div
        v-if="errorMessage"
        class="mb-5 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ errorMessage }}
      </div>

      <div
        v-if="loading"
        class="flex min-h-48 items-center justify-center text-gray-500 dark:text-gray-400"
      >
        <Icon name="refresh" size="lg" class="mr-2 animate-spin" />
        {{ t('common.loading') }}
      </div>

      <div v-else-if="connection && compatibleKeys.length" class="space-y-3">
        <div
          v-for="key in compatibleKeys"
          :key="key.id"
          class="flex flex-col gap-4 rounded-lg border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-600 dark:bg-dark-800 sm:flex-row sm:items-center sm:justify-between"
        >
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium text-gray-900 dark:text-white">{{ key.name }}</span>
              <span class="badge badge-success">{{ t('common.active') }}</span>
            </div>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ key.group?.name }} · <code>{{ maskApiKey(key.key) }}</code>
            </p>
          </div>
          <div v-if="modelStates[key.id]" class="w-full border-t border-gray-100 pt-4 dark:border-dark-600 sm:max-w-xl sm:border-l sm:border-t-0 sm:pl-4 sm:pt-0">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <div>
                <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('codexHelper.modelSelection') }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('codexHelper.modelSource') }}</p>
              </div>
              <button type="button" class="btn btn-secondary flex-shrink-0" :disabled="modelStates[key.id].loading" @click="syncModels(key)">
                <Icon name="refresh" size="sm" class="mr-2" :class="{ 'animate-spin': modelStates[key.id].loading }" />
                {{ modelStates[key.id].loading ? t('common.loading') : t('codexHelper.syncModels') }}
              </button>
            </div>
            <p v-if="modelStates[key.id].error" class="mt-2 text-xs text-red-600 dark:text-red-300">
              {{ modelStates[key.id].error }}
            </p>
            <div class="mt-3 grid gap-3 sm:grid-cols-2">
              <label class="block text-sm text-gray-700 dark:text-gray-300">
                {{ t('codexHelper.sessionModel') }}
                <select v-model="modelStates[key.id].model" class="form-select mt-1 w-full" :disabled="!modelStates[key.id].models.length">
                  <option v-if="!modelStates[key.id].models.length" :value="defaultHelperModel">{{ defaultHelperModel }}</option>
                  <option v-for="model in modelStates[key.id].models" :key="`session-${key.id}-${model}`" :value="model">{{ model }}</option>
                </select>
              </label>
              <label class="block text-sm text-gray-700 dark:text-gray-300">
                {{ t('codexHelper.reviewModel') }}
                <select v-model="modelStates[key.id].review_model" class="form-select mt-1 w-full" :disabled="!modelStates[key.id].models.length">
                  <option v-if="!modelStates[key.id].models.length" :value="defaultHelperModel">{{ defaultHelperModel }}</option>
                  <option v-for="model in modelStates[key.id].models" :key="`review-${key.id}-${model}`" :value="model">{{ model }}</option>
                </select>
              </label>
            </div>
            <button type="button" class="btn btn-primary mt-3 w-full justify-center" @click="connectKey(key)">
              <Icon name="check" size="sm" class="mr-2" />
              {{ t('codexHelper.useThisKey') }}
            </button>
          </div>
        </div>
      </div>

      <div
        v-else-if="!loading && !errorMessage"
        class="rounded-lg border border-gray-200 bg-white p-6 text-center dark:border-dark-600 dark:bg-dark-800"
      >
        <p class="font-medium text-gray-900 dark:text-white">{{ t('codexHelper.noKeys') }}</p>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('codexHelper.noKeysHint') }}</p>
        <RouterLink to="/keys" class="btn btn-primary mt-4 inline-flex">
          {{ t('codexHelper.manageKeys') }}
        </RouterLink>
      </div>

      <div class="mt-6 rounded-lg border border-blue-100 bg-blue-50 p-4 text-sm text-blue-800 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-200">
        {{ t('codexHelper.securityNote') }}
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { authAPI, keysAPI } from '@/api'
import type { ApiKey } from '@/types'
import { maskApiKey } from '@/utils/maskApiKey'
import {
  buildCodexHelperCallback,
  fetchCodexCompatibleModels,
  isCodexCompatibleKey,
  parseCodexHelperConnection,
  type CodexHelperConnection
} from '@/utils/codexHelper'

const defaultHelperModel = 'gpt-5.6-sol'

interface KeyModelState {
  loading: boolean
  models: string[]
  model: string
  review_model: string
  error: string
}

const route = useRoute()
const { t } = useI18n()
const loading = ref(true)
const errorMessage = ref('')
const compatibleKeys = ref<ApiKey[]>([])
const modelStates = ref<Record<number, KeyModelState>>({})
const connection = ref<CodexHelperConnection | null>(null)
const apiBaseUrl = ref('')

function queryString(value: unknown): string | undefined {
  if (typeof value === 'string') return value
  if (Array.isArray(value) && typeof value[0] === 'string') return value[0]
  return undefined
}

async function loadKeys(): Promise<ApiKey[]> {
  const items: ApiKey[] = []
  let page = 1
  let pages = 1
  do {
    const response = await keysAPI.list(page, 100, { status: 'active', sort_by: 'created_at', sort_order: 'desc' })
    items.push(...response.items)
    pages = Math.min(response.pages || 1, 20)
    page += 1
  } while (page <= pages)
  return items
}

async function initialize() {
  try {
    connection.value = parseCodexHelperConnection(
      queryString(route.query.callback),
      queryString(route.query.state)
    )
    const [settings, keys] = await Promise.all([authAPI.getPublicSettings(), loadKeys()])
    apiBaseUrl.value = settings.api_base_url || window.location.origin
    compatibleKeys.value = keys.filter(isCodexCompatibleKey)
    modelStates.value = Object.fromEntries(
      compatibleKeys.value.map((key) => [key.id, {
        loading: false,
        models: [],
        model: defaultHelperModel,
        review_model: defaultHelperModel,
        error: ''
      }])
    )
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : t('codexHelper.loadFailed')
  } finally {
    loading.value = false
  }
}

async function syncModels(key: ApiKey) {
  const state = modelStates.value[key.id]
  if (!state) return
  state.loading = true
  state.error = ''
  try {
    const models = await fetchCodexCompatibleModels(apiBaseUrl.value, key.key)
    state.models = models
    if (!models.includes(state.model)) state.model = models.includes(defaultHelperModel) ? defaultHelperModel : models[0]
    if (!models.includes(state.review_model)) state.review_model = state.model
  } catch (error) {
    state.error = error instanceof Error ? error.message : t('codexHelper.modelsLoadFailed')
  } finally {
    state.loading = false
  }
}

function connectKey(key: ApiKey) {
  if (!connection.value) return
  const state = modelStates.value[key.id]
  if (!state?.model.trim()) {
    errorMessage.value = t('codexHelper.modelRequired')
    return
  }
  try {
    window.location.assign(buildCodexHelperCallback(connection.value, apiBaseUrl.value, key, {
      model: state.model,
      review_model: state.review_model || state.model
    }))
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : t('codexHelper.connectFailed')
  }
}

onMounted(initialize)
</script>

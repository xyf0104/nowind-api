import { apiClient } from './client'

export interface OpenAIReauthorizationStart {
  auth_url: string
  session_id: string
}

export interface OpenAIReauthorizationToken {
  access_token: string
  refresh_token: string
  id_token?: string
  expires_in: number
  expires_at: number
  client_id?: string
  email?: string
  chatgpt_account_id?: string
  chatgpt_user_id?: string
  organization_id?: string
  plan_type?: string
}

export async function startOpenAIReauthorization(): Promise<OpenAIReauthorizationStart> {
  const { data } = await apiClient.post<OpenAIReauthorizationStart>('/tools/openai-oauth/authorize', {})
  return data
}

export async function exchangeOpenAIReauthorization(input: {
  session_id: string
  code: string
  state: string
}): Promise<OpenAIReauthorizationToken> {
  const { data } = await apiClient.post<OpenAIReauthorizationToken>('/tools/openai-oauth/exchange', input)
  return data
}

export default {
  startOpenAIReauthorization,
  exchangeOpenAIReauthorization,
}

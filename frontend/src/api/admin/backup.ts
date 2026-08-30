import { apiClient } from '../client'

export interface BackupS3Config {
  endpoint: string
  region: string
  bucket: string
  access_key_id: string
  secret_access_key?: string
  prefix: string
  force_path_style: boolean
}

export interface BackupScheduleConfig {
  enabled: boolean
  cron_expr: string
  retain_days: number
  retain_count: number
}

export interface BackupRecord {
  id: string
  status: 'pending' | 'running' | 'completed' | 'failed'
  backup_type: string
  file_name: string
  s3_key: string
  size_bytes: number
  triggered_by: string
  error_message?: string
  started_at: string
  finished_at?: string
  expires_at?: string
  progress?: string
  restore_status?: string
  restore_error?: string
  restored_at?: string
}

export interface CreateBackupRequest {
  expire_days?: number
}

export interface TestS3Response {
  ok: boolean
  message: string
}

export interface RuntimeExportRecord {
  id: string
  status: 'queued' | 'running' | 'completed' | 'failed'
  file_name: string
  size_bytes: number
  sha256?: string
  started_at: string
  finished_at?: string
  error_message?: string
}

export interface RuntimeExportDownload {
  blob: Blob
  fileName: string
}

// S3 Config
export async function getS3Config(): Promise<BackupS3Config> {
  const { data } = await apiClient.get<BackupS3Config>('/admin/backups/s3-config')
  return data
}

export async function updateS3Config(config: BackupS3Config): Promise<BackupS3Config> {
  const { data } = await apiClient.put<BackupS3Config>('/admin/backups/s3-config', config)
  return data
}

export async function testS3Connection(config: BackupS3Config): Promise<TestS3Response> {
  const { data } = await apiClient.post<TestS3Response>('/admin/backups/s3-config/test', config)
  return data
}

// Async image object storage
//
// Shares the S3 client with backups, so `reuse_backup_s3` borrows the endpoint and
// credentials configured above and only keeps its own bucket/prefix.
export interface ImageStorageConfig {
  enabled: boolean
  reuse_backup_s3: boolean
  bucket: string
  prefix: string
  public_base_url: string
  presign_expiry_hours: number
  max_download_bytes: number
  endpoint: string
  region: string
  access_key_id: string
  secret_access_key?: string
  force_path_style: boolean
}

export interface ImageStorageConfigResponse {
  config: ImageStorageConfig
  secret_configured: boolean
}

export async function getImageStorageConfig(): Promise<ImageStorageConfigResponse> {
  const { data } = await apiClient.get<ImageStorageConfigResponse>('/admin/backups/image-storage')
  return data
}

export async function updateImageStorageConfig(
  config: ImageStorageConfig,
): Promise<ImageStorageConfig> {
  const { data } = await apiClient.put<ImageStorageConfig>('/admin/backups/image-storage', config)
  return data
}

export async function testImageStorageConnection(
  config: ImageStorageConfig,
): Promise<TestS3Response> {
  const { data } = await apiClient.post<TestS3Response>(
    '/admin/backups/image-storage/test',
    config,
  )
  return data
}

// Schedule
export async function getSchedule(): Promise<BackupScheduleConfig> {
  const { data } = await apiClient.get<BackupScheduleConfig>('/admin/backups/schedule')
  return data
}

export async function updateSchedule(config: BackupScheduleConfig): Promise<BackupScheduleConfig> {
  const { data } = await apiClient.put<BackupScheduleConfig>('/admin/backups/schedule', config)
  return data
}

// Backup operations
export async function createBackup(req?: CreateBackupRequest): Promise<BackupRecord> {
  const { data } = await apiClient.post<BackupRecord>('/admin/backups', req || {})
  return data
}

export async function listBackups(): Promise<{ items: BackupRecord[] }> {
  const { data } = await apiClient.get<{ items: BackupRecord[] }>('/admin/backups')
  return data
}

export async function getBackup(id: string): Promise<BackupRecord> {
  const { data } = await apiClient.get<BackupRecord>(`/admin/backups/${id}`)
  return data
}

export async function deleteBackup(id: string): Promise<void> {
  await apiClient.delete(`/admin/backups/${id}`)
}

export async function getDownloadURL(id: string): Promise<{ url: string }> {
  const { data } = await apiClient.get<{ url: string }>(`/admin/backups/${id}/download-url`)
  return data
}

// Full migration packages are generated on the current Docker host and kept
// inside XIASS application storage until an administrator downloads or deletes
// them. All mutation and download calls are step-up protected server-side.
export async function createRuntimeExport(): Promise<RuntimeExportRecord> {
  const { data } = await apiClient.post<RuntimeExportRecord>('/admin/backups/runtime-exports')
  return data
}

export async function listRuntimeExports(): Promise<{ items: RuntimeExportRecord[] }> {
  const { data } = await apiClient.get<{ items: RuntimeExportRecord[] }>('/admin/backups/runtime-exports')
  return data
}

export async function downloadRuntimeExport(id: string): Promise<RuntimeExportDownload> {
  // Keep non-2xx replies in the success interceptor so a JSON STEP_UP_REQUIRED
  // envelope can still be decoded even though the success response is a blob.
  const response = await apiClient.get<Blob>(`/admin/backups/runtime-exports/${id}/download`, {
    responseType: 'blob',
    validateStatus: () => true,
  })
  if (response.status < 200 || response.status >= 300) {
    const body = await response.data.text().catch(() => '')
    let payload: { code?: string | number; reason?: string; message?: string; metadata?: Record<string, unknown> } = {}
    try {
      payload = JSON.parse(body) as typeof payload
    } catch {
      // The generic error below is intentional: a download error response must
      // not be treated as archive content or leak a reverse-proxy error page.
    }
    return Promise.reject({
      status: response.status,
      code: payload.code,
      reason: payload.reason,
      message: payload.message || 'Migration package download failed',
      metadata: payload.metadata,
    })
  }
  const disposition = String(response.headers?.['content-disposition'] || '')
  const encodedName = disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1]
  const quotedName = disposition.match(/filename="([^"]+)"/i)?.[1]
  const fileName = encodedName ? decodeURIComponent(encodedName) : quotedName || `xiass-migration-${id}.tar.gz`
  return { blob: response.data, fileName }
}

export async function deleteRuntimeExport(id: string): Promise<void> {
  await apiClient.delete(`/admin/backups/runtime-exports/${id}`)
}

// Restore
export async function restoreBackup(id: string, password: string): Promise<BackupRecord> {
  const { data } = await apiClient.post<BackupRecord>(`/admin/backups/${id}/restore`, { password })
  return data
}

export const backupAPI = {
  getS3Config,
  updateS3Config,
  testS3Connection,
  getImageStorageConfig,
  updateImageStorageConfig,
  testImageStorageConnection,
  getSchedule,
  updateSchedule,
  createBackup,
  listBackups,
  getBackup,
  deleteBackup,
  getDownloadURL,
  createRuntimeExport,
  listRuntimeExports,
  downloadRuntimeExport,
  deleteRuntimeExport,
  restoreBackup,
}

export default backupAPI

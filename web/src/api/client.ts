import type { Issue } from './issues'
import type { AttentionItem, CampaignView, GenerateResponse, RevisionRequest, Stats } from './types'

export class ApiError extends Error {
  status: number
  issues: Issue[]

  constructor(status: number, issues: Issue[]) {
    super(issues.map((i) => i.message).join('; ') || `HTTP ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.issues = issues
  }
}

export async function errorFrom(res: Response): Promise<ApiError> {
  const text = await res.text().catch(() => '')
  try {
    const body = JSON.parse(text)
    if (Array.isArray(body?.errors)) return new ApiError(res.status, body.errors)
  } catch {
  }
  return new ApiError(res.status, [{ path: '', code: `http_${res.status}`, message: text.slice(0, 200) || res.statusText }])
}

export interface ClientOptions {
  token: () => string | null
  baseUrl?: string
  fetch?: typeof fetch
  onUnauthorized?: () => void
}

export function createApi(opts: ClientOptions) {
  const base = opts.baseUrl ?? ''
  const doFetch = opts.fetch ?? ((input, init) => fetch(input, init))

  async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    const headers: Record<string, string> = { Authorization: `Bearer ${opts.token() ?? ''}` }
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    const res = await doFetch(base + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
    if (!res.ok) {
      const err = await errorFrom(res)
      if (res.status === 401) opts.onUnauthorized?.()
      throw err
    }
    return (await res.json()) as T
  }

  const c = (id: string) => `/api/campaigns/${encodeURIComponent(id)}`
  return {
    createCampaign: (brief: string, campaignUrl: string) =>
      request<CampaignView>('POST', '/api/campaigns', { brief, campaign_url: campaignUrl }),
    getCampaign: (id: string, signal?: AbortSignal) => request<CampaignView>('GET', c(id), undefined, signal),
    generate: (id: string, version: number) => request<GenerateResponse>('POST', `${c(id)}/generate`, { version }),
    saveRevision: (id: string, body: RevisionRequest) => request<CampaignView>('POST', `${c(id)}/revisions`, body),
    approve: (id: string, revisionId: string, version: number) =>
      request<CampaignView>('POST', `${c(id)}/approve`, { revision_id: revisionId, version }),
    reject: (id: string, version: number) => request<CampaignView>('POST', `${c(id)}/reject`, { version }),
    cancel: (id: string, version: number) => request<CampaignView>('POST', `${c(id)}/cancel`, { version }),
    getStats: (id: string, signal?: AbortSignal) => request<Stats>('GET', `${c(id)}/stats`, undefined, signal),
    getAttention: async (id: string, signal?: AbortSignal) =>
      (await request<{ messages: AttentionItem[] }>('GET', `${c(id)}/messages?filter=attention`, undefined, signal)).messages,
  }
}

export type Api = ReturnType<typeof createApi>

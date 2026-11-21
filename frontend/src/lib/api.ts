// A small fetch wrapper for the GotATicket API.
//
// The access token lives in memory only. The refresh token is an HttpOnly cookie the
// browser sends to /api/v1/auth/refresh, so it is never readable by JavaScript. When a
// request fails because the access token expired, the client refreshes once and retries.

import type { Paginated } from './types'

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: Record<string, string>

  constructor(status: number, code: string, message: string, fields: Record<string, string> = {}) {
    super(message)
    this.status = status
    this.code = code
    this.fields = fields
  }
}

let accessToken: string | null = null
let refreshing: Promise<string | null> | null = null
let refreshUnreachable = false
let onSessionExpired: (() => void) | null = null

export function setAccessToken(token: string | null) {
  accessToken = token
}

export function onExpiredSession(handler: () => void) {
  onSessionExpired = handler
}

/**
 * Whether the last session refresh failed because the server couldn't be reached, as
 * opposed to the server saying the user is signed out. Offline, the session is unknown.
 */
export function lastRefreshUnreachable(): boolean {
  return refreshUnreachable
}

/** Exchanges the refresh cookie for a new access token. Concurrent callers share one request. */
export function refreshSession(): Promise<string | null> {
  if (!refreshing) {
    refreshing = fetch('/api/v1/auth/refresh', { method: 'POST', credentials: 'include' })
      .then(async (res) => {
        refreshUnreachable = false
        if (!res.ok) return null
        const body = await res.json()
        return body.data.access_token as string
      })
      .catch(() => {
        refreshUnreachable = true
        return null
      })
      .then((token) => {
        accessToken = token
        refreshing = null
        return token
      })
  }
  return refreshing
}

type Query = Record<string, string | number | boolean | null | undefined>

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  body?: unknown
  query?: Query
  signal?: AbortSignal
}

function buildUrl(path: string, query?: Query) {
  const url = new URL(path.startsWith('/api') ? path : `/api/v1${path}`, window.location.origin)
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value !== undefined && value !== null && value !== '') url.searchParams.set(key, String(value))
  }
  return url.toString()
}

async function send(path: string, options: RequestOptions, retry = true): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  let body: BodyInit | undefined
  if (options.body instanceof FormData) {
    body = options.body
  } else if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(options.body)
  }
  if (accessToken) headers.Authorization = `Bearer ${accessToken}`

  const res = await fetch(buildUrl(path, options.query), {
    method: options.method ?? 'GET',
    headers,
    body,
    credentials: 'include',
    signal: options.signal,
  })
  if (res.status === 401 && retry && accessToken) {
    const token = await refreshSession()
    if (token) return send(path, options, false)
    onSessionExpired?.()
  }
  return res
}

async function toError(res: Response): Promise<ApiError> {
  try {
    const body = await res.json()
    const e = body.error ?? {}
    return new ApiError(res.status, e.code ?? 'error', e.message ?? res.statusText, e.fields ?? {})
  } catch {
    return new ApiError(res.status, 'network_error', 'The server could not be reached. Please try again.')
  }
}

/** The request never got an answer from the API: no connection, or the server is down. */
export function isNetworkError(e: unknown): boolean {
  return e instanceof TypeError || (e instanceof ApiError && e.code === 'network_error')
}

/** Calls the API and returns the `data` member of the response. */
export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const res = await send(path, options)
  if (!res.ok) throw await toError(res)
  if (res.status === 204) return undefined as T
  const body = await res.json()
  return body.data as T
}

/** Calls a paginated endpoint and returns rows with page metadata. */
export async function apiPage<T>(path: string, query: Query = {}): Promise<Paginated<T>> {
  const res = await send(path, { query })
  if (!res.ok) throw await toError(res)
  return (await res.json()) as Paginated<T>
}

/** Downloads a file from an authenticated endpoint. */
export async function download(path: string, query: Query = {}, fallbackName = 'download') {
  const res = await send(path, { query })
  if (!res.ok) throw await toError(res)
  const disposition = res.headers.get('Content-Disposition') ?? ''
  const name = /filename="([^"]+)"/.exec(disposition)?.[1] ?? fallbackName
  const url = URL.createObjectURL(await res.blob())
  const link = document.createElement('a')
  link.href = url
  link.download = name
  document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** Returns the human-readable message of any error. */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message
  if (err instanceof Error) return err.message
  return 'Something went wrong.'
}

/** Returns field-level validation errors, if any. */
export function fieldErrors(err: unknown): Record<string, string> {
  return err instanceof ApiError ? err.fields : {}
}

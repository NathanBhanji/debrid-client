// Minimal typed API client over the generated OpenAPI types.
// Browser requests authenticate with the HttpOnly session cookie; only the
// one-time setup endpoints carry the API key (proof of server ownership).
// Known limitation: URLs are root-absolute, so the UI only works with an
// empty server.base_path (the default) — see web/README.md.
import type { components } from './api.gen'

export type Status = components['schemas']['Status']
export type Torrent = components['schemas']['Torrent']
export type Account = components['schemas']['Account']
export type Settings = components['schemas']['Settings']
export type Health = components['schemas']['HealthOutBody']
export type Download = components['schemas']['Download']
export type User = components['schemas']['User']
export type AddAccount = components['schemas']['AddAccountInBody']
export type UpdateAccount = components['schemas']['UpdateAccountInBody']
export type ProviderKind = Account['kind']
export type AuthStatus = components['schemas']['AuthStatusOutBody']
export type Me = components['schemas']['MeOutBody']

export const PROVIDER_KINDS: Array<ProviderKind> = [
  'torbox',
  'realdebrid',
  'alldebrid',
  'premiumize',
  'debridlink',
]

export const PROVIDER_LABELS: Record<ProviderKind, string> = {
  torbox: 'TorBox',
  realdebrid: 'Real-Debrid',
  alldebrid: 'AllDebrid',
  premiumize: 'Premiumize',
  debridlink: 'Debrid-Link',
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
  }
}

// Session expiry surfaces as a window event so the auth gate can flip to the
// login screen no matter which call hit the 401.
export const UNAUTHORIZED_EVENT = 'debrid:unauthorized'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    ...init,
    headers: {
      ...(init?.headers ?? {}),
      ...(init?.body && typeof init.body === 'string'
        ? { 'Content-Type': 'application/json' }
        : {}),
    },
  })
  if (res.status === 401 && !path.startsWith('/auth/')) {
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
  }
  if (!res.ok) {
    let detail = `http ${res.status}`
    try {
      const body = (await res.json()) as { title?: string; detail?: string }
      detail = body.detail ?? body.title ?? detail
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(res.status, detail)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  health: () => request<Health>('/health'),
  status: () => request<Status>('/system/status'),
  torrents: {
    list: () => request<Array<Torrent>>('/torrents'),
    get: (id: string) =>
      request<Torrent>(`/torrents/${encodeURIComponent(id)}`),
    addMagnet: (magnet: string, category?: string) =>
      request<Torrent>('/torrents', {
        method: 'POST',
        body: JSON.stringify({ magnet, ...(category ? { category } : {}) }),
      }),
    remove: (id: string, opts?: { files?: boolean; provider?: boolean }) =>
      request<void>(
        `/torrents/${encodeURIComponent(id)}?files=${opts?.files ?? false}&provider=${opts?.provider ?? false}`,
        { method: 'DELETE' },
      ),
    retry: (id: string) =>
      request<Torrent>(`/torrents/${encodeURIComponent(id)}/retry`, {
        method: 'POST',
      }),
    // FormData bodies get their multipart boundary from the browser; request()
    // only forces Content-Type for string bodies, so this stays correct.
    addFile: (file: File, category?: string) => {
      const form = new FormData()
      form.append('file', file)
      if (category) form.append('category', category)
      return request<Torrent>('/torrents/file', { method: 'POST', body: form })
    },
    selectFiles: (id: string, fileIds: Array<string>) =>
      request<Torrent>(`/torrents/${encodeURIComponent(id)}/files`, {
        method: 'PUT',
        body: JSON.stringify({ file_ids: fileIds }),
      }),
  },
  downloads: {
    retry: (id: string) =>
      request<Download>(`/downloads/${encodeURIComponent(id)}/retry`, {
        method: 'POST',
      }),
  },
  accounts: {
    list: () => request<Array<Account>>('/accounts'),
    add: (body: AddAccount) =>
      request<Account>('/accounts', {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    update: (id: string, body: UpdateAccount) =>
      request<Account>(`/accounts/${encodeURIComponent(id)}`, {
        method: 'PATCH',
        body: JSON.stringify(body),
      }),
    remove: (id: string, opts?: { force?: boolean }) =>
      request<void>(
        `/accounts/${encodeURIComponent(id)}?force=${opts?.force ?? false}`,
        { method: 'DELETE' },
      ),
    test: (id: string) =>
      request<User>(`/accounts/${encodeURIComponent(id)}/test`, {
        method: 'POST',
      }),
  },
  settings: {
    get: () => request<Settings>('/settings'),
    update: (body: Settings) =>
      request<Settings>('/settings', {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
  },
  auth: {
    status: () => request<AuthStatus>('/auth/status'),
    me: () => request<Me>('/auth/me'),
    login: (username: string, password: string) =>
      request<{ username: string }>('/auth/login', {
        method: 'POST',
        body: JSON.stringify({ username, password }),
      }),
    logout: () => request<void>('/auth/logout', { method: 'POST' }),
    changePassword: (currentPassword: string, newPassword: string) =>
      request<void>('/auth/password', {
        method: 'POST',
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      }),
    // The two setup calls are the only browser requests that carry the API
    // key: onboarding proves server ownership with it, exactly once.
    setupPassword: (apiKey: string, username: string, password: string) =>
      request<{ username: string }>('/auth/setup/password', {
        method: 'POST',
        headers: { Authorization: `Bearer ${apiKey}` },
        body: JSON.stringify({ username, password }),
      }),
    setupOIDC: (
      apiKey: string,
      issuer: string,
      clientId: string,
      clientSecret: string,
    ) =>
      request<{ auth_url: string }>('/auth/setup/oidc', {
        method: 'POST',
        headers: { Authorization: `Bearer ${apiKey}` },
        body: JSON.stringify({
          issuer,
          client_id: clientId,
          ...(clientSecret ? { client_secret: clientSecret } : {}),
        }),
      }),
  },
}

// Server-sent events stream, authenticated by the session cookie.
// EventSource reconnects on its own; payloads are notifications, so callers
// re-fetch the resource rather than reading event data.
export function openEvents(
  onEvent: (type: string, data: unknown) => void,
  onFatal?: () => void,
): () => void {
  const es = new EventSource('/api/v1/events')
  // EventSource retries transient drops itself; CLOSED means it gave up
  // (e.g. the server rejected the key), so let the caller reconnect.
  es.onerror = () => {
    if (es.readyState === EventSource.CLOSED) onFatal?.()
  }
  const types = [
    'torrent.added',
    'torrent.updated',
    'torrent.deleted',
    'download.updated',
    'account.changed',
    'settings.changed',
    'heartbeat',
  ]
  for (const t of types) {
    es.addEventListener(t, (ev) => onEvent(t, JSON.parse(ev.data as string)))
  }
  return () => es.close()
}

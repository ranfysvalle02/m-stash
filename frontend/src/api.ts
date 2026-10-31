export interface UserClaims {
  uid: string
  email: string
  role: string
}

export interface Namespace {
  id: string
  slug: string
  kind: 'personal' | 'shared'
  displayName: string
  bio?: string
  avatarURL?: string
  isPublic: boolean
  createdAt: string
  updatedAt: string
}

export interface NamespaceIdentityInput {
  displayName: string
  bio: string
  avatarURL: string
  isPublic: boolean
}

export interface ResourceInput {
  slug: string
  title: string
  summary: string
  content: string
  data?: Record<string, unknown>
  visibility: 'private' | 'authenticated' | 'public'
}

export interface Resource extends ResourceInput {
  id: string
  type: string
  createdAt: string
  updatedAt: string
}

export interface ResourcePreview extends Omit<Resource, 'content' | 'data'> {}

export interface PublicNamespace {
  slug: string
  displayName: string
  bio?: string
  avatarURL?: string
}

export interface PublicResourcePreview {
  type: string
  slug: string
  title: string
  summary?: string
  createdAt: string
  updatedAt: string
}

export interface PublicResource extends PublicResourcePreview {
  content?: string
  data?: Record<string, unknown>
}

export interface BuilderApp {
  id: string
  slug: string
  displayName: string
  publishableKey: string
  allowedOrigins?: string[]
  active: boolean
  createdAt: string
  updatedAt: string
}

export interface BuilderAppInput {
  slug: string
  displayName: string
  adminEmail?: string
  adminPassword?: string
  allowedOrigins?: string[]
}

export interface Page<T> {
  data: T[]
  page: {
    limit: number
    nextCursor: string | null
  }
}

interface APIEnvelope<T> {
  data: T
}

interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown
  retrySession?: boolean
}

export class APIError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'APIError'
    this.status = status
  }
}

function csrfToken() {
  const prefix = 'm_stash_csrf='
  const cookie = document.cookie.split('; ').find((entry) => entry.startsWith(prefix))
  return cookie ? decodeURIComponent(cookie.slice(prefix.length)) : ''
}

// Publishable application key identifying which app this UI talks to (set at deploy time).
function appKey() {
  const fromGlobal = (window as unknown as { __M_STASH_APP__?: string }).__M_STASH_APP__
  if (fromGlobal) return fromGlobal
  const meta = document.querySelector('meta[name="m-stash-app"]')
  return meta?.getAttribute('content') ?? ''
}

async function parseResponse<T>(response: Response): Promise<T> {
  if (response.status === 204) return undefined as T
  const payload = (await response.json().catch(() => ({}))) as { error?: string }
  if (!response.ok) throw new APIError(response.status, payload.error ?? 'The request could not be completed.')
  return payload as T
}

let refreshPromise: Promise<void> | undefined

async function refreshSession() {
  if (!refreshPromise) {
    const headers: Record<string, string> = { 'X-CSRF-Token': csrfToken() }
    const key = appKey()
    if (key) headers['X-M-Stash-App'] = key
    refreshPromise = fetch('/v1/auth/refresh', {
      method: 'POST',
      credentials: 'include',
      headers,
    }).then(async (response) => { await parseResponse(response) }).finally(() => { refreshPromise = undefined })
  }
  return refreshPromise
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { body, retrySession = true, headers: providedHeaders, ...fetchOptions } = options
  const headers = new Headers(providedHeaders)
  const method = (fetchOptions.method ?? 'GET').toUpperCase()
  if (body !== undefined) headers.set('Content-Type', 'application/json')
  const key = appKey()
  if (key) headers.set('X-M-Stash-App', key)
  if (['POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) {
    const token = csrfToken()
    if (token) headers.set('X-CSRF-Token', token)
  }

  const response = await fetch(path, {
    ...fetchOptions,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'include',
  })
  if (retrySession && (response.status === 401 || response.status === 403) && !path.startsWith('/v1/auth/')) {
    try {
      await refreshSession()
      return request<T>(path, { ...options, retrySession: false })
    } catch {
      // Preserve the original API error for callers.
    }
  }
  return parseResponse<T>(response)
}

export const api = {
  publishableKey: () => appKey(),

  async signUpWithNamespace(email: string, password: string, username: string) {
    return request<{ token: string; user: { id: string; email: string; role: string }; namespace: Namespace }>('/v1/auth/signup', {
      method: 'POST', body: { email, password, username }, retrySession: false,
    })
  },

  async logIn(email: string, password: string) {
    return request<APIEnvelope<{ id: string; email: string; role: string }>>('/v1/auth/login', {
      method: 'POST', body: { email, password }, retrySession: false,
    })
  },

  async logOut() {
    return request<void>('/v1/auth/logout', { method: 'POST', retrySession: false })
  },

  async verifySession() {
    const response = await request<{ active: boolean; claims: UserClaims }>('/v1/auth/verify', { retrySession: false })
    return response.claims
  },

  async restoreSession() {
    await refreshSession()
    return api.verifySession()
  },

  async getNamespace() {
    const response = await request<APIEnvelope<Namespace>>('/v1/me/namespace')
    return response.data
  },

  async saveNamespace(input: NamespaceIdentityInput) {
    const response = await request<APIEnvelope<Namespace>>('/v1/me/namespace', { method: 'PUT', body: input })
    return response.data
  },

  async listResources(type: string, cursor?: string | null) {
    const search = new URLSearchParams({ limit: '20' })
    if (cursor) search.set('cursor', cursor)
    return request<Page<ResourcePreview>>(`/v1/me/resources/${encodeURIComponent(type)}?${search.toString()}`)
  },

  async createResource(type: string, resource: ResourceInput) {
    const response = await request<APIEnvelope<Resource>>(`/v1/me/resources/${encodeURIComponent(type)}`, { method: 'POST', body: resource })
    return response.data
  },

  async getResource(type: string, slug: string) {
    const response = await request<APIEnvelope<Resource>>(`/v1/me/resources/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`)
    return response.data
  },

  async saveResource(type: string, slug: string, resource: ResourceInput) {
    const response = await request<APIEnvelope<Resource>>(`/v1/me/resources/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`, { method: 'PUT', body: resource })
    return response.data
  },

  async deleteResource(type: string, slug: string) {
    return request<void>(`/v1/me/resources/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`, { method: 'DELETE' })
  },

  async getSharedNamespace() {
    const response = await request<APIEnvelope<Namespace>>('/v1/shared/namespace')
    return response.data
  },

  async listSharedResources(type: string, cursor?: string | null) {
    const search = new URLSearchParams({ limit: '20' })
    if (cursor) search.set('cursor', cursor)
    return request<Page<ResourcePreview>>(`/v1/shared/resources/${encodeURIComponent(type)}?${search.toString()}`)
  },

  async createSharedResource(type: string, resource: ResourceInput) {
    const response = await request<APIEnvelope<Resource>>(`/v1/shared/resources/${encodeURIComponent(type)}`, { method: 'POST', body: resource })
    return response.data
  },

  async getSharedResource(type: string, slug: string) {
    const response = await request<APIEnvelope<Resource>>(`/v1/shared/resources/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`)
    return response.data
  },

  async saveSharedResource(type: string, slug: string, resource: ResourceInput) {
    const response = await request<APIEnvelope<Resource>>(`/v1/shared/resources/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`, { method: 'PUT', body: resource })
    return response.data
  },

  async deleteSharedResource(type: string, slug: string) {
    return request<void>(`/v1/shared/resources/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`, { method: 'DELETE' })
  },

  async getPublicNamespace(username: string) {
    const response = await request<APIEnvelope<PublicNamespace>>(`/v1/public/${encodeURIComponent(username)}`)
    return response.data
  },

  async listPublicResources(username: string, type: string, cursor?: string | null) {
    const search = new URLSearchParams({ limit: '20' })
    if (cursor) search.set('cursor', cursor)
    return request<Page<PublicResourcePreview>>(`/v1/public/${encodeURIComponent(username)}/${encodeURIComponent(type)}?${search.toString()}`)
  },

  async getPublicResource(username: string, type: string, slug: string) {
    const response = await request<APIEnvelope<PublicResource>>(`/v1/public/${encodeURIComponent(username)}/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`)
    return response.data
  },

  async listPublicSharedResources(type: string, cursor?: string | null) {
    const search = new URLSearchParams({ limit: '20' })
    if (cursor) search.set('cursor', cursor)
    return request<Page<PublicResourcePreview>>(`/v1/public/shared/resources/${encodeURIComponent(type)}?${search.toString()}`)
  },

  async getPublicSharedResource(type: string, slug: string) {
    const response = await request<APIEnvelope<PublicResource>>(`/v1/public/shared/resources/${encodeURIComponent(type)}/${encodeURIComponent(slug)}`)
    return response.data
  },

  async builderLogin(email: string, password: string) {
    return request<{ token: string; builder: { id: string; email: string } }>('/v1/builder/auth/login', {
      method: 'POST', body: { email, password }, retrySession: false,
    })
  },

  async builderRefresh() {
    await request<{ active: boolean }>('/v1/builder/auth/refresh', { method: 'POST', retrySession: false })
  },

  async builderLogout() {
    return request<void>('/v1/builder/auth/logout', { method: 'POST', retrySession: false })
  },

  async builderMe() {
    const response = await request<APIEnvelope<{ id: string; email: string }>>('/v1/builder/me', { retrySession: false })
    return response.data
  },

  async listBuilderApps() {
    const response = await request<APIEnvelope<BuilderApp[]>>('/v1/builder/apps', { retrySession: false })
    return response.data
  },

  async createBuilderApp(input: BuilderAppInput) {
    return request<{ data: BuilderApp; secretKey: string }>('/v1/builder/apps', {
      method: 'POST', body: input, retrySession: false,
    })
  },
}

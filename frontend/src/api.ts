export interface UserClaims {
  uid: string
  email: string
  role: string
}

export interface ProfileLink {
  label: string
  url: string
}

export interface ProfileInput {
  handle: string
  displayName: string
  bio: string
  avatarURL: string
  links: ProfileLink[]
  isPublic: boolean
}

export interface Profile extends ProfileInput {
  id: string
}

export interface StashInput {
  title: string
  summary: string
  content: string
  tags: string[]
  isPublic: boolean
}

export interface Stash extends StashInput {
  id: string
  createdAt: string
  updatedAt: string
}

export interface StashPreview extends Omit<Stash, 'content'> {}

export interface PublicStash {
  _id: string
  author?: {
    handle: string
    displayName: string
    avatarURL?: string
  }
  title: string
  summary?: string
  content?: string
  tags?: string[]
  createdAt: string
  updatedAt: string
}

export interface PublicProfile {
  handle: string
  displayName: string
  bio?: string
  avatarURL?: string
  links?: ProfileLink[]
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

async function parseResponse<T>(response: Response): Promise<T> {
  if (response.status === 204) {
    return undefined as T
  }
  const payload = (await response.json().catch(() => ({}))) as { error?: string }
  if (!response.ok) {
    throw new APIError(response.status, payload.error ?? 'The request could not be completed.')
  }
  return payload as T
}

let refreshPromise: Promise<void> | undefined

async function refreshSession() {
  if (!refreshPromise) {
    refreshPromise = fetch('/v1/auth/refresh', {
      method: 'POST',
      credentials: 'include',
      headers: { 'X-CSRF-Token': csrfToken() },
    })
      .then(async (response) => {
        await parseResponse(response)
      })
      .finally(() => {
        refreshPromise = undefined
      })
  }
  return refreshPromise
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { body, retrySession = true, headers: providedHeaders, ...fetchOptions } = options
  const headers = new Headers(providedHeaders)
  const method = (fetchOptions.method ?? 'GET').toUpperCase()
  if (body !== undefined) {
    headers.set('Content-Type', 'application/json')
  }
  if (['POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) {
    const token = csrfToken()
    if (token) {
      headers.set('X-CSRF-Token', token)
    }
  }

  const response = await fetch(path, {
    ...fetchOptions,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'include',
  })
  if (
    retrySession &&
    (response.status === 401 || response.status === 403) &&
    !path.startsWith('/v1/auth/')
  ) {
    try {
      await refreshSession()
      return request<T>(path, { ...options, retrySession: false })
    } catch {
      // The original API error gives callers the clearest user-facing outcome.
    }
  }
  return parseResponse<T>(response)
}

export const api = {
  async verifySession() {
    const response = await request<{ active: boolean; claims: UserClaims }>('/v1/auth/verify', {
      retrySession: false,
    })
    return response.claims
  },

  async restoreSession() {
    await refreshSession()
    return api.verifySession()
  },

  async signUp(email: string, password: string) {
    return request<APIEnvelope<{ id: string; email: string; role: string }>>('/v1/auth/signup', {
      method: 'POST',
      body: { email, password },
      retrySession: false,
    })
  },

  async logIn(email: string, password: string) {
    return request<APIEnvelope<{ id: string; email: string; role: string }>>('/v1/auth/login', {
      method: 'POST',
      body: { email, password },
      retrySession: false,
    })
  },

  async logOut() {
    return request<void>('/v1/auth/logout', { method: 'POST', retrySession: false })
  },

  async getProfile() {
    const response = await request<APIEnvelope<Profile>>('/v1/me/profile')
    return response.data
  },

  async saveProfile(profile: ProfileInput) {
    const response = await request<APIEnvelope<Profile>>('/v1/me/profile', {
      method: 'PUT',
      body: profile,
    })
    return response.data
  },

  async listStashes(status: 'all' | 'draft' | 'published', cursor?: string | null) {
    const search = new URLSearchParams({ status, limit: '20' })
    if (cursor) {
      search.set('cursor', cursor)
    }
    return request<Page<StashPreview>>(`/v1/me/stashes?${search.toString()}`)
  },

  async createStash(stash: StashInput) {
    const response = await request<APIEnvelope<Stash>>('/v1/me/stashes', {
      method: 'POST',
      body: stash,
    })
    return response.data
  },

  async getStash(id: string) {
    const response = await request<APIEnvelope<Stash>>(`/v1/me/stashes/${id}`)
    return response.data
  },

  async saveStash(id: string, stash: StashInput) {
    const response = await request<APIEnvelope<Stash>>(`/v1/me/stashes/${id}`, {
      method: 'PUT',
      body: stash,
    })
    return response.data
  },

  async deleteStash(id: string) {
    return request<void>(`/v1/me/stashes/${id}`, { method: 'DELETE' })
  },

  async discoverStashes(tag?: string, cursor?: string | null) {
    const search = new URLSearchParams({ limit: '20' })
    if (tag) {
      search.set('tag', tag)
    }
    if (cursor) {
      search.set('cursor', cursor)
    }
    return request<Page<PublicStash>>(`/v1/public/stashes?${search.toString()}`)
  },

  async getPublicStash(id: string) {
    const response = await request<APIEnvelope<PublicStash>>(`/v1/public/stashes/${id}`)
    return response.data
  },

  async getPublicProfile(handle: string) {
    const response = await request<APIEnvelope<PublicProfile>>(`/v1/public/profiles/${handle}`)
    return response.data
  },

  async getPublicProfileStashes(handle: string, cursor?: string | null) {
    const search = new URLSearchParams({ limit: '20' })
    if (cursor) {
      search.set('cursor', cursor)
    }
    return request<Page<PublicStash>>(`/v1/public/profiles/${handle}/stashes?${search.toString()}`)
  },
}
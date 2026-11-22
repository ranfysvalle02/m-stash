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

export interface VibeField {
  key: string
  label: string
  kind: 'text' | 'longtext' | 'number' | 'price' | 'toggle' | 'date' | 'tag'
  options?: string[]
}

export interface VibeSection {
  type: string
  title: string
  audience: 'owner' | 'member' | 'member-public'
  view: 'grid' | 'list' | 'checklist' | 'timeline' | 'ranked'
  fields: VibeField[]
  actionLabel?: string
}

export interface VibeSpec {
  name: string
  tagline: string
  accent: 'teal' | 'indigo' | 'rose' | 'amber' | 'emerald' | 'slate'
  icon: string
  sections: VibeSection[]
  source?: 'model' | 'blueprint' | 'recipe'
}

// VibeRecipeMeta is one curated launchpad card from GET /v1/vibe/recipes. It
// carries only presentation metadata — never seeded samples.
export interface VibeRecipeMeta {
  id: string
  name: string
  tagline: string
  accent: 'teal' | 'indigo' | 'rose' | 'amber' | 'emerald' | 'slate'
  icon: string
  publicBlurb: string
  privateBlurb: string
  // remixIdea is a concrete, keyword-rich prompt that maps back to this recipe's
  // blueprint even without a model key; it is what "Remix" drops into the composer.
  remixIdea: string
  actionLabel?: string
}

export interface VibeSnippet {
  title: string
  language: string
  code: string
}

export interface VibeShare {
  id: string
  url: string
  previewUrl?: string
  zipUrl: string
  // skillUrl serves a plug-and-play SKILL.md pre-filled with this app's key and
  // sections; append ?download=1 to download it as a file.
  skillUrl?: string
  expiresAt: string
  claimed?: boolean
}

export interface VibeOwner {
  email: string
  password: string
}

export interface VibeResult {
  spec: VibeSpec
  share?: VibeShare
  idea?: string
  snippets?: VibeSnippet[]
  owner?: VibeOwner
  claim?: { token: string }
  publishableKey?: string
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

export interface AppLimits {
  maxUsers: number
  maxRecordsPerNamespace: number
  maxRecordBytes: number
}

export interface BuilderAppUsage {
  users: number
  records: number
  limits?: AppLimits
}

// InspectedUser is a single end user of an app as shown in the console data
// inspector. The password is never sent by the server.
export interface InspectedUser {
  id: string
  email: string
  role: string
  username?: string
  createdAt: string
}

// InspectedResource is a single record of an app as shown in the console data
// inspector, including its decoded data/content and owning namespace.
export interface InspectedResource {
  id: string
  type: string
  slug: string
  title: string
  summary?: string
  visibility: 'private' | 'authenticated' | 'public'
  namespaceSlug?: string
  namespaceKind?: 'personal' | 'shared'
  data?: Record<string, unknown>
  content?: string
  createdAt: string
  updatedAt: string
}

export interface BuilderMe {
  id: string
  email: string
  plan?: string
  appCount?: number
  maxApps?: number
  limits?: AppLimits
  // operator is true for the super-operator who manages the whole deployment.
  operator?: boolean
}

// MetricsRoute is one normalized route/status row in the live telemetry.
export interface MetricsRoute {
  method: string
  route: string
  status: number
  count: number
  avgLatencyMs: number
}

// MetricsSnapshot mirrors the Go metricsSnapshot for the operator pulse view.
export interface MetricsSnapshot {
  startedAt: string
  uptimeSeconds: number
  inFlight: number
  totalRequests: number
  statusClasses: Record<string, number>
  avgLatencyMs: number
  routes: MetricsRoute[]
  rateLimited: number
  sandboxesPurged: number
  sandboxesClaimed: number
  builderSignups: number
  trySandboxes: number
  outboxEvents: Record<string, number>
}

export interface AdminOverview {
  counts: Record<string, number>
  recordsByVisibility: Record<string, number>
  outboxByState: Record<string, number>
  metrics: MetricsSnapshot
  webhookRunning: boolean
}

// ResolvedSettings is the deployment's effective, live configuration. Durations
// are serialized as Go duration strings (e.g. "24h0m0s").
export interface ResolvedSettings {
  trySandbox: boolean
  builderSignupOpen: boolean
  builderSignupDailyLimit: number
  demoSeed: boolean
  webhookDelivery: boolean
  freeTierMaxApps: number
  freeTierMaxUsers: number
  freeTierMaxRecords: number
  freeTierMaxRecordBytes: number
  vibeMaxSandboxes: number
  vibeDailyLimit: number
  trySandboxTTL: string
  vibeShareTTL: string
}

// SettingsOverrides holds only the fields an operator has overridden from the
// .env baseline; absent keys fall back to it.
export interface SettingsOverrides {
  trySandbox?: boolean
  trySandboxTTL?: string
  builderSignupOpen?: boolean
  builderSignupDailyLimit?: number
  demoSeed?: boolean
  webhookDelivery?: boolean
  freeTierMaxApps?: number
  freeTierMaxUsers?: number
  freeTierMaxRecords?: number
  freeTierMaxRecordBytes?: number
  vibeMaxSandboxes?: number
  vibeDailyLimit?: number
  vibeShareTTL?: string
}

// EnvironmentPosture reports read-only, boot-fixed deployment configuration. No
// secret value is ever included — only whether each is configured.
export interface EnvironmentPosture {
  geminiConfigured: boolean
  geminiModel: string
  vibeCoolingDown: boolean
  metricsTokenConfigured: boolean
  platformTokenConfigured: boolean
  operatorConfigured: boolean
  trustProxy: boolean
  sessionCookieSecure: boolean
  allowedOrigins: string[]
  publicBaseURL: string
  database: string
  webhookRunning: boolean
}

export interface AdminSettings {
  effective: ResolvedSettings
  envDefaults: ResolvedSettings
  overrides: SettingsOverrides
  environment: EnvironmentPosture
}

export interface AdminSettingsPatch {
  trySandbox?: boolean
  trySandboxTTL?: string
  builderSignupOpen?: boolean
  builderSignupDailyLimit?: number
  demoSeed?: boolean
  webhookDelivery?: boolean
  freeTierMaxApps?: number
  freeTierMaxUsers?: number
  freeTierMaxRecords?: number
  freeTierMaxRecordBytes?: number
  vibeMaxSandboxes?: number
  vibeDailyLimit?: number
  vibeShareTTL?: string
  resetKeys?: string[]
  resetAll?: boolean
}

export interface AdminAppView {
  id: string
  slug: string
  displayName: string
  publishableKey: string
  active: boolean
  sandbox: boolean
  allowedOrigins?: string[]
  ownerId?: string
  ownerEmail?: string
  ownerRole?: string
  users: number
  records: number
  limits: AppLimits
  expiresAt?: string
  claimedAt?: string
  createdAt: string
  updatedAt: string
}

export interface AdminAppPatch {
  active?: boolean
  displayName?: string
  allowedOrigins?: string[]
  limits?: AppLimits
  unlimited?: boolean
  extendHours?: number
  ownerId?: string
  promote?: boolean
}

export interface AdminBuilderView {
  id: string
  email: string
  role: string
  plan?: string
  appCount: number
  isBootstrapAdmin: boolean
  createdAt: string
}

export interface AdminBuilderCreate {
  email: string
  password: string
  role: 'builder' | 'operator'
  plan: 'free' | 'unlimited'
}

export interface AdminBuilderPatch {
  plan?: 'free' | 'unlimited'
  role?: 'builder' | 'operator'
  password?: string
  applyLimitsToApps?: boolean
}

export interface OutboxDelivery {
  state: string
  attempts: number
  availableAt: string
}

export interface OutboxEvent {
  id: string
  appId?: string
  type: string
  schemaVersion: string
  occurredAt: string
  requestId: string
  namespaceId?: string
  actor: { id: string; role: string }
  resource: { collection: string; id?: string }
  data?: Record<string, unknown>
  delivery: OutboxDelivery
}

export interface TrySandboxResult {
  publishableKey: string
  expiresAt: string
  apiBase: string
  claim: { token: string }
}

export interface ClaimResult {
  data: BuilderApp
  secretKey: string
}

// ServiceCapabilities is the subset of /.well-known/m-stash.json the UI reads to
// hide features (try-it, self-serve signup) that this deployment hasn't enabled,
// and to learn whether a bootstrap operator is configured.
export interface ServiceCapabilities {
  trySandbox: boolean
  builderSignup: boolean
  operatorConfigured: boolean
  // vibeEnabled is true when the lab is usable at all (recipes/blueprints need
  // only a live sandbox); vibeAIEnabled is true only when a model is configured.
  vibeEnabled: boolean
  vibeAIEnabled: boolean
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

  // A readable hint that a session exists; the csrf cookie is set (non-HttpOnly) alongside the session.
  hasSessionHint: () => csrfToken() !== '',

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

  // vibe launches an app either from a curated recipe id or a free-text idea. A
  // recipe is instant (no model); an idea uses the model when configured and
  // falls back to a deterministic blueprint otherwise.
  async vibe(input: string | { recipe?: string; idea?: string }): Promise<VibeResult> {
    const body = typeof input === 'string' ? { idea: input } : input
    const response = await request<APIEnvelope<VibeSpec> & { share?: VibeShare; snippets?: VibeSnippet[]; owner?: VibeOwner; claim?: { token: string }; publishableKey?: string }>('/v1/vibe', { method: 'POST', body, retrySession: false })
    return { spec: response.data, share: response.share, snippets: response.snippets, owner: response.owner, claim: response.claim, publishableKey: response.publishableKey }
  },

  // listVibeRecipes returns the curated launchpad cards.
  async listVibeRecipes(): Promise<VibeRecipeMeta[]> {
    const response = await request<APIEnvelope<VibeRecipeMeta[]>>('/v1/vibe/recipes', { retrySession: false })
    return response.data ?? []
  },

  // getServiceManifest reports which optional features this deployment enabled.
  async getServiceManifest(): Promise<ServiceCapabilities> {
    const response = await request<{ capabilities?: { trySandbox?: { enabled?: boolean }; builderSignup?: { enabled?: boolean }; operatorBootstrap?: { configured?: boolean }; vibe?: { enabled?: boolean; aiEnabled?: boolean } } }>('/.well-known/m-stash.json', { retrySession: false })
    return {
      trySandbox: response.capabilities?.trySandbox?.enabled ?? false,
      builderSignup: response.capabilities?.builderSignup?.enabled ?? false,
      operatorConfigured: response.capabilities?.operatorBootstrap?.configured ?? false,
      vibeEnabled: response.capabilities?.vibe?.enabled ?? false,
      vibeAIEnabled: response.capabilities?.vibe?.aiEnabled ?? false,
    }
  },

  // startTrySandbox provisions a blank live sandbox. The returned claim token is
  // shown only once, so callers persist it (localStorage) to claim later.
  async startTrySandbox(): Promise<TrySandboxResult> {
    return request<TrySandboxResult>('/v1/try', { method: 'POST', retrySession: false })
  },

  async getVibeShare(id: string): Promise<VibeResult> {
    const response = await request<APIEnvelope<VibeSpec> & { share: VibeShare; idea: string; snippets?: VibeSnippet[]; publishableKey?: string }>(`/v1/vibe/${encodeURIComponent(id)}`, { retrySession: false })
    return { spec: response.data, share: response.share, idea: response.idea, snippets: response.snippets, publishableKey: response.publishableKey }
  },

  async builderLogin(email: string, password: string) {
    return request<{ token: string; builder: { id: string; email: string; operator?: boolean } }>('/v1/builder/auth/login', {
      method: 'POST', body: { email, password }, retrySession: false,
    })
  },

  async builderSignup(email: string, password: string) {
    return request<{ token: string; builder: { id: string; email: string; plan?: string; operator?: boolean } }>('/v1/builder/auth/signup', {
      method: 'POST', body: { email, password }, retrySession: false,
    })
  },

  // claimApp promotes a sandbox into a live app owned by the signed-in builder.
  // Only the one-time claim token authorizes this; the pk_ and data are kept.
  async claimApp(input: { publishableKey: string; claimToken: string; allowedOrigins?: string[]; displayName?: string }) {
    return request<ClaimResult>('/v1/builder/apps/claim', {
      method: 'POST', body: input, retrySession: false,
    })
  },

  async builderRefresh() {
    await request<{ active: boolean }>('/v1/builder/auth/refresh', { method: 'POST', retrySession: false })
  },

  async builderLogout() {
    return request<void>('/v1/builder/auth/logout', { method: 'POST', retrySession: false })
  },

  async builderMe() {
    const response = await request<APIEnvelope<BuilderMe>>('/v1/builder/me', { retrySession: false })
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

  async rotateBuilderAppSecret(id: string) {
    return request<{ data: BuilderApp; secretKey: string }>(`/v1/builder/apps/${encodeURIComponent(id)}/rotate-secret`, {
      method: 'POST', retrySession: false,
    })
  },

  async updateBuilderApp(id: string, input: { allowedOrigins?: string[]; displayName?: string; active?: boolean }) {
    const response = await request<APIEnvelope<BuilderApp>>(`/v1/builder/apps/${encodeURIComponent(id)}`, {
      method: 'PATCH', body: input, retrySession: false,
    })
    return response.data
  },

  async builderAppUsage(id: string) {
    const response = await request<APIEnvelope<BuilderAppUsage>>(`/v1/builder/apps/${encodeURIComponent(id)}/usage`, { retrySession: false })
    return response.data
  },

  // deleteBuilderApp permanently removes an owned app and all of its tenant data,
  // freeing a slot against the free-tier app cap.
  async deleteBuilderApp(id: string) {
    return request<void>(`/v1/builder/apps/${encodeURIComponent(id)}`, { method: 'DELETE', retrySession: false })
  },

  async listBuilderAppUsers(id: string) {
    const response = await request<APIEnvelope<InspectedUser[]>>(`/v1/builder/apps/${encodeURIComponent(id)}/users`, { retrySession: false })
    return response.data
  },

  async deleteBuilderAppUser(id: string, userId: string) {
    return request<void>(`/v1/builder/apps/${encodeURIComponent(id)}/users/${encodeURIComponent(userId)}`, { method: 'DELETE', retrySession: false })
  },

  async listBuilderAppResources(id: string, filters: { type?: string; visibility?: string } = {}) {
    const search = new URLSearchParams()
    if (filters.type) search.set('type', filters.type)
    if (filters.visibility) search.set('visibility', filters.visibility)
    const query = search.toString()
    const path = `/v1/builder/apps/${encodeURIComponent(id)}/resources${query ? `?${query}` : ''}`
    const response = await request<APIEnvelope<InspectedResource[]>>(path, { retrySession: false })
    return response.data
  },

  async deleteBuilderAppResource(id: string, resourceId: string) {
    return request<void>(`/v1/builder/apps/${encodeURIComponent(id)}/resources/${encodeURIComponent(resourceId)}`, { method: 'DELETE', retrySession: false })
  },

  // --- Operator control plane (super-operator only) -----------------------

  async adminOverview() {
    const response = await request<APIEnvelope<AdminOverview>>('/v1/admin/overview', { retrySession: false })
    return response.data
  },

  async adminGetSettings() {
    const response = await request<APIEnvelope<AdminSettings>>('/v1/admin/settings', { retrySession: false })
    return response.data
  },

  async adminPatchSettings(patch: AdminSettingsPatch) {
    const response = await request<APIEnvelope<AdminSettings>>('/v1/admin/settings', { method: 'PATCH', body: patch, retrySession: false })
    return response.data
  },

  async adminClearVibeCooldown() {
    return request<APIEnvelope<{ vibeCoolingDown: boolean }>>('/v1/admin/vibe/clear-cooldown', { method: 'POST', retrySession: false })
  },

  async adminListApps(filters: { filter?: string; q?: string } = {}) {
    const search = new URLSearchParams()
    if (filters.filter) search.set('filter', filters.filter)
    if (filters.q) search.set('q', filters.q)
    const query = search.toString()
    const response = await request<APIEnvelope<AdminAppView[]>>(`/v1/admin/apps${query ? `?${query}` : ''}`, { retrySession: false })
    return response.data
  },

  async adminUpdateApp(id: string, patch: AdminAppPatch) {
    const response = await request<APIEnvelope<AdminAppView>>(`/v1/admin/apps/${encodeURIComponent(id)}`, { method: 'PATCH', body: patch, retrySession: false })
    return response.data
  },

  async adminDeleteApp(id: string) {
    return request<void>(`/v1/admin/apps/${encodeURIComponent(id)}`, { method: 'DELETE', retrySession: false })
  },

  async adminSweepSandboxes() {
    const response = await request<APIEnvelope<{ purged: number }>>('/v1/admin/sandboxes/sweep', { method: 'POST', retrySession: false })
    return response.data
  },

  async adminListAppUsers(id: string) {
    const response = await request<APIEnvelope<InspectedUser[]>>(`/v1/admin/apps/${encodeURIComponent(id)}/users`, { retrySession: false })
    return response.data
  },

  async adminDeleteAppUser(id: string, userId: string) {
    return request<void>(`/v1/admin/apps/${encodeURIComponent(id)}/users/${encodeURIComponent(userId)}`, { method: 'DELETE', retrySession: false })
  },

  async adminListAppResources(id: string, filters: { type?: string; visibility?: string } = {}) {
    const search = new URLSearchParams()
    if (filters.type) search.set('type', filters.type)
    if (filters.visibility) search.set('visibility', filters.visibility)
    const query = search.toString()
    const response = await request<APIEnvelope<InspectedResource[]>>(`/v1/admin/apps/${encodeURIComponent(id)}/resources${query ? `?${query}` : ''}`, { retrySession: false })
    return response.data
  },

  async adminDeleteAppResource(id: string, resourceId: string) {
    return request<void>(`/v1/admin/apps/${encodeURIComponent(id)}/resources/${encodeURIComponent(resourceId)}`, { method: 'DELETE', retrySession: false })
  },

  async adminListBuilders() {
    const response = await request<APIEnvelope<AdminBuilderView[]>>('/v1/admin/builders', { retrySession: false })
    return response.data
  },

  async adminCreateBuilder(input: AdminBuilderCreate) {
    const response = await request<APIEnvelope<AdminBuilderView>>('/v1/admin/builders', { method: 'POST', body: input, retrySession: false })
    return response.data
  },

  async adminUpdateBuilder(id: string, patch: AdminBuilderPatch) {
    return request<APIEnvelope<{ id: string }>>(`/v1/admin/builders/${encodeURIComponent(id)}`, { method: 'PATCH', body: patch, retrySession: false })
  },

  async adminDeleteBuilder(id: string) {
    return request<void>(`/v1/admin/builders/${encodeURIComponent(id)}`, { method: 'DELETE', retrySession: false })
  },

  async adminListOutbox(state?: string) {
    const search = new URLSearchParams()
    if (state) search.set('state', state)
    const query = search.toString()
    const response = await request<APIEnvelope<OutboxEvent[]>>(`/v1/admin/outbox${query ? `?${query}` : ''}`, { retrySession: false })
    return response.data
  },

  async adminRetryOutbox(id: string) {
    return request<APIEnvelope<{ retried: number }>>(`/v1/admin/outbox/${encodeURIComponent(id)}/retry`, { method: 'POST', retrySession: false })
  },

  async adminRetryDeadOutbox() {
    const response = await request<APIEnvelope<{ retried: number }>>('/v1/admin/outbox/retry-dead', { method: 'POST', retrySession: false })
    return response.data
  },

  async adminPruneDelivered() {
    const response = await request<APIEnvelope<{ pruned: number }>>('/v1/admin/outbox/delivered', { method: 'DELETE', retrySession: false })
    return response.data
  },

  // adminTinkerSandbox provisions a throwaway sandbox for the operator to drive
  // the live API loop against, with the same shape as the public try-it.
  async adminTinkerSandbox(): Promise<TrySandboxResult> {
    return request<TrySandboxResult>('/v1/admin/tinker/sandbox', { method: 'POST', retrySession: false })
  },
}

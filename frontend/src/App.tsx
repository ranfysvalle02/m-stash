import { useCallback, useEffect, useState, type FormEvent } from 'react'
import {
  ArrowLeft,
  ArrowUpRight,
  BookOpen,
  Check,
  ChevronRight,
  Compass,
  Copy,
  Database,
  FilePenLine,
  Globe2,
  Key,
  Link2,
  LoaderCircle,
  Lock,
  LogOut,
  Menu,
  PenLine,
  Plus,
  Save,
  Settings,
  ShieldCheck,
  Trash2,
  UserRound,
  X,
} from 'lucide-react'
import ReactMarkdown from 'react-markdown'
import rehypeSanitize from 'rehype-sanitize'
import remarkGfm from 'remark-gfm'
import {
  BrowserRouter,
  Link,
  Navigate,
  NavLink,
  Route,
  Routes,
  useNavigate,
  useParams,
  useSearchParams,
} from 'react-router-dom'
import { APIError, api, type Profile, type ProfileInput, type PublicProfile, type PublicStash, type StashInput, type StashPreview, type UserClaims } from './api'
import './App.css'

type SessionState =
  | { kind: 'loading' }
  | { kind: 'anonymous' }
  | { kind: 'authenticated'; claims: UserClaims }

const emptyProfile: ProfileInput = {
  handle: '',
  displayName: '',
  bio: '',
  avatarURL: '',
  links: [],
  isPublic: true,
}

const emptyStash: StashInput = {
  title: '',
  summary: '',
  content: '',
  tags: [],
  isPublic: false,
}

const composeEnvironment = `MONGO_DB=app_db
JWT_SECRET=replace-with-a-random-secret-at-least-32-characters
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=replace-with-an-admin-password-at-least-12-characters
SESSION_COOKIE_SECURE=false`

const externalMongoEnvironment = `MONGO_URI=mongodb+srv://user:password@cluster.example.net/?retryWrites=true
MONGO_DB=app_db
JWT_SECRET=replace-with-a-random-secret-at-least-32-characters
ALLOWED_ORIGINS=https://your-domain.example
SESSION_COOKIE_SECURE=true
TRUST_PROXY=true`

const applicationPolicyBlueprint = `{
  "rules": {
    "playerProfiles": {
      "read": { "ownerId": "$auth.uid" },
      "write": { "ownerId": "$auth.uid" },
      "allowed_write_fields": ["ownerId", "displayName", "loadout"]
    },
    "leaderboard": {
      "read": true,
      "write": false
    }
  }
}`

const environmentVariables = [
  { name: 'MONGO_URI', requirement: 'Required with external Mongo', description: 'MongoDB replica-set or sharded-cluster connection URI. The included Compose stack creates this internally.' },
  { name: 'MONGO_DB', requirement: 'Required', description: 'Database name used for profiles, stashes, application collections, sessions, and the durable outbox.' },
  { name: 'JWT_SECRET', requirement: 'Required', description: 'At least 32 random characters. Signs API tokens and browser sessions.' },
  { name: 'ADMIN_EMAIL + ADMIN_PASSWORD', requirement: 'Optional pair', description: 'Create the initial admin or promote an existing matching account. The password is only used on first creation.' },
  { name: 'SESSION_COOKIE_SECURE', requirement: 'Required for local HTTP', description: 'Use false only on localhost over HTTP. Keep true for HTTPS deployments.' },
  { name: 'ACCESS_TOKEN_TTL + REFRESH_SESSION_TTL', requirement: 'Optional', description: 'Control short-lived access tokens and longer browser refresh sessions.' },
  { name: 'ALLOWED_ORIGINS', requirement: 'Recommended', description: 'Comma-separated browser origins when a separate web client calls the API.' },
  { name: 'TRUST_PROXY', requirement: 'Conditional', description: 'Set true only behind a proxy you control that terminates TLS and supplies forwarded headers.' },
  { name: 'METRICS_TOKEN', requirement: 'Recommended in production', description: 'At least 32 characters to enable the protected Prometheus metrics endpoint.' },
  { name: 'PORT + JWT_ISSUER + JWT_AUDIENCE + M_STASH_CONFIG', requirement: 'Optional', description: 'Use for listener selection, token claims, and selected legacy file-based configuration.' },
]

function useBrowserSession() {
  const [session, setSession] = useState<SessionState>({ kind: 'loading' })

  async function resolveSession() {
    try {
      const claims = await api.verifySession()
      setSession({ kind: 'authenticated', claims })
      return claims
    } catch {
      try {
        const claims = await api.restoreSession()
        setSession({ kind: 'authenticated', claims })
        return claims
      } catch {
        setSession({ kind: 'anonymous' })
        return undefined
      }
    }
  }

  async function restore() {
    setSession({ kind: 'loading' })
    return resolveSession()
  }

  useEffect(() => {
    let active = true
    void api.verifySession()
      .then((claims) => {
        if (active) setSession({ kind: 'authenticated', claims })
      })
      .catch(async () => {
        try {
          const claims = await api.restoreSession()
          if (active) setSession({ kind: 'authenticated', claims })
        } catch {
          if (active) setSession({ kind: 'anonymous' })
        }
      })
    return () => { active = false }
  }, [])

  return { session, restore, setSession }
}

function App() {
  const browserSession = useBrowserSession()

  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Navigate to="/discover" replace />} />
        <Route path="/discover" element={<DiscoveryPage />} />
        <Route path="/guide" element={<SetupGuidePage />} />
        <Route path="/login" element={<AuthenticationPage mode="login" onAuthenticated={browserSession.restore} />} />
        <Route path="/signup" element={<AuthenticationPage mode="signup" onAuthenticated={browserSession.restore} />} />
        <Route path="/p/:id" element={<PublicStashPage />} />
        <Route path="/@:handle" element={<PublicProfilePage />} />
        <Route path="/app/*" element={<Workspace session={browserSession.session} onSessionChange={browserSession.setSession} />} />
        <Route path="*" element={<NotFoundPage />} />
      </Routes>
    </BrowserRouter>
  )
}

function AuthenticationPage({ mode, onAuthenticated }: { mode: 'login' | 'signup'; onAuthenticated: () => Promise<UserClaims | undefined> }) {
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const isSignUp = mode === 'signup'

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      if (isSignUp) {
        await api.signUp(email, password)
      } else {
        await api.logIn(email, password)
      }
      await onAuthenticated()
      navigate('/app/stashes')
    } catch (requestError) {
      setError(messageFromError(requestError))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="auth-page">
      <Link className="wordmark" to="/discover"><span>m</span>stash</Link>
      <section className="auth-panel" aria-labelledby="auth-title">
        <p className="eyebrow">Your application data plane</p>
        <h1 id="auth-title">{isSignUp ? 'Start with a clean slate.' : 'Welcome back.'}</h1>
        <form onSubmit={submit}>
          <label>
            Email
            <input autoComplete="email" inputMode="email" onChange={(event) => setEmail(event.target.value)} required type="email" value={email} />
          </label>
          <label>
            Password
            <input autoComplete={isSignUp ? 'new-password' : 'current-password'} minLength={12} onChange={(event) => setPassword(event.target.value)} required type="password" value={password} />
          </label>
          {error && <p className="form-error" role="alert">{error}</p>}
          <button className="button primary full-width" disabled={submitting} type="submit">
            {submitting ? <LoaderCircle aria-hidden="true" className="spin" size={17} /> : <ChevronRight aria-hidden="true" size={17} />}
            {isSignUp ? 'Create account' : 'Continue'}
          </button>
        </form>
        <p className="auth-switch">
          {isSignUp ? 'Already have an account?' : 'Need an account?'}{' '}
          <Link to={isSignUp ? '/login' : '/signup'}>{isSignUp ? 'Log in' : 'Create one'}</Link>
        </p>
        <Link className="auth-guide-link" to="/guide"><BookOpen aria-hidden="true" size={16} /> How m-stash runs</Link>
      </section>
    </main>
  )
}

function Workspace({ session, onSessionChange }: { session: SessionState; onSessionChange: (state: SessionState) => void }) {
  if (session.kind === 'loading') {
    return <LoadingPage />
  }
  if (session.kind === 'anonymous') {
    return <Navigate to="/login" replace />
  }
  return <WorkspaceGate claims={session.claims} onSessionChange={onSessionChange} />
}

function WorkspaceGate({ claims, onSessionChange }: { claims: UserClaims; onSessionChange: (state: SessionState) => void }) {
  const [profile, setProfile] = useState<Profile | undefined>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    async function loadProfile() {
      setLoading(true)
      setError('')
      try {
        const nextProfile = await api.getProfile()
        if (active) {
          setProfile(nextProfile)
        }
      } catch (requestError) {
        if (active && !(requestError instanceof APIError && requestError.status === 404)) {
          setError(messageFromError(requestError))
        }
      } finally {
        if (active) {
          setLoading(false)
        }
      }
    }
    void loadProfile()
    return () => { active = false }
  }, [])

  if (loading) {
    return <LoadingPage />
  }
  if (error) {
    return <StatePage title="Could not load your workspace" detail={error} />
  }
  return (
    <WorkspaceShell claims={claims} profile={profile} onSessionChange={onSessionChange}>
      <Routes>
        <Route path="onboarding" element={<ProfileEditor initial={emptyProfile} onboarding onSaved={setProfile} />} />
        <Route path="stashes" element={<Dashboard />} />
        <Route path="stashes/new" element={<StashEditor />} />
        <Route path="stashes/:id" element={<StashEditor />} />
        <Route path="records" element={<RecordsPage />} />
        <Route path="posts" element={<Navigate to="/app/stashes" replace />} />
        <Route path="posts/new" element={<Navigate to="/app/stashes/new" replace />} />
        <Route path="posts/:id" element={<StashEditor />} />
        <Route path="profile" element={<ProfileEditor initial={profile ?? emptyProfile} onSaved={setProfile} />} />
        <Route path="setup" element={<SetupGuidePage workspace />} />
        <Route path="*" element={<Navigate to="stashes" replace />} />
      </Routes>
    </WorkspaceShell>
  )
}

function WorkspaceShell({ children, claims, profile, onSessionChange }: { children: React.ReactNode; claims: UserClaims; profile?: Profile; onSessionChange: (state: SessionState) => void }) {
  const [menuOpen, setMenuOpen] = useState(false)
  const navigate = useNavigate()

  async function logOut() {
    try {
      await api.logOut()
    } finally {
      onSessionChange({ kind: 'anonymous' })
      navigate('/discover')
    }
  }

  return (
    <div className="workspace">
      <aside className={menuOpen ? 'sidebar open' : 'sidebar'}>
        <Link className="wordmark" to="/app/stashes"><span>m</span>stash</Link>
        <nav aria-label="Workspace">
          <NavLink end onClick={() => setMenuOpen(false)} to="/app/stashes"><BookOpen aria-hidden="true" size={18} /> Stashes</NavLink>
          <NavLink onClick={() => setMenuOpen(false)} to="/app/stashes/new"><PenLine aria-hidden="true" size={18} /> New stash</NavLink>
          <NavLink onClick={() => setMenuOpen(false)} to="/app/records"><Database aria-hidden="true" size={18} /> Records</NavLink>
          <NavLink onClick={() => setMenuOpen(false)} to="/app/profile"><Settings aria-hidden="true" size={18} /> Profile</NavLink>
          <NavLink onClick={() => setMenuOpen(false)} to="/app/setup"><ShieldCheck aria-hidden="true" size={18} /> Setup</NavLink>
        </nav>
        <div className="sidebar-foot">
          {profile?.isPublic && <Link className="site-link" to={`/@${profile.handle}`}><Globe2 aria-hidden="true" size={17} /> View site <ArrowUpRight aria-hidden="true" size={15} /></Link>}
          <div className="account-row"><span className="avatar small">{initials(profile?.displayName ?? claims.email)}</span><span>{profile?.displayName ?? claims.email}</span></div>
          <button className="text-button" onClick={logOut} type="button"><LogOut aria-hidden="true" size={16} /> Log out</button>
        </div>
      </aside>
      <div className="workspace-content">
        <header className="mobile-header">
          <button aria-expanded={menuOpen} aria-label="Toggle workspace navigation" className="icon-button" onClick={() => setMenuOpen(!menuOpen)} title="Toggle navigation" type="button"><Menu aria-hidden="true" size={21} /></button>
          <Link className="wordmark" to="/app/stashes"><span>m</span>stash</Link>
          <Link aria-label="Create stash" className="icon-button" title="Create stash" to="/app/stashes/new"><Plus aria-hidden="true" size={21} /></Link>
        </header>
        {children}
      </div>
    </div>
  )
}

function ProfileEditor({ initial, onboarding = false, onSaved }: { initial: ProfileInput; onboarding?: boolean; onSaved: (profile: Profile) => void }) {
  const navigate = useNavigate()
  const [profile, setProfile] = useState<ProfileInput>(initial)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)

  function update<K extends keyof ProfileInput>(key: K, value: ProfileInput[K]) {
    setProfile({ ...profile, [key]: value })
    setSaved(false)
  }

  function updateLink(index: number, key: 'label' | 'url', value: string) {
    const links = profile.links.map((link, linkIndex) => linkIndex === index ? { ...link, [key]: value } : link)
    update('links', links)
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSaving(true)
    setError('')
    try {
      const savedProfile = await api.saveProfile(profile)
      onSaved(savedProfile)
      setSaved(true)
      if (onboarding) {
        navigate('/app/stashes/new')
      }
    } catch (requestError) {
      setError(messageFromError(requestError))
    } finally {
      setSaving(false)
    }
  }

  return (
    <main className="settings-page">
      <div className="page-heading compact">
        <p className="eyebrow">{onboarding ? 'First things first' : 'Public identity'}</p>
        <h1>{onboarding ? 'Make your corner of the web.' : 'Profile'}</h1>
        <p>{onboarding ? 'Create an optional public identity for this account.' : 'Control the public identity attached to this account.'}</p>
      </div>
      <form className="profile-form" onSubmit={save}>
        <div className="form-grid">
          <label>Display name<input maxLength={80} onChange={(event) => update('displayName', event.target.value)} required value={profile.displayName} /></label>
          <label>Handle<div className="handle-input"><span>m.stash/</span><input maxLength={32} onChange={(event) => update('handle', event.target.value.toLowerCase())} pattern="[a-z0-9_-]{3,32}" required value={profile.handle} /></div></label>
        </div>
        <label>Short bio<textarea maxLength={500} onChange={(event) => update('bio', event.target.value)} placeholder="A few words about your work." rows={3} value={profile.bio} /></label>
        <label>Avatar URL<input inputMode="url" onChange={(event) => update('avatarURL', event.target.value)} placeholder="https://" type="url" value={profile.avatarURL} /></label>
        <fieldset className="links-fieldset">
          <legend>Links</legend>
          {profile.links.map((link, index) => <div className="link-row" key={`${index}-${link.url}`}>
            <input aria-label={`Link ${index + 1} label`} maxLength={80} onChange={(event) => updateLink(index, 'label', event.target.value)} placeholder="Label" value={link.label} />
            <input aria-label={`Link ${index + 1} URL`} onChange={(event) => updateLink(index, 'url', event.target.value)} placeholder="https://" type="url" value={link.url} />
            <button aria-label={`Remove ${link.label || 'link'}`} className="icon-button danger" onClick={() => update('links', profile.links.filter((_, linkIndex) => linkIndex !== index))} title="Remove link" type="button"><X aria-hidden="true" size={17} /></button>
          </div>)}
          {profile.links.length < 8 && <button className="text-button" onClick={() => update('links', [...profile.links, { label: '', url: '' }])} type="button"><Plus aria-hidden="true" size={16} /> Add link</button>}
        </fieldset>
        <label className="switch-row"><input checked={profile.isPublic} onChange={(event) => update('isPublic', event.target.checked)} type="checkbox" /><span><strong>Public profile</strong><small>Give this account a public home for shared stashes.</small></span></label>
        <p className="profile-visibility-note"><Globe2 aria-hidden="true" size={16} /> A profile is optional. Shared stashes appear in discovery; a public profile gives them a stable account home. <Link to="/guide">See starter patterns</Link></p>
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="form-actions">
          {saved && <p className="save-state"><Check aria-hidden="true" size={16} /> Saved</p>}
          <button className="button primary" disabled={saving} type="submit">{saving ? <LoaderCircle aria-hidden="true" className="spin" size={17} /> : <Save aria-hidden="true" size={17} />}{onboarding ? 'Save profile' : 'Save profile'}</button>
        </div>
      </form>
    </main>
  )
}

function Dashboard() {
  const [status, setStatus] = useState<'all' | 'draft' | 'published'>('all')
  const [stashes, setStashes] = useState<StashPreview[]>([])
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')
  const [deleting, setDeleting] = useState<string | undefined>()

  useEffect(() => {
    let active = true
    async function load() {
      setLoading(true)
      setError('')
      try {
        const page = await api.listStashes(status)
        if (active) {
          setStashes(page.data)
          setNextCursor(page.page.nextCursor)
        }
      } catch (requestError) {
        if (active) {
          setError(messageFromError(requestError))
        }
      } finally {
        if (active) {
          setLoading(false)
        }
      }
    }
    void load()
    return () => { active = false }
  }, [status])

  async function loadMore() {
    if (!nextCursor) return
    setLoadingMore(true)
    try {
      const page = await api.listStashes(status, nextCursor)
      setStashes([...stashes, ...page.data])
      setNextCursor(page.page.nextCursor)
    } catch (requestError) {
      setError(messageFromError(requestError))
    } finally {
      setLoadingMore(false)
    }
  }

  async function removeStash(id: string) {
    setDeleting(id)
    try {
      await api.deleteStash(id)
      setStashes(stashes.filter((stash) => stash.id !== id))
    } catch (requestError) {
      setError(messageFromError(requestError))
    } finally {
      setDeleting(undefined)
    }
  }

  return (
    <main className="dashboard-page">
      <div className="page-heading with-action">
        <div><p className="eyebrow">Starter collection</p><h1>Stashes</h1></div>
        <Link className="button primary" to="/app/stashes/new"><Plus aria-hidden="true" size={17} /> New stash</Link>
      </div>
      <div className="filter-bar" aria-label="Stash visibility">
        {(['all', 'draft', 'published'] as const).map((option) => <button className={status === option ? 'filter active' : 'filter'} key={option} onClick={() => setStatus(option)} type="button">{option === 'all' ? 'All stashes' : option === 'draft' ? 'Private' : 'Public'}</button>)}
      </div>
      <section className="workspace-guide-callout" aria-label="Workspace guide">
        <div><p className="eyebrow"><ShieldCheck aria-hidden="true" size={15} /> Starter, not ceiling</p><h2>Stashes are one collection pattern.</h2><p>Use them for flexible shared items. Add policy-backed collections for game state, inventories, profiles, and any records your application needs.</p></div>
        <Link className="button secondary" to="/app/records"><Database aria-hidden="true" size={16} /> Explore records</Link>
      </section>
      {error && <p className="form-error" role="alert">{error}</p>}
      {loading ? <LoadingRows /> : stashes.length === 0 ? <EmptyStashes status={status} /> : <div className="post-list">
        {stashes.map((stash) => <article className="post-row" key={stash.id}>
          <div className="post-row-main"><div className="post-row-title"><Link to={`/app/stashes/${stash.id}`}>{stash.title}</Link><span className={stash.isPublic ? 'status published' : 'status'}>{stash.isPublic ? 'Public' : 'Private'}</span></div><p>{stash.summary || 'No description yet.'}</p><div className="post-meta"><time dateTime={stash.updatedAt}>Updated {displayDate(stash.updatedAt)}</time>{stash.tags.map((tag) => <span className="tag" key={tag}>{tag}</span>)}</div></div>
          <div className="post-row-actions"><Link aria-label={`Edit ${stash.title}`} className="icon-button" title="Edit stash" to={`/app/stashes/${stash.id}`}><FilePenLine aria-hidden="true" size={18} /></Link><button aria-label={`Delete ${stash.title}`} className="icon-button danger" disabled={deleting === stash.id} onClick={() => { if (window.confirm(`Delete “${stash.title}”? This cannot be undone.`)) void removeStash(stash.id) }} title="Delete stash" type="button"><Trash2 aria-hidden="true" size={18} /></button></div>
        </article>)}
      </div>}
      {nextCursor && <div className="load-more"><button className="button secondary" disabled={loadingMore} onClick={() => void loadMore()} type="button">{loadingMore && <LoaderCircle aria-hidden="true" className="spin" size={17} />} Load more</button></div>}
    </main>
  )
}

function StashEditor() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [stash, setStash] = useState<StashInput>(emptyStash)
  const [mode, setMode] = useState<'write' | 'preview'>('write')
  const [loading, setLoading] = useState(Boolean(id))
  const [saving, setSaving] = useState(false)
  const [saveState, setSaveState] = useState<'saved' | 'unsaved' | 'error'>('saved')
  const [error, setError] = useState('')
  const [tagText, setTagText] = useState('')

  useEffect(() => {
	const stashID = id ?? ''
  if (!stashID) return
    let active = true
    async function load() {
      try {
    const existing = await api.getStash(stashID)
        if (active) {
          setStash(existing)
          setSaveState('saved')
        }
      } catch (requestError) {
        if (active) setError(messageFromError(requestError))
      } finally {
        if (active) setLoading(false)
      }
    }
    void load()
    return () => { active = false }
  }, [id])

  function update<K extends keyof StashInput>(key: K, value: StashInput[K]) {
    setStash({ ...stash, [key]: value })
    setSaveState('unsaved')
    setError('')
  }

	const saveStash = useCallback(async (nextIsPublic?: boolean) => {
    const input = nextIsPublic === undefined ? stash : { ...stash, isPublic: nextIsPublic }
    setSaving(true)
    setError('')
    try {
      if (id) {
        const updated = await api.saveStash(id, input)
        setStash(updated)
        setSaveState('saved')
      } else {
        const created = await api.createStash(input)
        setStash(created)
        setSaveState('saved')
        navigate(`/app/stashes/${created.id}`, { replace: true })
      }
    } catch (requestError) {
      setSaveState('error')
      setError(messageFromError(requestError))
    } finally {
      setSaving(false)
    }
  }, [id, navigate, stash])

  useEffect(() => {
    if (!id || loading || saveState !== 'unsaved') return
    const timer = window.setTimeout(() => { void saveStash() }, 900)
    return () => window.clearTimeout(timer)
  }, [id, loading, saveState, saveStash])

  function addTag() {
    const nextTag = tagText.trim().toLowerCase()
    if (!nextTag || stash.tags.includes(nextTag)) return
    update('tags', [...stash.tags, nextTag])
    setTagText('')
  }

  if (loading) return <LoadingPage />
  if (error && id && !stash.title) return <StatePage title="Could not open this stash" detail={error} />

  return (
    <main className="editor-page">
      <header className="editor-header">
        <Link className="back-link" to="/app/stashes"><ArrowLeft aria-hidden="true" size={17} /> Stashes</Link>
        <div className="editor-controls"><p aria-live="polite" className={saveState === 'error' ? 'save-state error' : 'save-state'}>{saveState === 'saved' ? <><Check aria-hidden="true" size={15} /> Saved</> : saveState === 'error' ? 'Save failed' : 'Saving changes'}</p><button className="button secondary" disabled={saving} onClick={() => void saveStash()} type="button"><Save aria-hidden="true" size={16} /> Save</button><button className="button primary" disabled={saving} onClick={() => void saveStash(!stash.isPublic)} type="button">{stash.isPublic ? 'Make private' : 'Share publicly'}</button></div>
      </header>
      <div className="editor-canvas">
        <input aria-label="Stash name" className="title-input" maxLength={200} onChange={(event) => update('title', event.target.value)} placeholder="Untitled stash" value={stash.title} />
        <textarea aria-label="Stash description" className="summary-input" maxLength={500} onChange={(event) => update('summary', event.target.value)} placeholder="A short description for people or systems using this item." rows={2} value={stash.summary} />
        <div className="tag-editor"><div className="tag-stack">{stash.tags.map((tag) => <span className="tag removable" key={tag}>{tag}<button aria-label={`Remove ${tag} tag`} onClick={() => update('tags', stash.tags.filter((existing) => existing !== tag))} type="button"><X aria-hidden="true" size={13} /></button></span>)}</div><input aria-label="Add tag" onChange={(event) => setTagText(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); addTag() } }} placeholder="Add a tag" value={tagText} /><button aria-label="Add tag" className="icon-button" onClick={addTag} title="Add tag" type="button"><Plus aria-hidden="true" size={16} /></button></div>
        <div className="editor-mode" role="tablist" aria-label="Stash mode"><button aria-selected={mode === 'write'} className={mode === 'write' ? 'active' : ''} onClick={() => setMode('write')} role="tab" type="button">Edit</button><button aria-selected={mode === 'preview'} className={mode === 'preview' ? 'active' : ''} onClick={() => setMode('preview')} role="tab" type="button">Preview</button></div>
        {mode === 'write' ? <textarea aria-label="Stash details in Markdown" className="content-editor" onChange={(event) => update('content', event.target.value)} placeholder="Add details, instructions, structured context, or Markdown..." value={stash.content} /> : <article className="markdown-body preview-body"><Markdown content={stash.content || '*Nothing to preview yet.*'} /></article>}
        <p className="publish-note"><Lock aria-hidden="true" size={15} /> {stash.isPublic ? 'This stash is public and appears in discovery.' : 'This stash is private until you share it.'} <Link to="/app/setup">See data patterns</Link></p>
        {error && <p className="form-error" role="alert">{error}</p>}
      </div>
    </main>
  )
}

function RecordsPage() {
  const [collection, setCollection] = useState('stashes')
  const [queryText, setQueryText] = useState('{}')
  const [documents, setDocuments] = useState<Record<string, unknown>[]>([])
  const [limit, setLimit] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function queryRecords(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault()
    const collectionName = collection.trim()
    if (!/^[a-zA-Z0-9._-]{1,80}$/.test(collectionName)) {
      setError('Use a collection name with letters, numbers, dots, underscores, or hyphens.')
      return
    }

    let query: Record<string, unknown>
    try {
      const parsed = JSON.parse(queryText) as unknown
      if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
        throw new Error('The filter must be a JSON object.')
      }
      query = parsed as Record<string, unknown>
    } catch (queryError) {
      setError(queryError instanceof Error && queryError.message === 'The filter must be a JSON object.' ? queryError.message : 'Enter a valid JSON object for the filter.')
      return
    }

    setLoading(true)
    setError('')
    try {
      const result = await api.findDocuments(collectionName, query)
      setDocuments(result.data)
      setLimit(result.page.limit)
    } catch (requestError) {
      setError(messageFromError(requestError))
      setDocuments([])
      setLimit(0)
    } finally {
      setLoading(false)
    }
  }

  return <main className="records-page">
    <header className="records-intro">
      <p className="eyebrow"><Database aria-hidden="true" size={15} /> Policy-backed data</p>
      <h1>Records</h1>
      <p>Inspect the collections your server explicitly allows. This is not raw database access: every request is evaluated against the authenticated collection rule before MongoDB sees it.</p>
    </header>
    <section className="record-patterns" aria-label="Collection patterns">
      <article><code>profiles</code><p>Account identity, preferences, and app-facing user metadata.</p></article>
      <article><code>stashes</code><p>Flexible private or shared items, from documentation to release artifacts.</p></article>
      <article><code>gameState</code><p>Configure server-owned rules for health, currency, ranks, and trusted outcomes.</p></article>
    </section>
    <form className="records-query" onSubmit={(event) => void queryRecords(event)}>
      <label>Collection<input autoCapitalize="none" onChange={(event) => setCollection(event.target.value)} spellCheck={false} value={collection} /></label>
      <label>Match filter<textarea aria-describedby="query-help" onChange={(event) => setQueryText(event.target.value)} rows={4} spellCheck={false} value={queryText} /></label>
      <p id="query-help">Use a JSON object. An empty object returns records permitted by the collection's read rule.</p>
      <button className="button primary" disabled={loading} type="submit">{loading ? <LoaderCircle aria-hidden="true" className="spin" size={17} /> : <Database aria-hidden="true" size={17} />} Query records</button>
    </form>
    <section className="records-results" aria-live="polite">
      <div className="records-results-heading"><div><p className="eyebrow">Query result</p><h2>{loading ? 'Querying...' : `${documents.length} record${documents.length === 1 ? '' : 's'}`}</h2></div>{limit > 0 && <span>Policy limit: {limit}</span>}</div>
      {error ? <p className="form-error" role="alert">{error}</p> : <pre className="records-output"><code>{documents.length > 0 ? JSON.stringify(documents, null, 2) : 'Run a query to inspect permitted records.'}</code></pre>}
    </section>
  </main>
}

function DiscoveryPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const tag = searchParams.get('tag') ?? ''
  const [stashes, setStashes] = useState<PublicStash[]>([])
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    async function load() {
      setLoading(true)
      setError('')
      try {
        const page = await api.discoverStashes(tag || undefined)
        if (active) {
          setStashes(page.data)
          setNextCursor(page.page.nextCursor)
        }
      } catch (requestError) {
        if (active) setError(messageFromError(requestError))
      } finally {
        if (active) setLoading(false)
      }
    }
    void load()
    return () => { active = false }
  }, [tag])

  async function loadMore() {
    if (!nextCursor) return
    try {
      const page = await api.discoverStashes(tag || undefined, nextCursor)
      setStashes([...stashes, ...page.data])
      setNextCursor(page.page.nextCursor)
    } catch (requestError) {
      setError(messageFromError(requestError))
    }
  }

  return <PublicLayout>
    <main className="discovery-page">
      <div className="discover-intro"><p className="eyebrow"><Compass aria-hidden="true" size={15} /> Browse</p><h1>Shared stashes from every build.</h1><p>Reference items, release artifacts, game resources, experiments, and useful context.</p></div>
      <div className="tag-filter"><label htmlFor="tag-filter">Filter by tag</label><input id="tag-filter" onChange={(event) => { const nextTag = event.target.value.trim(); setSearchParams(nextTag ? { tag: nextTag } : {}) }} placeholder="Try game, release, research" value={tag} />{tag && <button className="text-button" onClick={() => setSearchParams({})} type="button"><X aria-hidden="true" size={15} /> Clear</button>}</div>
      {error && <p className="form-error" role="alert">{error}</p>}
      {loading ? <LoadingRows /> : stashes.length === 0 ? <StatePage title="Nothing shared yet" detail="Public stashes will appear here when their owners are ready to share them." /> : <div className="public-post-list">{stashes.map((stash) => <PublicPostPreview key={stash._id} stash={stash} />)}</div>}
      {nextCursor && <div className="load-more"><button className="button secondary" onClick={() => void loadMore()} type="button">Load more</button></div>}
    </main>
  </PublicLayout>
}

function PublicPostPreview({ stash }: { stash: PublicStash }) {
  return <article className="public-post-preview"><div><div className="post-meta"><time dateTime={stash.createdAt}>{displayDate(stash.createdAt)}</time>{stash.author && <Link className="author-byline" to={`/@${stash.author.handle}`}><Avatar name={stash.author.displayName} url={stash.author.avatarURL} /> {stash.author.displayName}</Link>}</div><h2><Link to={`/p/${stash._id}`}>{stash.title}</Link></h2><p>{stash.summary || 'Open this stash to inspect the details.'}</p><div className="tag-stack">{stash.tags?.map((tag) => <Link className="tag" key={tag} to={`/discover?tag=${encodeURIComponent(tag)}`}>{tag}</Link>)}</div></div><Link aria-label={`Open ${stash.title}`} className="read-link" to={`/p/${stash._id}`}><ArrowUpRight aria-hidden="true" size={19} /></Link></article>
}

function PublicStashPage() {
  const { id = '' } = useParams()
  const [stash, setStash] = useState<PublicStash | undefined>()
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    async function load() {
      try {
        const publicStash = await api.getPublicStash(id)
        if (active) setStash(publicStash)
      } catch (requestError) {
        if (active) setError(messageFromError(requestError))
      }
    }
    void load()
    return () => { active = false }
  }, [id])

  return <PublicLayout>{error ? <StatePage title="This stash is unavailable" detail={error} /> : !stash ? <LoadingPage /> : <main className="reader-page"><Link className="back-link" to="/discover"><ArrowLeft aria-hidden="true" size={17} /> Discover</Link><header className="article-header"><div className="post-meta"><time dateTime={stash.createdAt}>{displayDate(stash.createdAt)}</time>{stash.author && <Link className="author-byline" to={`/@${stash.author.handle}`}><Avatar name={stash.author.displayName} url={stash.author.avatarURL} /> {stash.author.displayName}</Link>}{stash.tags?.map((tag) => <Link className="tag" key={tag} to={`/discover?tag=${encodeURIComponent(tag)}`}>{tag}</Link>)}</div><h1>{stash.title}</h1>{stash.summary && <p>{stash.summary}</p>}</header><article className="markdown-body"><Markdown content={stash.content ?? ''} /></article></main>}</PublicLayout>
}

function PublicProfilePage() {
  const { handle = '' } = useParams()
  const [profile, setProfile] = useState<PublicProfile | undefined>()
  const [stashes, setStashes] = useState<PublicStash[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    async function load() {
      try {
        const [publicProfile, page] = await Promise.all([api.getPublicProfile(handle), api.getPublicProfileStashes(handle)])
        if (active) {
          setProfile(publicProfile)
          setStashes(page.data)
        }
      } catch (requestError) {
        if (active) setError(messageFromError(requestError))
      }
    }
    void load()
    return () => { active = false }
  }, [handle])

  return <PublicLayout>{error ? <StatePage title="This profile is unavailable" detail={error} /> : !profile ? <LoadingPage /> : <main className="profile-page"><header className="profile-hero"><Avatar name={profile.displayName} url={profile.avatarURL} /><div><p className="eyebrow">@{profile.handle}</p><h1>{profile.displayName}</h1>{profile.bio && <p>{profile.bio}</p>}{profile.links && profile.links.length > 0 && <div className="profile-links">{profile.links.map((link) => <a href={link.url} key={link.url} rel="noreferrer" target="_blank"><Link2 aria-hidden="true" size={15} /> {link.label}</a>)}</div>}</div></header><section className="profile-posts"><h2>Shared stashes</h2>{stashes.length === 0 ? <p className="quiet">No public stashes yet.</p> : <div className="public-post-list">{stashes.map((stash) => <PublicPostPreview key={stash._id} stash={stash} />)}</div>}</section></main>}</PublicLayout>
}

function SetupGuidePage({ workspace = false }: { workspace?: boolean }) {
  const content = <GuideContent />
  return workspace ? <main className="setup-page">{content}</main> : <PublicLayout><main className="setup-page public-guide">{content}</main></PublicLayout>
}

function GuideContent() {
  return <>
    <header className="guide-intro">
      <p className="eyebrow"><ShieldCheck aria-hidden="true" size={15} /> Operator guide</p>
      <h1>Know exactly what runs your application data.</h1>
      <p>m-stash combines identity, policy-backed MongoDB access, a browser workspace, and a transactional event outbox in one deployable service. Configure secrets in your host, never in the browser.</p>
    </header>
    <section className="guide-section workflow-section">
      <div className="guide-section-heading"><p className="eyebrow"><BookOpen aria-hidden="true" size={15} /> Starter patterns</p><h2>Use stashes when they fit. Define your own collections when they do not.</h2></div>
      <ol className="workflow-list"><li><span>01</span><div><h3>Profiles</h3><p>Optional account identity, preferences, and public-facing metadata.</p></div></li><li><span>02</span><div><h3>Stashes</h3><p>Flexible owned items with tags, rich context, and an optional public surface.</p></div></li><li><span>03</span><div><h3>Application data</h3><p>Add inventory, match, leaderboard, or workflow collections in your policy configuration.</p></div></li><li><span>04</span><div><h3>Trusted outcomes</h3><p>Keep health, currency, ranks, and scoring authoritative in a service you control.</p></div></li></ol>
    </section>
    <section className="guide-section data-ownership-section">
      <div className="guide-section-heading"><p className="eyebrow"><ShieldCheck aria-hidden="true" size={15} /> Data ownership</p><h2>Policies decide who may change a field. Your game or application decides what is true.</h2></div>
      <div className="ownership-grid"><article><h3>Good client-owned data</h3><p>Display names, avatars, player-selected loadouts, drafts, and user-authored stashes. Scope these to the authenticated account and allow only the specific fields a client should control.</p></article><article><h3>Keep it authoritative</h3><p>Hit points, currency, anti-cheat scores, rank changes, and match outcomes should be written by trusted application logic after it validates the action. A client can request an action; it must not declare the outcome.</p></article></div>
    </section>
    <section className="guide-section config-section">
      <div className="guide-section-heading"><p className="eyebrow"><Settings aria-hidden="true" size={15} /> Policy blueprint</p><h2>Give clients ownership, not the keys to every outcome.</h2><p>Load rules with <code>M_STASH_CONFIG</code>. This example lets a signed-in player manage only their profile fields, lets anyone read the leaderboard, and blocks browser writes to scores. A configured <code>rules</code> map replaces the starter rules, so include every collection you intend to keep.</p></div>
      <div className="config-snippets"><ConfigSnippet title="Player profiles and leaderboard" description="Trusted game or application logic writes health, currency, scores, and rank changes after validating the requested action." content={applicationPolicyBlueprint} /></div>
    </section>
    <section className="guide-section">
      <div className="guide-section-heading"><p className="eyebrow"><Database aria-hidden="true" size={15} /> Choose a data path</p><h2>Start with the stack you actually have.</h2></div>
      <div className="guide-paths">
        <article className="guide-path"><span className="guide-number">01</span><h3>Included Compose stack</h3><p>Copy <code>.env.example</code> to <code>.env</code>, set its required secrets, then start MongoDB as a one-node replica set and m-stash together. Open <code>http://localhost:4000</code> when the health check is ready.</p><code>cp .env.example .env && docker compose up --build --wait</code></article>
        <article className="guide-path"><span className="guide-number">02</span><h3>Your existing MongoDB</h3><p>Use a replica set or sharded cluster. Pass its URI to the service, set your public browser origin, and keep cookies secure behind HTTPS.</p><code>docker run ... m-stash</code></article>
      </div>
    </section>
    <section className="guide-section environment-section">
      <div className="guide-section-heading"><p className="eyebrow"><Key aria-hidden="true" size={15} /> Environment contract</p><h2>Variables are the control plane.</h2><p>Names and intent are visible here. Actual values remain in your host environment or secret manager.</p></div>
      <div className="environment-list">
        {environmentVariables.map((variable) => <article className="environment-row" key={variable.name}><div><code>{variable.name}</code><span className={variable.requirement.startsWith('Required') ? 'requirement required' : 'requirement'}>{variable.requirement}</span></div><p>{variable.description}</p></article>)}
      </div>
    </section>
    <section className="guide-section config-section">
      <div className="guide-section-heading"><p className="eyebrow"><Settings aria-hidden="true" size={15} /> Start from safe placeholders</p><h2>Two useful configuration shapes.</h2></div>
      <div className="config-snippets"><ConfigSnippet title="Local Compose" description="The included Compose file runs MongoDB only on its private network and wires its replica-set URI into m-stash." content={composeEnvironment} /><ConfigSnippet title="Hosted MongoDB" description="Set these through your platform's encrypted environment-variable or secret-management controls." content={externalMongoEnvironment} /></div>
    </section>
    <section className="guide-section guardrail-section">
      <div><p className="eyebrow"><Lock aria-hidden="true" size={15} /> Before you ship</p><h2>Keep the browser out of your database.</h2></div>
      <ul>
        <li><Check aria-hidden="true" size={17} /> Keep <code>JWT_SECRET</code>, Mongo credentials, and metrics tokens out of browser code.</li>
        <li><Check aria-hidden="true" size={17} /> Set <code>SESSION_COOKIE_SECURE=true</code> on every HTTPS deployment.</li>
        <li><Check aria-hidden="true" size={17} /> Set <code>TRUST_PROXY=true</code> only when a proxy you control forwards HTTPS information.</li>
        <li><Check aria-hidden="true" size={17} /> Set both admin variables together. Existing accounts are promoted without replacing their password.</li>
      </ul>
    </section>
  </>
}

function ConfigSnippet({ title, description, content }: { title: string; description: string; content: string }) {
  const [copied, setCopied] = useState(false)

  async function copyConfiguration() {
    try {
      await navigator.clipboard.writeText(content)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1800)
    } catch {
      setCopied(false)
    }
  }

  return <article className="config-snippet"><div className="snippet-heading"><div><h3>{title}</h3><p>{description}</p></div><button aria-label={`Copy ${title} configuration`} className="icon-button" onClick={() => void copyConfiguration()} title="Copy configuration" type="button">{copied ? <Check aria-hidden="true" size={17} /> : <Copy aria-hidden="true" size={17} />}</button></div><pre><code>{content}</code></pre></article>
}

function PublicLayout({ children }: { children: React.ReactNode }) {
  return <div className="public-shell"><header className="public-header"><Link className="wordmark" to="/discover"><span>m</span>stash</Link><nav><Link to="/discover">Discover</Link><Link to="/guide">How it works</Link><Link className="button secondary small-button" to="/login"><UserRound aria-hidden="true" size={16} /> Log in</Link></nav></header>{children}<footer className="public-footer"><Link className="wordmark" to="/discover"><span>m</span>stash</Link><Link to="/guide">Run your own workspace</Link><p>Made for work worth returning to.</p></footer></div>
}

function Markdown({ content }: { content: string }) {
  return <ReactMarkdown rehypePlugins={[rehypeSanitize]} remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
}

function Avatar({ name, url }: { name: string; url?: string }) {
  return url ? <img alt="" className="avatar profile-avatar" src={url} /> : <span className="avatar profile-avatar">{initials(name)}</span>
}

function LoadingPage() { return <main className="loading-page"><LoaderCircle aria-label="Loading" className="spin" size={26} /></main> }
function LoadingRows() { return <div className="loading-rows" aria-label="Loading stashes"><span /><span /><span /></div> }
function EmptyStashes({ status }: { status: 'all' | 'draft' | 'published' }) { return <section className="empty-state"><FilePenLine aria-hidden="true" size={28} /><h2>{status === 'all' ? 'Your stash is empty.' : `No ${status === 'draft' ? 'private' : 'public'} stashes.`}</h2><p>Store a reference item, release artifact, shared resource, or any context your app needs.</p><Link className="button primary" to="/app/stashes/new"><Plus aria-hidden="true" size={17} /> New stash</Link></section> }
function StatePage({ title, detail }: { title: string; detail: string }) { return <main className="state-page"><h1>{title}</h1><p>{detail}</p><Link className="button secondary" to="/discover">Browse shared stashes</Link></main> }
function NotFoundPage() { return <PublicLayout><StatePage title="That page has moved on." detail="The address does not point to anything here." /></PublicLayout> }

function initials(name: string) { return name.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]).join('').toUpperCase() || 'M' }
function displayDate(value: string) { return new Intl.DateTimeFormat('en', { day: 'numeric', month: 'short', year: 'numeric' }).format(new Date(value)) }
function messageFromError(error: unknown) { return error instanceof Error ? error.message : 'Something went wrong. Please try again.' }

export default App

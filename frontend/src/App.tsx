import { useCallback, useEffect, useState, type FormEvent } from 'react'
import {
  ArrowLeft,
  ArrowUpRight,
  BookOpen,
  Check,
  ChevronRight,
  Compass,
  FilePenLine,
  Globe2,
  Link2,
  LoaderCircle,
  LogOut,
  Menu,
  PenLine,
  Plus,
  Save,
  Settings,
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
  useLocation,
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
      navigate(isSignUp ? '/app/onboarding' : '/app/posts')
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
        <p className="eyebrow">Your publishing desk</p>
        <h1 id="auth-title">{isSignUp ? 'Start with a blank page.' : 'Welcome back.'}</h1>
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
  const location = useLocation()
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
  const onboarding = location.pathname === '/app/onboarding'
  if (!profile && !onboarding) {
    return <Navigate to="/app/onboarding" replace />
  }
  if (profile && onboarding) {
    return <Navigate to="/app/posts" replace />
  }

  return (
    <WorkspaceShell claims={claims} profile={profile} onSessionChange={onSessionChange}>
      <Routes>
        <Route path="onboarding" element={<ProfileEditor initial={emptyProfile} onboarding onSaved={setProfile} />} />
        <Route path="posts" element={<Dashboard />} />
        <Route path="posts/new" element={<PostEditor />} />
        <Route path="posts/:id" element={<PostEditor />} />
        <Route path="profile" element={<ProfileEditor initial={profile ?? emptyProfile} onSaved={setProfile} />} />
        <Route path="*" element={<Navigate to="posts" replace />} />
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
        <Link className="wordmark" to="/app/posts"><span>m</span>stash</Link>
        <nav aria-label="Workspace">
          <NavLink end onClick={() => setMenuOpen(false)} to="/app/posts"><BookOpen aria-hidden="true" size={18} /> Posts</NavLink>
          <NavLink onClick={() => setMenuOpen(false)} to="/app/posts/new"><PenLine aria-hidden="true" size={18} /> New post</NavLink>
          <NavLink onClick={() => setMenuOpen(false)} to="/app/profile"><Settings aria-hidden="true" size={18} /> Profile</NavLink>
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
          <Link className="wordmark" to="/app/posts"><span>m</span>stash</Link>
          <Link aria-label="Create post" className="icon-button" title="Create post" to="/app/posts/new"><Plus aria-hidden="true" size={21} /></Link>
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
        navigate('/app/posts/new')
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
        <p>{onboarding ? 'Choose the name and address readers will recognize.' : 'Control what readers see when they visit your site.'}</p>
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
        <label className="switch-row"><input checked={profile.isPublic} onChange={(event) => update('isPublic', event.target.checked)} type="checkbox" /><span><strong>Public profile</strong><small>Let readers find this page and your published posts.</small></span></label>
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="form-actions">
          {saved && <p className="save-state"><Check aria-hidden="true" size={16} /> Saved</p>}
          <button className="button primary" disabled={saving} type="submit">{saving ? <LoaderCircle aria-hidden="true" className="spin" size={17} /> : <Save aria-hidden="true" size={17} />}{onboarding ? 'Continue to first post' : 'Save profile'}</button>
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
        <div><p className="eyebrow">Workspace</p><h1>Posts</h1></div>
        <Link className="button primary" to="/app/posts/new"><Plus aria-hidden="true" size={17} /> New post</Link>
      </div>
      <div className="filter-bar" aria-label="Post status">
        {(['all', 'draft', 'published'] as const).map((option) => <button className={status === option ? 'filter active' : 'filter'} key={option} onClick={() => setStatus(option)} type="button">{option === 'all' ? 'All posts' : option === 'draft' ? 'Drafts' : 'Published'}</button>)}
      </div>
      {error && <p className="form-error" role="alert">{error}</p>}
      {loading ? <LoadingRows /> : stashes.length === 0 ? <EmptyPosts status={status} /> : <div className="post-list">
        {stashes.map((stash) => <article className="post-row" key={stash.id}>
          <div className="post-row-main"><div className="post-row-title"><Link to={`/app/posts/${stash.id}`}>{stash.title}</Link><span className={stash.isPublic ? 'status published' : 'status'}>{stash.isPublic ? 'Published' : 'Draft'}</span></div><p>{stash.summary || 'No summary yet.'}</p><div className="post-meta"><time dateTime={stash.updatedAt}>Updated {displayDate(stash.updatedAt)}</time>{stash.tags.map((tag) => <span className="tag" key={tag}>{tag}</span>)}</div></div>
          <div className="post-row-actions"><Link aria-label={`Edit ${stash.title}`} className="icon-button" title="Edit post" to={`/app/posts/${stash.id}`}><FilePenLine aria-hidden="true" size={18} /></Link><button aria-label={`Delete ${stash.title}`} className="icon-button danger" disabled={deleting === stash.id} onClick={() => { if (window.confirm(`Delete “${stash.title}”? This cannot be undone.`)) void removeStash(stash.id) }} title="Delete post" type="button"><Trash2 aria-hidden="true" size={18} /></button></div>
        </article>)}
      </div>}
      {nextCursor && <div className="load-more"><button className="button secondary" disabled={loadingMore} onClick={() => void loadMore()} type="button">{loadingMore && <LoaderCircle aria-hidden="true" className="spin" size={17} />} Load more</button></div>}
    </main>
  )
}

function PostEditor() {
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
        navigate(`/app/posts/${created.id}`, { replace: true })
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
  if (error && id && !stash.title) return <StatePage title="Could not open this post" detail={error} />

  return (
    <main className="editor-page">
      <header className="editor-header">
        <Link className="back-link" to="/app/posts"><ArrowLeft aria-hidden="true" size={17} /> Posts</Link>
        <div className="editor-controls"><p aria-live="polite" className={saveState === 'error' ? 'save-state error' : 'save-state'}>{saveState === 'saved' ? <><Check aria-hidden="true" size={15} /> Saved</> : saveState === 'error' ? 'Save failed' : 'Saving changes'}</p><button className="button secondary" disabled={saving} onClick={() => void saveStash()} type="button"><Save aria-hidden="true" size={16} /> Save</button><button className="button primary" disabled={saving} onClick={() => void saveStash(!stash.isPublic)} type="button">{stash.isPublic ? 'Unpublish' : 'Publish'}</button></div>
      </header>
      <div className="editor-canvas">
        <input aria-label="Post title" className="title-input" maxLength={200} onChange={(event) => update('title', event.target.value)} placeholder="Untitled" value={stash.title} />
        <textarea aria-label="Post summary" className="summary-input" maxLength={500} onChange={(event) => update('summary', event.target.value)} placeholder="A brief summary for readers." rows={2} value={stash.summary} />
        <div className="tag-editor"><div className="tag-stack">{stash.tags.map((tag) => <span className="tag removable" key={tag}>{tag}<button aria-label={`Remove ${tag} tag`} onClick={() => update('tags', stash.tags.filter((existing) => existing !== tag))} type="button"><X aria-hidden="true" size={13} /></button></span>)}</div><input aria-label="Add tag" onChange={(event) => setTagText(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); addTag() } }} placeholder="Add a tag" value={tagText} /><button aria-label="Add tag" className="icon-button" onClick={addTag} title="Add tag" type="button"><Plus aria-hidden="true" size={16} /></button></div>
        <div className="editor-mode" role="tablist" aria-label="Editor mode"><button aria-selected={mode === 'write'} className={mode === 'write' ? 'active' : ''} onClick={() => setMode('write')} role="tab" type="button">Write</button><button aria-selected={mode === 'preview'} className={mode === 'preview' ? 'active' : ''} onClick={() => setMode('preview')} role="tab" type="button">Preview</button></div>
        {mode === 'write' ? <textarea aria-label="Post content in Markdown" className="content-editor" onChange={(event) => update('content', event.target.value)} placeholder="Begin writing in Markdown..." value={stash.content} /> : <article className="markdown-body preview-body"><Markdown content={stash.content || '*Nothing to preview yet.*'} /></article>}
        {error && <p className="form-error" role="alert">{error}</p>}
      </div>
    </main>
  )
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
      <div className="discover-intro"><p className="eyebrow"><Compass aria-hidden="true" size={15} /> Browse</p><h1>Fresh from the stash.</h1><p>Notes, working ideas, and small pieces of useful writing.</p></div>
      <div className="tag-filter"><label htmlFor="tag-filter">Filter by tag</label><input id="tag-filter" onChange={(event) => { const nextTag = event.target.value.trim(); setSearchParams(nextTag ? { tag: nextTag } : {}) }} placeholder="Try product, writing, release" value={tag} />{tag && <button className="text-button" onClick={() => setSearchParams({})} type="button"><X aria-hidden="true" size={15} /> Clear</button>}</div>
      {error && <p className="form-error" role="alert">{error}</p>}
      {loading ? <LoadingRows /> : stashes.length === 0 ? <StatePage title="Nothing here yet" detail="Published work will appear here as soon as it is shared." /> : <div className="public-post-list">{stashes.map((stash) => <PublicPostPreview key={stash._id} stash={stash} />)}</div>}
      {nextCursor && <div className="load-more"><button className="button secondary" onClick={() => void loadMore()} type="button">Load more</button></div>}
    </main>
  </PublicLayout>
}

function PublicPostPreview({ stash }: { stash: PublicStash }) {
  return <article className="public-post-preview"><div><div className="post-meta"><time dateTime={stash.createdAt}>{displayDate(stash.createdAt)}</time>{stash.author && <Link className="author-byline" to={`/@${stash.author.handle}`}><Avatar name={stash.author.displayName} url={stash.author.avatarURL} /> {stash.author.displayName}</Link>}</div><h2><Link to={`/p/${stash._id}`}>{stash.title}</Link></h2><p>{stash.summary || 'Open to read the full note.'}</p><div className="tag-stack">{stash.tags?.map((tag) => <Link className="tag" key={tag} to={`/discover?tag=${encodeURIComponent(tag)}`}>{tag}</Link>)}</div></div><Link aria-label={`Read ${stash.title}`} className="read-link" to={`/p/${stash._id}`}><ArrowUpRight aria-hidden="true" size={19} /></Link></article>
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

  return <PublicLayout>{error ? <StatePage title="This post is unavailable" detail={error} /> : !stash ? <LoadingPage /> : <main className="reader-page"><Link className="back-link" to="/discover"><ArrowLeft aria-hidden="true" size={17} /> Discover</Link><header className="article-header"><div className="post-meta"><time dateTime={stash.createdAt}>{displayDate(stash.createdAt)}</time>{stash.author && <Link className="author-byline" to={`/@${stash.author.handle}`}><Avatar name={stash.author.displayName} url={stash.author.avatarURL} /> {stash.author.displayName}</Link>}{stash.tags?.map((tag) => <Link className="tag" key={tag} to={`/discover?tag=${encodeURIComponent(tag)}`}>{tag}</Link>)}</div><h1>{stash.title}</h1>{stash.summary && <p>{stash.summary}</p>}</header><article className="markdown-body"><Markdown content={stash.content ?? ''} /></article></main>}</PublicLayout>
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

  return <PublicLayout>{error ? <StatePage title="This profile is unavailable" detail={error} /> : !profile ? <LoadingPage /> : <main className="profile-page"><header className="profile-hero"><Avatar name={profile.displayName} url={profile.avatarURL} /><div><p className="eyebrow">@{profile.handle}</p><h1>{profile.displayName}</h1>{profile.bio && <p>{profile.bio}</p>}{profile.links && profile.links.length > 0 && <div className="profile-links">{profile.links.map((link) => <a href={link.url} key={link.url} rel="noreferrer" target="_blank"><Link2 aria-hidden="true" size={15} /> {link.label}</a>)}</div>}</div></header><section className="profile-posts"><h2>Published work</h2>{stashes.length === 0 ? <p className="quiet">No published posts yet.</p> : <div className="public-post-list">{stashes.map((stash) => <PublicPostPreview key={stash._id} stash={stash} />)}</div>}</section></main>}</PublicLayout>
}

function PublicLayout({ children }: { children: React.ReactNode }) {
  return <div className="public-shell"><header className="public-header"><Link className="wordmark" to="/discover"><span>m</span>stash</Link><nav><Link to="/discover">Discover</Link><Link className="button secondary small-button" to="/login"><UserRound aria-hidden="true" size={16} /> Log in</Link></nav></header>{children}<footer className="public-footer"><Link className="wordmark" to="/discover"><span>m</span>stash</Link><p>Made for work worth returning to.</p></footer></div>
}

function Markdown({ content }: { content: string }) {
  return <ReactMarkdown rehypePlugins={[rehypeSanitize]} remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
}

function Avatar({ name, url }: { name: string; url?: string }) {
  return url ? <img alt="" className="avatar profile-avatar" src={url} /> : <span className="avatar profile-avatar">{initials(name)}</span>
}

function LoadingPage() { return <main className="loading-page"><LoaderCircle aria-label="Loading" className="spin" size={26} /></main> }
function LoadingRows() { return <div className="loading-rows" aria-label="Loading posts"><span /><span /><span /></div> }
function EmptyPosts({ status }: { status: 'all' | 'draft' | 'published' }) { return <section className="empty-state"><FilePenLine aria-hidden="true" size={28} /><h2>{status === 'all' ? 'Your desk is clear.' : `No ${status} posts.`}</h2><p>Start a note, shape an idea, and decide when it is ready.</p><Link className="button primary" to="/app/posts/new"><Plus aria-hidden="true" size={17} /> New post</Link></section> }
function StatePage({ title, detail }: { title: string; detail: string }) { return <main className="state-page"><h1>{title}</h1><p>{detail}</p><Link className="button secondary" to="/discover">Go to discovery</Link></main> }
function NotFoundPage() { return <PublicLayout><StatePage title="That page has moved on." detail="The address does not point to anything here." /></PublicLayout> }

function initials(name: string) { return name.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]).join('').toUpperCase() || 'M' }
function displayDate(value: string) { return new Intl.DateTimeFormat('en', { day: 'numeric', month: 'short', year: 'numeric' }).format(new Date(value)) }
function messageFromError(error: unknown) { return error instanceof Error ? error.message : 'Something went wrong. Please try again.' }

export default App

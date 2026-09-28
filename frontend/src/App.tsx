import {
  createContext,
  useContext,
  useEffect,
  useState,
  type FormEvent,
} from "react";
import {
  ArrowLeft,
  ArrowUpRight,
  BookOpen,
  Check,
  ChevronRight,
  Compass,
  Database,
  FilePenLine,
  Globe2,
  LoaderCircle,
  LogOut,
  Menu,
  PenLine,
  Plus,
  Save,
  Settings,
  ShieldCheck,
  Trash2,
  UserRound,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import rehypeSanitize from "rehype-sanitize";
import remarkGfm from "remark-gfm";
import {
  BrowserRouter,
  Link,
  Navigate,
  NavLink,
  Route,
  Routes,
  useNavigate,
  useParams,
} from "react-router-dom";
import {
  APIError,
  api,
  type Namespace,
  type NamespaceIdentityInput,
  type PublicNamespace,
  type PublicResource,
  type PublicResourcePreview,
  type ResourceInput,
  type ResourcePreview,
  type UserClaims,
} from "./api";
import "./App.css";

type SessionState =
  | { kind: "loading" }
  | { kind: "anonymous" }
  | { kind: "authenticated"; claims: UserClaims };

const SessionContext = createContext<SessionState>({ kind: "loading" });

const emptyResource: ResourceInput = {
  slug: "",
  title: "",
  summary: "",
  content: "",
  visibility: "private",
};

const usernamePattern = /^[a-z0-9](?:[a-z0-9]|[-_](?=[a-z0-9])){2,31}$/;

function normalizeUsername(value: string) {
  return value.trim().toLowerCase();
}

function usernameValidationMessage(value: string) {
  if (value.length < 3 || value.length > 32) {
    return "Use 3-32 characters.";
  }
  if (!/^[a-z0-9]/.test(value) || !/[a-z0-9]$/.test(value)) {
    return "Start and end with a lowercase letter or number.";
  }
  if (/[-_]{2}|[-_][-_]/.test(value)) {
    return "Use one hyphen or underscore between name parts.";
  }
  if (!usernamePattern.test(value)) {
    return "Use lowercase letters, numbers, hyphens, or underscores only.";
  }
  return "";
}

function useBrowserSession() {
  const [session, setSession] = useState<SessionState>({ kind: "loading" });

  async function restore() {
    setSession({ kind: "loading" });
    try {
      const claims = await api.verifySession();
      setSession({ kind: "authenticated", claims });
      return claims;
    } catch {
      try {
        const claims = await api.restoreSession();
        setSession({ kind: "authenticated", claims });
        return claims;
      } catch {
        setSession({ kind: "anonymous" });
        return undefined;
      }
    }
  }

  useEffect(() => {
    void restore();
  }, []);
  return { session, restore, setSession };
}

function App() {
  const browserSession = useBrowserSession();
  return (
    <BrowserRouter>
      <SessionContext.Provider value={browserSession.session}>
        <Routes>
          <Route path="/" element={<Navigate replace to="/discover" />} />
          <Route path="/discover" element={<NamespaceLookupPage />} />
          <Route path="/guide" element={<GuidePage />} />
          <Route
            path="/login"
            element={
              <AuthenticationPage
                mode="login"
                onAuthenticated={browserSession.restore}
              />
            }
          />
          <Route
            path="/signup"
            element={
              <AuthenticationPage
                mode="signup"
                onAuthenticated={browserSession.restore}
              />
            }
          />
          <Route
            path="/app/*"
            element={
              <Workspace
                session={browserSession.session}
                setSession={browserSession.setSession}
              />
            }
          />
          <Route path="/public" element={<PublicSharedDataPage />} />
          <Route
            path="/public/:type"
            element={<PublicSharedResourceCollectionPage />}
          />
          <Route
            path="/public/:type/:slug"
            element={<PublicSharedResourcePage />}
          />
          <Route
            path="/:username/:type/:slug"
            element={<PublicResourcePage />}
          />
          <Route
            path="/:username/:type"
            element={<PublicResourceCollectionPage />}
          />
          <Route path="/:username" element={<PublicNamespacePage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </SessionContext.Provider>
    </BrowserRouter>
  );
}

function AuthenticationPage({
  mode,
  onAuthenticated,
}: {
  mode: "login" | "signup";
  onAuthenticated: () => Promise<UserClaims | undefined>;
}) {
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [username, setUsername] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const isSignUp = mode === "signup";
  const usernameIssue = username ? usernameValidationMessage(username) : "";

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    if (isSignUp) {
      const issue = usernameValidationMessage(username);
      if (issue) {
        setError(`Choose a valid username. ${issue}`);
        return;
      }
    }
    setSubmitting(true);
    try {
      if (isSignUp)
        await api.signUpWithNamespace(
          email,
          password,
          normalizeUsername(username),
        );
      else await api.logIn(email, password);
      await onAuthenticated();
      navigate("/app/resources/record");
    } catch (requestError) {
      setError(messageFromError(requestError));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="auth-page">
      <Link className="wordmark" to="/discover">
        <span>m</span>stash
      </Link>
      <section className="auth-panel" aria-labelledby="auth-title">
        <p className="eyebrow">Identity-scoped application data</p>
        <h1 id="auth-title">
          {isSignUp ? "Create your personal scope." : "Welcome back."}
        </h1>
        <form onSubmit={submit}>
          <label>
            Email
            <input
              autoComplete="email"
              inputMode="email"
              onChange={(event) => setEmail(event.target.value)}
              required
              type="email"
              value={email}
            />
          </label>
          <label>
            Password
            <input
              autoComplete={isSignUp ? "new-password" : "current-password"}
              minLength={12}
              onChange={(event) => setPassword(event.target.value)}
              required
              type="password"
              value={password}
            />
          </label>
          {isSignUp && (
            <label>
              Username
              <span className="handle-input">
                <span>m.stash/</span>
                <input
                  autoCapitalize="none"
                  autoComplete="username"
                  aria-describedby="username-help"
                  aria-invalid={Boolean(usernameIssue)}
                  maxLength={32}
                  onChange={(event) =>
                    setUsername(normalizeUsername(event.target.value))
                  }
                  required
                  spellCheck={false}
                  value={username}
                />
              </span>
              <small
                className={
                  usernameIssue
                    ? "username-feedback invalid"
                    : username
                      ? "username-feedback valid"
                      : "username-feedback"
                }
                id="username-help"
              >
                {usernameIssue || username
                  ? usernameIssue || `Your permanent route: /${username}`
                  : "3-32 lowercase letters or numbers; single - or _ between parts."}
              </small>
            </label>
          )}
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <button
            className="button primary full-width"
            disabled={submitting || Boolean(usernameIssue)}
            type="submit"
          >
            {submitting ? (
              <LoaderCircle aria-hidden="true" className="spin" size={17} />
            ) : (
              <ChevronRight aria-hidden="true" size={17} />
            )}
            {isSignUp ? "Create account" : "Continue"}
          </button>
        </form>
        <p className="auth-switch">
          {isSignUp ? "Already have an account?" : "Need an account?"}{" "}
          <Link to={isSignUp ? "/login" : "/signup"}>
            {isSignUp ? "Log in" : "Create one"}
          </Link>
        </p>
        <Link className="auth-guide-link" to="/guide">
          <ShieldCheck aria-hidden="true" size={16} /> How it works
        </Link>
      </section>
    </main>
  );
}

function Workspace({
  session,
  setSession,
}: {
  session: SessionState;
  setSession: (state: SessionState) => void;
}) {
  if (session.kind === "loading") return <LoadingPage />;
  if (session.kind === "anonymous") return <Navigate replace to="/login" />;
  return <WorkspaceGate claims={session.claims} setSession={setSession} />;
}

function WorkspaceGate({
  claims,
  setSession,
}: {
  claims: UserClaims;
  setSession: (state: SessionState) => void;
}) {
  const [namespace, setNamespace] = useState<Namespace>();
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    void api
      .getNamespace()
      .then((value) => {
        if (active) setNamespace(value);
      })
      .catch((requestError) => {
        if (
          requestError instanceof APIError &&
          requestError.status === 404 &&
          claims.role === "admin"
        ) {
          return;
        }
        if (active) setError(messageFromError(requestError));
      });
    return () => {
      active = false;
    };
  }, []);

  if (error)
    return <StatePage title="Could not load your workspace" detail={error} />;
  if (!namespace && claims.role !== "admin") return <LoadingPage />;
  return (
    <WorkspaceShell
      claims={claims}
      namespace={namespace}
      setSession={setSession}
    >
      <Routes>
        <Route
          path="resources/:type"
          element={
            namespace ? (
              <NamespaceDashboard scope="personal" canWrite />
            ) : (
              <Navigate replace to="/app/shared" />
            )
          }
        />
        <Route
          path="resources/:type/new"
          element={
            namespace ? (
              <NamespaceResourceEditor scope="personal" canWrite />
            ) : (
              <Navigate replace to="/app/shared" />
            )
          }
        />
        <Route
          path="resources/:type/:slug"
          element={
            namespace ? (
              <NamespaceResourceEditor scope="personal" canWrite />
            ) : (
              <Navigate replace to="/app/shared" />
            )
          }
        />
        <Route
          path="shared"
          element={<SharedDataHome canWrite={claims.role === "admin"} />}
        />
        <Route
          path="shared/:type"
          element={
            <NamespaceDashboard
              scope="shared"
              canWrite={claims.role === "admin"}
            />
          }
        />
        <Route
          path="shared/:type/new"
          element={
            <NamespaceResourceEditor
              scope="shared"
              canWrite={claims.role === "admin"}
            />
          }
        />
        <Route
          path="shared/:type/:slug"
          element={
            <NamespaceResourceEditor
              scope="shared"
              canWrite={claims.role === "admin"}
            />
          }
        />
        <Route
          path="namespace"
          element={
            namespace ? (
              <NamespaceSettings />
            ) : (
              <Navigate replace to="/app/shared" />
            )
          }
        />
        <Route path="guide" element={<GuideContent />} />
        <Route
          path="*"
          element={
            <Navigate
              replace
              to={namespace ? "resources/record" : "shared"}
            />
          }
        />
      </Routes>
    </WorkspaceShell>
  );
}

function WorkspaceShell({
  children,
  claims,
  namespace,
  setSession,
}: {
  children: React.ReactNode;
  claims: UserClaims;
  namespace?: Namespace;
  setSession: (state: SessionState) => void;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const navigate = useNavigate();
  async function logOut() {
    try {
      await api.logOut();
    } finally {
      setSession({ kind: "anonymous" });
      navigate("/discover");
    }
  }
  return (
    <div className="workspace">
      <aside className={menuOpen ? "sidebar open" : "sidebar"}>
        <Link
          className="wordmark"
          to={namespace ? "/app/resources/record" : "/app/shared"}
        >
          <span>m</span>stash
        </Link>
        <nav aria-label="Workspace">
          {namespace && (
            <>
              <NavLink
                end
                onClick={() => setMenuOpen(false)}
                to="/app/resources/record"
              >
                <BookOpen aria-hidden="true" size={18} /> My data
              </NavLink>
              <NavLink
                onClick={() => setMenuOpen(false)}
                to="/app/resources/record/new"
              >
                <PenLine aria-hidden="true" size={18} /> New record
              </NavLink>
            </>
          )}
          <NavLink onClick={() => setMenuOpen(false)} to="/app/shared">
            <Database aria-hidden="true" size={18} /> Shared app data
          </NavLink>
          {namespace && (
            <NavLink onClick={() => setMenuOpen(false)} to="/app/namespace">
              <Settings aria-hidden="true" size={18} /> Namespace
            </NavLink>
          )}
          <NavLink onClick={() => setMenuOpen(false)} to="/app/guide">
            <ShieldCheck aria-hidden="true" size={18} /> Platform guide
          </NavLink>
        </nav>
        <div className="sidebar-foot">
          {namespace?.isPublic && (
            <Link className="site-link" to={`/${namespace.slug}`}>
              <Globe2 aria-hidden="true" size={17} /> View namespace{" "}
              <ArrowUpRight aria-hidden="true" size={15} />
            </Link>
          )}
          {claims.role === "admin" && (
            <Link className="site-link" to="/public">
              <Globe2 aria-hidden="true" size={17} /> View deployment public data{" "}
              <ArrowUpRight aria-hidden="true" size={15} />
            </Link>
          )}
          <div className="account-row">
            <span className="avatar small">
              {initials(
                namespace?.displayName || namespace?.slug || claims.email,
              )}
            </span>
            <span>{namespace?.slug ?? "Deployment admin"}</span>
          </div>
          <button
            className="text-button"
            onClick={() => void logOut()}
            type="button"
          >
            <LogOut aria-hidden="true" size={16} /> Log out
          </button>
        </div>
      </aside>
      <div className="workspace-content">
        <header className="mobile-header">
          <button
            aria-expanded={menuOpen}
            aria-label="Toggle workspace navigation"
            className="icon-button"
            onClick={() => setMenuOpen(!menuOpen)}
            title="Toggle navigation"
            type="button"
          >
            <Menu aria-hidden="true" size={21} />
          </button>
          <Link
            className="wordmark"
            to={namespace ? "/app/resources/record" : "/app/shared"}
          >
            <span>m</span>stash
          </Link>
          {namespace ? (
            <Link
              aria-label="Create record"
              className="icon-button"
              title="Create record"
              to="/app/resources/record/new"
            >
              <Plus aria-hidden="true" size={21} />
            </Link>
          ) : claims.role === "admin" ? (
            <Link
              aria-label="Manage shared data"
              className="icon-button"
              title="Manage shared data"
              to="/app/shared"
            >
              <Plus aria-hidden="true" size={21} />
            </Link>
          ) : (
            <span />
          )}
        </header>
        {children}
      </div>
    </div>
  );
}

function SharedDataHome({ canWrite }: { canWrite: boolean }) {
  const navigate = useNavigate();
  const [type, setType] = useState("config");
  const normalizedType = normalizeResourceType(type);
  const typeIssue = resourceTypeValidationMessage(normalizedType);
  function openBucket(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!typeIssue) navigate(`/app/shared/${normalizedType}`);
  }
  return (
    <main className="dashboard-page shared-data-home">
      <div className="page-heading compact">
        <p className="eyebrow">Deployment-owned app data</p>
        <h1>Shared app data</h1>
        <p>
          Types are reusable buckets. Create as many as the application needs,
          then set visibility per record: private for operators, signed-in for
          members, or public for anyone at a deployment URL.
        </p>
      </div>
      <section className="workspace-guide-callout">
        <div>
          <p className="eyebrow">
            <Globe2 aria-hidden="true" size={15} /> Public is a visibility,
            not a bucket
          </p>
          <h2>Publish the app facts people need.</h2>
          <p>
            A public config record can expose an app name or version; a private
            config record can hold operator-only details. Both belong in the
            same typed bucket and remain controlled by the deployment.
          </p>
        </div>
        <Link className="button secondary" to="/public">
          <Globe2 aria-hidden="true" size={16} /> View deployment public data
        </Link>
      </section>
      <section className="shared-bucket-selector" aria-labelledby="bucket-selector-title">
        <div>
          <p className="eyebrow">Open or create a bucket</p>
          <h2 id="bucket-selector-title">Choose a data type</h2>
          <p>
            Use a type that names the application concern, such as config,
            catalog, feature-flag, leaderboard, or a custom type.
          </p>
        </div>
        <form className="records-query" onSubmit={openBucket}>
          <label>
            Data type
            <input
              autoCapitalize="none"
              aria-describedby="shared-type-help"
              aria-invalid={Boolean(typeIssue)}
              maxLength={32}
              onChange={(event) => setType(event.target.value)}
              placeholder="config"
              spellCheck={false}
              value={type}
            />
            <small
              className={
                typeIssue ? "username-feedback invalid" : "username-feedback"
              }
              id="shared-type-help"
            >
              {typeIssue || "This type becomes a durable shared-data bucket."}
            </small>
          </label>
          <button className="button primary" disabled={Boolean(typeIssue)} type="submit">
            <ArrowUpRight aria-hidden="true" size={17} /> Open bucket
          </button>
        </form>
        {canWrite && !typeIssue && (
          <Link
            className="button secondary"
            to={`/app/shared/${normalizedType}/new`}
          >
            <Plus aria-hidden="true" size={17} /> Create a {normalizedType} record
          </Link>
        )}
      </section>
      <section className="shared-bucket-grid" aria-label="Suggested shared data buckets">
        {sharedDataBuckets.map((bucket) => (
          <Link key={bucket.type} to={`/app/shared/${bucket.type}`}>
            <span>{bucket.type}</span>
            <h2>{bucket.title}</h2>
            <p>{bucket.description}</p>
            <ArrowUpRight aria-hidden="true" size={17} />
          </Link>
        ))}
      </section>
      {!canWrite && (
        <p className="profile-visibility-note">
          <ShieldCheck aria-hidden="true" size={16} /> You can read shared
          records available to your account. Only deployment administrators can
          create, edit, or publish them.
        </p>
      )}
    </main>
  );
}

function NamespaceDashboard({
  scope,
  canWrite,
}: {
  scope: "personal" | "shared";
  canWrite: boolean;
}) {
  const { type = "record" } = useParams();
  const shared = scope === "shared";
  const routeBase = shared ? "/app/shared" : "/app/resources";
  const scopeLabel = shared ? "Deployment shared data" : "Personal data";
  const visibilityOptions: Array<"all" | ResourceInput["visibility"]> = shared
    ? canWrite
      ? ["all", "private", "authenticated", "public"]
      : ["all", "authenticated", "public"]
    : ["all", "private", "public"];
  const [resources, setResources] = useState<ResourcePreview[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [visibility, setVisibility] = useState<
    "all" | ResourceInput["visibility"]
  >("all");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [deleting, setDeleting] = useState<string>();
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    void api[shared ? "listSharedResources" : "listResources"](type)
      .then((page) => {
        if (active) {
          setResources(page.data);
          setNextCursor(page.page.nextCursor);
        }
      })
      .catch((requestError) => {
        if (active) setError(messageFromError(requestError));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [shared, type]);

  const visibleResources =
    visibility === "all"
      ? resources
      : resources.filter((resource) => resource.visibility === visibility);
  async function loadMore() {
    if (!nextCursor) return;
    setLoadingMore(true);
    try {
      const page = await api[shared ? "listSharedResources" : "listResources"](
        type,
        nextCursor,
      );
      setResources([...resources, ...page.data]);
      setNextCursor(page.page.nextCursor);
    } catch (requestError) {
      setError(messageFromError(requestError));
    } finally {
      setLoadingMore(false);
    }
  }
  async function removeResource(slug: string) {
    setDeleting(slug);
    try {
      await api[shared ? "deleteSharedResource" : "deleteResource"](type, slug);
      setResources(resources.filter((resource) => resource.slug !== slug));
    } catch (requestError) {
      setError(messageFromError(requestError));
    } finally {
      setDeleting(undefined);
    }
  }

  return (
    <main className="dashboard-page">
      <div className="page-heading with-action">
        <div>
          <p className="eyebrow">{scopeLabel}</p>
          <h1>{type}s</h1>
        </div>
        {canWrite && (
          <Link className="button primary" to={`${routeBase}/${type}/new`}>
            <Plus aria-hidden="true" size={17} /> New {type}
          </Link>
        )}
      </div>
      <div className="filter-bar" aria-label="Resource visibility">
        {visibilityOptions.map((option) => (
          <button
            className={visibility === option ? "filter active" : "filter"}
            key={option}
            onClick={() => setVisibility(option)}
            type="button"
          >
            {option === "all"
              ? "All"
              : option === "authenticated"
                ? "Signed in"
                : option === "private"
                  ? "Private"
                  : "Public"}
          </button>
        ))}
      </div>
      <section className="workspace-guide-callout">
        <div>
          <p className="eyebrow">
            <ShieldCheck aria-hidden="true" size={15} />{" "}
            {shared ? "One controlled shared scope" : "Your route, your work"}
          </p>
          <h2>
            {shared
              ? "Shared app data stays deployment-controlled."
              : "Every resource belongs to your namespace."}
          </h2>
          <p>
            {shared
              ? "Types are buckets, while visibility belongs to each record. Administrators and trusted backend services control writes; public records appear at deployment URLs under /public."
              : "m-stash derives the namespace from your signed-in account, so a browser cannot write to someone else’s route."}
          </p>
        </div>
        <Link className="button secondary" to={shared ? "/public" : "/app/guide"}>
          {shared ? (
            <>
              <Globe2 aria-hidden="true" size={16} /> Public deployment data
            </>
          ) : (
            <>
              <ShieldCheck aria-hidden="true" size={16} /> How it works
            </>
          )}
        </Link>
      </section>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {loading ? (
        <LoadingRows />
      ) : visibleResources.length === 0 ? (
        <section className="empty-state activation-state">
          <PenLine aria-hidden="true" size={28} />
          <p className="eyebrow">
            {shared ? "Shared application state" : "Start your namespace"}
          </p>
          <h2>No {type}s yet.</h2>
          <p>
            {shared
              ? canWrite
                ? `Create a shared ${type} record, then choose whether it stays private, serves signed-in users, or publishes at /public/${type}/... .`
                : "An administrator has not published shared data of this type yet."
              : `Create a private ${type} first, then make it public when its canonical route is ready to share.`}
          </p>
          {canWrite && (
            <Link className="button primary" to={`${routeBase}/${type}/new`}>
              <Plus aria-hidden="true" size={17} /> Create {type}
            </Link>
          )}
        </section>
      ) : (
        <div className="post-list">
          {visibleResources.map((resource) => (
            <article className="post-row" key={resource.id}>
              <div className="post-row-main">
                <div className="post-row-title">
                  <Link to={`${routeBase}/${type}/${resource.slug}`}>
                    {resource.title}
                  </Link>
                  <span
                    className={
                      resource.visibility === "public"
                        ? "status published"
                        : "status"
                    }
                  >
                    {resource.visibility}
                  </span>
                </div>
                <p>{resource.summary || `/${type}/${resource.slug}`}</p>
                <div className="post-meta">
                  <code>
                    /{type}/{resource.slug}
                  </code>
                  <time dateTime={resource.updatedAt}>
                    Updated {displayDate(resource.updatedAt)}
                  </time>
                </div>
              </div>
              {canWrite && (
                <div className="post-row-actions">
                  <Link
                    aria-label={`Edit ${resource.title}`}
                    className="icon-button"
                    title="Edit resource"
                    to={`${routeBase}/${type}/${resource.slug}`}
                  >
                    <FilePenLine aria-hidden="true" size={18} />
                  </Link>
                  <button
                    aria-label={`Delete ${resource.title}`}
                    className="icon-button danger"
                    disabled={deleting === resource.slug}
                    onClick={() => {
                      if (
                        window.confirm(
                          `Delete “${resource.title}”? This cannot be undone.`,
                        )
                      )
                        void removeResource(resource.slug);
                    }}
                    title="Delete resource"
                    type="button"
                  >
                    <Trash2 aria-hidden="true" size={18} />
                  </button>
                </div>
              )}
            </article>
          ))}
        </div>
      )}
      {nextCursor && (
        <div className="load-more">
          <button
            className="button secondary"
            disabled={loadingMore}
            onClick={() => void loadMore()}
            type="button"
          >
            {loadingMore && (
              <LoaderCircle aria-hidden="true" className="spin" size={17} />
            )}{" "}
            Load more
          </button>
        </div>
      )}
    </main>
  );
}

function NamespaceResourceEditor({
  scope,
  canWrite,
}: {
  scope: "personal" | "shared";
  canWrite: boolean;
}) {
  const { type = "record", slug } = useParams();
  const navigate = useNavigate();
  const shared = scope === "shared";
  const routeBase = shared ? "/app/shared" : "/app/resources";
  const creating = !slug;
  const [resource, setResource] = useState<ResourceInput>(emptyResource);
  const [loading, setLoading] = useState(!creating);
  const [saving, setSaving] = useState(false);
  const [mode, setMode] = useState<"write" | "preview">("write");
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (!slug) return;
    let active = true;
    void api[shared ? "getSharedResource" : "getResource"](type, slug)
      .then((value) => {
        if (active) setResource(value);
      })
      .catch((requestError) => {
        if (active) setError(messageFromError(requestError));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [shared, slug, type]);

  function update<K extends keyof ResourceInput>(
    key: K,
    value: ResourceInput[K],
  ) {
    setResource({ ...resource, [key]: value });
    setSaved(false);
    setError("");
  }
  async function save() {
    setSaving(true);
    setError("");
    try {
      if (creating) {
        const created = await api[
          shared ? "createSharedResource" : "createResource"
        ](type, resource);
        navigate(`${routeBase}/${type}/${created.slug}`, { replace: true });
      } else if (slug) {
        const updated = await api[
          shared ? "saveSharedResource" : "saveResource"
        ](type, slug, resource);
        setResource(updated);
        setSaved(true);
      }
    } catch (requestError) {
      setError(messageFromError(requestError));
    } finally {
      setSaving(false);
    }
  }

  if (!canWrite)
    return (
      <StatePage
        title="Shared data is read-only"
        detail="Only deployment administrators can edit shared records."
      />
    );
  if (loading) return <LoadingPage />;
  if (error && !resource.title && !creating)
    return <StatePage title="Could not open this resource" detail={error} />;
  return (
    <main className="editor-page">
      <header className="editor-header">
        <Link className="back-link" to={`${routeBase}/${type}`}>
          <ArrowLeft aria-hidden="true" size={17} /> {type}s
        </Link>
        <div className="editor-controls">
          <p aria-live="polite" className="save-state">
            {saved && (
              <>
                <Check aria-hidden="true" size={15} /> Saved
              </>
            )}
          </p>
          <button
            className="button primary"
            disabled={saving}
            onClick={() => void save()}
            type="button"
          >
            {saving ? (
              <LoaderCircle aria-hidden="true" className="spin" size={16} />
            ) : (
              <Save aria-hidden="true" size={16} />
            )}
            {creating ? `Create ${type}` : "Save changes"}
          </button>
        </div>
      </header>
      <div className="editor-canvas">
        {creating ? (
          <label>
            URL slug
            <input
              autoCapitalize="none"
              maxLength={64}
              onChange={(event) =>
                update("slug", event.target.value.toLowerCase())
              }
              pattern="[a-z0-9]+(-[a-z0-9]+)*"
              placeholder="profile-card"
              value={resource.slug}
            />
          </label>
        ) : (
          <p className="eyebrow">
            /{type}/{slug}
          </p>
        )}
        <input
          aria-label="Resource title"
          className="title-input"
          maxLength={200}
          onChange={(event) => update("title", event.target.value)}
          placeholder={`Untitled ${type}`}
          value={resource.title}
        />
        <textarea
          aria-label="Resource summary"
          className="summary-input"
          maxLength={500}
          onChange={(event) => update("summary", event.target.value)}
          placeholder="What this is and why it matters."
          rows={2}
          value={resource.summary}
        />
        <label className="visibility-select">
          Visibility
          <select
            onChange={(event) =>
              update(
                "visibility",
                event.target.value as ResourceInput["visibility"],
              )
            }
            value={resource.visibility}
          >
            <option value="private">Private</option>
            {shared && <option value="authenticated">Signed-in users</option>}
            <option value="public">Public</option>
          </select>
          <small>
            {shared
              ? `Signed-in entries are visible to every account. Public entries are available without an account at /public/${type}/${resource.slug || "your-record"}.`
              : "Public entries resolve through your username/type/slug route."}
          </small>
        </label>
        <div className="editor-mode" role="tablist" aria-label="Resource mode">
          <button
            aria-selected={mode === "write"}
            className={mode === "write" ? "active" : ""}
            onClick={() => setMode("write")}
            role="tab"
            type="button"
          >
            Edit
          </button>
          <button
            aria-selected={mode === "preview"}
            className={mode === "preview" ? "active" : ""}
            onClick={() => setMode("preview")}
            role="tab"
            type="button"
          >
            Preview
          </button>
        </div>
        {mode === "write" ? (
          <textarea
            aria-label="Resource content in Markdown"
            className="content-editor"
            onChange={(event) => update("content", event.target.value)}
            placeholder="Describe the record or add Markdown content..."
            value={resource.content}
          />
        ) : (
          <article className="markdown-body preview-body">
            <Markdown
              content={resource.content || "*Nothing to preview yet.*"}
            />
          </article>
        )}
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
      </div>
    </main>
  );
}

function NamespaceSettings() {
  const [namespace, setNamespace] = useState<Namespace>();
  const [input, setInput] = useState<NamespaceIdentityInput>({
    displayName: "",
    bio: "",
    avatarURL: "",
    isPublic: true,
  });
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    void api
      .getNamespace()
      .then((value) => {
        if (active) {
          setNamespace(value);
          setInput({
            displayName: value.displayName,
            bio: value.bio ?? "",
            avatarURL: value.avatarURL ?? "",
            isPublic: value.isPublic,
          });
        }
      })
      .catch((requestError) => {
        if (active) setError(messageFromError(requestError));
      });
    return () => {
      active = false;
    };
  }, []);
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true);
    setSaved(false);
    setError("");
    try {
      const updated = await api.saveNamespace(input);
      setNamespace(updated);
      setSaved(true);
    } catch (requestError) {
      setError(messageFromError(requestError));
    } finally {
      setSaving(false);
    }
  }
  if (!namespace && !error) return <LoadingPage />;
  if (!namespace)
    return <StatePage title="Could not load your namespace" detail={error} />;
  return (
    <main className="settings-page">
      <div className="page-heading compact">
        <p className="eyebrow">Profile and sharing</p>
        <h1>@{namespace.slug}</h1>
        <p>
          Your username is permanent. Keep the profile current and choose
          whether its public records can be discovered at this route.
        </p>
      </div>
      <form className="profile-form" onSubmit={save}>
        <label>
          Display name
          <input
            maxLength={80}
            onChange={(event) =>
              setInput({ ...input, displayName: event.target.value })
            }
            required
            value={input.displayName}
          />
        </label>
        <label>
          Short bio
          <textarea
            maxLength={500}
            onChange={(event) =>
              setInput({ ...input, bio: event.target.value })
            }
            placeholder="A few words about you or your application."
            rows={3}
            value={input.bio}
          />
        </label>
        <label>
          Avatar URL
          <input
            inputMode="url"
            onChange={(event) =>
              setInput({ ...input, avatarURL: event.target.value })
            }
            placeholder="https://"
            type="url"
            value={input.avatarURL}
          />
        </label>
        <label className="switch-row">
          <input
            checked={input.isPublic}
            onChange={(event) =>
              setInput({ ...input, isPublic: event.target.checked })
            }
            type="checkbox"
          />
          <span>
            <strong>Publish this profile</strong>
            <small>
              When disabled, this profile and its public records stay hidden.
            </small>
          </span>
        </label>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <div className="form-actions">
          {saved && (
            <p className="save-state">
              <Check aria-hidden="true" size={16} /> Saved
            </p>
          )}
          <button className="button primary" disabled={saving} type="submit">
            {saving ? (
              <LoaderCircle aria-hidden="true" className="spin" size={17} />
            ) : (
              <Save aria-hidden="true" size={17} />
            )}{" "}
            Save namespace
          </button>
        </div>
      </form>
    </main>
  );
}

function NamespaceLookupPage() {
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const usernameIssue = username ? usernameValidationMessage(username) : "";
  return (
    <PublicLayout>
      <main className="discovery-page">
        <div className="discover-intro">
          <p className="eyebrow">
            <Compass aria-hidden="true" size={15} /> Application data ownership
          </p>
          <h1>Two scopes. One source of truth.</h1>
          <p>
            m-stash gives every application personal data for signed-in people
            and shared data for the deployment. Ownership comes from identity,
            so browsers never send owner IDs or database credentials.
          </p>
          <div className="discover-actions">
            <Link className="button primary" to="/signup">
              <UserRound aria-hidden="true" size={17} /> Create account
            </Link>
            <Link className="discover-guide-link" to="/guide">
              See the model <ArrowUpRight aria-hidden="true" size={16} />
            </Link>
          </div>
        </div>
        <section className="scope-model" aria-labelledby="scope-model-title">
          <header>
            <p className="eyebrow">Two scopes, one ownership model</p>
            <h2 id="scope-model-title">
              Personal data for people. Shared data for the app.
            </h2>
          </header>
          <article>
            <h3>Personal scope</h3>
            <p>
              Each account owns its profiles, preferences, saves, and
              user-created records. m-stash resolves that owner from the
              session, not from a browser-supplied ID.
            </p>
          </article>
          <article>
            <h3>Shared app scope</h3>
            <p>
              Deployment administrators and trusted services own the facts
              every user should see: app name, app version, configuration,
              feature flags, catalogs, leaderboards, and release notes.
            </p>
          </article>
        </section>
        <section className="public-lookup" aria-labelledby="public-lookup-title">
          <div>
            <p className="eyebrow">Optional publishing</p>
            <h2 id="public-lookup-title">Browse a public profile</h2>
            <p>
              Publishing is opt-in. Enter a username to view records that
              account has chosen to share publicly.
            </p>
          </div>
          <form
            className="records-query"
            onSubmit={(event) => {
              event.preventDefault();
              if (!usernameIssue) navigate(`/${username}`);
            }}
          >
            <label>
              Public username
              <input
                autoCapitalize="none"
                aria-describedby="namespace-lookup-help"
                aria-invalid={Boolean(usernameIssue)}
                onChange={(event) =>
                  setUsername(normalizeUsername(event.target.value))
                }
                placeholder="alex-dev"
                spellCheck={false}
                value={username}
              />
              <small
                className={
                  usernameIssue
                    ? "username-feedback invalid"
                    : "username-feedback"
                }
                id="namespace-lookup-help"
              >
                {usernameIssue || "Enter a username with a published profile."}
              </small>
            </label>
            <button
              className="button primary"
              disabled={!username || Boolean(usernameIssue)}
              type="submit"
            >
              <ArrowUpRight aria-hidden="true" size={17} /> View profile
            </button>
          </form>
        </section>
      </main>
    </PublicLayout>
  );
}

const sharedDataBuckets = [
  {
    type: "config",
    title: "Configuration",
    description: "App name, version, supported clients, and release settings.",
  },
  {
    type: "feature-flag",
    title: "Feature flags",
    description: "Deployment-controlled capabilities available to clients.",
  },
  {
    type: "catalog",
    title: "Catalogs",
    description: "Published inventories, plans, reference data, or menus.",
  },
  {
    type: "leaderboard",
    title: "Leaderboards",
    description: "Server-computed scores and ranked public results.",
  },
] as const;

function normalizeResourceType(value: string) {
  return value.trim().toLowerCase();
}

function resourceTypeValidationMessage(value: string) {
  if (value.length < 3 || value.length > 32) {
    return "Use 3-32 lowercase letters, numbers, or hyphens.";
  }
  if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(value)) {
    return "Start and end with a letter or number; use single hyphens between words.";
  }
  return "";
}

function PublicSharedDataPage() {
  const navigate = useNavigate();
  const [type, setType] = useState("config");
  const normalizedType = normalizeResourceType(type);
  const typeIssue = resourceTypeValidationMessage(normalizedType);
  function openBucket(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!typeIssue) navigate(`/public/${normalizedType}`);
  }
  return (
    <PublicLayout>
      <main className="discovery-page public-data-page">
        <div className="discover-intro">
          <p className="eyebrow">
            <Globe2 aria-hidden="true" size={15} /> Deployment public data
          </p>
          <h1>Public app data.</h1>
          <p>
            This is the deployment's public surface: app records deliberately
            published for anyone to read, independent of personal profiles.
          </p>
        </div>
        <section className="public-lookup" aria-labelledby="public-bucket-title">
          <div>
            <p className="eyebrow">Public buckets</p>
            <h2 id="public-bucket-title">Browse a public bucket</h2>
            <p>
              A type is a bucket such as config, feature-flag, catalog, or
              leaderboard. Only records explicitly marked public appear here.
            </p>
          </div>
          <form className="records-query" onSubmit={openBucket}>
            <label>
              Data type
              <input
                autoCapitalize="none"
                aria-describedby="public-type-help"
                aria-invalid={Boolean(typeIssue)}
                maxLength={32}
                onChange={(event) => setType(event.target.value)}
                placeholder="config"
                spellCheck={false}
                value={type}
              />
              <small
                className={
                  typeIssue ? "username-feedback invalid" : "username-feedback"
                }
                id="public-type-help"
              >
                {typeIssue || "Enter a public shared-data bucket."}
              </small>
            </label>
            <button className="button primary" disabled={Boolean(typeIssue)} type="submit">
              <ArrowUpRight aria-hidden="true" size={17} /> Open bucket
            </button>
          </form>
        </section>
        <section className="shared-bucket-grid" aria-label="Suggested public data buckets">
          {sharedDataBuckets.map((bucket) => (
            <Link key={bucket.type} to={`/public/${bucket.type}`}>
              <span>{bucket.type}</span>
              <h2>{bucket.title}</h2>
              <p>{bucket.description}</p>
              <ArrowUpRight aria-hidden="true" size={17} />
            </Link>
          ))}
        </section>
      </main>
    </PublicLayout>
  );
}

function PublicSharedResourceCollectionPage() {
  const { type = "" } = useParams();
  const [resources, setResources] = useState<PublicResourcePreview[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    void api
      .listPublicSharedResources(type)
      .then((page) => {
        if (active) {
          setResources(page.data);
          setNextCursor(page.page.nextCursor);
        }
      })
      .catch((requestError) => {
        if (active) setError(messageFromError(requestError));
      });
    return () => {
      active = false;
    };
  }, [type]);
  async function loadMore() {
    if (!nextCursor) return;
    try {
      const page = await api.listPublicSharedResources(type, nextCursor);
      setResources([...resources, ...page.data]);
      setNextCursor(page.page.nextCursor);
    } catch (requestError) {
      setError(messageFromError(requestError));
    }
  }
  return (
    <PublicLayout>
      <main className="discovery-page">
        <Link className="back-link" to="/public">
          <ArrowLeft aria-hidden="true" size={17} /> Deployment public data
        </Link>
        <div className="discover-intro">
          <p className="eyebrow">Public deployment bucket</p>
          <h1>{type}</h1>
          <p>Deployment-owned {type} records published for anyone to read.</p>
        </div>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        {resources.length === 0 && !error ? (
          <section className="empty-state discovery-empty">
            <Globe2 aria-hidden="true" size={28} />
            <h2>No public {type} records yet.</h2>
            <p>
              The deployment has not published any records in this bucket yet.
            </p>
          </section>
        ) : (
          <div className="public-post-list">
            {resources.map((resource) => (
              <article className="public-post-preview" key={resource.slug}>
                <div>
                  <div className="post-meta">
                    <time dateTime={resource.createdAt}>
                      {displayDate(resource.createdAt)}
                    </time>
                    <span className="tag">{resource.type}</span>
                  </div>
                  <h2>
                    <Link to={`/public/${resource.type}/${resource.slug}`}>
                      {resource.title}
                    </Link>
                  </h2>
                  <p>
                    {resource.summary ||
                      "Open this deployment-published record to view the details."}
                  </p>
                </div>
                <Link
                  aria-label={`Open ${resource.title}`}
                  className="read-link"
                  to={`/public/${resource.type}/${resource.slug}`}
                >
                  <ArrowUpRight aria-hidden="true" size={19} />
                </Link>
              </article>
            ))}
          </div>
        )}
        {nextCursor && (
          <div className="load-more">
            <button
              className="button secondary"
              onClick={() => void loadMore()}
              type="button"
            >
              Load more
            </button>
          </div>
        )}
      </main>
    </PublicLayout>
  );
}

function PublicSharedResourcePage() {
  const { type = "", slug = "" } = useParams();
  const [resource, setResource] = useState<PublicResource>();
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    void api
      .getPublicSharedResource(type, slug)
      .then((value) => {
        if (active) setResource(value);
      })
      .catch((requestError) => {
        if (active) setError(messageFromError(requestError));
      });
    return () => {
      active = false;
    };
  }, [slug, type]);
  return (
    <PublicLayout>
      {error ? (
        <StatePage title="This deployment record is unavailable" detail={error} />
      ) : !resource ? (
        <LoadingPage />
      ) : (
        <main className="reader-page">
          <Link className="back-link" to={`/public/${type}`}>
            <ArrowLeft aria-hidden="true" size={17} /> Public {type}
          </Link>
          <header className="article-header">
            <p className="eyebrow">
              /public/{resource.type}/{resource.slug}
            </p>
            <h1>{resource.title}</h1>
            {resource.summary && <p>{resource.summary}</p>}
          </header>
          <article className="markdown-body">
            <Markdown content={resource.content ?? ""} />
          </article>
        </main>
      )}
    </PublicLayout>
  );
}

function PublicNamespacePage() {
  const { username = "" } = useParams();
  const navigate = useNavigate();
  const [namespace, setNamespace] = useState<PublicNamespace>();
  const [type, setType] = useState("record");
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    void api
      .getPublicNamespace(username)
      .then((value) => {
        if (active) setNamespace(value);
      })
      .catch((requestError) => {
        if (active) setError(messageFromError(requestError));
      });
    return () => {
      active = false;
    };
  }, [username]);
  return (
    <PublicLayout>
      {error ? (
        <StatePage title="This namespace is unavailable" detail={error} />
      ) : !namespace ? (
        <LoadingPage />
      ) : (
        <main className="profile-page">
          <header className="profile-hero">
            <Avatar
              name={namespace.displayName || namespace.slug}
              url={namespace.avatarURL}
            />
            <div>
              <p className="eyebrow">@{namespace.slug}</p>
              <h1>{namespace.displayName || namespace.slug}</h1>
              {namespace.bio && <p>{namespace.bio}</p>}
            </div>
          </header>
          <section className="profile-posts">
            <h2>Browse public data</h2>
            <form
              className="records-query"
              onSubmit={(event) => {
                event.preventDefault();
                const value = type.trim().toLowerCase();
                if (value) navigate(`/${namespace.slug}/${value}`);
              }}
            >
              <label>
                Resource type
                <input
                  autoCapitalize="none"
                  onChange={(event) => setType(event.target.value)}
                  placeholder="record"
                  value={type}
                />
              </label>
              <button className="button primary" type="submit">
                Browse
              </button>
            </form>
          </section>
        </main>
      )}
    </PublicLayout>
  );
}

function PublicResourceCollectionPage() {
  const { username = "", type = "" } = useParams();
  const [resources, setResources] = useState<PublicResourcePreview[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    void api
      .listPublicResources(username, type)
      .then((page) => {
        if (active) {
          setResources(page.data);
          setNextCursor(page.page.nextCursor);
        }
      })
      .catch((requestError) => {
        if (active) setError(messageFromError(requestError));
      });
    return () => {
      active = false;
    };
  }, [type, username]);
  async function loadMore() {
    if (!nextCursor) return;
    try {
      const page = await api.listPublicResources(username, type, nextCursor);
      setResources([...resources, ...page.data]);
      setNextCursor(page.page.nextCursor);
    } catch (requestError) {
      setError(messageFromError(requestError));
    }
  }
  return (
    <PublicLayout>
      <main className="discovery-page">
        <div className="discover-intro">
          <p className="eyebrow">@{username}</p>
          <h1>{type}s</h1>
          <p>Public data from this namespace.</p>
        </div>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        {resources.length === 0 && !error ? (
          <section className="empty-state discovery-empty">
            <Compass aria-hidden="true" size={28} />
            <h2>No public {type}s yet.</h2>
            <p>This namespace has not shared data of this type.</p>
          </section>
        ) : (
          <div className="public-post-list">
            {resources.map((resource) => (
              <article className="public-post-preview" key={resource.slug}>
                <div>
                  <div className="post-meta">
                    <time dateTime={resource.createdAt}>
                      {displayDate(resource.createdAt)}
                    </time>
                    <span className="tag">{resource.type}</span>
                  </div>
                  <h2>
                    <Link to={`/${username}/${resource.type}/${resource.slug}`}>
                      {resource.title}
                    </Link>
                  </h2>
                  <p>
                    {resource.summary ||
                      "Open this public resource to view the details."}
                  </p>
                </div>
                <Link
                  aria-label={`Open ${resource.title}`}
                  className="read-link"
                  to={`/${username}/${resource.type}/${resource.slug}`}
                >
                  <ArrowUpRight aria-hidden="true" size={19} />
                </Link>
              </article>
            ))}
          </div>
        )}
        {nextCursor && (
          <div className="load-more">
            <button
              className="button secondary"
              onClick={() => void loadMore()}
              type="button"
            >
              Load more
            </button>
          </div>
        )}
      </main>
    </PublicLayout>
  );
}

function PublicResourcePage() {
  const { username = "", type = "", slug = "" } = useParams();
  const [resource, setResource] = useState<PublicResource>();
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    void api
      .getPublicResource(username, type, slug)
      .then((value) => {
        if (active) setResource(value);
      })
      .catch((requestError) => {
        if (active) setError(messageFromError(requestError));
      });
    return () => {
      active = false;
    };
  }, [slug, type, username]);
  return (
    <PublicLayout>
      {error ? (
        <StatePage title="This public resource is unavailable" detail={error} />
      ) : !resource ? (
        <LoadingPage />
      ) : (
        <main className="reader-page">
          <Link className="back-link" to={`/${username}/${type}`}>
            <ArrowLeft aria-hidden="true" size={17} /> @{username}/{type}
          </Link>
          <header className="article-header">
            <p className="eyebrow">
              /{username}/{resource.type}/{resource.slug}
            </p>
            <h1>{resource.title}</h1>
            {resource.summary && <p>{resource.summary}</p>}
          </header>
          <article className="markdown-body">
            <Markdown content={resource.content ?? ""} />
          </article>
        </main>
      )}
    </PublicLayout>
  );
}

function GuidePage() {
  return (
    <PublicLayout>
      <main className="setup-page public-guide">
        <GuideContent />
      </main>
    </PublicLayout>
  );
}
function GuideContent() {
  return (
    <>
      <header className="guide-intro">
        <p className="eyebrow">
          <ShieldCheck aria-hidden="true" size={15} /> The ownership model
        </p>
        <h1>Ownership is the data model.</h1>
        <p>
          m-stash resolves personal writes from the signed-in account. The
          deployment owns shared app data such as name, version, configuration,
          feature flags, catalogs, and leaderboards. Clients receive only what
          their identity and a record's visibility permit.
        </p>
      </header>
      <section className="guide-section workflow-section">
        <div className="guide-section-heading">
          <p className="eyebrow">
            <BookOpen aria-hidden="true" size={15} /> The core model
          </p>
          <h2>Two scopes. One server-enforced rule.</h2>
        </div>
        <ol className="workflow-list">
          <li>
            <span>01</span>
            <div>
              <h3>Identify</h3>
              <p>
                Signup claims one immutable username and personal namespace.
              </p>
            </div>
          </li>
          <li>
            <span>02</span>
            <div>
              <h3>Store</h3>
              <p>
                Profiles, preferences, saves, and app records live in typed
                buckets with durable routes.
              </p>
            </div>
          </li>
          <li>
            <span>03</span>
            <div>
              <h3>Scope</h3>
              <p>
                Personal records belong to an account. Shared app records
                belong to the deployment. Visibility decides who can read each
                record.
              </p>
            </div>
          </li>
          <li>
            <span>04</span>
            <div>
              <h3>Observe</h3>
              <p>
                MongoDB transactions and the durable outbox record every
                mutation.
              </p>
            </div>
          </li>
        </ol>
      </section>
      <section className="guide-section data-ownership-section">
        <div className="guide-section-heading">
          <p className="eyebrow">
            <ShieldCheck aria-hidden="true" size={15} /> Ownership without
            spoofing
          </p>
          <h2>The server derives authority instead of trusting a browser.</h2>
        </div>
        <div className="ownership-grid">
          <article>
            <h3>Personal data</h3>
            <p>
              Every profile, preference, and save record is written with the
              authenticated user’s resolved namespace and creator ID.
            </p>
          </article>
          <article>
            <h3>Shared app data</h3>
            <p>
              Administrators and trusted backends control app-wide facts such
              as app name, version, and feature configuration. Public shared
              records resolve at routes such as{" "}
              <code>/public/config/release</code>; public personal records
              resolve at{" "}
              <code>/alex-dev/record/profile</code>.
            </p>
          </article>
        </div>
      </section>
    </>
  );
}

function PublicLayout({ children }: { children: React.ReactNode }) {
  const session = useContext(SessionContext);
  const authenticated = session.kind === "authenticated";
  return (
    <div className="public-shell">
      <header className="public-header">
        <Link className="wordmark" to="/discover">
          <span>m</span>stash
        </Link>
        <nav>
          <Link to="/discover">User data</Link>
          <Link to="/public">Deployment data</Link>
          <Link to="/guide">How it works</Link>
          <Link
            className="button secondary small-button"
            to={authenticated ? "/app/resources/record" : "/login"}
          >
            {authenticated ? (
              <>
                <BookOpen aria-hidden="true" size={16} /> Workspace
              </>
            ) : (
              <>
                <UserRound aria-hidden="true" size={16} /> Log in
              </>
            )}
          </Link>
        </nav>
      </header>
      {children}
      <footer className="public-footer">
        <Link className="wordmark" to="/discover">
          <span>m</span>stash
        </Link>
        <Link to="/guide">Platform guide</Link>
        <p>Application data, owned by design.</p>
      </footer>
    </div>
  );
}

function Markdown({ content }: { content: string }) {
  return (
    <ReactMarkdown rehypePlugins={[rehypeSanitize]} remarkPlugins={[remarkGfm]}>
      {content}
    </ReactMarkdown>
  );
}
function Avatar({ name, url }: { name: string; url?: string }) {
  return url ? (
    <img alt="" className="avatar profile-avatar" src={url} />
  ) : (
    <span className="avatar profile-avatar">{initials(name)}</span>
  );
}
function LoadingPage() {
  return (
    <main className="loading-page">
      <LoaderCircle aria-label="Loading" className="spin" size={26} />
    </main>
  );
}
function LoadingRows() {
  return (
    <div className="loading-rows" aria-label="Loading data">
      <span />
      <span />
      <span />
    </div>
  );
}
function StatePage({ title, detail }: { title: string; detail: string }) {
  return (
    <main className="state-page">
      <h1>{title}</h1>
      <p>{detail}</p>
      <Link className="button secondary" to="/discover">
        Find a namespace
      </Link>
    </main>
  );
}
function NotFoundPage() {
  return (
    <PublicLayout>
      <StatePage
        title="That page has moved on."
        detail="The address does not point to anything here."
      />
    </PublicLayout>
  );
}
function initials(name: string) {
  return (
    name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((part) => part[0])
      .join("")
      .toUpperCase() || "M"
  );
}
function displayDate(value: string) {
  return new Intl.DateTimeFormat("en", {
    day: "numeric",
    month: "short",
    year: "numeric",
  }).format(new Date(value));
}
function messageFromError(error: unknown) {
  return error instanceof Error
    ? error.message
    : "Something went wrong. Please try again.";
}

export default App;

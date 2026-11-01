import {
  createContext,
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
  Code2,
  Compass,
  Copy,
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
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import rehypeSanitize from "rehype-sanitize";
import remarkGfm from "remark-gfm";
import {
  BrowserRouter,
  Link,
  Navigate,
  NavLink,
  Outlet,
  Route,
  Routes,
  useNavigate,
  useOutletContext,
  useParams,
} from "react-router-dom";
import {
  APIError,
  api,
  type BuilderApp,
  type BuilderAppInput,
  type BuilderAppUsage,
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

// Derive a URL-safe slug from free text so users never have to think about slugs.
function slugify(value: string) {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64)
    .replace(/-+$/g, "");
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
    if (!api.hasSessionHint()) {
      setSession({ kind: "anonymous" });
      return undefined;
    }
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
          <Route path="/privacy" element={<PrivacyPage />} />
          <Route path="/faq" element={<FAQPage />} />
          <Route path="/console" element={<ConsolePage />} />
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
            path="/app"
            element={
              <Workspace
                session={browserSession.session}
                setSession={browserSession.setSession}
              />
            }
          >
            <Route index element={<WorkspaceIndexRoute />} />
            <Route
              path="resources/:type"
              element={<PersonalDashboardRoute />}
            />
            <Route
              path="resources/:type/new"
              element={<PersonalEditorRoute />}
            />
            <Route
              path="resources/:type/:slug"
              element={<PersonalEditorRoute />}
            />
            <Route path="shared" element={<SharedHomeRoute />} />
            <Route path="shared/:type" element={<SharedDashboardRoute />} />
            <Route path="shared/:type/new" element={<SharedEditorRoute />} />
            <Route path="shared/:type/:slug" element={<SharedEditorRoute />} />
            <Route path="namespace" element={<NamespaceSettingsRoute />} />
            <Route path="guide" element={<GuideContent />} />
            <Route path="*" element={<WorkspaceIndexRoute />} />
          </Route>
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
        <MustacheMark />
        <span>m</span>stash
      </Link>
      <section className="auth-panel" aria-labelledby="auth-title">
        <p className="eyebrow">
          Hosted demo app
        </p>
        <h1 id="auth-title">
          {isSignUp ? "Create a demo user." : "Sign in to the demo."}
        </h1>
        <p className="auth-intro">
          {isSignUp
            ? "This creates an example user for the hosted demo, not a developer account. To add m-stash to your app, start with the builder quickstart."
            : "This signs you in to the hosted example app. Your own app's users sign up in your product, through the m-stash API."}
        </p>
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
                  ? usernameIssue || `Public profile: ${window.location.origin}/${username}`
                  : "Use 3-32 lowercase letters or numbers, with single - or _ separators."}
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
            {isSignUp ? "Create demo user" : "Sign in to demo"}
          </button>
        </form>
        <p className="auth-switch">
          {isSignUp ? "Already tried the demo?" : "New to the demo?"} {" "}
          <Link to={isSignUp ? "/login" : "/signup"}>
            {isSignUp ? "Sign in" : "Create a demo user"}
          </Link>
        </p>
        <Link className="auth-guide-link" to="/guide">
          <ShieldCheck aria-hidden="true" size={16} /> Builder quickstart
        </Link>
        <nav className="auth-policy-links" aria-label="Account information">
          <Link to="/privacy">Privacy</Link>
          <Link to="/faq">FAQ</Link>
        </nav>
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
      <Outlet context={{ namespace, claims } satisfies WorkspaceContext} />
    </WorkspaceShell>
  );
}

type WorkspaceContext = {
  namespace?: Namespace;
  claims: UserClaims;
};

function useWorkspace(): WorkspaceContext {
  return useOutletContext<WorkspaceContext>();
}

function WorkspaceIndexRoute() {
  const { namespace, claims } = useWorkspace();
  return (
    <Navigate
      replace
      to={claims.role === "admin" || !namespace ? "/app/shared" : "/app/resources/record"}
    />
  );
}

function PersonalDashboardRoute() {
  const { namespace } = useWorkspace();
  return namespace ? (
    <NamespaceDashboard scope="personal" canWrite />
  ) : (
    <Navigate replace to="/app/shared" />
  );
}

function PersonalEditorRoute() {
  const { namespace } = useWorkspace();
  return namespace ? (
    <NamespaceResourceEditor
      scope="personal"
      canWrite
      username={namespace.slug}
      namespacePublic={namespace.isPublic}
    />
  ) : (
    <Navigate replace to="/app/shared" />
  );
}

function SharedHomeRoute() {
  const { claims } = useWorkspace();
  return <SharedDataHome canWrite={claims.role === "admin"} />;
}

function SharedDashboardRoute() {
  const { claims } = useWorkspace();
  return <NamespaceDashboard scope="shared" canWrite={claims.role === "admin"} />;
}

function SharedEditorRoute() {
  const { claims } = useWorkspace();
  return (
    <NamespaceResourceEditor
      scope="shared"
      canWrite={claims.role === "admin"}
      isAdmin={claims.role === "admin"}
      namespacePublic
    />
  );
}

function NamespaceSettingsRoute() {
  const { namespace } = useWorkspace();
  return namespace ? (
    <NamespaceSettings />
  ) : (
    <Navigate replace to="/app/shared" />
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
          to={claims.role === "admin" || !namespace ? "/app/shared" : "/app/resources/record"}
        >
          <MustacheMark />
          <span>m</span>stash
        </Link>
        <nav aria-label="Workspace">
          {claims.role === "admin" && (
            <NavLink onClick={() => setMenuOpen(false)} to="/app/shared">
              <Database aria-hidden="true" size={18} /> App data
            </NavLink>
          )}
          {namespace && (
            <>
              <NavLink
                end
                onClick={() => setMenuOpen(false)}
                to="/app/resources/record"
              >
                <BookOpen aria-hidden="true" size={18} /> User data
              </NavLink>
              <NavLink
                onClick={() => setMenuOpen(false)}
                to="/app/resources/record/new"
              >
                <PenLine aria-hidden="true" size={18} /> Add user data
              </NavLink>
            </>
          )}
          {claims.role !== "admin" && (
            <NavLink onClick={() => setMenuOpen(false)} to="/app/shared">
              <Database aria-hidden="true" size={18} /> App data
            </NavLink>
          )}
          {namespace && (
            <NavLink onClick={() => setMenuOpen(false)} to="/app/namespace">
              <Settings aria-hidden="true" size={18} /> Profile
            </NavLink>
          )}
          <NavLink onClick={() => setMenuOpen(false)} to="/app/guide">
            <ShieldCheck aria-hidden="true" size={18} /> Builder guide
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
            to={claims.role === "admin" || !namespace ? "/app/shared" : "/app/resources/record"}
          >
            <MustacheMark />
            <span>m</span>stash
          </Link>
          {claims.role === "admin" ? (
            <Link
              aria-label="Manage shared data"
              className="icon-button"
              title="Manage shared data"
              to="/app/shared"
            >
              <Plus aria-hidden="true" size={21} />
            </Link>
          ) : namespace ? (
            <Link
              aria-label="Create user data record"
              className="icon-button"
              title="Create user data record"
              to="/app/resources/record/new"
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
          <p className="eyebrow">App management</p>
        <h1>App data</h1>
        <p>
          Manage app-wide config, feature flags, catalogs, and leaderboards.
          Each record has its own access level: private to operators, readable
          by signed-in users, or public to everyone.
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
              : `This is your private space — nothing is public until you say so. Create your first ${type} to get started.`}
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

function UseInYourApp({
  endpoint,
  appKey,
  publicURL,
  isPublic,
  recordIsPublic,
  visibility,
  shared,
  isAdmin,
}: {
  endpoint: string;
  appKey: string;
  publicURL?: string;
  isPublic: boolean;
  recordIsPublic: boolean;
  visibility: ResourceInput["visibility"];
  shared: boolean;
  isAdmin: boolean;
}) {
  const [lang, setLang] = useState<"curl" | "js" | "python">("curl");
  const [copied, setCopied] = useState(false);
  const key = appKey || "pk_your_app_key";
  const authenticated = !isPublic;
  const accessDenied = shared && visibility === "private" && !isAdmin;
  const accessNote = isPublic
    ? "Public records can be read by anyone. No user login is required."
    : shared
      ? visibility === "authenticated"
        ? "Available to signed-in users. The request needs a valid session or access token."
        : isAdmin
          ? "Private app data requires an admin session or server key. Never put a server key in browser code."
          : "Private app data is restricted to administrators and trusted servers. Your account cannot read it."
      : recordIsPublic
        ? "This record is Public, but your profile is private. Your signed-in account can still read it."
        : "This private record is available to your signed-in account. Browser examples use your current session; server examples need an access token from login.";
  const snippets: Record<"curl" | "js" | "python", string> = authenticated
    ? {
        curl: `curl "${endpoint}" \\\n  -H "X-M-Stash-App: ${key}" \\\n  -H "Authorization: Bearer $ACCESS_TOKEN"`,
        js: `const res = await fetch("${endpoint}", {\n  credentials: "include",\n  headers: { "X-M-Stash-App": "${key}" },\n});\nif (!res.ok) throw new Error("Request failed: " + res.status);\nconst { data } = await res.json();`,
        python: `import os\nimport requests\n\nres = requests.get(\n    "${endpoint}",\n    headers={\n        "X-M-Stash-App": "${key}",\n        "Authorization": f"Bearer {os.environ['ACCESS_TOKEN']}",\n    },\n)\nres.raise_for_status()\nprint(res.json()["data"])`,
      }
    : {
        curl: `curl "${endpoint}" \\\n  -H "X-M-Stash-App: ${key}"`,
        js: `const res = await fetch("${endpoint}", {\n  headers: { "X-M-Stash-App": "${key}" },\n});\nif (!res.ok) throw new Error("Request failed: " + res.status);\nconst { data } = await res.json();`,
        python: `import requests\n\nres = requests.get(\n    "${endpoint}",\n    headers={"X-M-Stash-App": "${key}"},\n)\nres.raise_for_status()\nprint(res.json()["data"])`,
      };
  async function copy() {
    try {
      await navigator.clipboard.writeText(snippets[lang]);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard may be unavailable; ignore.
    }
  }
  return (
    <section className="use-in-app">
      <div className="use-in-app-head">
        <p className="eyebrow">
          <Code2 aria-hidden="true" size={15} /> Use in your app
        </p>
        {isPublic && publicURL ? (
          <a
            className="use-in-app-url"
            href={publicURL}
            rel="noreferrer"
            target="_blank"
          >
            {publicURL} <ArrowUpRight aria-hidden="true" size={13} />
          </a>
        ) : (
          <span className="use-in-app-note">{accessNote}</span>
        )}
      </div>
      {isPublic && <p className="use-in-app-access">{accessNote}</p>}
      {accessDenied ? (
        <p className="use-in-app-note">
          Ask an app administrator for access or have a trusted server make
          this request. No user-level snippet can read this private app data.
        </p>
      ) : (
        <>
          <div
            aria-label="Snippet language"
            className="use-in-app-tabs"
            role="tablist"
          >
            {(["curl", "js", "python"] as const).map((option) => (
              <button
                aria-selected={lang === option}
                className={lang === option ? "active" : ""}
                key={option}
                onClick={() => setLang(option)}
                role="tab"
                type="button"
              >
                {option === "curl"
                  ? "cURL"
                  : option === "js"
                    ? "JavaScript"
                    : "Python"}
              </button>
            ))}
          </div>
          <pre className="use-in-app-code">
            <code>{snippets[lang]}</code>
          </pre>
          {authenticated && lang !== "js" && (
            <p className="use-in-app-note">
              Set ACCESS_TOKEN from the <code>token</code> returned by
              <code> POST /v1/auth/login</code> (or use an app secret on a
              trusted server). Keep credentials out of browser code and source
              control.
            </p>
          )}
          <button
            className="button secondary use-in-app-copy"
            onClick={() => void copy()}
            type="button"
          >
            {copied ? (
              <>
                <Check aria-hidden="true" size={15} /> Copied
              </>
            ) : (
              <>
                <Copy aria-hidden="true" size={15} /> Copy snippet
              </>
            )}
          </button>
        </>
      )}
    </section>
  );
}

function NamespaceResourceEditor({
  scope,
  canWrite,
  username,
  isAdmin = false,
  namespacePublic = true,
}: {
  scope: "personal" | "shared";
  canWrite: boolean;
  username?: string;
  isAdmin?: boolean;
  namespacePublic?: boolean;
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
  const [slugTouched, setSlugTouched] = useState(false);
  const canReadPublicly =
    resource.visibility === "public" && (shared || namespacePublic);

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
        const slugValue =
          resource.slug || slugify(resource.title) || `${type}-${Date.now()}`;
        const created = await api[
          shared ? "createSharedResource" : "createResource"
        ](type, { ...resource, slug: slugValue });
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
        <input
          aria-label="Resource title"
          className="title-input"
          maxLength={200}
          onChange={(event) => {
            const title = event.target.value;
            setResource((current) => ({
              ...current,
              title,
              slug: creating && !slugTouched ? slugify(title) : current.slug,
            }));
            setSaved(false);
            setError("");
          }}
          placeholder={`Untitled ${type}`}
          value={resource.title}
        />
        {creating ? (
          <label className="slug-field">
            Link
            <input
              autoCapitalize="none"
              maxLength={64}
              onChange={(event) => {
                setSlugTouched(true);
                update("slug", event.target.value.toLowerCase());
              }}
              pattern="[a-z0-9]+(-[a-z0-9]+)*"
              placeholder="auto-from-title"
              value={resource.slug}
            />
            <small>
              Auto-generated from the title — edit if you want. Lives at /{type}/
              {resource.slug || "…"}.
            </small>
          </label>
        ) : (
          <p className="eyebrow">
            /{type}/{slug}
          </p>
        )}
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
        {!creating && slug && (
          <UseInYourApp
            appKey={api.publishableKey()}
            endpoint={
              canReadPublicly
                ? shared
                  ? `${window.location.origin}/v1/public/shared/resources/${type}/${slug}`
                  : `${window.location.origin}/v1/public/${username ?? ""}/${type}/${slug}`
                : shared
                  ? `${window.location.origin}/v1/shared/resources/${type}/${slug}`
                  : `${window.location.origin}/v1/me/resources/${type}/${slug}`
            }
            isPublic={canReadPublicly}
            recordIsPublic={resource.visibility === "public"}
            visibility={resource.visibility}
            isAdmin={isAdmin}
            publicURL={
              shared
                ? `${window.location.origin}/public/${type}/${slug}`
                : username
                  ? `${window.location.origin}/${username}/${type}/${slug}`
                  : undefined
            }
                shared={shared}
          />
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

function LiveApiDemo() {
  const [state, setState] = useState<"idle" | "loading" | "done" | "error">(
    "idle",
  );
  const [payload, setPayload] = useState<Record<string, unknown> | null>(null);
  const [elapsed, setElapsed] = useState<number | null>(null);
  const [errorText, setErrorText] = useState("");

  async function run() {
    setState("loading");
    setErrorText("");
    const started = performance.now();
    try {
      const resource = await api.getPublicSharedResource("config", "release");
      setPayload(resource.data ?? {});
      setElapsed(Math.max(1, Math.round(performance.now() - started)));
      setState("done");
    } catch (requestError) {
      setErrorText(messageFromError(requestError));
      setState("error");
    }
  }

  return (
    <section className="live-demo" aria-labelledby="live-demo-title">
      <div className="live-demo-head">
        <p className="eyebrow">
          <Code2 aria-hidden="true" size={15} /> See for yourself
        </p>
        <h2 id="live-demo-title">Press run. Watch a real backend answer.</h2>
        <p>
          No account, no keys, no setup — this hits the live m-stash API right
          now. Your app reads its own data the exact same way.
        </p>
      </div>
      <div className="live-demo-terminal">
        <div className="live-demo-bar">
          <span className="live-demo-dot" />
          <span className="live-demo-dot" />
          <span className="live-demo-dot" />
          <code>GET /v1/public/shared/resources/config/release</code>
        </div>
        <div className="live-demo-body">
          {state === "idle" && (
            <p className="live-demo-hint">// the response appears here</p>
          )}
          {state === "loading" && (
            <p className="live-demo-hint">
              <LoaderCircle aria-hidden="true" className="spin" size={15} />{" "}
              calling the API…
            </p>
          )}
          {state === "error" && <p className="live-demo-error">{errorText}</p>}
          {state === "done" && payload && (
            <>
              <p className="live-demo-status">
                <Check aria-hidden="true" size={14} /> 200 OK
                {elapsed !== null ? ` · ${elapsed} ms` : ""}
              </p>
              <pre>
                <code>{JSON.stringify(payload, null, 2)}</code>
              </pre>
            </>
          )}
        </div>
      </div>
      <div className="live-demo-actions">
        <button
          className="button primary"
          disabled={state === "loading"}
          onClick={() => void run()}
          type="button"
        >
          {state === "done" ? (
            <>
              <ArrowUpRight aria-hidden="true" size={17} /> Run it again
            </>
          ) : (
            <>
              <Code2 aria-hidden="true" size={17} /> Run the request
            </>
          )}
        </button>
        {state === "done" && (
          <p className="live-demo-realize">
            That’s the whole move — the server owns the data, your app just asks.
          </p>
        )}
      </div>
    </section>
  );
}

function NamespaceLookupPage() {
  return (
    <PublicLayout>
      <main className="discovery-page">
        <div className="discover-intro">
          <p className="eyebrow">
            <Compass aria-hidden="true" size={15} /> For AI app builders
          </p>
          <h1>Give your AI-built app a backend.</h1>
          <p>
            Add sign-up, login, and private per-user data without wiring a
            database or writing security rules. m-stash handles identity and
            ownership behind a small API. MongoDB stays behind the curtain.
          </p>
          <div className="discover-actions">
            <a
              className="button primary"
              href="https://github.com/ranfysvalle02/m-stash/blob/main/quick.md"
              rel="noreferrer"
              target="_blank"
            >
              <ArrowUpRight aria-hidden="true" size={17} /> Build your first app
            </a>
            <Link className="discover-guide-link" to="/public">
              Explore the live demo <ArrowUpRight aria-hidden="true" size={16} />
            </Link>
          </div>
          <p className="builder-promise">
            Your users sign up in <em>your</em> app. No m-stash user account
            required.
          </p>
        </div>
        <LiveApiDemo />
        <section className="scope-model" aria-labelledby="scope-model-title">
          <header>
            <p className="eyebrow">The risky backend work, already done</p>
            <h2 id="scope-model-title">
              Your AI builds the app. m-stash supplies the backend.
            </h2>
          </header>
          <article>
            <h3>Auth included</h3>
            <p>
              Ready-to-use sign-up, login, and sessions. Your app owns the
              experience; m-stash handles identity underneath.
            </p>
          </article>
          <article>
            <h3>Ownership enforced</h3>
            <p>
              The server scopes records to the signed-in user. The browser
              never chooses an owner or receives database credentials.
            </p>
          </article>
        </section>
        <section className="public-lookup" aria-labelledby="public-lookup-title">
          <div>
            <p className="eyebrow">MongoDB underneath</p>
            <h2 id="public-lookup-title">A small API between your app and your data.</h2>
            <p>
              Build against m-stash instead of MongoDB. Self-host it, keep
              control of your database, and reach it directly when you need
              the escape hatch.
            </p>
          </div>
          <a
            className="button secondary"
            href="https://github.com/ranfysvalle02/m-stash/blob/main/quick.md"
            rel="noreferrer"
            target="_blank"
          >
            Read the build guide <ArrowUpRight aria-hidden="true" size={16} />
          </a>
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
            <Globe2 aria-hidden="true" size={15} /> Read-only live demo
          </p>
          <h1>Your app's backend, out loud.</h1>
          <p>
            Every record here is a live m-stash API response — the same backend
            an AI-built app calls for auth and per-user data. A developer demo,
            not a product to sign up for. Builders start with the{" "}
            <Link to="/guide">Quickstart</Link>.
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
          <ArrowLeft aria-hidden="true" size={17} /> All public data
        </Link>
        <div className="discover-intro">
          <p className="eyebrow">
            <Globe2 aria-hidden="true" size={15} /> Public bucket
          </p>
          <h1>{type}</h1>
          <p>
            The exact JSON any client reads from{" "}
            <code>GET /v1/public/shared/resources/{type}</code> — the {type}{" "}
            records this app chose to make public. No auth, no keys.
          </p>
        </div>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        {resources.length === 0 && !error ? (
          <section className="empty-state discovery-empty">
            <Globe2 aria-hidden="true" size={28} />
            <h2>Nothing public in {type} yet.</h2>
            <p>
              When this app publishes a public {type} record, it shows up here
              — and in the API — for every client, instantly.
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
                      "Open to read the full record."}
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
          {resource.data && Object.keys(resource.data).length > 0 && (
            <section className="json-payload" aria-label="JSON payload">
              <p className="eyebrow">
                <Globe2 aria-hidden="true" size={15} /> The JSON any client reads
              </p>
              <pre>
                <code>{JSON.stringify(resource.data, null, 2)}</code>
              </pre>
            </section>
          )}
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
          <ShieldCheck aria-hidden="true" size={15} /> For AI app builders
        </p>
        <h1>Ship the app. Skip the backend setup.</h1>
        <p>
          Add sign-up, login, and server-enforced user ownership to the app
          you're building. Your users use your product; m-stash works behind
          its API. No m-stash accounts for your users, no database access in
          the frontend.
        </p>
        <div className="discover-actions">
          <a
            className="button primary"
            href="https://github.com/ranfysvalle02/m-stash/blob/main/quickstart.md"
            rel="noreferrer"
            target="_blank"
          >
            Start with the quickstart <ArrowUpRight aria-hidden="true" size={16} />
          </a>
          <Link className="discover-guide-link" to="/public">
            Explore the live demo <ArrowUpRight aria-hidden="true" size={16} />
          </Link>
        </div>
      </header>
      <section className="guide-section workflow-section">
        <div className="guide-section-heading">
          <p className="eyebrow">
            <BookOpen aria-hidden="true" size={15} /> Your path to production
          </p>
          <h2>Three steps. No hand-written ownership rules.</h2>
        </div>
        <ol className="workflow-list">
          <li>
            <span>01</span>
            <div>
              <h3>Deploy m-stash</h3>
              <p>
                Self-host the service with a MongoDB replica set and a few
                secrets. Keep your database and platform credentials on the
                server.
              </p>
            </div>
          </li>
          <li>
            <span>02</span>
            <div>
              <h3>Provision your app</h3>
              <p>
                Bootstrap a builder account with your platform token once, then
                sign in to the <Link to="/console">builder console</Link> to
                create apps and copy their keys — no platform token in the
                browser.
              </p>
            </div>
          </li>
          <li>
            <span>03</span>
            <div>
              <h3>Call the API from your product</h3>
              <p>
                Your UI owns sign-up and login. m-stash derives record ownership
                from the authenticated identity and enforces access on every
                request.
              </p>
            </div>
          </li>
          <li>
            <span>04</span>
            <div>
              <h3>Keep building</h3>
              <p>
                Store user data separately from app-wide config. Start private;
                choose deliberately when something should be shared or public.
              </p>
            </div>
          </li>
        </ol>
      </section>
      <section className="guide-section builder-examples">
        <div className="guide-section-heading">
          <p className="eyebrow">
            <Code2 aria-hidden="true" size={15} /> Build something real
          </p>
          <h2>Profiles for users. Config for your app.</h2>
          <p>
            Your app owns the sign-up screen. After m-stash returns a user token,
            save a profile under that identity. Manage shared config separately
            with an admin credential on your server.
          </p>
        </div>
        <div className="config-snippets">
          <CopyableSnippet
            title="Bootstrap a builder account"
            description="Run once on your server with the platform token. Then sign in to the console."
            code={[
              'curl "$API_URL/v1/builder/accounts" \\',
              '  -H "Authorization: Bearer $PLATFORM_TOKEN" \\',
              '  -H "Content-Type: application/json" \\',
              `  -d '{"email":"you@example.com","password":"a-long-unique-password"}'`,
            ].join("\n")}
          />
          <CopyableSnippet
            title="Create a private user profile"
            description="Run in your app after sign-up. The owner comes from the user's token."
            code={[
              'const response = await fetch(`${API_URL}/v1/me/resources/profile`, {',
              '  method: "POST",',
              '  headers: {',
              '    "X-M-Stash-App": APP_KEY,',
              '    Authorization: `Bearer ${accessToken}`,',
              '    "Content-Type": "application/json",',
              '  },',
              '  body: JSON.stringify({',
              '    slug: "main",',
              '    title: "My profile",',
              '    visibility: "private",',
              '    data: { displayName: "Ada" },',
              '  }),',
              '});',
              'if (!response.ok) throw new Error("Profile save failed");',
            ].join("\n")}
          />
          <CopyableSnippet
            title="Manage app-wide config"
            description="Run on a trusted server. Never expose APP_SECRET in browser code."
            code={`curl "$API_URL/v1/shared/resources/config" \\
  -H "X-M-Stash-App: $APP_KEY" \\
  -H "Authorization: Bearer $APP_SECRET" \\
  -H "Content-Type: application/json" \\
  -d '{"slug":"release","title":"Release config","visibility":"authenticated","data":{"version":"1.0.0"}}'`}
          />
        </div>
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

function CopyableSnippet({
  title,
  description,
  code,
}: {
  title: string;
  description: string;
  code: string;
}) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  }

  return (
    <article className="config-snippet">
      <div className="snippet-heading">
        <div>
          <h3>{title}</h3>
          <p>{description}</p>
        </div>
        <button
          aria-label={copied ? "Copied" : `Copy ${title} example`}
          className="icon-button"
          onClick={() => void copy()}
          title={copied ? "Copied" : "Copy example"}
          type="button"
        >
          {copied ? <Check aria-hidden="true" size={17} /> : <Copy aria-hidden="true" size={17} />}
        </button>
      </div>
      <pre><code>{code}</code></pre>
    </article>
  );
}

function FAQPage() {
  const questions = [
    ["What is m-stash?", "A backend layer for AI app builders. It gives your product sign-up, login, and server-enforced per-user data through an API, with MongoDB behind the service instead of exposed to your frontend."],
    ["Do my users need m-stash accounts?", "No. Your users sign up and log in through your product's UI. Your app calls the m-stash auth API behind the scenes; m-stash is infrastructure, not another consumer account your users need to manage."],
    ["How do I start building?", "Follow the Quickstart to self-host m-stash and configure MongoDB. Bootstrap a builder account once with your platform token, then use the builder console to create apps, copy their keys, edit allowed origins, and rotate secrets — no platform token in the browser."],
    ["What is the hosted sign-up page for?", "It creates a sample user in the live demo app so you can inspect the app-user experience. It does not create a developer account or provision an app. Builders should start with the Quickstart."],
    ["How does m-stash keep users' data separate?", "The API derives ownership from the authenticated identity. Clients do not send an owner ID, and the server applies that identity to reads and writes. Personal and app-wide data use separate scopes."],
    ["Do I need MongoDB in my frontend?", "No. Your application calls the m-stash API; database credentials stay server-side. MongoDB powers storage underneath and remains under your deployment's control."],
    ["Can I make records public?", "Yes, when your app deliberately chooses public visibility. Public data can be read without signing in, copied, and potentially indexed. Private is the default for records created through the workspace."],
    ["How do app keys work?", "The publishable key identifies your app and can be used in frontend requests. It does not grant access to private data. Keep platform tokens, passwords, and server-only app secrets out of browser code."],
    ["Who runs the hosted demo?", "The demo runs on Render and uses MongoDB. For production, you deploy and operate your own instance. This site does not publish an uptime guarantee, pricing commitment, or support response time."],
  ];
  return (
    <PublicLayout>
      <main className="setup-page information-page">
        <header className="information-intro">
          <p className="eyebrow">The essentials</p>
          <h1>Frequently asked questions</h1>
          <p>Builder-first answers about the backend layer and the hosted demo.</p>
        </header>
        <div className="faq-list">
          {questions.map(([question, answer]) => (
            <details key={question}>
              <summary>{question}</summary>
              <p>{answer}</p>
            </details>
          ))}
        </div>
        <p className="information-note">For details on data handling, read our <Link to="/privacy">Privacy Policy</Link>.</p>
      </main>
    </PublicLayout>
  );
}

function PrivacyPage() {
  return (
    <PublicLayout>
      <main className="setup-page information-page">
        <header className="information-intro">
          <p className="eyebrow">Last updated October 31, 2026</p>
          <h1>Privacy Policy</h1>
          <p>This policy describes the hosted m-stash developer demo. Signing up here creates a sample app-user account, not a developer account. The operator controls hosting, database access, and retention.</p>
        </header>
        <section><h2>What we store</h2><p>Your account includes your email address, a hashed password, username, and account identifiers. Your profile may include a display name, bio, avatar URL, and public visibility setting. Records include the content and structured data you submit, ownership identifiers, visibility, and creation and update times.</p></section>
        <section><h2>Why we use it</h2><p>This data lets the service sign you in, associate records with your account, enforce access rules, and deliver records to authorized clients. Authentication rate limiting uses IP-based identifiers. Browser sessions store hashed refresh tokens and expiry information.</p></section>
        <section><h2>Public and private data</h2><p>Signup creates a public sample welcome record containing your username and sample text. New records you create default to Private. Public records and public profile information are accessible without signing in; your email and password hash are not part of those public responses. Public is not an unlisted or secret-link mode.</p><p>Private means restricted by the application's access rules, not end-to-end encrypted. Authorized database and deployment operators may access stored data. Do not store passwords, payment details, or other sensitive secrets in records.</p></section>
        <section><h2>Cookies and external requests</h2><p>The app uses first-party cookies for access tokens, refresh sessions, and protection against cross-site request forgery. Access and refresh cookies are HttpOnly. These cookies are needed for browser sign-in; they are not advertising cookies.</p><p>Avatar and Markdown image URLs may cause your browser to contact external image hosts. Those hosts can receive your IP address and request metadata under their own policies.</p></section>
        <section><h2>Hosting, logs, and delivery</h2><p>This hosted deployment runs on Render and stores application data in MongoDB. Hosting and database providers process data needed to operate the service. The app logs request identifiers, methods, normalized routes, response statuses, and timing. Infrastructure providers may keep additional operational logs.</p><p>Mutations can leave event metadata in a durable event outbox. If administrators configure webhooks, event data may be sent to their chosen destinations. The operator is responsible for those destinations and their retention policies.</p></section>
        <section><h2>Retention and deletion</h2><p>Records remain stored until deleted. Session and rate-limit entries expire, although database cleanup may not be immediate. Deleting a record removes it from active record storage; copies in backups, logs, event history, webhook destinations, or third-party caches may remain according to the operator's policies.</p><p>There is currently no self-service account deletion or full account export. This site does not publish a fixed backup or log retention period. You can make your profile and records private or delete individual records from the workspace.</p></section>
        <section><h2>Requests and policy changes</h2><p>For account removal, access or correction requests, retention details, or privacy concerns, contact the operator of the deployment you use. A dedicated privacy contact and legal operator identity have not yet been published for this hosted site; this policy does not imply a staffed support channel.</p><p>Changes to this policy will appear on this page with an updated date. Self-hosted operators should provide their own identity, contact details, retention schedule, and any notices required in their jurisdiction.</p></section>
      </main>
    </PublicLayout>
  );
}

type BuilderIdentity = { id: string; email: string };

type BuilderState =
  | { kind: "loading" }
  | { kind: "anonymous" }
  | { kind: "authenticated"; builder: BuilderIdentity };

function ConsolePage() {
  const [state, setState] = useState<BuilderState>({ kind: "loading" });

  useEffect(() => {
    let active = true;
    async function restore() {
      try {
        const builder = await api.builderMe();
        if (active) setState({ kind: "authenticated", builder });
        return;
      } catch {
        // Fall through to a refresh attempt before giving up.
      }
      try {
        await api.builderRefresh();
        const builder = await api.builderMe();
        if (active) setState({ kind: "authenticated", builder });
      } catch {
        if (active) setState({ kind: "anonymous" });
      }
    }
    void restore();
    return () => {
      active = false;
    };
  }, []);

  return (
    <PublicLayout>
      {state.kind === "loading" ? (
        <LoadingPage />
      ) : state.kind === "anonymous" ? (
        <ConsoleLogin
          onAuthenticated={(builder) =>
            setState({ kind: "authenticated", builder })
          }
        />
      ) : (
        <ConsoleDashboard
          builder={state.builder}
          onSignedOut={() => setState({ kind: "anonymous" })}
        />
      )}
    </PublicLayout>
  );
}

function ConsoleLogin({
  onAuthenticated,
}: {
  onAuthenticated: (builder: BuilderIdentity) => void;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setSubmitting(true);
    try {
      const result = await api.builderLogin(email, password);
      onAuthenticated(result.builder);
    } catch (requestError) {
      setError(messageFromError(requestError));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="setup-page information-page">
      <header className="information-intro">
        <p className="eyebrow">Builder console</p>
        <h1>Sign in to manage your apps.</h1>
        <p>
          Builder accounts are created by the deployment operator with the
          platform token. This console never sees that token.
        </p>
      </header>
      <form className="profile-form console-form" onSubmit={submit}>
        <label>
          Email
          <input
            autoComplete="email"
            onChange={(event) => setEmail(event.target.value)}
            required
            type="email"
            value={email}
          />
        </label>
        <label>
          Password
          <input
            autoComplete="current-password"
            onChange={(event) => setPassword(event.target.value)}
            required
            type="password"
            value={password}
          />
        </label>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <div className="form-actions">
          <button className="button primary" disabled={submitting} type="submit">
            {submitting ? (
              <LoaderCircle aria-hidden="true" className="spin" size={17} />
            ) : (
              <ChevronRight aria-hidden="true" size={17} />
            )}{" "}
            Sign in
          </button>
        </div>
      </form>
      <p className="information-note">
        No builder account yet? Ask your operator to run the bootstrap in the{" "}
        <Link to="/guide">builder guide</Link>.
      </p>
    </main>
  );
}

function ConsoleDashboard({
  builder,
  onSignedOut,
}: {
  builder: BuilderIdentity;
  onSignedOut: () => void;
}) {
  const [apps, setApps] = useState<BuilderApp[]>();
  const [error, setError] = useState("");
  const [reveal, setReveal] = useState<{
    app: BuilderApp;
    secretKey: string;
    rotated: boolean;
  }>();
  const [displayName, setDisplayName] = useState("");
  const [slug, setSlug] = useState("");
  const [adminEmail, setAdminEmail] = useState("");
  const [adminPassword, setAdminPassword] = useState("");
  const [creating, setCreating] = useState(false);
  const [copied, setCopied] = useState("");

  useEffect(() => {
    let active = true;
    void api
      .listBuilderApps()
      .then((list) => {
        if (active) setApps(list);
      })
      .catch((requestError) => {
        if (active) {
          setApps([]);
          setError(messageFromError(requestError));
        }
      });
    return () => {
      active = false;
    };
  }, []);

  async function copy(label: string, value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(label);
      window.setTimeout(() => setCopied(""), 1500);
    } catch {
      setCopied("");
    }
  }

  async function createApp(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setCreating(true);
    try {
      const input: BuilderAppInput = { slug, displayName };
      if (adminEmail && adminPassword) {
        input.adminEmail = adminEmail;
        input.adminPassword = adminPassword;
      }
      const result = await api.createBuilderApp(input);
      setReveal({ app: result.data, secretKey: result.secretKey, rotated: false });
      setApps((current) => [result.data, ...(current ?? [])]);
      setDisplayName("");
      setSlug("");
      setAdminEmail("");
      setAdminPassword("");
    } catch (requestError) {
      setError(messageFromError(requestError));
    } finally {
      setCreating(false);
    }
  }

  async function signOut() {
    try {
      await api.builderLogout();
    } finally {
      onSignedOut();
    }
  }

  function handleUpdated(updated: BuilderApp) {
    setApps((current) =>
      (current ?? []).map((item) => (item.id === updated.id ? updated : item)),
    );
  }

  function handleRotated(app: BuilderApp, secretKey: string) {
    setReveal({ app, secretKey, rotated: true });
  }

  return (
    <main className="setup-page console-page">
      <div className="console-head">
        <div>
          <p className="eyebrow">Builder console</p>
          <h1>Your apps</h1>
          <p className="console-id">Signed in as {builder.email}</p>
        </div>
        <button className="text-button" onClick={() => void signOut()} type="button">
          <LogOut aria-hidden="true" size={16} /> Sign out
        </button>
      </div>

      {reveal && (
        <section className="console-secret" role="alert">
          <p className="eyebrow">
            <ShieldCheck aria-hidden="true" size={15} /> Save your secret key now
          </p>
          <h2>
            {reveal.rotated
              ? `New secret key for ${reveal.app.displayName}`
              : `${reveal.app.displayName} is ready.`}
          </h2>
          <p>
            The secret key is shown once. Store it on your server; it is never
            displayed again{reveal.rotated ? ", and the previous key no longer works" : ""}.
          </p>
          <div className="key-row">
            <span>Publishable key</span>
            <code>{reveal.app.publishableKey}</code>
            <button
              aria-label="Copy publishable key"
              className="icon-button"
              onClick={() => void copy("pk", reveal.app.publishableKey)}
              type="button"
            >
              {copied === "pk" ? (
                <Check aria-hidden="true" size={16} />
              ) : (
                <Copy aria-hidden="true" size={16} />
              )}
            </button>
          </div>
          <div className="key-row">
            <span>Secret key</span>
            <code>{reveal.secretKey}</code>
            <button
              aria-label="Copy secret key"
              className="icon-button"
              onClick={() => void copy("sk", reveal.secretKey)}
              type="button"
            >
              {copied === "sk" ? (
                <Check aria-hidden="true" size={16} />
              ) : (
                <Copy aria-hidden="true" size={16} />
              )}
            </button>
          </div>
          <button
            className="button secondary"
            onClick={() => setReveal(undefined)}
            type="button"
          >
            I saved the secret key
          </button>
        </section>
      )}

      <section className="console-create">
        <h2>Create an app</h2>
        <form className="profile-form" onSubmit={createApp}>
          <div className="form-grid">
            <label>
              App name
              <input
                maxLength={80}
                onChange={(event) => setDisplayName(event.target.value)}
                placeholder="Arcade"
                required
                value={displayName}
              />
            </label>
            <label>
              Slug
              <input
                autoCapitalize="none"
                onChange={(event) => setSlug(normalizeResourceType(event.target.value))}
                placeholder="arcade"
                required
                spellCheck={false}
                value={slug}
              />
            </label>
          </div>
          <div className="form-grid">
            <label>
              Admin email (optional)
              <input
                onChange={(event) => setAdminEmail(event.target.value)}
                placeholder="admin@example.com"
                type="email"
                value={adminEmail}
              />
            </label>
            <label>
              Admin password (optional)
              <input
                minLength={12}
                onChange={(event) => setAdminPassword(event.target.value)}
                placeholder="at least 12 characters"
                type="password"
                value={adminPassword}
              />
            </label>
          </div>
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <div className="form-actions">
            <button
              className="button primary"
              disabled={creating || !slug || !displayName}
              type="submit"
            >
              {creating ? (
                <LoaderCircle aria-hidden="true" className="spin" size={17} />
              ) : (
                <Plus aria-hidden="true" size={17} />
              )}{" "}
              Create app
            </button>
          </div>
        </form>
      </section>

      <section className="console-apps">
        <h2>Apps</h2>
        {!apps ? (
          <LoadingRows />
        ) : apps.length === 0 ? (
          <p className="console-empty">No apps yet. Create your first one above.</p>
        ) : (
          <ul className="console-app-list">
            {apps.map((item) => (
              <ConsoleAppRow
                app={item}
                key={item.id}
                onError={setError}
                onRotated={handleRotated}
                onUpdated={handleUpdated}
              />
            ))}
          </ul>
        )}
      </section>
    </main>
  );
}

function ConsoleAppRow({
  app,
  onRotated,
  onUpdated,
  onError,
}: {
  app: BuilderApp;
  onRotated: (app: BuilderApp, secretKey: string) => void;
  onUpdated: (app: BuilderApp) => void;
  onError: (message: string) => void;
}) {
  const [origins, setOrigins] = useState((app.allowedOrigins ?? []).join("\n"));
  const [busy, setBusy] = useState<"" | "save" | "rotate" | "active">("");
  const [copied, setCopied] = useState(false);
  const [savedOrigins, setSavedOrigins] = useState(false);
  const [usage, setUsage] = useState<BuilderAppUsage>();
  const [showConnect, setShowConnect] = useState(false);

  useEffect(() => {
    let active = true;
    void api
      .builderAppUsage(app.id)
      .then((value) => {
        if (active) setUsage(value);
      })
      .catch(() => {
        // Usage is best-effort; a failure should not block management.
      });
    return () => {
      active = false;
    };
  }, [app.id]);

  const connectSnippet = [
    `const API_URL = "${window.location.origin}";`,
    `const APP_KEY = "${app.publishableKey}";`,
    "",
    "export async function signUp(email, password, username) {",
    "  const res = await fetch(`${API_URL}/v1/auth/signup`, {",
    '    method: "POST",',
    '    credentials: "include",',
    '    headers: { "X-M-Stash-App": APP_KEY, "Content-Type": "application/json" },',
    "    body: JSON.stringify({ email, password, username }),",
    "  });",
    '  if (!res.ok) throw new Error("Sign-up failed");',
    "  return res.json();",
    "}",
    "",
    "export async function saveRecord(type, body) {",
    "  const res = await fetch(`${API_URL}/v1/me/resources/${type}`, {",
    '    method: "POST",',
    '    credentials: "include",',
    '    headers: { "X-M-Stash-App": APP_KEY, "Content-Type": "application/json" },',
    "    body: JSON.stringify(body),",
    "  });",
    '  if (!res.ok) throw new Error("Save failed");',
    "  return res.json();",
    "}",
  ].join("\n");

  async function copyKey() {
    try {
      await navigator.clipboard.writeText(app.publishableKey);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  }

  async function saveOrigins() {
    setBusy("save");
    onError("");
    try {
      const list = origins
        .split(/[\n,]/)
        .map((value) => value.trim())
        .filter(Boolean);
      const updated = await api.updateBuilderApp(app.id, { allowedOrigins: list });
      onUpdated(updated);
      setOrigins((updated.allowedOrigins ?? []).join("\n"));
      setSavedOrigins(true);
      window.setTimeout(() => setSavedOrigins(false), 1500);
    } catch (requestError) {
      onError(messageFromError(requestError));
    } finally {
      setBusy("");
    }
  }

  async function rotate() {
    if (
      !window.confirm(
        `Rotate the secret key for ${app.displayName}? The current key stops working immediately.`,
      )
    ) {
      return;
    }
    setBusy("rotate");
    onError("");
    try {
      const result = await api.rotateBuilderAppSecret(app.id);
      onUpdated(result.data);
      onRotated(result.data, result.secretKey);
    } catch (requestError) {
      onError(messageFromError(requestError));
    } finally {
      setBusy("");
    }
  }

  async function toggleActive() {
    const next = !app.active;
    if (
      !next &&
      !window.confirm(
        `Disable ${app.displayName}? Its publishable key stops working until you re-enable it.`,
      )
    ) {
      return;
    }
    setBusy("active");
    onError("");
    try {
      const updated = await api.updateBuilderApp(app.id, { active: next });
      onUpdated(updated);
    } catch (requestError) {
      onError(messageFromError(requestError));
    } finally {
      setBusy("");
    }
  }

  return (
    <li className={app.active ? "console-app-card" : "console-app-card disabled"}>
      <div className="console-app-row">
        <div className="console-app-main">
          <div className="console-app-title">
            <h3>{app.displayName}</h3>
            {!app.active && <span className="status">Disabled</span>}
          </div>
          <p className="console-slug">/{app.slug}</p>
          <p className="console-usage">
            {usage
              ? `${usage.users} user${usage.users === 1 ? "" : "s"} · ${usage.records} record${usage.records === 1 ? "" : "s"}`
              : "…"}
          </p>
        </div>
        <div className="console-key">
          <code>{app.publishableKey}</code>
          <button
            aria-label={`Copy publishable key for ${app.displayName}`}
            className="icon-button"
            onClick={() => void copyKey()}
            type="button"
          >
            {copied ? (
              <Check aria-hidden="true" size={16} />
            ) : (
              <Copy aria-hidden="true" size={16} />
            )}
          </button>
        </div>
      </div>
      <div className="console-app-manage">
        <label>
          Allowed browser origins
          <textarea
            onChange={(event) => setOrigins(event.target.value)}
            placeholder={"https://app.example.com\nhttps://staging.example.com"}
            rows={2}
            spellCheck={false}
            value={origins}
          />
          <small>One origin per line. Leave empty to use the deployment default.</small>
        </label>
        <div className="console-app-actions">
          <button
            className="button secondary"
            disabled={busy !== ""}
            onClick={() => void saveOrigins()}
            type="button"
          >
            {busy === "save" ? (
              <LoaderCircle aria-hidden="true" className="spin" size={15} />
            ) : savedOrigins ? (
              <Check aria-hidden="true" size={15} />
            ) : (
              <Save aria-hidden="true" size={15} />
            )}{" "}
            Save origins
          </button>
          <button
            className="button secondary"
            onClick={() => setShowConnect((value) => !value)}
            type="button"
          >
            <Code2 aria-hidden="true" size={15} /> {showConnect ? "Hide client" : "Get client code"}
          </button>
          <button
            className="button secondary danger-button"
            disabled={busy !== ""}
            onClick={() => void rotate()}
            type="button"
          >
            {busy === "rotate" ? (
              <LoaderCircle aria-hidden="true" className="spin" size={15} />
            ) : (
              <ShieldCheck aria-hidden="true" size={15} />
            )}{" "}
            Rotate secret
          </button>
          <button
            className="button secondary danger-button"
            disabled={busy !== ""}
            onClick={() => void toggleActive()}
            type="button"
          >
            {busy === "active" ? (
              <LoaderCircle aria-hidden="true" className="spin" size={15} />
            ) : (
              <Trash2 aria-hidden="true" size={15} />
            )}{" "}
            {app.active ? "Disable" : "Enable"}
          </button>
        </div>
        {showConnect && (
          <CopyableSnippet
            title="Starter client"
            description="Paste into your app. The publishable key is safe in frontend code."
            code={connectSnippet}
          />
        )}
      </div>
    </li>
  );
}

function MustacheMark() {
  return (
    <svg
      aria-hidden="true"
      className="wordmark-mark"
      focusable="false"
      viewBox="8 26 104 32"
    >
      <path
        fill="currentColor"
        d="M60 40C66 32 70 28 76 28 84 28 88 31 92 34 98 36 100 37 102 38 107 40 110 34 108 28 106 32 103 40 100 46 94 53 88 56 82 55 74 53 68 50 60 52 52 50 46 53 38 55 32 56 26 53 20 46 17 40 14 32 12 28 10 34 13 40 18 38 20 37 22 36 28 34 32 31 36 28 44 28 50 28 54 32 60 40Z"
      />
    </svg>
  );
}

function PublicLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="public-shell">
      <header className="public-header">
        <Link className="wordmark" to="/discover">
          <MustacheMark />
          <span>m</span>stash
        </Link>
        <nav>
          <Link to="/guide">For builders</Link>
          <Link to="/public">Live demo</Link>
          <Link to="/console">Console</Link>
          <a
            className="button secondary small-button"
            href="https://github.com/ranfysvalle02/m-stash/blob/main/quickstart.md"
            rel="noreferrer"
            target="_blank"
          >
            Quickstart <ArrowUpRight aria-hidden="true" size={15} />
          </a>
        </nav>
      </header>
      {children}
      <footer className="public-footer">
        <Link className="wordmark" to="/discover">
          <MustacheMark />
          <span>m</span>stash
        </Link>
        <nav aria-label="Help and policies">
          <Link to="/guide">How it works</Link>
          <Link to="/faq">FAQ</Link>
          <Link to="/privacy">Privacy Policy</Link>
        </nav>
        <p>Stash it. ’Stache it. — application data, owned by design.</p>
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
        Back to m-stash
      </Link>
    </main>
  );
}
function NotFoundPage() {
  return (
    <PublicLayout>
      <StatePage
        title="This page shaved."
        detail="Nothing’s stashed at this address — let’s get you back."
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

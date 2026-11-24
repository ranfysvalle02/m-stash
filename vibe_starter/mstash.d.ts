// Type declarations for mstash.js — the zero-dependency m-stash client.
//
// Keep this file next to mstash.js and TypeScript picks it up automatically:
//
//   import { createClient } from "./mstash.js";
//   type Note = { body: string };
//   const stash = createClient({ api: "https://…", app: "pk_…" });
//   const { data } = await stash.me.get<Note>("notes", "first");
//   data.data?.body; // string | undefined

/** Who can read a record. `authenticated` (any signed-in user) is for shared records only. */
export type Visibility = "private" | "authenticated" | "public";

/** Free-form JSON stored on a record under `data`. */
export type RecordData = Record<string, unknown>;

export interface User {
  id: string;
  email: string;
  role: string;
  createdAt: string;
}

export interface Namespace {
  id: string;
  slug: string;
  kind: "personal" | "shared";
  displayName: string;
  bio?: string;
  avatarURL?: string;
  isPublic: boolean;
  createdAt: string;
  updatedAt: string;
}

/** A full record, as returned by `get`, `create`, `update`, and `increment`. */
export interface Resource<T extends RecordData = RecordData> {
  id: string;
  namespaceId: string;
  creatorId: string;
  type: string;
  slug: string;
  title: string;
  summary?: string;
  content?: string;
  data?: T;
  visibility: Visibility;
  createdAt: string;
  updatedAt: string;
}

/** A list entry. Lists omit `content` and `data`; call `get` for the full record. */
export interface ResourcePreview {
  id: string;
  type: string;
  slug: string;
  title: string;
  summary?: string;
  visibility: Visibility;
  createdAt: string;
  updatedAt: string;
}

/** A record served anonymously. Ownership and visibility are not exposed. */
export interface PublicResource<T extends RecordData = RecordData> {
  type: string;
  slug: string;
  title: string;
  summary?: string;
  content?: string;
  data?: T;
  createdAt: string;
  updatedAt: string;
}

export interface PublicResourcePreview {
  type: string;
  slug: string;
  title: string;
  summary?: string;
  createdAt: string;
  updatedAt: string;
}

export interface PublicProfile {
  slug: string;
  displayName: string;
  bio?: string;
  avatarURL?: string;
}

export interface ResourceInput<T extends RecordData = RecordData> {
  /** URL-safe id, unique per type. Required on create; ignored on update (the path wins). */
  slug?: string;
  title: string;
  summary?: string;
  content?: string;
  data?: T;
  visibility: Visibility;
}

export interface Single<T> {
  data: T;
}

export interface Page<T> {
  data: T[];
  page: {
    limit: number;
    /** Pass to the next `list` call; `null` on the last page. */
    nextCursor: string | null;
  };
}

export interface ListOptions {
  limit?: number;
  cursor?: string | null;
}

export interface SignupInput {
  email: string;
  password: string;
  /** 3–32 lowercase letters, numbers, `-` or `_`. Immutable once claimed. */
  username: string;
}

export interface LoginInput {
  email: string;
  password: string;
}

export interface SignupResult {
  token: string;
  user: User;
  namespace: Namespace;
}

export interface LoginResult {
  token: string;
  user: User;
}

export interface ResourceClient<WriteResult = never> {
  list(type: string, options?: ListOptions): Promise<Page<ResourcePreview>>;
  get<T extends RecordData = RecordData>(type: string, slug: string): Promise<Single<Resource<T>>>;
  create<T extends RecordData = RecordData>(
    type: string,
    record: ResourceInput<T> & { slug: string },
  ): Promise<Single<Resource<T>>>;
  update<T extends RecordData = RecordData>(
    type: string,
    slug: string,
    record: ResourceInput<T>,
  ): Promise<Single<Resource<T>>>;
  /**
   * Atomically add `by` (default 1) to a numeric field. `field` may be `"likes"` or `"data.likes"`.
   * Never read-modify-write a counter.
   */
  increment<T extends RecordData = RecordData>(
    type: string,
    slug: string,
    field: string,
    by?: number,
  ): Promise<Single<Resource<T>> | WriteResult>;
  /** Resolves to `null` (HTTP 204). */
  remove(type: string, slug: string): Promise<null>;
}

export interface PublicClient {
  /** Public shared records of one type. */
  list(type: string, options?: ListOptions): Promise<Page<PublicResourcePreview>>;
  /** One public shared record. */
  get<T extends RecordData = RecordData>(type: string, slug: string): Promise<Single<PublicResource<T>>>;
  /** One public record from a user's personal space. */
  user<T extends RecordData = RecordData>(
    username: string,
    type: string,
    slug: string,
  ): Promise<Single<PublicResource<T>>>;
  /** A user's public records of one type. */
  user(username: string, type: string): Promise<Page<PublicResourcePreview>>;
}

export interface MStashClient {
  /** The current bearer token, or `null` when signed out. */
  readonly token: string | null;
  setToken(next: string | null | undefined): void;
  /** Creates the account and keeps its token on this client. */
  signup(input: SignupInput): Promise<SignupResult>;
  /** Signs in and keeps the token on this client. */
  login(input: LoginInput): Promise<LoginResult>;
  /** Forgets the token locally. */
  logout(): void;
  /** The signed-in user's own records. */
  me: ResourceClient;
  /**
   * App-wide records. Any signed-in user reads; the app owner or a secret key writes.
   * With a secret key, `increment` resolves to `null` because the key cannot read.
   */
  shared: ResourceClient<null>;
  /** Anonymous reads; no login needed. */
  public: PublicClient;
}

export interface ClientOptions {
  /** Your m-stash deployment origin, such as `https://m-stash.com`. */
  api: string;
  /** The app's publishable key (`pk_…`). Safe in browser code. */
  app: string;
  /** A bearer token to start with. On a server this may be the secret key (`sk_…`); never ship that to a browser. */
  token?: string | null;
  /** Custom fetch, such as for tests or older runtimes. Defaults to `globalThis.fetch`. */
  fetch?: typeof fetch;
}

export declare function createClient(options: ClientOptions): MStashClient;

export declare class MStashError extends Error {
  constructor(status: number, body: unknown);
  name: "MStashError";
  /** HTTP status code. */
  status: number;
  /** Parsed error body, usually `{ error: string }`. */
  body: { error?: string; [key: string]: unknown } | null;
}

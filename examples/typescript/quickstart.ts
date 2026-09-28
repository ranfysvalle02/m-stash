// Walks through the m-stash quickstart flow: signup, public profile, stashes, and a WebSocket query.
// Usage: API_URL=http://localhost:4000 npm start
import { WebSocket } from "ws";

const API_URL = process.env.API_URL || "http://localhost:4000";

interface AuthUser {
  id: string;
  email: string;
  role: string;
}

interface AuthResponse {
  token: string;
  user: AuthUser;
}

interface ApiEnvelope<T> {
  data: T;
}

interface Stash {
  _id: string;
  title: string;
  summary?: string;
  content?: string;
  tags?: string[];
  isPublic?: boolean;
}

interface PublicProfile {
  _id: string;
  handle: string;
  displayName: string;
  bio?: string;
  avatarURL?: string;
  links?: { label: string; url: string }[];
}

interface ApiOptions {
  method?: string;
  token?: string;
  body?: unknown;
}

function randomSuffix(): string {
  return Math.random().toString(36).slice(2, 8);
}

async function api<T>(path: string, { method = "GET", token, body }: ApiOptions = {}): Promise<T> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (token) headers.Authorization = `Bearer ${token}`;

  const res = await fetch(`${API_URL}${path}`, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(`${method} ${path} -> ${res.status}: ${JSON.stringify(data)}`);
  }
  return data as T;
}

function wsFind<T>(token: string, collection: string, query: Record<string, unknown>): Promise<T> {
  return new Promise((resolve, reject) => {
    const wsUrl = `${API_URL.replace(/^http/, "ws")}/v1/ws/${collection}`;
    const socket = new WebSocket(wsUrl, { headers: { Authorization: `Bearer ${token}` } });

    socket.on("open", () => {
      socket.send(JSON.stringify({ action: "find", query }));
    });
    socket.on("message", (data: Buffer) => {
      socket.close();
      resolve(JSON.parse(data.toString()) as T);
    });
    socket.on("error", reject);
  });
}

async function main(): Promise<void> {
  const suffix = randomSuffix();
  const email = `demo-${suffix}@example.com`;
  const password = "use-a-long-unique-password";
  const handle = `demo-${suffix}`;

  console.log("1. Sign up");
  const signup = await api<AuthResponse>("/v1/auth/signup", {
    method: "POST",
    body: { email, password },
  });
  const { token } = signup;
  const userId = signup.user.id;
  console.log(`   signed up as ${signup.user.email} (id: ${userId})`);

  console.log("2. Publish a public profile");
  await api("/v1/db/profiles/insertOne", {
    method: "POST",
    token,
    body: {
      payload: {
        handle,
        displayName: "Demo User",
        bio: "Created by the TypeScript quickstart script.",
        avatarURL: "https://example.com/avatar.jpg",
        links: [{ label: "Website", url: "https://example.com" }],
        isPublic: true,
      },
    },
  });
  console.log(`   profile handle: ${handle}`);

  console.log("3. Read the public profile anonymously");
  const publicProfile = await api<ApiEnvelope<PublicProfile>>(`/v1/public/profiles/${handle}`);
  console.log("  ", publicProfile.data);

  console.log("4. Create a private stash");
  await api("/v1/db/stashes/insertOne", {
    method: "POST",
    token,
    body: {
      payload: { title: "Private draft", content: "Only the owner can read this.", isPublic: false },
    },
  });

  console.log("5. Create a public stash");
  await api("/v1/db/stashes/insertOne", {
    method: "POST",
    token,
    body: {
      payload: {
        title: "Hello, public web",
        summary: "A visible post.",
        content: "This is shared intentionally.",
        tags: ["intro"],
        isPublic: true,
      },
    },
  });

  console.log("6. List the signed-in user's stashes (private + public)");
  const mine = await api<ApiEnvelope<Stash[]>>("/v1/db/stashes/find", {
    method: "POST",
    token,
    body: { query: { ownerId: userId } },
  });
  console.log(
    "  ",
    mine.data.map((s) => `${s.title} (public: ${Boolean(s.isPublic)})`)
  );

  console.log("7. List only the public stashes anonymously");
  const publicStashes = await api<ApiEnvelope<Stash[]>>(`/v1/public/profiles/${handle}/stashes`);
  console.log(
    "  ",
    publicStashes.data.map((s) => s.title)
  );

  console.log("8. Live query over WebSocket");
  const wsResult = await wsFind<ApiEnvelope<Stash[]>>(token, "stashes", { isPublic: false });
  console.log("   ws response:", wsResult);
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});

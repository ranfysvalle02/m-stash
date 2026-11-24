---
title: Add login and private data to any web page
summary: One HTML file, no install, no build step. Sign users up, log them in, and give each one notes only they can read.
minutes: 10
level: Beginner
order: 2
---

By the end you will have a single `index.html` where people sign up, log in, and save notes that are private to them — enforced by the server, not by your code.

## 1. Get a publishable key

A publishable key (`pk_...`) names your app. It is safe to put in browser code.

- **Have an app?** Copy its key from the [console]({{API}}/console).
- **Just trying it?** Get a throwaway sandbox app in one call (if this deployment has the try-it sandbox on):

```sh
curl -X POST "{{API}}/v1/try"
# -> { "publishableKey": "pk_...", "expiresAt": "...", "claim": { "token": "..." } }
```

Save the `claim.token` if you might want to keep the sandbox later.

## 2. Create the page

Save this as `index.html` and replace `pk_your_publishable_key`:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>My notes</title>
</head>
<body>
  <h1>My notes</h1>

  <form id="auth">
    <input name="email" type="email" placeholder="Email" required />
    <input name="password" type="password" placeholder="Password (12+ characters)" minlength="12" required />
    <input name="username" placeholder="Username (sign up only)" />
    <button value="signup">Sign up</button>
    <button value="login">Log in</button>
  </form>

  <form id="compose" hidden>
    <input name="text" placeholder="Write a note" required />
    <button>Save</button>
  </form>
  <ul id="notes"></ul>

  <script type="module">
    import { createClient } from "{{API}}/mstash.js";

    const stash = createClient({ api: "{{API}}", app: "pk_your_publishable_key" });
    const auth = document.querySelector("#auth");
    const compose = document.querySelector("#compose");
    const list = document.querySelector("#notes");

    auth.addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = new FormData(auth);
      const credentials = { email: form.get("email"), password: form.get("password") };
      try {
        if (event.submitter.value === "signup") {
          await stash.signup({ ...credentials, username: form.get("username") });
        } else {
          await stash.login(credentials);
        }
        auth.hidden = true;
        compose.hidden = false;
        await refresh();
      } catch (error) {
        alert(error.message);
      }
    });

    compose.addEventListener("submit", async (event) => {
      event.preventDefault();
      const text = new FormData(compose).get("text");
      await stash.me.create("notes", {
        slug: `note-${Date.now()}`,
        title: text,
        visibility: "private",
        data: { text },
      });
      compose.reset();
      await refresh();
    });

    async function refresh() {
      const { data } = await stash.me.list("notes");
      list.replaceChildren(
        ...data.map((note) => Object.assign(document.createElement("li"), { textContent: note.title })),
      );
    }
  </script>
</body>
</html>
```

## 3. Run it

Serve the folder (modules don't load from `file://`):

```sh
python3 -m http.server 8000   # or: npx serve .
```

Open <http://localhost:8000>, sign up, and add a few notes. Then open a private window, sign up as someone else, and look: their list is empty. Ownership comes from the token, so no code you write can leak one user's notes to another.

## How it works

- `createClient` sends your publishable key as `X-M-Stash-App` on every call and keeps the bearer token from `signup` / `login`.
- `stash.me.*` is the signed-in user's own space: `list`, `get`, `create`, `update`, `increment`, and `remove`.
- Every record needs a `slug` (3-64 lowercase letters, digits, or hyphens; unique per type), a `title`, and a `visibility` (`private` or `public` here). `data` is any JSON you like — no schema, no migration.
- A type like `notes` springs into existence the first time you write to it.

## When something fails

| You see | Fix |
| --- | --- |
| A CORS error in the console | Add `http://localhost:8000` under **Allowed browser origins** for the app in the console. Sandboxes from `/v1/try` allow every origin. |
| `401 Unknown application key` | The key is wrong, or the sandbox expired. |
| `403 Invalid or expired token` | Access tokens last 15 minutes by default. Log in again. |
| `409 ... already exists` | That slug, email, or username is taken. |

## Next

- [Public profiles and counters](/tutorials/public-profiles-and-counters)
- [Run a backend with your secret key](/tutorials/backend-with-your-secret-key)

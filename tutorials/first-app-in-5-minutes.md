---
title: Your first app in 5 minutes
summary: Describe an app, click through a live version, keep it, and download a build kit you can open in any editor.
minutes: 5
level: Beginner
order: 1
---

You will go from an idea to a running app with sign-up, login, private per-user data, and public pages — without writing a schema, a security rule, or a line of backend code.

## 1. Describe it

Open the [vibe lab]({{API}}/vibe) and type one sentence, for example:

> A plant swap where I list cuttings for everyone to browse and each member keeps a private wishlist.

Or launch one of the instant recipes. Either way you get a **live app** on a short-lived sandbox: a real m-stash tenant with an owner login, seeded public records, and a working sign-up flow.

## 2. Click through it

Use the persona bar above the preview:

- **Public visitor** sees the public catalog without logging in.
- **Customer A** and **Customer B** each sign up and save private records the other can't read.
- **App owner** signs in with the one-time owner login and edits the public data.

Watch the **Live API wiretap**: every click is a real API call, and the server — not the page — decides who can read and write what.

## 3. Change it

Type a change into the refine bar, like `add a 1-5 rating to each wishlist item`. The lab rebuilds the app as a fresh sandbox, keeping existing fields so saved data still fits. Repeat until it feels right.

## 4. Keep it

Sandboxes expire. Click **Keep this app**, create a free builder account, and the sandbox becomes a live app you own. The publishable key does not change, and every user and record you created comes with it.

## 5. Take it anywhere

In the [console]({{API}}/console), click **Build kit** on your app. Unzip it and serve the folder:

```sh
cd m-stash-starter
python3 -m http.server 8000   # or: npx serve .
```

Open <http://localhost:8000>. It is the same app, now in your own folder. Then prove it works end to end:

```sh
./verify.sh   # needs curl and jq; prints "m-stash verify: OK"
```

## What's in the kit

| File | Use it for |
| --- | --- |
| `index.html`, `app.js`, `views.js`, `app.css` | The app itself — plain HTML, CSS, and JS, no build step. |
| `mstash.js` | A zero-dependency client for your own frontend or backend. |
| `mstash.json` | The app's data model. |
| `.env.example` | API base and publishable key for server code. |
| `SKILL.md`, `AGENTS.md` | Rules for your AI coding agent. |

## Next

- [Add login and private data to any web page](/tutorials/login-and-private-data)
- [Build with your AI coding agent](/tutorials/build-with-your-ai-agent)

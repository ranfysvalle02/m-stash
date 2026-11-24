---
title: Build with your AI coding agent
summary: Give Cursor, Claude Code, or Codex a skill file so it wires m-stash correctly the first time, then have it prove its work.
minutes: 5
level: Beginner
order: 5
---

AI agents are great at UI and terrible at guessing an API's security rules. m-stash fixes the second half: ownership is enforced by the server, and a skill file tells the agent exactly how to call it.

## 1. Give your agent the skill

Pick one:

- **For one app** (best): download **SKILL.md** from your app in the [console]({{API}}/console), or use the copy in your build kit. It contains your publishable key, your exact data model, and a verify script.
- **For any project:** use the generic skill for this deployment:

```sh
curl -o SKILL.md "{{API}}/skill.md"
```

Put it where your agent looks:

```sh
mkdir -p .cursor/skills/m-stash && cp SKILL.md .cursor/skills/m-stash/   # Cursor
mkdir -p .claude/skills/m-stash && cp SKILL.md .claude/skills/m-stash/   # Claude Code
```

For Codex and other agents, keep `AGENTS.md` from the build kit at the project root. The full machine-readable reference lives at [{{API}}/llms.txt]({{API}}/llms.txt).

## 2. Ask for features, not plumbing

Prompts that work well:

- *"Add a reviews section where each signed-in member can rate items 1-5. Ratings are private; show each member only their own."*
- *"Give every member a public profile page listing the projects they marked public."*
- *"Add a like button to the public catalog. Likes go through a small server endpoint that verifies the user and increments the count with the secret key."*

The skill teaches the agent the rules it must not break: send `X-M-Stash-App` on every call, always set `visibility`, keep `sk_` keys out of frontend code, and never work around a 401/403 on the client.

## 3. Make it prove its work

End every request with:

> Then run `./verify.sh` and fix anything until it prints `m-stash verify: OK`.

The script signs up a throwaway member, writes and reads every member section, and checks the public ones. If the agent broke the contract, it fails loudly instead of shipping a bug.

## 4. When the agent gets stuck

The skill includes a table that maps each real error to its fix. The usual suspects:

| Error | Usual cause |
| --- | --- |
| `400 visibility must be ...` | The agent forgot `visibility` on a create. |
| `403 Only administrators can write shared` | A member wrote to `shared`; it should be `me`, or go through your server. |
| CORS error | Add the dev server's origin under **Allowed browser origins** in the console. |

## Next

- [Public profiles, shared catalogs, and counters](/tutorials/public-profiles-and-counters)
- [Run a backend with your secret key](/tutorials/backend-with-your-secret-key)

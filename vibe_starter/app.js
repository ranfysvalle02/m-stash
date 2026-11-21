(() => {
  "use strict";

  const config = JSON.parse(document.getElementById("m-stash-config").textContent);
  const root = document.getElementById("app");
  const storageKey = "m-stash-starter:" + config.app;
  let session = loadSession();

  function loadSession() {
    try {
      return JSON.parse(sessionStorage.getItem(storageKey)) || null;
    } catch {
      return null;
    }
  }

  function saveSession(next) {
    session = next;
    if (next) sessionStorage.setItem(storageKey, JSON.stringify(next));
    else sessionStorage.removeItem(storageKey);
  }

  function el(tag, props, ...children) {
    const node = document.createElement(tag);
    for (const [key, value] of Object.entries(props || {})) {
      if (value == null || value === false) continue;
      if (key.startsWith("on")) node.addEventListener(key.slice(2), value);
      else if (key === "className") node.className = value;
      else node.setAttribute(key, value === true ? "" : value);
    }
    for (const child of children.flat()) {
      if (child == null || child === false) continue;
      node.append(child instanceof Node ? child : document.createTextNode(String(child)));
    }
    return node;
  }

  function randomHex(bytes) {
    return Array.from(crypto.getRandomValues(new Uint8Array(bytes)), (b) => b.toString(16).padStart(2, "0")).join("");
  }

  function slugify(title) {
    const base = title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 48) || "record";
    return base + "-" + randomHex(2);
  }

  // Bearer tokens with credentials "omit" keep this app from touching any cookie session on the same origin.
  async function api(path, { method = "GET", body, auth = true } = {}) {
    const headers = { "X-M-Stash-App": config.app };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (auth && session) headers.Authorization = "Bearer " + session.token;
    const response = await fetch(config.api + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "omit",
    });
    if (response.status === 204) return null;
    const payload = await response.json().catch(() => ({}));
    if (response.status === 401 && auth && session) {
      saveSession(null);
      render();
    }
    if (!response.ok) throw new Error(payload.error || "Request failed (" + response.status + ")");
    return payload;
  }

  async function adoptToken(token, username) {
    saveSession({ token, username: username || null });
    if (username) return;
    try {
      const namespace = await api("/v1/me/namespace");
      saveSession({ token, username: namespace.data.slug });
    } catch {
      // Accounts without a personal namespace can still use shared data.
    }
  }

  async function signUp(email, password, username) {
    const result = await api("/v1/auth/signup", { method: "POST", body: { email, password, username }, auth: false });
    await adoptToken(result.token, result.namespace && result.namespace.slug);
  }

  async function logIn(email, password) {
    const result = await api("/v1/auth/login", { method: "POST", body: { email, password }, auth: false });
    await adoptToken(result.token);
  }

  function throwaway() {
    const username = "vibe-" + randomHex(4);
    return signUp(username + "@example.com", randomHex(16), username);
  }

  function header() {
    return el(
      "header",
      { className: "hero" },
      el("p", { className: "eyebrow" }, "Built on m-stash"),
      el("h1", {}, config.idea),
      config.summary ? el("p", { className: "lede" }, config.summary) : null,
    );
  }

  function keyBanner() {
    return el(
      "p",
      { className: "banner" },
      "Set your publishable key: replace pk_your_publishable_key in index.html. See README.md.",
    );
  }

  function authPanel() {
    const status = el("p", { className: "status", role: "status" });
    const email = el("input", { type: "email", placeholder: "you@example.com", autocomplete: "email", "aria-label": "Email" });
    const password = el("input", { type: "password", placeholder: "Password (12+ characters)", autocomplete: "current-password", "aria-label": "Password" });
    const username = el("input", { placeholder: "Username (sign-up only)", autocomplete: "username", "aria-label": "Username" });

    async function run(action) {
      status.textContent = "Working…";
      try {
        await action();
        render();
      } catch (error) {
        status.textContent = error.message;
      }
    }

    return el(
      "form",
      {
        className: "card auth",
        onsubmit: (event) => {
          event.preventDefault();
          run(() => logIn(email.value.trim(), password.value));
        },
      },
      el("h2", {}, "Sign in to try it"),
      email,
      password,
      username,
      el(
        "div",
        { className: "row" },
        el("button", { type: "submit" }, "Log in"),
        el(
          "button",
          { type: "button", className: "secondary", onclick: () => run(() => signUp(email.value.trim(), password.value, username.value.trim().toLowerCase())) },
          "Sign up",
        ),
      ),
      config.throwaway
        ? el("button", { type: "button", className: "ghost wide", onclick: () => run(throwaway) }, "⚡ One click: use a throwaway account")
        : null,
      status,
    );
  }

  function cardHead(part) {
    return [
      el(
        "div",
        { className: "card-head" },
        el("h2", {}, part.name || part.type),
        el("span", { className: "badge" }, part.scope + " · " + part.type + " · " + part.visibility),
      ),
      part.purpose ? el("p", { className: "hint" }, part.purpose) : null,
    ];
  }

  function emptyRow(text) {
    return el("li", { className: "empty" }, text);
  }

  function personalCard(part) {
    const list = el("ul", { className: "records" }, emptyRow("Loading…"));
    const status = el("p", { className: "status", role: "status" });
    const title = el("input", { placeholder: "Title", maxlength: "120", "aria-label": "Title" });
    const data = el("textarea", { rows: "3", spellcheck: "false", "aria-label": "Data (JSON object)" }, "{}");
    const visibility = el(
      "select",
      { "aria-label": "Visibility" },
      el("option", { value: "private" }, "private"),
      el("option", { value: "public" }, "public"),
    );
    visibility.value = part.visibility === "public" ? "public" : "private";

    async function refresh() {
      try {
        const page = await api("/v1/me/resources/" + part.type + "?limit=20");
        list.replaceChildren(...(page.data.length ? page.data.map((item) => recordRow(part, item, refresh)) : [emptyRow("Nothing saved yet. Add one above.")]));
      } catch (error) {
        list.replaceChildren(emptyRow(error.message));
      }
    }

    async function create(event) {
      event.preventDefault();
      status.textContent = "";
      const name = title.value.trim();
      if (!name) {
        status.textContent = "Give it a title.";
        return;
      }
      let parsed;
      try {
        parsed = JSON.parse(data.value.trim() || "{}");
        if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error();
      } catch {
        status.textContent = 'Data must be a JSON object, like {"key": "value"}.';
        return;
      }
      try {
        await api("/v1/me/resources/" + part.type, {
          method: "POST",
          body: { slug: slugify(name), title: name, visibility: visibility.value, data: parsed },
        });
        title.value = "";
        await refresh();
      } catch (error) {
        status.textContent = error.message;
      }
    }

    refresh();
    return el(
      "article",
      { className: "card" },
      cardHead(part),
      el("form", { className: "composer", onsubmit: create }, title, data, el("div", { className: "row" }, visibility, el("button", { type: "submit" }, "Save"))),
      status,
      list,
    );
  }

  function recordRow(part, item, refresh) {
    const publicLink =
      item.visibility === "public" && session.username
        ? el(
            "a",
            {
              href: config.api + "/v1/public/" + session.username + "/" + part.type + "/" + item.slug + "?app=" + encodeURIComponent(config.app),
              target: "_blank",
              rel: "noreferrer",
            },
            "Public link ↗",
          )
        : null;
    const remove = el(
      "button",
      {
        type: "button",
        className: "ghost",
        onclick: async () => {
          remove.disabled = true;
          try {
            await api("/v1/me/resources/" + part.type + "/" + item.slug, { method: "DELETE" });
            await refresh();
          } catch (error) {
            remove.disabled = false;
            remove.textContent = error.message;
          }
        },
      },
      "Delete",
    );
    return el(
      "li",
      {},
      el("div", { className: "record-title" }, el("strong", {}, item.title || item.slug), el("span", { className: "badge" }, item.visibility)),
      el("div", { className: "row" }, publicLink, remove),
    );
  }

  function sharedCard(part) {
    const list = el("ul", { className: "records" }, emptyRow("Loading…"));
    api("/v1/shared/resources/" + part.type + "?limit=20")
      .then((page) => {
        list.replaceChildren(
          ...(page.data.length
            ? page.data.map((item) =>
                el("li", {}, el("div", { className: "record-title" }, el("strong", {}, item.title || item.slug), el("span", { className: "badge" }, item.visibility))),
              )
            : [emptyRow("No records yet. Your backend writes these with the app's secret key.")]),
        );
      })
      .catch((error) => list.replaceChildren(emptyRow(error.message)));
    return el(
      "article",
      { className: "card" },
      cardHead(part),
      el("p", { className: "hint" }, "Shared app data: signed-in users read it; only an admin or your backend can write it."),
      list,
    );
  }

  function workspace() {
    const bar = el(
      "div",
      { className: "bar" },
      el("span", {}, "Signed in", session.username ? [" as ", el("strong", {}, session.username)] : null),
      el("button", { type: "button", className: "ghost", onclick: () => { saveSession(null); render(); } }, "Sign out"),
    );
    const cards = config.parts.length
      ? config.parts.map((part) => (part.scope === "shared" ? sharedCard(part) : personalCard(part)))
      : [el("p", { className: "card" }, "This plan has no data model yet.")];
    return el("section", { className: "workspace" }, bar, cards);
  }

  function render() {
    root.replaceChildren(
      header(),
      config.app.startsWith("pk_your") ? keyBanner() : null,
      session ? workspace() : authPanel(),
      el("footer", {}, "Powered by m-stash. Ownership is enforced on the server for every request."),
    );
  }

  render();
})();

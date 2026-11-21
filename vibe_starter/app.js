(() => {
  "use strict";

  const config = JSON.parse(document.getElementById("m-stash-config").textContent);
  const root = document.getElementById("app");
  const storageKey = "m-stash-starter:" + config.app;
  let session = loadSession();

  const ICONS = {
    bolt: '<path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    trash: '<path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m2 0-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>',
    external: '<path d="M14 3h7v7M10 14 21 3M21 14v5a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5"/>',
    logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>',
    lock: '<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
    globe: '<circle cx="12" cy="12" r="10"/><path d="M2 12h20M12 2a15 15 0 0 1 0 20 15 15 0 0 1 0-20z"/>',
  };

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
    append(node, children);
    return node;
  }

  // append flattens children and drops null/false so a conditional branch never renders as the text "null".
  function append(node, children) {
    for (const child of children.flat()) {
      if (child == null || child === false) continue;
      node.append(child instanceof Node ? child : document.createTextNode(String(child)));
    }
  }

  function setChildren(node, children) {
    node.replaceChildren();
    append(node, children);
  }

  function icon(name) {
    const span = el("span", { className: "icon", "aria-hidden": "true" });
    span.innerHTML =
      '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
      (ICONS[name] || "") +
      "</svg>";
    return span;
  }

  function randomHex(bytes) {
    return Array.from(crypto.getRandomValues(new Uint8Array(bytes)), (b) => b.toString(16).padStart(2, "0")).join("");
  }

  function slugify(title) {
    const base = title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 48) || "record";
    return base + "-" + randomHex(2);
  }

  function visibilityClass(visibility) {
    if (visibility === "public") return "pill pill-public";
    if (visibility === "authenticated") return "pill pill-auth";
    return "pill pill-private";
  }

  // Bearer tokens with credentials "omit" keep this sandbox from touching any cookie session on the same origin.
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

  function topbar() {
    return el(
      "header",
      { className: "topbar" },
      el("div", { className: "brand" }, el("span", { className: "brand-dot" }), "m-stash sandbox"),
      session
        ? el(
            "div",
            { className: "session" },
            el("span", { className: "muted sm" }, session.username ? "@" + session.username : "signed in"),
            el("button", { className: "btn ghost sm", type: "button", onclick: () => { saveSession(null); render(); } }, icon("logout"), "Sign out"),
          )
        : el("span", { className: "muted sm" }, "Live preview"),
    );
  }

  function hero() {
    return el(
      "section",
      { className: "hero" },
      el("p", { className: "eyebrow" }, "Built on m-stash"),
      el("h1", {}, config.idea || "Your backend, live"),
      config.summary ? el("p", { className: "lede" }, config.summary) : null,
    );
  }

  function keyBanner() {
    return el(
      "p",
      { className: "banner" },
      "Set your publishable key: replace pk_your_publishable_key in index.html (see README.md), then reload.",
    );
  }

  function field(labelText, control, hintText) {
    return el(
      "label",
      { className: "field" },
      el("span", { className: "field-label" }, labelText),
      control,
      hintText ? el("span", { className: "field-hint" }, hintText) : null,
    );
  }

  function segmented(options, initial, onChange) {
    let value = initial;
    const buttons = [];
    const node = el("div", { className: "segmented", role: "group" });
    for (const option of options) {
      const button = el(
        "button",
        {
          type: "button",
          className: option.value === value ? "active" : "",
          onclick: () => {
            value = option.value;
            buttons.forEach((candidate) => candidate.classList.toggle("active", candidate.dataset.value === value));
            if (onChange) onChange(value);
          },
        },
        option.label,
      );
      button.dataset.value = option.value;
      buttons.push(button);
      node.append(button);
    }
    return { node, value: () => value };
  }

  function authPanel() {
    const status = el("p", { className: "status", role: "status" });
    const email = el("input", { className: "input", type: "email", placeholder: "you@example.com", autocomplete: "email" });
    const password = el("input", { className: "input", type: "password", placeholder: "At least 12 characters", autocomplete: "current-password" });
    const username = el("input", { className: "input", placeholder: "pick-a-handle", autocomplete: "username" });
    const usernameField = field("Username", username, "Lowercase letters, numbers, and hyphens.");
    usernameField.hidden = true;
    const submit = el("button", { type: "submit", className: "btn primary wide" }, "Log in");

    let mode = "login";
    const tabs = segmented(
      [
        { value: "login", label: "Log in" },
        { value: "signup", label: "Sign up" },
      ],
      "login",
      (next) => {
        mode = next;
        usernameField.hidden = next !== "signup";
        submit.textContent = next === "signup" ? "Create account" : "Log in";
      },
    );

    async function run(action) {
      status.textContent = "Working…";
      status.className = "status";
      try {
        await action();
        render();
      } catch (error) {
        status.textContent = error.message;
      }
    }

    const form = el(
      "form",
      {
        className: "stack",
        onsubmit: (event) => {
          event.preventDefault();
          const address = email.value.trim();
          if (mode === "signup") run(() => signUp(address, password.value, username.value.trim().toLowerCase()));
          else run(() => logIn(address, password.value));
        },
      },
      tabs.node,
      field("Email", email),
      field("Password", password),
      usernameField,
      submit,
      status,
    );

    return el(
      "section",
      { className: "auth-wrap" },
      el(
        "div",
        { className: "card auth" },
        el("h2", {}, "Try this backend live"),
        el("p", { className: "muted" }, "Sign in to read and write real records. This sandbox uses a token, so it can’t touch any real session."),
        config.throwaway ? el("button", { className: "btn accent wide", type: "button", onclick: () => run(throwaway) }, icon("bolt"), "One-click throwaway account") : null,
        config.throwaway ? el("div", { className: "divider" }, "or use your own") : null,
        form,
      ),
    );
  }

  function cardHead(part) {
    return el(
      "div",
      { className: "card-head" },
      el(
        "div",
        { className: "card-title" },
        el("span", { className: "scope-dot scope-" + (part.scope === "shared" ? "shared" : "personal") }),
        el("h2", {}, part.name || part.type),
      ),
      el(
        "div",
        { className: "tags" },
        el("span", { className: "pill" }, part.scope),
        el("code", { className: "type-chip" }, part.type),
        el("span", { className: visibilityClass(part.visibility) }, part.visibility),
      ),
    );
  }

  function emptyRow(text) {
    return el("li", { className: "empty" }, text);
  }

  function recordRow(part, item, refresh) {
    const publicLink =
      item.visibility === "public" && session.username
        ? el(
            "a",
            {
              className: "record-link",
              href: config.api + "/v1/public/" + session.username + "/" + part.type + "/" + item.slug + "?app=" + encodeURIComponent(config.app),
              target: "_blank",
              rel: "noreferrer",
              title: "Open the public link",
            },
            icon("external"),
          )
        : null;
    const remove = el("button", {
      type: "button",
      className: "icon-btn danger",
      title: "Delete",
      onclick: async () => {
        remove.disabled = true;
        try {
          await api("/v1/me/resources/" + part.type + "/" + item.slug, { method: "DELETE" });
          await refresh();
        } catch (error) {
          remove.disabled = false;
          remove.title = error.message;
        }
      },
    });
    remove.append(icon("trash"));
    return el(
      "li",
      { className: "record" },
      el(
        "div",
        { className: "record-main" },
        el("strong", {}, item.title || item.slug),
        el("code", { className: "slug" }, part.type + "/" + item.slug),
      ),
      el("div", { className: "record-side" }, el("span", { className: visibilityClass(item.visibility) }, item.visibility), publicLink, remove),
    );
  }

  function personalCard(part) {
    const list = el("ul", { className: "records" }, emptyRow("Loading…"));
    const status = el("p", { className: "status", role: "status" });
    const title = el("input", { className: "input", placeholder: "e.g. " + (part.name || part.type), maxlength: "120" });
    const data = el("textarea", { className: "input mono", rows: "3", spellcheck: "false", placeholder: '{ "example": true }' }, "{}");
    const vis = segmented(
      [
        { value: "private", label: "Private" },
        { value: "public", label: "Public" },
      ],
      part.visibility === "public" ? "public" : "private",
    );

    async function refresh() {
      try {
        const page = await api("/v1/me/resources/" + part.type + "?limit=20");
        setChildren(list, page.data.length ? page.data.map((item) => recordRow(part, item, refresh)) : [emptyRow("No records yet — add your first above.")]);
      } catch (error) {
        setChildren(list, [emptyRow(error.message)]);
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
        status.textContent = 'Data must be a JSON object, like {"level": 3}.';
        return;
      }
      try {
        await api("/v1/me/resources/" + part.type, {
          method: "POST",
          body: { slug: slugify(name), title: name, visibility: vis.value(), data: parsed },
        });
        title.value = "";
        data.value = "{}";
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
      part.purpose ? el("p", { className: "muted" }, part.purpose) : null,
      el(
        "form",
        { className: "composer", onsubmit: create },
        field("Title", title),
        field("Data (JSON)", data),
        el(
          "div",
          { className: "composer-foot" },
          field("Visibility", vis.node),
          el("button", { type: "submit", className: "btn primary" }, icon("plus"), "Add record"),
        ),
        status,
      ),
      list,
    );
  }

  function sharedCard(part) {
    const list = el("ul", { className: "records" }, emptyRow("Loading…"));
    api("/v1/shared/resources/" + part.type + "?limit=20")
      .then((page) => {
        setChildren(
          list,
          page.data.length
            ? page.data.map((item) =>
                el(
                  "li",
                  { className: "record" },
                  el("div", { className: "record-main" }, el("strong", {}, item.title || item.slug), el("code", { className: "slug" }, part.type + "/" + item.slug)),
                  el("div", { className: "record-side" }, el("span", { className: visibilityClass(item.visibility) }, item.visibility)),
                ),
              )
            : [emptyRow("No records yet. Your backend writes these with the app’s secret key.")],
        );
      })
      .catch((error) => setChildren(list, [emptyRow(error.message)]));
    return el(
      "article",
      { className: "card" },
      cardHead(part),
      part.purpose ? el("p", { className: "muted" }, part.purpose) : null,
      el("p", { className: "note" }, icon("lock"), "Read-only here: signed-in users read shared data; only an admin or your backend writes it."),
      list,
    );
  }

  function workspace() {
    const cards = config.parts.length
      ? config.parts.map((part) => (part.scope === "shared" ? sharedCard(part) : personalCard(part)))
      : [el("div", { className: "card" }, el("p", { className: "muted" }, "This plan has no data model yet."))];
    return el("section", { className: "stack-lg" }, ...cards);
  }

  function render() {
    setChildren(root, [
      el(
        "div",
        { className: "page" },
        topbar(),
        el(
          "div",
          { className: "container" },
          hero(),
          config.app.startsWith("pk_your") ? keyBanner() : null,
          session ? workspace() : authPanel(),
          el("footer", { className: "footer" }, "Powered by m-stash — ownership is enforced on the server for every request."),
        ),
      ),
    ]);
  }

  render();
})();

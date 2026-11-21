(() => {
  "use strict";

  // ---------------------------------------------------------------------------
  // Config + roots. The config is the only data this runtime trusts; it arrives
  // as inert JSON (never executed) and carries no secrets — just a publishable
  // key, the API base, the render mode, and the spec's presentation.
  // ---------------------------------------------------------------------------
  const config = JSON.parse(document.getElementById("m-stash-config").textContent);
  const root = document.getElementById("app");
  const isPreview = config.mode === "preview";
  const sections = Array.isArray(config.sections) ? config.sections : [];

  // Accent is applied through a body class so the strict CSP (style-src 'self')
  // never needs an inline style attribute.
  document.body.classList.add("accent-" + (config.accent || "teal"));

  // Owner credentials for the one-click preview sign-in arrive in the URL
  // fragment (never sent to the server). Capture them into memory, then scrub
  // the fragment so the password never lingers in the address bar or history.
  let ownerCreds = null;
  (function captureOwnerCreds() {
    const marker = "#owner=";
    const hash = location.hash || "";
    if (hash.indexOf(marker) !== 0) return;
    const raw = hash.slice(marker.length);
    const sep = raw.indexOf(":");
    if (sep > 0) {
      try {
        ownerCreds = {
          email: decodeURIComponent(raw.slice(0, sep)),
          password: decodeURIComponent(raw.slice(sep + 1)),
        };
      } catch (_) {
        ownerCreds = null;
      }
    }
    try {
      history.replaceState(null, document.title, location.pathname + location.search + "#/");
    } catch (_) {
      // Some sandboxed contexts forbid URL rewriting; the creds are already in memory.
      location.hash = "#/";
    }
  })();

  // ---------------------------------------------------------------------------
  // Fixed icon set. The only place innerHTML is used, and only with these fixed
  // SVG strings — never user content — so CSP stays satisfied.
  // ---------------------------------------------------------------------------
  const ICONS = {
    music: '<path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>',
    cart: '<circle cx="9" cy="21" r="1"/><circle cx="20" cy="21" r="1"/><path d="M1 1h4l2.7 13.4a2 2 0 0 0 2 1.6h9.7a2 2 0 0 0 2-1.6L23 6H6"/>',
    book: '<path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/>',
    dumbbell: '<path d="M6 7v10M18 7v10M3 9v6M21 9v6M6 12h12"/>',
    note: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M8 13h8M8 17h5"/>',
    calendar: '<rect x="3" y="4" width="18" height="18" rx="2"/><path d="M16 2v4M8 2v4M3 10h18"/>',
    trophy: '<path d="M8 21h8M12 17v4M7 4h10v5a5 5 0 0 1-10 0z"/><path d="M5 4H3v2a3 3 0 0 0 3 3M19 4h2v2a3 3 0 0 1-3 3"/>',
    user: '<circle cx="12" cy="8" r="4"/><path d="M4 21v-1a6 6 0 0 1 6-6h4a6 6 0 0 1 6 6v1"/>',
    box: '<path d="M21 8 12 3 3 8v8l9 5 9-5z"/><path d="M3 8l9 5 9-5M12 13v8"/>',
    sparkles: '<path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/><path d="M19 4l.6 1.6M5 19l.6 1.6"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    trash: '<path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m2 0-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>',
    edit: '<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/>',
    logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>',
    bolt: '<path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/>',
    lock: '<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
    check: '<path d="M20 6 9 17l-5-5"/>',
    shield: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>',
    external: '<path d="M14 3h7v7M10 14 21 3M21 14v5a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5"/>',
  };

  function icon(name, size) {
    const span = el("span", { className: "icon", "aria-hidden": "true" });
    const px = String(size || 18);
    span.innerHTML =
      '<svg viewBox="0 0 24 24" width="' + px + '" height="' + px +
      '" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
      (ICONS[name] || ICONS.box) +
      "</svg>";
    return span;
  }

  // ---------------------------------------------------------------------------
  // DOM helpers. Everything is textContent-only; the only markup injection is
  // the fixed icon set above.
  // ---------------------------------------------------------------------------
  function el(tag, props, ...children) {
    const node = document.createElement(tag);
    for (const [key, value] of Object.entries(props || {})) {
      if (value == null || value === false) continue;
      if (key.startsWith("on") && typeof value === "function") node.addEventListener(key.slice(2), value);
      else if (key === "className") node.className = value;
      else node.setAttribute(key, value === true ? "" : String(value));
    }
    append(node, children);
    return node;
  }

  function append(node, children) {
    for (const child of children.flat()) {
      if (child == null || child === false) continue;
      node.append(child instanceof Node ? child : document.createTextNode(String(child)));
    }
  }

  function setChildren(node, children) {
    node.replaceChildren();
    append(node, Array.isArray(children) ? children : [children]);
  }

  function randomHex(bytes) {
    return Array.from(crypto.getRandomValues(new Uint8Array(bytes)), (b) => b.toString(16).padStart(2, "0")).join("");
  }

  function slugify(title) {
    let base = String(title)
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 48)
      .replace(/-+$/g, "");
    if (base.length < 2) base = "item";
    return base + "-" + randomHex(3);
  }

  // ---------------------------------------------------------------------------
  // Sessions. One bearer token per role, scoped to this app key so two different
  // starters on the same origin never share a session.
  // ---------------------------------------------------------------------------
  function sessionKey(role) {
    return "m-stash-starter:" + config.app + ":" + role;
  }

  // Web Storage throws in a sandboxed iframe with an opaque origin (the live
  // preview in the console) and in some privacy modes. Memory is the source of
  // truth for this page; sessionStorage only mirrors it so a reload survives
  // on a normal origin (the downloaded starter).
  const memoryStore = new Map();
  function storageGet(key) {
    if (memoryStore.has(key)) return memoryStore.get(key);
    try {
      const stored = sessionStorage.getItem(key);
      if (stored !== null) memoryStore.set(key, stored);
      return stored;
    } catch (_) {
      return null;
    }
  }
  function storageSet(key, value) {
    memoryStore.set(key, value);
    try {
      sessionStorage.setItem(key, value);
    } catch (_) {
      // Memory-only session: fine for a preview.
    }
  }
  function storageRemove(key) {
    memoryStore.delete(key);
    try {
      sessionStorage.removeItem(key);
    } catch (_) {
      // Nothing persisted to remove.
    }
  }

  function getSession(role) {
    try {
      return JSON.parse(storageGet(sessionKey(role))) || null;
    } catch (_) {
      return null;
    }
  }
  function setSession(role, value) {
    if (value) storageSet(sessionKey(role), JSON.stringify(value));
    else storageRemove(sessionKey(role));
  }

  // ---------------------------------------------------------------------------
  // API helper. Bearer tokens with credentials "omit" keep this sandbox from
  // touching any cookie session that may live on the same origin.
  //
  // The app key also rides in the query string: browsers send CORS preflights
  // without custom headers, so ?app= is how the server learns which app's
  // allowed origins apply. Without it, a starter opened from file:// or
  // localhost would be rejected whenever the host restricts ALLOWED_ORIGINS.
  // ---------------------------------------------------------------------------
  function withAppKey(path) {
    return path + (path.indexOf("?") === -1 ? "?" : "&") + "app=" + encodeURIComponent(config.app);
  }
  async function api(path, options) {
    const opts = options || {};
    const headers = { "X-M-Stash-App": config.app };
    if (opts.body !== undefined) headers["Content-Type"] = "application/json";
    const session = opts.role ? getSession(opts.role) : null;
    if (session && session.token) headers.Authorization = "Bearer " + session.token;
    let response;
    try {
      response = await fetch(config.api + withAppKey(path), {
        method: opts.method || "GET",
        headers,
        body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
        credentials: "omit",
      });
    } catch (_) {
      throw new Error("Can't reach the server. Check your connection and try again.");
    }
    if (response.status === 204) return null;
    const payload = await response.json().catch(() => ({}));
    if (response.status === 401 && opts.role && session) {
      setSession(opts.role, null);
      render();
    }
    if (!response.ok) throw new Error(payload.error || payload.message || "Request failed (" + response.status + ")");
    return payload;
  }

  async function signup(role, creds) {
    const result = await api("/v1/auth/signup", { method: "POST", body: creds });
    setSession(role, {
      token: result.token,
      email: creds.email,
      username: (result.namespace && result.namespace.slug) || creds.username || null,
      roleName: result.user && result.user.role,
    });
    return result;
  }

  async function login(role, creds) {
    const result = await api("/v1/auth/login", { method: "POST", body: creds });
    setSession(role, {
      token: result.token,
      email: creds.email,
      username: (result.user && result.user.email) || creds.email,
      roleName: result.user && result.user.role,
    });
    return result;
  }

  async function createThrowawayCustomer() {
    const hex = randomHex(4);
    return signup("member", {
      username: "shopper-" + hex,
      email: "shopper-" + hex + "@example.com",
      password: randomHex(12),
    });
  }

  // ---------------------------------------------------------------------------
  // Formatting shared with views.js.
  // ---------------------------------------------------------------------------
  function formatPrice(value) {
    const number = Number(value);
    if (!Number.isFinite(number)) return String(value);
    try {
      return new Intl.NumberFormat(undefined, { style: "currency", currency: "USD" }).format(number);
    } catch (_) {
      return "$" + number.toFixed(2);
    }
  }

  function formatDate(value) {
    const date = new Date(value);
    if (isNaN(date.getTime())) return String(value);
    try {
      return date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
    } catch (_) {
      return String(value);
    }
  }

  function formatField(field, value) {
    switch (field.kind) {
      case "price":
        return formatPrice(value);
      case "toggle":
        return value ? "Yes" : "No";
      case "date":
        return formatDate(value);
      case "tag":
        return Array.isArray(value) ? value.join(", ") : String(value);
      default:
        return String(value);
    }
  }

  function chip(text) {
    return el("span", { className: "chip" }, String(text));
  }

  // Toolkit handed to the view renderers in views.js.
  const ui = { el, icon, setChildren, formatPrice, formatDate, formatField, chip };

  // ---------------------------------------------------------------------------
  // Spec helpers.
  // ---------------------------------------------------------------------------
  function ownerSections() {
    return sections.filter((section) => section.audience === "owner");
  }
  function memberSections() {
    return sections.filter((section) => section.audience !== "owner");
  }
  function titleField(section) {
    return (section.fields || []).find((field) => field.kind === "text" || field.kind === "longtext");
  }
  function sectionVisibility(section) {
    return section.audience === "member" ? "private" : "public";
  }
  function deriveTitle(section, values) {
    const field = titleField(section);
    if (field && values[field.key] != null && String(values[field.key]).trim() !== "") {
      return String(values[field.key]).trim().slice(0, 200);
    }
    return (section.title || section.type).slice(0, 200);
  }
  function buildBody(section, values) {
    const title = deriveTitle(section, values);
    return { slug: slugify(title), title, visibility: sectionVisibility(section), data: values };
  }

  // ---------------------------------------------------------------------------
  // Small reusable pieces.
  // ---------------------------------------------------------------------------
  function iconBadge(name) {
    return el("span", { className: "icon-badge" }, icon(name || config.icon || "box", 20));
  }

  function skeleton(count, variant) {
    const nodes = [];
    for (let i = 0; i < count; i += 1) {
      nodes.push(
        el(
          "div",
          { className: "skel-card" + (variant ? " skel-" + variant : "") },
          el("div", { className: "skel-line skel-line-lg" }),
          el("div", { className: "skel-line" }),
          el("div", { className: "skel-line skel-line-sm" }),
        ),
      );
    }
    return el("div", { className: "skel-wrap" + (variant === "grid" ? " skel-grid" : "") }, ...nodes);
  }

  function emptyState(text, action) {
    return el("div", { className: "empty" }, el("p", {}, text), action || null);
  }

  function errorNote(message) {
    return el("div", { className: "error-note" }, icon("lock", 15), el("span", {}, message));
  }

  function linkButton(label, href, iconName) {
    return el("a", { className: "btn ghost sm", href }, iconName ? icon(iconName, 15) : null, label);
  }

  function textButton(label, onClick, iconName) {
    return el("button", { type: "button", className: "text-btn", onclick: onClick }, iconName ? icon(iconName, 15) : null, label);
  }

  function status() {
    return el("p", { className: "status", role: "status" });
  }

  // Build a labelled control for one spec field.
  function fieldControl(field, initial) {
    const id = "f-" + field.key + "-" + randomHex(2);
    let input;
    const kind = field.kind;
    if (kind === "longtext") input = el("textarea", { className: "input", rows: "3", id });
    else if (kind === "toggle") input = el("input", { type: "checkbox", className: "checkbox", id });
    else if (kind === "number" || kind === "price")
      input = el("input", { type: "number", step: kind === "price" ? "0.01" : "any", className: "input", id });
    else if (kind === "date") input = el("input", { type: "date", className: "input", id });
    else input = el("input", { type: "text", className: "input", id, placeholder: kind === "tag" ? "comma, separated" : "" });

    if (initial != null) {
      if (kind === "toggle") input.checked = Boolean(initial);
      else if (kind === "tag") input.value = Array.isArray(initial) ? initial.join(", ") : String(initial);
      else input.value = String(initial);
    }

    const labelText = el("span", { className: "field-label" }, field.label || field.key);
    const node =
      kind === "toggle"
        ? el("label", { className: "field field-toggle", for: id }, input, labelText)
        : el("label", { className: "field", for: id }, labelText, input);

    function get() {
      if (kind === "toggle") return input.checked;
      const raw = input.value.trim();
      if (kind === "number" || kind === "price") {
        if (raw === "") return undefined;
        const number = Number(raw);
        return Number.isFinite(number) ? number : undefined;
      }
      if (kind === "tag") return raw ? raw.split(",").map((part) => part.trim()).filter(Boolean) : [];
      return raw;
    }
    function reset() {
      if (kind === "toggle") input.checked = false;
      else input.value = "";
    }
    return { field, node, get, reset, focus: () => input.focus() };
  }

  // Build a create/edit form from a section's fields.
  function buildForm(section, opts) {
    const options = opts || {};
    const controls = (section.fields || []).map((field) =>
      fieldControl(field, options.initial ? options.initial[field.key] : undefined),
    );
    const feedback = status();
    const submit = el(
      "button",
      { type: "submit", className: "btn primary" },
      icon(options.submitIcon || "plus", 16),
      options.submitLabel || "Add",
    );
    const actions = el(
      "div",
      { className: "form-actions" },
      submit,
      options.onCancel ? el("button", { type: "button", className: "btn ghost", onclick: options.onCancel }, "Cancel") : null,
    );
    const form = el(
      "form",
      {
        className: "composer" + (options.compact ? " compact" : ""),
        onsubmit: async (event) => {
          event.preventDefault();
          feedback.textContent = "";
          feedback.className = "status";
          const values = {};
          for (const control of controls) {
            const value = control.get();
            if (value === undefined || value === "") continue;
            if (Array.isArray(value) && value.length === 0) continue;
            values[control.field.key] = value;
          }
          submit.disabled = true;
          try {
            await options.onSubmit(values, () => controls.forEach((control) => control.reset()));
          } catch (error) {
            feedback.textContent = error.message;
          } finally {
            submit.disabled = false;
          }
        },
      },
      el("div", { className: "form-grid" }, ...controls.map((control) => control.node)),
      actions,
      feedback,
    );
    return form;
  }

  // A compact set of field chips for a record (used in management lists).
  function recordMeta(section, item) {
    const data = item.data || {};
    const chips = [];
    for (const field of section.fields || []) {
      if (field.kind === "longtext") continue;
      const value = data[field.key];
      if (value == null || value === "" || (Array.isArray(value) && !value.length)) continue;
      chips.push(
        el(
          "span",
          { className: "meta-chip" },
          el("span", { className: "meta-chip-label" }, field.label || field.key),
          el("span", { className: "meta-chip-value" }, formatField(field, value)),
        ),
      );
      if (chips.length >= 5) break;
    }
    return chips;
  }

  // ---------------------------------------------------------------------------
  // Storefront (#/) — public, no login required.
  // ---------------------------------------------------------------------------
  function heroBlock() {
    return el(
      "section",
      { className: "hero" },
      el("div", { className: "hero-top" }, iconBadge(config.icon), el("span", { className: "hero-eyebrow" }, "Live storefront")),
      el("h1", { className: "hero-title" }, config.name || "Your shop"),
      config.tagline ? el("p", { className: "hero-tagline" }, config.tagline) : null,
    );
  }

  function shelfHead(section) {
    return el(
      "div",
      { className: "shelf-head" },
      el("h2", { className: "shelf-title" }, section.title || section.type),
      el("span", { className: "shelf-sub" }, "Updated by the shop owner"),
    );
  }

  async function loadStorefrontSection(section, body) {
    try {
      const page = await api("/v1/public/shared/resources/" + section.type + "?limit=20");
      const previews = (page.data || []).slice(0, 12);
      if (!previews.length) {
        setChildren(body, [
          emptyState(
            "Nothing here yet.",
            linkButton("Sign in as the owner to add items", "#/owner", "shield"),
          ),
        ]);
        return;
      }
      const items = await Promise.all(
        previews.map((preview) =>
          api("/v1/public/shared/resources/" + section.type + "/" + preview.slug)
            .then((detail) => detail.data)
            .catch(() => ({ type: section.type, slug: preview.slug, title: preview.title, summary: preview.summary, data: {} })),
        ),
      );
      setChildren(body, [window.MStashViews.render(section, items, ui)]);
    } catch (error) {
      setChildren(body, [errorNote(error.message)]);
    }
  }

  function storefront() {
    const owned = ownerSections();
    const stack = el("div", { className: "stack-lg" });
    if (!owned.length) {
      stack.append(
        el(
          "div",
          { className: "card pad" },
          el("p", { className: "muted" }, "This app is all about your personal space."),
          linkButton("Open my account", "#/account", "user"),
        ),
      );
    }
    for (const section of owned) {
      const body = el("div", { className: "shelf-body" }, skeleton(section.view === "grid" ? 3 : 2, section.view === "grid" ? "grid" : null));
      const shelf = el("section", { className: "shelf" }, shelfHead(section), body);
      loadStorefrontSection(section, body);
      stack.append(shelf);
    }
    return el("div", {}, heroBlock(), stack);
  }

  // ---------------------------------------------------------------------------
  // Account (#/account) — the customer experience.
  // ---------------------------------------------------------------------------
  function customerAuthCard() {
    const feedback = status();
    const email = el("input", { className: "input", type: "email", placeholder: "you@example.com", autocomplete: "email" });
    const password = el("input", { className: "input", type: "password", placeholder: "At least 12 characters", autocomplete: "current-password" });
    const username = el("input", { className: "input", placeholder: "pick-a-handle", autocomplete: "username" });
    const usernameField = el("label", { className: "field" }, el("span", { className: "field-label" }, "Username"), username);
    usernameField.hidden = true;
    const submit = el("button", { type: "submit", className: "btn primary wide" }, "Log in");

    let mode = "login";
    const tabs = segmented(
      [
        { value: "login", label: "Log in" },
        { value: "signup", label: "Create account" },
      ],
      "login",
      (next) => {
        mode = next;
        usernameField.hidden = next !== "signup";
        submit.textContent = next === "signup" ? "Create account" : "Log in";
      },
    );

    async function run(action) {
      feedback.textContent = "Working…";
      feedback.className = "status working";
      try {
        await action();
        render();
      } catch (error) {
        feedback.textContent = error.message;
        feedback.className = "status";
      }
    }

    const form = el(
      "form",
      {
        className: "stack",
        onsubmit: (event) => {
          event.preventDefault();
          const address = email.value.trim();
          if (mode === "signup") run(() => signup("member", { email: address, password: password.value, username: username.value.trim().toLowerCase() }));
          else run(() => login("member", { email: address, password: password.value }));
        },
      },
      tabs,
      el("label", { className: "field" }, el("span", { className: "field-label" }, "Email"), email),
      el("label", { className: "field" }, el("span", { className: "field-label" }, "Password"), password),
      usernameField,
      submit,
      feedback,
    );

    return el(
      "div",
      { className: "auth-wrap" },
      el(
        "div",
        { className: "card auth-card" },
        el("div", { className: "auth-icon" }, iconBadge("user")),
        el("h2", {}, "Your account"),
        el("p", { className: "muted" }, "Sign in to keep your own private records. Your data is yours — the owner can't read it."),
        isPreview ? el("button", { className: "btn accent wide", type: "button", onclick: () => run(createThrowawayCustomer) }, icon("bolt", 16), "One-click customer account") : null,
        isPreview ? el("div", { className: "divider" }, "or use your own") : null,
        form,
      ),
    );
  }

  function memberRecordRow(section, item, refresh) {
    const remove = el("button", { type: "button", className: "icon-btn danger", title: "Delete", "aria-label": "Delete" });
    remove.append(icon("trash", 15));
    remove.addEventListener("click", async () => {
      remove.disabled = true;
      try {
        await api("/v1/me/resources/" + section.type + "/" + item.slug, { method: "DELETE", role: "member" });
        await refresh();
      } catch (error) {
        remove.disabled = false;
        remove.title = error.message;
      }
    });
    return el(
      "li",
      { className: "manager-item" },
      el(
        "div",
        { className: "mi-main" },
        el("strong", {}, item.title || item.slug),
        el("div", { className: "mi-fields" }, ...recordMeta(section, item)),
      ),
      el("div", { className: "mi-actions" }, el("span", { className: "pill pill-" + sectionVisibility(section) }, sectionVisibility(section)), remove),
    );
  }

  function memberSectionCard(section) {
    const list = el("ul", { className: "manager-list" });
    const body = el("div", {}, skeleton(2));

    async function refresh() {
      try {
        const page = await api("/v1/me/resources/" + section.type + "?limit=20", { role: "member" });
        const previews = (page.data || []).slice(0, 12);
        if (!previews.length) {
          setChildren(body, [emptyState("No " + (section.title || section.type).toLowerCase() + " yet — add your first above.")]);
          return;
        }
        const items = await Promise.all(
          previews.map((preview) =>
            api("/v1/me/resources/" + section.type + "/" + preview.slug, { role: "member" })
              .then((detail) => detail.data)
              .catch(() => ({ slug: preview.slug, title: preview.title, data: {} })),
          ),
        );
        setChildren(list, items.map((item) => memberRecordRow(section, item, refresh)));
        setChildren(body, [list]);
      } catch (error) {
        setChildren(body, [errorNote(error.message)]);
      }
    }

    const form = buildForm(section, {
      submitLabel: "Add",
      onSubmit: async (values, reset) => {
        await api("/v1/me/resources/" + section.type, { method: "POST", body: buildBody(section, values), role: "member" });
        reset();
        await refresh();
      },
    });

    refresh();
    return el(
      "section",
      { className: "card manager" },
      el(
        "div",
        { className: "section-head" },
        el("h2", {}, section.title || section.type),
        el("span", { className: "pill pill-" + sectionVisibility(section) }, sectionVisibility(section) === "public" ? "public page" : "private"),
      ),
      form,
      body,
    );
  }

  function account() {
    const session = getSession("member");
    if (!session) return customerAuthCard();
    const members = memberSections();
    const stack = el("div", { className: "stack-lg" });
    stack.append(
      el(
        "div",
        { className: "welcome" },
        el("h1", {}, "Hi, " + (session.username || session.email)),
        el("p", { className: "muted" }, "Everything below is yours. Private records stay private to you."),
      ),
    );
    if (!members.length) {
      stack.append(el("div", { className: "card pad" }, el("p", { className: "muted" }, "This app has no customer sections.")));
    } else {
      for (const section of members) stack.append(memberSectionCard(section));
    }
    return stack;
  }

  // ---------------------------------------------------------------------------
  // Owner (#/owner) — manage the public catalog.
  // ---------------------------------------------------------------------------
  function ownerAuthCard() {
    const feedback = status();
    const email = el("input", { className: "input", type: "email", placeholder: "owner@example.com", autocomplete: "email" });
    const password = el("input", { className: "input", type: "password", placeholder: "Owner password", autocomplete: "current-password" });

    async function run(action) {
      feedback.textContent = "Signing in…";
      feedback.className = "status working";
      try {
        const result = await action();
        if (!result || !result.user || result.user.role !== "admin") {
          setSession("owner", null);
          throw new Error("That account isn't the shop owner.");
        }
        render();
      } catch (error) {
        feedback.textContent = error.message;
        feedback.className = "status";
      }
    }

    const form = el(
      "form",
      {
        className: "stack",
        onsubmit: (event) => {
          event.preventDefault();
          run(() => login("owner", { email: email.value.trim(), password: password.value }));
        },
      },
      el("label", { className: "field" }, el("span", { className: "field-label" }, "Email"), email),
      el("label", { className: "field" }, el("span", { className: "field-label" }, "Password"), password),
      el("button", { type: "submit", className: "btn primary wide" }, "Sign in as owner"),
      feedback,
    );

    return el(
      "div",
      { className: "auth-wrap" },
      el(
        "div",
        { className: "card auth-card" },
        el("div", { className: "auth-icon" }, iconBadge("shield")),
        el("h2", {}, "Shop owner"),
        el("p", { className: "muted" }, "Sign in to edit the catalog and anything else customers see. Changes appear on the storefront instantly."),
        isPreview && ownerCreds
          ? el("button", { className: "btn accent wide", type: "button", onclick: () => run(() => login("owner", ownerCreds)) }, icon("shield", 16), "Sign in as the shop owner")
          : null,
        isPreview && ownerCreds ? el("div", { className: "divider" }, "or type credentials") : null,
        form,
      ),
    );
  }

  function ownerRecordRow(section, item, refresh) {
    const row = el("li", { className: "manager-item editable" });
    let editor = null;

    const editBtn = el("button", { type: "button", className: "icon-btn", title: "Edit", "aria-label": "Edit" });
    editBtn.append(icon("edit", 15));
    const delBtn = el("button", { type: "button", className: "icon-btn danger", title: "Delete", "aria-label": "Delete" });
    delBtn.append(icon("trash", 15));

    const view = el(
      "div",
      { className: "mi-view" },
      el(
        "div",
        { className: "mi-main" },
        el("strong", {}, item.title || item.slug),
        el("div", { className: "mi-fields" }, ...recordMeta(section, item)),
      ),
      el("div", { className: "mi-actions" }, editBtn, delBtn),
    );
    row.append(view);

    delBtn.addEventListener("click", async () => {
      delBtn.disabled = true;
      try {
        await api("/v1/shared/resources/" + section.type + "/" + item.slug, { method: "DELETE", role: "owner" });
        await refresh();
      } catch (error) {
        delBtn.disabled = false;
        delBtn.title = error.message;
      }
    });

    editBtn.addEventListener("click", () => {
      if (editor) {
        editor.remove();
        editor = null;
        return;
      }
      editor = buildForm(section, {
        submitLabel: "Save",
        submitIcon: "check",
        compact: true,
        initial: item.data || {},
        onCancel: () => {
          editor.remove();
          editor = null;
        },
        onSubmit: async (values) => {
          await api("/v1/shared/resources/" + section.type + "/" + item.slug, { method: "PUT", body: buildBody(section, values), role: "owner" });
          await refresh();
        },
      });
      row.append(editor);
    });

    return row;
  }

  function ownerSectionCard(section) {
    const list = el("ul", { className: "manager-list" });
    const body = el("div", {}, skeleton(2));

    async function refresh() {
      try {
        const page = await api("/v1/shared/resources/" + section.type + "?limit=20", { role: "owner" });
        const previews = (page.data || []).slice(0, 20);
        if (!previews.length) {
          setChildren(body, [emptyState("No items yet. Add your first above — it appears on the storefront instantly.")]);
          return;
        }
        const items = await Promise.all(
          previews.map((preview) =>
            api("/v1/shared/resources/" + section.type + "/" + preview.slug, { role: "owner" })
              .then((detail) => detail.data)
              .catch(() => ({ slug: preview.slug, title: preview.title, data: {} })),
          ),
        );
        setChildren(list, items.map((item) => ownerRecordRow(section, item, refresh)));
        setChildren(body, [list]);
      } catch (error) {
        setChildren(body, [errorNote(error.message)]);
      }
    }

    const form = buildForm(section, {
      submitLabel: "Publish",
      onSubmit: async (values, reset) => {
        await api("/v1/shared/resources/" + section.type, { method: "POST", body: buildBody(section, values), role: "owner" });
        reset();
        await refresh();
      },
    });

    refresh();
    return el(
      "section",
      { className: "card manager" },
      el(
        "div",
        { className: "section-head" },
        el("h2", {}, section.title || section.type),
        el("span", { className: "pill pill-public" }, "public"),
      ),
      el("p", { className: "muted sub" }, "You're the owner. Everyone can read these; only you can change them."),
      form,
      body,
    );
  }

  function owner() {
    const session = getSession("owner");
    if (!session) return ownerAuthCard();
    const owned = ownerSections();
    const stack = el("div", { className: "stack-lg" });
    stack.append(
      el(
        "div",
        { className: "welcome" },
        el("h1", {}, "Owner tools"),
        el("p", { className: "muted" }, "Signed in as " + (session.email || "owner") + ". Edits go live on the storefront."),
      ),
    );
    if (!owned.length) {
      stack.append(el("div", { className: "card pad" }, el("p", { className: "muted" }, "This app has no owner-managed sections.")));
    } else {
      for (const section of owned) stack.append(ownerSectionCard(section));
    }
    return stack;
  }

  // ---------------------------------------------------------------------------
  // Shell: nav + routing.
  // ---------------------------------------------------------------------------
  function segmented(items, initial, onChange) {
    let value = initial;
    const buttons = [];
    const node = el("div", { className: "segmented", role: "group" });
    for (const item of items) {
      const button = el(
        "button",
        {
          type: "button",
          className: item.value === value ? "active" : "",
          onclick: () => {
            value = item.value;
            buttons.forEach((candidate) => candidate.classList.toggle("active", candidate.dataset.value === value));
            if (onChange) onChange(value);
          },
        },
        item.label,
      );
      button.dataset.value = item.value;
      buttons.push(button);
      node.append(button);
    }
    return node;
  }

  function currentPath() {
    const hash = (location.hash || "").replace(/^#/, "");
    if (hash === "/account" || hash === "account") return "/account";
    if (hash === "/owner" || hash === "owner") return "/owner";
    return "/";
  }

  function navLink(href, label, active) {
    return el("a", { className: "nav-link" + (active ? " active" : ""), href }, label);
  }

  function topbar(path) {
    const role = path === "/owner" ? "owner" : path === "/account" ? "member" : null;
    const session = role ? getSession(role) : null;
    const right = el("div", { className: "topbar-right" });
    if (session) {
      right.append(
        el("span", { className: "who" }, session.username || session.email),
        textButton("Sign out", () => {
          setSession(role, null);
          render();
        }, "logout"),
      );
    }
    return el(
      "header",
      { className: "topbar" },
      el("a", { className: "brand", href: "#/" }, iconBadge(config.icon), el("span", { className: "brand-name" }, config.name || "m-stash")),
      el(
        "nav",
        { className: "nav" },
        navLink("#/", "Shop", path === "/"),
        navLink("#/account", "My account", path === "/account"),
        navLink("#/owner", "Owner", path === "/owner"),
      ),
      right,
    );
  }

  function footer() {
    return el(
      "footer",
      { className: "footer" },
      el("span", {}, "Built on "),
      el("strong", {}, "m-stash"),
      el("span", {}, " — ownership is enforced on the server for every request."),
    );
  }

  function render() {
    const path = currentPath();
    const view = path === "/account" ? account() : path === "/owner" ? owner() : storefront();
    setChildren(root, [
      el("div", { className: "app-shell" }, topbar(path), el("main", { className: "container" }, view), footer()),
    ]);
  }

  window.addEventListener("hashchange", render);
  render();
})();

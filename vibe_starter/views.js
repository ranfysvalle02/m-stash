(() => {
  "use strict";

  // views.js owns one renderer per spec "view". Each receives the ui toolkit
  // from app.js (el, icon, formatters), the section metadata, and the live
  // records fetched from the API. Rendering is textContent-only; app.js's icon
  // helper is the single source of (fixed) markup.

  function dataValue(item, key) {
    return item && item.data ? item.data[key] : undefined;
  }

  // actionButton renders the cross-tab call to action (e.g. "Book this cut") that
  // app.js wires on a public item. It is omitted when there is no action.
  function actionButton(ui, action, item) {
    if (!action) return null;
    return ui.el(
      "button",
      { type: "button", className: "act-btn", onclick: () => action.onAct(item) },
      ui.icon("bolt", 14),
      action.label,
    );
  }

  function hasValue(value) {
    return !(value == null || value === "" || (Array.isArray(value) && value.length === 0));
  }

  function fieldsByKind(section, kind) {
    return (section.fields || []).filter((field) => field.kind === kind);
  }

  function firstField(section, kinds) {
    return (section.fields || []).find((field) => kinds.indexOf(field.kind) !== -1);
  }

  function displayName(section, item) {
    const nameField = firstField(section, ["text"]);
    if (nameField && hasValue(dataValue(item, nameField.key))) return String(dataValue(item, nameField.key));
    return item.title || item.slug;
  }

  function description(section, item) {
    const longField = firstField(section, ["longtext"]);
    if (longField && hasValue(dataValue(item, longField.key))) return String(dataValue(item, longField.key));
    return item.summary || "";
  }

  function tagChips(ui, section, item) {
    const chips = [];
    for (const field of fieldsByKind(section, "tag")) {
      const value = dataValue(item, field.key);
      const list = Array.isArray(value) ? value : hasValue(value) ? [value] : [];
      for (const tag of list) chips.push(ui.chip(tag));
    }
    return chips;
  }

  // Secondary fields shown as label/value pairs (skips those rendered elsewhere).
  function metaPairs(ui, section, item, skipKeys) {
    const pairs = [];
    for (const field of section.fields || []) {
      if (skipKeys.indexOf(field.key) !== -1) continue;
      if (field.kind === "longtext" || field.kind === "tag") continue;
      const value = dataValue(item, field.key);
      if (!hasValue(value)) continue;
      pairs.push(
        ui.el(
          "div",
          { className: "meta" },
          ui.el("span", { className: "meta-label" }, field.label || field.key),
          ui.el("span", { className: "meta-value" }, ui.formatField(field, value)),
        ),
      );
    }
    return pairs;
  }

  function gridView(ui, section, items, action) {
    const priceField = firstField(section, ["price"]);
    const nameField = firstField(section, ["text"]);
    const grid = ui.el("div", { className: "grid" });
    for (const item of items) {
      const card = ui.el("article", { className: "product" });
      const top = ui.el(
        "div",
        { className: "product-top" },
        ui.el("h3", { className: "product-name" }, displayName(section, item)),
        priceField && hasValue(dataValue(item, priceField.key))
          ? ui.el("span", { className: "price" }, ui.formatPrice(dataValue(item, priceField.key)))
          : null,
      );
      card.append(top);
      const desc = description(section, item);
      if (desc) card.append(ui.el("p", { className: "product-desc" }, desc));
      const chips = tagChips(ui, section, item);
      if (chips.length) card.append(ui.el("div", { className: "chips" }, ...chips));
      const skip = [nameField && nameField.key, priceField && priceField.key].filter(Boolean);
      const pairs = metaPairs(ui, section, item, skip);
      if (pairs.length) card.append(ui.el("div", { className: "metas" }, ...pairs));
      const act = actionButton(ui, action, item);
      if (act) card.append(ui.el("div", { className: "act-row" }, act));
      grid.append(card);
    }
    return grid;
  }

  function timelineView(ui, section, items, action) {
    const dateField = firstField(section, ["date"]);
    const nameField = firstField(section, ["text"]);
    const sorted = items.slice().sort((a, b) => {
      const da = dateField ? dataValue(a, dateField.key) : a.createdAt;
      const db = dateField ? dataValue(b, dateField.key) : b.createdAt;
      return String(da || "").localeCompare(String(db || ""));
    });
    const line = ui.el("div", { className: "timeline" });
    for (const item of sorted) {
      const dateValue = dateField ? dataValue(item, dateField.key) : null;
      const body = ui.el(
        "div",
        { className: "tl-body" },
        hasValue(dateValue) ? ui.el("span", { className: "tl-date" }, ui.formatDate(dateValue)) : null,
        ui.el("h3", { className: "tl-title" }, displayName(section, item)),
      );
      const desc = description(section, item);
      if (desc) body.append(ui.el("p", { className: "tl-desc" }, desc));
      const pairs = metaPairs(ui, section, item, [nameField && nameField.key, dateField && dateField.key].filter(Boolean));
      if (pairs.length) body.append(ui.el("div", { className: "metas" }, ...pairs));
      const act = actionButton(ui, action, item);
      if (act) body.append(ui.el("div", { className: "act-row" }, act));
      line.append(
        ui.el(
          "div",
          { className: "tl-entry" },
          ui.el("div", { className: "tl-rail" }, ui.el("span", { className: "tl-dot" })),
          body,
        ),
      );
    }
    return line;
  }

  function rankedView(ui, section, items, action) {
    const scoreField = firstField(section, ["number", "price"]);
    const nameField = firstField(section, ["text"]);
    const sorted = items.slice().sort((a, b) => {
      const sa = scoreField ? Number(dataValue(a, scoreField.key)) || 0 : 0;
      const sb = scoreField ? Number(dataValue(b, scoreField.key)) || 0 : 0;
      return sb - sa;
    });
    const list = ui.el("ol", { className: "ranked" });
    sorted.forEach((item, index) => {
      const scoreValue = scoreField ? dataValue(item, scoreField.key) : null;
      list.append(
        ui.el(
          "li",
          { className: "rank" + (index < 3 ? " rank-top" : "") },
          ui.el("span", { className: "rank-badge" }, String(index + 1)),
          ui.el("span", { className: "rank-name" }, displayName(section, item)),
          hasValue(scoreValue)
            ? ui.el("span", { className: "rank-score" }, scoreField.kind === "price" ? ui.formatPrice(scoreValue) : String(scoreValue))
            : null,
          actionButton(ui, action, item),
        ),
      );
    });
    return list;
  }

  function listView(ui, section, items, checklist, action) {
    const nameField = firstField(section, ["text"]);
    const toggleField = firstField(section, ["toggle"]);
    const list = ui.el("ul", { className: "rows" });
    for (const item of items) {
      const left = ui.el("div", { className: "row-left" });
      if (checklist) {
        const done = toggleField ? Boolean(dataValue(item, toggleField.key)) : false;
        left.append(ui.el("span", { className: "check" + (done ? " done" : "") }, done ? ui.icon("check", 14) : null));
      }
      const main = ui.el("div", { className: "row-main" }, ui.el("strong", {}, displayName(section, item)));
      const desc = description(section, item);
      if (desc) main.append(ui.el("span", { className: "row-sub" }, desc));
      const chips = tagChips(ui, section, item);
      if (chips.length) main.append(ui.el("div", { className: "chips" }, ...chips));
      left.append(main);
      const row = ui.el("li", { className: "row" }, left);
      const pairs = metaPairs(ui, section, item, [nameField && nameField.key, toggleField && toggleField.key].filter(Boolean));
      const right = ui.el("div", { className: "row-metas" }, ...pairs);
      const act = actionButton(ui, action, item);
      if (act) right.append(act);
      if (pairs.length || act) row.append(right);
      list.append(row);
    }
    return list;
  }

  window.MStashViews = {
    render(section, items, ui, action) {
      if (!items || !items.length) return ui.el("div", {});
      switch (section.view) {
        case "grid":
          return gridView(ui, section, items, action);
        case "timeline":
          return timelineView(ui, section, items, action);
        case "ranked":
          return rankedView(ui, section, items, action);
        case "checklist":
          return listView(ui, section, items, true, action);
        default:
          return listView(ui, section, items, false, action);
      }
    },
  };
})();

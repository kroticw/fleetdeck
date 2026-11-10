// web/js/settings.js — the settings section of the centre column.
//
// Every global setting of the panel's config.yaml, laid out as the settings
// design (variant A): a column of sections on the left, and on the right the
// executors -- Claude Code and Codex side by side -- the default executor,
// notifications, the panel and its look. Each change is written at once
// (PATCH /api/settings) and the page is drawn again from the server's answer,
// so what is on screen is always what the file now holds.
import { fetchSettings, saveSettings } from "./api.js";
import { escapeHTML } from "./header.js";
import { t } from "./i18n.js";
import { DEFAULT_FONT_SIZE, MAX_FONT_SIZE, MIN_FONT_SIZE } from "./terminalfont.js";
import { ACCENTS } from "./theme.js";

// The approval presets the Codex card offers, as the two keys they write.
const APPROVALS = [
  { id: "read", label: "set_appr_read", hint: "set_appr_read_hint", sandbox: "read-only", policy: "on-request" },
  { id: "auto", label: "set_appr_auto", hint: "set_appr_auto_hint", sandbox: "workspace-write", policy: "on-request" },
  { id: "full", label: "set_appr_full", hint: "set_appr_full_hint", sandbox: "danger-full-access", policy: "never" },
];

const SECTIONS = [
  { id: "executors", label: "set_nav_executors" },
  { id: "notify", label: "set_nav_notify" },
  { id: "panel", label: "set_nav_panel" },
  { id: "look", label: "set_nav_look" },
];

const h = escapeHTML;

function attrs(map) {
  return Object.entries(map)
    .filter(([, v]) => v !== false && v !== undefined && v !== null)
    .map(([k, v]) => (v === true ? ` ${k}=""` : ` ${k}="${h(String(v))}"`))
    .join("");
}

function toggle(key, on, label, { disabled = false } = {}) {
  return `<button type="button" class="set-switch${on ? " on" : ""}" role="switch"${attrs({ "aria-checked": on ? "true" : "false", "aria-label": label, "data-key": key, "data-value": on ? "false" : "true", "data-kind": "bool", disabled })}><span class="set-knob"></span></button>`;
}

function segmented(key, label, options, current, { wide = false, kind = "string" } = {}) {
  const buttons = options
    .map(
      (o) =>
        `<button type="button" role="radio"${attrs({ "aria-checked": o.value === current ? "true" : "false", "data-key": key, "data-value": o.value, "data-kind": kind, "data-set": o.set, disabled: o.disabled })} class="set-seg${o.value === current ? " on" : ""}">${h(o.label)}</button>`,
    )
    .join("");
  return `<div class="set-segmented${wide ? " wide" : ""}" role="radiogroup" aria-label="${h(label)}">${buttons}</div>`;
}

function select(key, options, current) {
  const opts = options.map((o) => `<option value="${h(o.value)}"${o.value === current ? " selected" : ""}>${h(o.label)}</option>`).join("");
  return `<select class="set-input" data-key="${h(key)}">${opts}</select>`;
}

function text(key, value, { label = "", placeholder = "", narrow = false } = {}) {
  return `<input type="text" class="set-input${narrow ? " narrow" : ""}"${attrs({ "data-key": key, value: value ?? "", placeholder, "aria-label": label || false })}>`;
}

function restartBadge(page, key) {
  return page.restart.includes(key) ? `<span class="set-badge">${h(t("set_restart"))}</span>` : "";
}

function status(ok, words) {
  return `<div class="set-status${ok ? " ok" : ""}"><svg width="8" height="8" viewBox="0 0 8 8" aria-hidden="true"><circle cx="4" cy="4" r="4" fill="currentColor"></circle></svg>${h(words)}</div>`;
}

function claudeCard(page) {
  const v = page.values;
  const c = page.claude;
  const known = ["", ...c.models.filter((m) => m !== c.defaultModel)];
  const model = v["workers.model"];
  const other = model !== "" && !known.includes(model);
  const models = [
    { value: "", label: `${capital(c.defaultModel)} ${t("set_model_default")}` },
    ...c.models.filter((m) => m !== c.defaultModel).map((m) => ({ value: m, label: capital(m) })),
    { value: "\u0000other", label: t("set_model_other") },
  ];
  const modes = [{ value: "", label: `${c.defaultPermissionMode} ${t("set_model_default")}` }, ...c.permissionModes.filter((m) => m !== c.defaultPermissionMode).map((m) => ({ value: m, label: m }))];
  const efforts = [{ value: "", label: t("set_effort_auto") }, ...c.efforts.map((e) => ({ value: e, label: e }))];
  return `<section class="set-card">
<div class="set-card-head"><div class="set-titles"><h2>Claude Code</h2><span class="set-muted">${h(c.version ? `CLI ${c.version}` : t("set_not_found"))}</span></div>${toggle("", true, "Claude Code", { disabled: true })}</div>
${status(Boolean(c.version), c.version ? `${t("set_connected")} · ${t("set_claude_required")}` : c.versionError || t("set_not_found"))}
<label class="set-field">${h(t("set_model"))}${select("workers.model", models, other ? "\u0000other" : model)}</label>
${other ? `<label class="set-field">${h(t("set_model_other_label"))}${text("workers.model", model)}</label>` : ""}
<div class="set-field"><span>${h(t("set_effort"))}</span>${segmented("workers.effort", `${t("set_effort")} Claude`, efforts, v["workers.effort"], { wide: true })}</div>
<label class="set-field">${h(t("set_permissions"))}${select("workers.permission_mode", modes, v["workers.permission_mode"])}</label>
<div class="set-row-inline"><div class="set-titles"><span class="set-strong">${h(t("set_sandbox"))}</span><span class="set-muted set-small">${h(t("set_sandbox_hint"))}</span></div>${toggle("workers.sandbox", v["workers.sandbox"], t("set_sandbox"))}</div>
</section>`;
}

function codexCard(page) {
  const v = page.values;
  const c = page.codex;
  const enabled = v["executors.codex.enabled"];
  const current = v["executors.codex.model"];
  const chosen = c.models.find((m) => m.id === current) ?? c.models.find((m) => m.default);
  let modelField;
  if (c.models.length > 0) {
    // Unset is an option of its own: Codex then picks the model, and the one
    // it calls its default is named, not shown as chosen -- marked chosen, it
    // could not be picked to write it.
    const fallback = c.models.find((m) => m.default);
    const options = [
      { value: "", label: fallback ? `${t("set_model_codex_default")} — ${fallback.id}` : t("set_model_codex_default") },
      ...c.models.map((m) => ({ value: m.id, label: m.id })),
    ];
    if (current && !c.models.some((m) => m.id === current)) options.push({ value: current, label: current });
    modelField = select("executors.codex.model", options, current);
  } else {
    modelField = text("executors.codex.model", current, { placeholder: t("set_model_codex_default") });
  }
  const efforts = [{ value: "", label: t("set_effort_auto") }, ...(chosen?.efforts ?? []).map((e) => ({ value: e, label: e }))];
  const effort = v["executors.codex.effort"];
  if (effort && !efforts.some((e) => e.value === effort)) efforts.push({ value: effort, label: effort });
  const approval = APPROVALS.find((a) => a.sandbox === v["executors.codex.sandbox"] && a.policy === v["executors.codex.approval_policy"]);
  const approvals = APPROVALS.map((a) => ({ value: a.id, label: t(a.label), set: JSON.stringify({ "executors.codex.sandbox": a.sandbox, "executors.codex.approval_policy": a.policy }) }));
  const statusWords = !enabled ? t("set_codex_off") : c.version ? t("set_connected") : c.versionError || t("set_not_found");
  return `<section class="set-card">
<div class="set-card-head"><div class="set-titles"><h2>Codex</h2><span class="set-muted">${h(c.version ? `codex-cli ${c.version}` : t("set_not_found"))}</span></div>${toggle("executors.codex.enabled", enabled, "Codex")}</div>
${status(enabled && Boolean(c.version), statusWords)}
<label class="set-field">${h(t("set_model"))}${modelField}</label>
${c.modelsError && enabled ? `<span class="set-muted set-small">${h(c.modelsError)}</span>` : ""}
<div class="set-field"><span>${h(t("set_effort"))}</span>${segmented("executors.codex.effort", `${t("set_effort")} Codex`, efforts, effort, { wide: true })}<span class="set-muted set-small">${h(t("set_effort_codex_hint"))}</span></div>
<div class="set-field"><span>${h(t("set_approvals"))}</span>${segmented("approvals", t("set_approvals"), approvals, approval?.id ?? "", { wide: true, kind: "set" })}<span class="set-muted set-small">${h(t(approval?.hint ?? "set_appr_codex_hint"))}</span></div>
<div class="set-pair">
<label class="set-field">CODEX_HOME${text("executors.codex.home", v["executors.codex.home"], { placeholder: "~/.codex" })}</label>
<label class="set-field">${h(t("set_profile"))}${text("executors.codex.profile", v["executors.codex.profile"], { placeholder: t("set_profile_none") })}</label>
</div>
</section>`;
}

function row(title, hint, control, { last = false, badge = "" } = {}) {
  return `<div class="set-row${last ? " last" : ""}"><div class="set-titles"><span class="set-row-title">${h(title)}${badge}</span>${hint ? `<span class="set-muted set-small">${h(hint)}</span>` : ""}</div>${control}</div>`;
}

function notifySection(page) {
  const v = page.values;
  return `<h1 id="settings-notify" class="set-h1 spaced">${h(t("set_nav_notify"))}</h1>
<section class="set-card set-list">
${row(t("set_notify_waiting"), t("set_notify_waiting_hint"), toggle("notify.enabled.waiting", v["notify.enabled.waiting"], t("set_notify_waiting")))}
${row(t("set_notify_failed"), "", toggle("notify.enabled.failed", v["notify.enabled.failed"], t("set_notify_failed")))}
${row(t("set_notify_blocked"), "", toggle("notify.enabled.card_blocked", v["notify.enabled.card_blocked"], t("set_notify_blocked")))}
${row(t("set_notify_silent"), t("set_notify_silent_hint"), `<div class="set-inline"><label class="set-inline-label">${h(t("set_threshold"))}${text("notify.silence_after", v["notify.silence_after"], { narrow: true })}</label>${toggle("notify.enabled.silent", v["notify.enabled.silent"], t("set_notify_silent"))}</div>`, { last: true })}
</section>`;
}

function panelSection(page) {
  const v = page.values;
  const command = (v["agent.command"] ?? []).join("\n");
  return `<h1 id="settings-panel" class="set-h1 spaced">${h(t("set_nav_panel"))}</h1>
<section class="set-card set-list">
${row(t("set_usage"), t("set_usage_hint"), toggle("usage.enabled", v["usage.enabled"], t("set_usage")))}
${row(t("set_usage_labels"), t("set_usage_labels_hint"), `<div class="set-inline"><label class="set-inline-label">Claude${text("usage.labels.claude", v["usage.labels.claude"], { placeholder: t("set_usage_label_claude") })}</label><label class="set-inline-label">Codex${text("usage.labels.codex", v["usage.labels.codex"], { placeholder: t("set_usage_label_codex") })}</label></div>`)}
${row(t("set_port"), t("set_port_hint"), `<input type="text" inputmode="numeric" class="set-input narrow" data-key="server.port" data-kind="int" value="${h(String(v["server.port"]))}" aria-label="${h(t("set_port"))}">`, { badge: restartBadge(page, "server.port") })}
${row(t("set_poll"), t("set_poll_hint"), text("daemon.poll_interval", v["daemon.poll_interval"], { label: t("set_poll"), narrow: true }), { badge: restartBadge(page, "daemon.poll_interval") })}
<details class="set-advanced"><summary>${h(t("set_advanced"))}</summary>
<div class="set-advanced-body">
<label class="set-field">${h(t("set_agent_command"))}${restartBadge(page, "agent.command")}<textarea class="set-input" rows="2" data-key="agent.command" data-kind="list">${h(command)}</textarea></label>
<label class="set-field">${h(t("set_agent_dir"))}${restartBadge(page, "agent.config_dir")}${text("agent.config_dir", v["agent.config_dir"], { placeholder: "~/.claude" })}</label>
<label class="set-field">${h(t("set_statusline_wrap"))}${restartBadge(page, "statusline.wrap")}${text("statusline.wrap", v["statusline.wrap"])}</label>
<label class="set-field">${h(t("set_statusline_limits"))}${restartBadge(page, "statusline.rate_limits_path")}${text("statusline.rate_limits_path", v["statusline.rate_limits_path"])}</label>
</div></details>
</section>`;
}

function lookSection(page) {
  const v = page.values;
  const themes = [
    { value: "", label: t("set_theme_auto") },
    { value: "light", label: t("set_theme_light") },
    { value: "dark", label: t("set_theme_dark") },
  ];
  const size = v["ui.terminal_font_size"] || DEFAULT_FONT_SIZE;
  const step = (n) => Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, size + n));
  const fontValue = (n) => (n === DEFAULT_FONT_SIZE ? 0 : n);
  const accent = ACCENTS.includes(v["ui.accent"]) ? v["ui.accent"] : ACCENTS[0];
  const swatches = ACCENTS.map(
    (a) =>
      `<button type="button" role="radio"${attrs({ "aria-checked": a === accent ? "true" : "false", "data-key": "ui.accent", "data-value": a, "data-kind": "string" })} class="set-swatch${a === accent ? " on" : ""}"><span class="set-swatch-dot" data-accent-swatch="${a}"></span><span class="set-swatch-name">${h(t(`accent_${a}`))}</span></button>`,
  ).join("");
  const glasses = [
    { value: "", label: t("set_glass_full") },
    { value: "reduced", label: t("set_glass_reduced") },
    { value: "opaque", label: t("set_glass_opaque") },
  ];
  const grounds = [
    { value: "", label: t("set_term_bg_theme") },
    { value: "dark", label: t("set_term_bg_dark") },
  ];
  return `<h1 id="settings-look" class="set-h1 spaced">${h(t("set_nav_look"))}</h1>
<section class="set-card set-list">
<div class="set-row"><div class="set-titles"><h2>${h(t("set_theme"))}</h2><span class="set-muted">${h(t("set_theme_hint"))}</span></div>${segmented("ui.theme", t("set_theme"), themes, v["ui.theme"])}</div>
<div class="set-row set-row-stack"><div class="set-titles"><h2>${h(t("set_accent"))}</h2><span class="set-muted">${h(t("set_accent_hint"))}</span></div><div class="set-swatches" role="radiogroup" aria-label="${h(t("set_accent"))}">${swatches}</div></div>
<div class="set-row last"><div class="set-titles"><h2>${h(t("set_glass"))}</h2><span class="set-muted">${h(t("set_glass_hint"))}</span></div>${segmented("ui.glass", t("set_glass"), glasses, v["ui.glass"] ?? "")}</div>
</section>
<section class="set-card set-list">
<div class="set-row"><div class="set-titles set-grow"><h2>${h(t("set_font"))}</h2><span class="set-muted">${h(t("set_font_hint"))}</span><code class="set-preview" style="font-size: ${size}px">$ fleetdeck runtime --key codex:4317cfac</code></div>
<div class="set-stepper">
<button type="button" class="set-step" data-key="ui.terminal_font_size" data-kind="int" data-value="${fontValue(step(-1))}" aria-label="${h(t("set_font_smaller"))}"${size <= MIN_FONT_SIZE ? " disabled" : ""}>A−</button>
<button type="button" class="set-step on" data-key="ui.terminal_font_size" data-kind="int" data-value="0" aria-label="${h(`${t("set_font_reset")} ${DEFAULT_FONT_SIZE}`)}">${size}</button>
<button type="button" class="set-step" data-key="ui.terminal_font_size" data-kind="int" data-value="${fontValue(step(1))}" aria-label="${h(t("set_font_bigger"))}"${size >= MAX_FONT_SIZE ? " disabled" : ""}>A+</button>
</div></div>
<div class="set-row last"><div class="set-titles"><h2>${h(t("set_term_bg"))}</h2><span class="set-muted">${h(t("set_term_bg_hint"))}</span></div>${segmented("ui.terminal_background", t("set_term_bg"), grounds, v["ui.terminal_background"] ?? "")}</div>
</section>`;
}

function capital(s) {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}

/** settingsHTML is the whole page for a GET /api/settings answer. */
export function settingsHTML(page, { active = "executors", error = "" } = {}) {
  const v = page.values;
  const defaults = [
    { value: "claude", label: "Claude Code" },
    { value: "codex", label: "Codex", disabled: !v["executors.codex.enabled"] },
  ];
  const nav = SECTIONS.map((s) => `<a href="#settings-${s.id}" class="set-nav-link${s.id === active ? " on" : ""}" data-section="${s.id}"${s.id === active ? ' aria-current="true"' : ""}>${h(t(s.label))}</a>`).join("");
  return `<div class="settings-layout">
<nav class="set-nav" aria-label="${h(t("tab_settings"))}">${nav}</nav>
<div class="set-main">
${error ? `<p class="set-error" role="alert">${h(`${t("set_error")} ${error}`)}</p>` : ""}
<div class="set-head"><h1 id="settings-executors" class="set-h1">${h(t("set_nav_executors"))}</h1><p class="set-muted">${h(t("set_exec_hint"))}</p></div>
<div class="set-grid">${claudeCard(page)}${codexCard(page)}</div>
<section class="set-card set-row-card"><div class="set-titles"><h2>${h(t("set_default_exec"))}</h2><span class="set-muted">${h(t("set_default_exec_hint"))}</span></div>${segmented("default_executor", t("set_default_exec"), defaults, v["default_executor"])}</section>
${notifySection(page)}
${panelSection(page)}
${lookSection(page)}
<p class="set-muted set-small">${h(t("set_saved_note"))}</p>
</div>
</div>`;
}

// valueOf reads the value a control writes, in the kind its key holds.
function valueOf(control) {
  const kind = control.dataset.kind ?? "string";
  const raw = "value" in control.dataset ? control.dataset.value : control.value;
  if (kind === "bool") return raw === "true";
  if (kind === "int") return Number(String(raw).trim() || 0);
  if (kind === "list") {
    return String(raw)
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
  }
  return raw;
}

/**
 * renderSettings fills root with the settings section and keeps it: it asks
 * for the page now, and writes every change the operator makes. `onSaved`
 * hears each page the server answers with, so the rest of the window can
 * follow what changed (the theme, the terminals' type).
 */
export function renderSettings(root, { load = fetchSettings, save = saveSettings, onSaved = () => {} } = {}) {
  let page = null;
  let active = "executors";
  let error = "";
  const draw = () => {
    if (!page) return;
    const scroll = root.scrollTop;
    root.innerHTML = settingsHTML(page, { active, error });
    root.scrollTop = scroll;
  };
  const write = async (values) => {
    try {
      page = await save(values);
      error = "";
      onSaved(page);
    } catch (err) {
      error = err?.message ?? String(err);
    }
    draw();
  };
  root.classList.add("settings");
  root.innerHTML = `<p class="set-muted set-loading">${h(t("set_loading"))}</p>`;
  load().then(
    (p) => {
      page = p;
      draw();
    },
    (err) => {
      root.innerHTML = `<p class="set-error" role="alert">${h(`${t("set_load_error")} ${err?.message ?? err}`)}</p>`;
    },
  );

  root.addEventListener("click", (event) => {
    const link = event.target.closest?.(".set-nav-link");
    if (link) {
      event.preventDefault();
      active = link.dataset.section;
      draw();
      root.querySelector?.(`#settings-${active}`)?.scrollIntoView?.({ block: "start" });
      return;
    }
    const control = event.target.closest?.("button[data-key]");
    if (!control || control.disabled || !page) return;
    if (control.dataset.kind === "set") {
      write(JSON.parse(control.dataset.set));
      return;
    }
    write({ [control.dataset.key]: valueOf(control) });
  });
  root.addEventListener("change", (event) => {
    const control = event.target;
    if (!control?.dataset?.key || control.tagName === "BUTTON" || !page) return;
    if (control.tagName === "SELECT" && control.value === "\u0000other") {
      // "Other…" asks for a name rather than writing one.
      page = { ...page, values: { ...page.values, [control.dataset.key]: page.values[control.dataset.key] || " " } };
      draw();
      return;
    }
    write({ [control.dataset.key]: valueOf(control) });
  });
  return {
    refresh: () =>
      load().then((p) => {
        page = p;
        draw();
      }),
    // Shows one section, now or, before the values load, when they do.
    showSection: (id) => {
      active = id;
      draw();
      root.querySelector?.(`#settings-${id}`)?.scrollIntoView?.({ block: "start" });
    },
  };
}

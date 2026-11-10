// web/js/capsules.js — what the window draws in the bar across its top, in the
// page's words.
//
// In the fleetdeck window the brand, the fleet, the limits, an update, the
// counters and the theme are system controls in glass, drawn by the window
// (cmd/fleetdeck-window/capsulemodel.go). The window keeps no strings and no
// colours of its own: the board hands it this model, in the language and theme
// the page is in, and hands it again whenever it changes.

import { buildLabel } from "./buildcheck.js";

// fleets is fleet.js's fleetEntries; limits header.js's limitsOf; update the
// version on offer, or ""; counts how many sessions wait, how many stalled and
// how many never said; theme "light", "dark" or null for the system's; colors
// the levels' colours and attn and muted.
// limitsMenu is header.js's limitsMenuOf: the window's menu under the limits
// capsule. A percentage the menu has no number for is -1 to the window.
export function capsuleModel({ build, fleet, fleets, limits, limitsMenu, update, counts, theme, t, colors, accent, opaque }) {
  const counter = (n, word, color) => ({ text: `${n} ${word}`, color });
  const waitingNumber = `${counts.waiting}${counts.unknown > 0 ? "+?" : ""}`;
  return {
    version: 1,
    brand: { name: "fleetdeck", version: buildLabel(build) },
    fleet: {
      name: fleet,
      label: t("fleet_menu"),
      fleets: fleets.map(({ name, current, waiting }) => ({ name, current, waiting })),
      all: t("fleet_menu_all"),
      add: t("fleet_menu_new"),
    },
    limitsLabel: t("limits_title"),
    limits: limits.map(({ label, pct, level }) => ({
      label,
      text: typeof pct === "number" ? `${pct}%` : "—",
      level,
      color: colors[level],
    })),
    ...(limitsMenu
      ? {
          limitsMenu: {
            title: t("limits_title"),
            note: t("limits_busiest_first"),
            models: limitsMenu.models.map((m) => ({
              name: m.name,
              kind: m.kind,
              color: colors[m.level],
              rows: m.rows.map((r) => ({ title: r.title, text: r.text, pct: r.pct ?? -1, note: r.note, color: colors[r.level] })),
            })),
            updated: limitsMenu.updated,
            sources: t("limits_sources"),
          },
        }
      : {}),
    ...(update ? { update: { text: t("update_available").replace("{version}", update), action: t("update_button") } } : {}),
    counters: [
      counter(waitingNumber, t("waiting_count"), counts.waiting > 0 ? colors.attn : colors.muted),
      counts.stalled > 0
        ? counter(counts.stalled, t("stalled_count"), colors.attn)
        : { ...counter(0, t("stalled_count"), colors.muted), hollow: true },
    ],
    theme: {
      label: t("set_theme"),
      choice: theme ?? "system",
      light: t("set_theme_light"),
      system: t("set_theme_auto"),
      dark: t("set_theme_dark"),
    },
    ...(accent ? { accent } : {}),
    ...(opaque ? { opaque: true } : {}),
  };
}

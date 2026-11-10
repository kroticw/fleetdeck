// web/js/capsules.js — what the window draws in its capsules, in the page's words.
//
// In the fleetdeck window the tabs, the new card button and the limits are
// system controls in glass, drawn by the window. The window keeps no
// strings and no colours of its own: the board hands it this model, in the
// language and theme the page is in, and hands it again whenever it changes.

export function capsuleModel({ section, limits, t, colors, accent, opaque }) {
  return {
    version: 1,
    tabs: [
      { id: "board", label: t("tab_board"), selected: section === "board" },
      { id: "docs", label: t("tab_docs"), selected: section === "docs" },
      { id: "settings", label: t("tab_settings"), selected: section === "settings" },
    ],
    newCard: { label: t("new_card") },
    limits: limits.map(({ label, pct, level }) => ({
      label,
      text: typeof pct === "number" ? `${pct}%` : "—",
      level,
      color: colors[level],
    })),
    ...(accent ? { accent } : {}),
    ...(opaque ? { opaque: true } : {}),
  };
}

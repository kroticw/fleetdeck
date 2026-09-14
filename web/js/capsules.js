// web/js/capsules.js — what the window draws in its capsules, in the page's words.
//
// In the fleetdeck window the tabs, the new card button, the theme and the
// limits are system controls in glass, drawn by the window. The window keeps no
// strings and no colours of its own: the board hands it this model, in the
// language and theme the page is in, and hands it again whenever it changes.

export function capsuleModel({ section, themeLabel, limits, t, colors }) {
  return {
    version: 1,
    tabs: [
      { id: "board", label: t("tab_board"), selected: section === "board" },
      { id: "docs", label: t("tab_docs"), selected: section === "docs" },
    ],
    newCard: { label: t("new_card") },
    theme: { label: themeLabel },
    limits: limits.map(({ label, pct, level }) => ({
      label,
      text: typeof pct === "number" ? `${pct}%` : "—",
      level,
      color: colors[level],
    })),
  };
}

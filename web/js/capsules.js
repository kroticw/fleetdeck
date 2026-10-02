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
    // An aged value says how old it is in the text, as the browser tab's gauge
    // does. Without it the window drew a day-old reading exactly like a fresh
    // one: the age reached only the fill colour of a thin bar, which is not
    // something an operator glancing at the row notices. tooltip is the
    // board's sentence about the limit, for the pointer; always a string, so
    // the window never reads a missing field.
    limits: limits.map(({ label, pct, level, age, title }) => ({
      label,
      text: typeof pct === "number" ? `${pct}%${age ? ` · ${age}` : ""}` : "—",
      level,
      color: colors[level],
      tooltip: title ?? "",
    })),
  };
}

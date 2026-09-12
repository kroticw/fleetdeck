// The dictionary. Two properties matter enough to pin: a key with no entry has
// to come out as the key, because a blank label on screen reads as a
// deliberately empty one and hides the missing entry; and every key the card
// panel asks for has to exist in both dictionaries, not only in whichever one
// this machine's locale happens to select.
//
// i18n.js picks its dictionary once, when the module is evaluated, so a language
// cannot be switched at runtime. Each language is therefore loaded as its own
// module instance: a distinct query string makes node treat it as a separate
// module rather than serving the one already in its cache.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// The keys web/js/card.js asks for. Listed here rather than imported from the
// module so that a key added to the panel and forgotten in one dictionary fails
// this test instead of rendering in the wrong language in the browser.
const KEYS = [
  "card_close",
  "card_waiting",
  "card_gone",
  "card_parse_error",
  // Master's own key, reused by the panel rather than restated under a second
  // name: same fact, same sentence.
  "session_dead",
  "card_not_committed",
  "card_write_refused",
  "backlinks",

  // web/js/sections.js and web/js/docs.js. The tab labels are on this list for
  // the same reason as the rest: a tab that fell back to its own key would read
  // "tab_docs" in the interface.
  "tab_board",
  "tab_docs",
  // web/js/header.js's fleet switcher and web/js/sessions.js's fleet groups.
  // "fleet_none" above all: it heads the sessions no fleet claims, and a
  // heading reading its own key would hide what the group is.
  "fleet_switch",
  "fleet_none",
  "fleet_sessions",
  "fleet_waiting",
  // web/js/sessions.js's two groups for the sessions that are not running,
  // and web/js/board.js's mark for a card whose session is stopped. The
  // badges above all: a row falling back to "stopped_badge" would say
  // nothing about what is wrong with the session it sits on, which is the
  // whole reason the row is there.
  // web/js/sessions.js's folded strip. Both are titles on a bare number, and
  // a title falling back to its own key would leave the number unexplained —
  // which is the state the strip was added to end.
  "folded_running",
  "folded_unknown",
  "stopped_group",
  "stopped_badge",
  // The resume button and everything it can say. resume_working and
  // resume_failed matter as much as the label: they are the only two things
  // on screen during and after a resume, and either of them falling back to
  // its own key would leave the operator reading "resume_failed" as the
  // reason a session did not come back.
  "resume",
  "resume_hint",
  "resume_working",
  "resume_failed",
  "gone_group",
  "gone_badge",
  "gone_no_cwd",
  "last_state",
  "stopped_unknown",
  "session_stopped",
  // web/js/newcard.js. The missing-commit note especially: it is the one line
  // that says the card is on the board and not in its history, and falling
  // through to its own key would print "new_card_not_committed" instead.
  "new_card",
  "new_card_title",
  "new_card_zone",
  "new_card_create",
  "new_card_cancel",
  "new_card_title_required",
  "new_card_not_committed",
  // web/js/setup.js. setup_outside above all: it is the sentence that says
  // what is written outside the chosen folder before the button is pressed.
  "setup_title",
  "setup_intro",
  "setup_board",
  "setup_docs",
  "setup_outside",
  "setup_path",
  "setup_choose",
  "setup_choose_message",
  "setup_choose_prompt",
  "setup_create",
  "setup_path_required",
  "setup_skipped",
  "setup_opening",
  "setup_failed",
  "setup_no_handover",
  // The orchestrator step of the same page. The wizard_warn_* lines above all:
  // they are what a person reads before adding to a session's conversation, and
  // a warning that fell through to its keys would warn nobody.
  "wizard_setup_done",
  "wizard_title",
  "wizard_intro",
  "wizard_message",
  "wizard_brief",
  "wizard_brief_show",
  "wizard_new_title",
  "wizard_new_text",
  "wizard_new_button",
  "wizard_new_unavailable",
  "wizard_existing_title",
  "wizard_existing_text",
  "wizard_no_sessions",
  "wizard_daemon_down",
  "wizard_current",
  "wizard_waiting_for_you",
  "wizard_context",
  "wizard_doing_unknown",
  "wizard_warn_title",
  "wizard_warn_lead",
  "wizard_warn_kept",
  "wizard_warn_context",
  "wizard_warn_context_now",
  "wizard_warn_busy",
  "wizard_warn_final",
  "wizard_warn_replaces",
  "wizard_warn_again",
  "wizard_appoint_button",
  "wizard_skip",
  "wizard_rerun_hint",
  "wizard_working",
  "wizard_starting",
  "wizard_done",
  "wizard_failed",
  "wizard_open",
  "orchestrator_wizard",
  "orchestrator_wizard_hint",
  "pick_doc",
  "doc_opening",
  "docs_empty",
  "docs_list_failed",
  "doc_open_failed",
  // The keys web/js/session.js asks for. In the same list because the property
  // being checked is the same one, and because a key that only exists in the
  // language this machine happens to run in is exactly as invisible here as it
  // is there.
  "tab_digest",
  "tab_screen",
  "write_to_session",
  "close_session",
  "no_steps",
  "terminal_missing",
  // The one string in the panel that says a button leaves this machine. Missing
  // from a dictionary it renders as "keys_to_session" above four bare glyphs,
  // which is the state the operator already could not read.
  "keys_to_session",

  // Attaching an image (web/js/session.js). The permission line especially: it
  // is the one that keeps a session stopping to ask from being read as a hang,
  // and falling through to its own key would say "image_may_ask_permission" at
  // exactly the moment a person needs a sentence.
  "image_no_session",
  "image_too_large",
  "image_wrong_type",
  "image_may_ask_permission",

  // web/js/sessions.js's own column header and the theme override's button
  // (web/js/header.js renders it, web/js/theme.js decides its state) — both
  // added by the same task that added this comment. On this list for the
  // same reason as everything else here: a key missing from one dictionary
  // fails here, not silently in whichever language this machine's locale
  // is not.
  "sessions_title",
  "theme_auto",
  "theme_light",
  "theme_dark",

  // web/js/header.js's stale-usage notices, added alongside the fresh
  // usage_down muting -- same reason as everything else on this list.
  // Split into two once "sign-in needed" turned out to be shown for
  // causes sign-in cannot fix (a rate limit): the wording now depends on
  // cmd/fleetdeck/collect.go's classifyUsageError, not only on how long
  // the failure has lasted.
  "usage_down_auth",
  "usage_down_rate_limited",

  // The gauges' own last-known-value label, shown when a failed refresh
  // falls back to usage.Fetcher's cache (cmd/fleetdeck/collect.go) instead
  // of blanking the limits to "—" for one poll cycle.
  "last_known",

  // The signature under the operator's own words in a thread (web/js/steps.js).
  // It is the whole of the distinction between what he typed and what an agent
  // sent him, so falling through to its own key would print "typed_here" where
  // the point was to be readable at a glance.
  "typed_here",

  // The controls that size the orchestrator column (web/js/orchestrator.js).
  // The unfold one carries the most weight: when the column is folded it is the
  // only thing left on screen, and a button reading "column_unfold" is a column
  // nobody can get back.
  "column_drag",
  "column_fold",
  "column_unfold",

  // The size shown over a terminal whose type changed (web/js/liveterminal.js).
  // It is the one word saying the session changed for everyone watching it;
  // falling through to its own key would print "terminal_font_session" there.
  "terminal_font_session",
  "terminal_font_limit",
  // The font buttons' own names (web/js/fontcontrols.js): a button whose
  // tooltip reads "terminal_font_bigger" says nothing about what it does.
  "terminal_font_smaller",
  "terminal_font_bigger",
  "terminal_font_reset",

  // The start page (web/js/start.js) — the screen the application opens on.
  // start_new_text above all: it is the sentence that says the panel shows the
  // new fleet only after it is restarted, and it is read before the button, not
  // discovered after it.
  "start_intro",
  "start_last",
  "start_footer",
  "start_new",
  "start_new_title",
  "start_new_text",
  "start_new_name",
  "start_new_path",
  "start_new_outside",
  "start_new_create",
  "start_new_cancel",
  "start_name_required",
  "start_new_unavailable",
  "start_made",
  "start_failed",
  "start_offline",

  // The fleet menu in the panel's header (web/js/header.js). It replaced the
  // row of fleet buttons, so these carry what the row used to say by being a
  // row: fleet_menu_all is the way back to the start page, and fleet_menu_new
  // the way to a fleet that does not exist yet.
  "fleet_menu",
  "fleet_menu_all",
  "fleet_menu_new",
];

// Presence in a dictionary cannot be observed through t(): a Russian lookup
// falls back to the English text, so a key missing from `ru` is indistinguishable
// from one translated identically. Comparing the two languages' output would
// therefore fail the first term that is genuinely spelled the same in both. The
// dictionaries are read from the source instead, which is what "present in both"
// actually means.
const source = readFileSync(new URL("../js/i18n.js", import.meta.url), "utf8");

function dictionary(name) {
  const match = new RegExp(`const ${name} = \\{([\\s\\S]*?)\\n\\};`).exec(source);
  assert.ok(match, `web/js/i18n.js no longer declares a flat "const ${name} = {...}" object`);
  return match[1];
}

function declares(body, key) {
  return new RegExp(`(^|\\n)\\s*${key}:`).test(body);
}

let instance = 0;

async function loadWith(language) {
  const original = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  Object.defineProperty(globalThis, "navigator", { value: { language }, configurable: true });
  try {
    instance += 1;
    return await import(new URL(`../js/i18n.js?instance=${instance}`, import.meta.url).href);
  } finally {
    if (original) Object.defineProperty(globalThis, "navigator", original);
    else delete globalThis.navigator;
  }
}

test("a missing key renders as the key, never as nothing", async () => {
  for (const language of ["en-GB", "ru-RU"]) {
    const { t } = await loadWith(language);
    assert.equal(t("no_such_key_anywhere"), "no_such_key_anywhere");
  }
});

// The orchestrator wizard asks the panel for the working order in this code, so
// a Russian page must say "ru": otherwise the person reads the wizard in Russian
// and the session is sent the English file.
test("the page's language code follows the dictionary it speaks", async () => {
  for (const [language, code] of [["ru-RU", "ru"], ["ru", "ru"], ["en-GB", "en"], ["de-DE", "en"]]) {
    const { langCode } = await loadWith(language);
    assert.equal(langCode, code, `${language} speaks ${code}`);
  }
});

test("every key the panels ask for is in both dictionaries", () => {
  const en = dictionary("en");
  const ru = dictionary("ru");
  for (const key of KEYS) {
    assert.ok(declares(en, key), `${key} is missing from the English dictionary`);
    assert.ok(declares(ru, key), `${key} is missing from the Russian dictionary`);
  }
});

test("and none of them falls through to the key itself", async () => {
  const english = await loadWith("en-GB");
  const russian = await loadWith("ru-RU");
  for (const key of KEYS) {
    assert.notEqual(english.t(key), key, `${key} renders as its own name in English`);
    assert.notEqual(russian.t(key), key, `${key} renders as its own name in Russian`);
  }
});

test("Russian is chosen for a Russian locale and English for anything else", async () => {
  assert.equal((await loadWith("ru-RU")).t("card_gone"), "карточка исчезла");
  assert.equal((await loadWith("en-GB")).t("card_gone"), "card is gone");
  assert.equal((await loadWith("de-DE")).t("card_gone"), "card is gone");
});

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

// The keys web/js/card.js asks for. Listed here rather than imported from the
// module so that a key added to the panel and forgotten in one dictionary fails
// this test instead of rendering in the wrong language in the browser.
const KEYS = [
  "card_close",
  "card_waiting",
  "card_gone",
  "card_parse_error",
  "card_session_dead",
  "card_not_committed",
  "card_write_refused",
  "backlinks",
];

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

test("every key the card panel asks for is answered in both languages", async () => {
  const english = await loadWith("en-GB");
  const russian = await loadWith("ru-RU");
  for (const key of KEYS) {
    assert.notEqual(english.t(key), key, `${key} has no entry in the English dictionary`);
    assert.notEqual(russian.t(key), key, `${key} has no entry in the Russian dictionary`);
    // The Russian lookup falls back to English, so an entry missing from ru
    // reads as English rather than as the key. Comparing the two is what
    // actually catches it.
    assert.notEqual(english.t(key), russian.t(key), `${key} is untranslated`);
  }
});

test("Russian is chosen for a Russian locale and English for anything else", async () => {
  assert.equal((await loadWith("ru-RU")).t("card_gone"), "карточка исчезла");
  assert.equal((await loadWith("en-GB")).t("card_gone"), "card is gone");
  assert.equal((await loadWith("de-DE")).t("card_gone"), "card is gone");
});

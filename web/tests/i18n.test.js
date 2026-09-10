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
  "card_session_dead",
  "card_not_committed",
  "card_write_refused",
  "backlinks",
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

test("every key the card panel asks for is in both dictionaries", () => {
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

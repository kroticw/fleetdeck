// The renderer's job is to turn a card body written by an autonomous agent into
// text. These are the tests that have to fail if it ever stops doing that.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { renderMarkdown } from "../js/markdown.js";

const snapshot = JSON.parse(
  readFileSync(new URL("./testdata/snapshot.json", import.meta.url), "utf8"),
);

const cardNames = new Set(
  snapshot.cards.map((card) => card.path.split("/").pop().replace(/\.md$/, "")),
);

test("a card body cannot inject an element", () => {
  const body = 'An agent wrote <img src=x onerror=alert(1)> and <script>alert(1)</script>.';
  const html = renderMarkdown(body, new Set());

  assert.ok(!html.includes("<img"), `an <img element survived: ${html}`);
  assert.ok(!html.includes("<script"), `a <script element survived: ${html}`);
  assert.ok(html.includes("&lt;img src=x onerror=alert(1)&gt;"), html);
  assert.ok(html.includes("&lt;script&gt;alert(1)&lt;/script&gt;"), html);
});

test("the fixture card body, which carries both, renders as text", () => {
  const card = snapshot.cards.find((c) => c.path === "/board/fleet-ui.md");
  // Proves the fixture itself still holds hostile content: a test that renders a
  // sanitised body would pass no matter what the renderer did.
  assert.ok(card.body.includes("<img src=x onerror=alert(1)>"), "the fixture body lost its <img>");
  assert.ok(card.body.includes("<script>alert(1)</script>"), "the fixture body lost its <script>");

  const html = renderMarkdown(card.body, cardNames);
  assert.ok(!html.includes("<img"), html);
  assert.ok(!html.includes("<script"), html);
});

test("a fenced code block is escaped, not executed and not re-parsed", () => {
  const html = renderMarkdown("```sh\n<b>**not bold**</b>\n```", new Set());
  assert.ok(html.includes("<pre><code>"), html);
  assert.ok(html.includes("&lt;b&gt;**not bold**&lt;/b&gt;"), html);
  assert.ok(!html.includes("<strong>"), html);
});

test("an unterminated fence is closed rather than left open", () => {
  const html = renderMarkdown("```\nstill open", new Set());
  assert.equal(html.split("<pre><code>").length - 1, 1);
  assert.equal(html.split("</code></pre>").length - 1, 1);
});

test("a wiki link name cannot break out of the attribute it lands in", () => {
  const known = new Set(['weird"name', "weird"]);
  const html = renderMarkdown('see [[weird"name]]', known);

  const attribute = /data-link="([^"]*)"/.exec(html);
  assert.ok(attribute, `no data-link attribute was produced: ${html}`);
  assert.equal(attribute[1], "weird&quot;name");
  // The literal quote must not appear anywhere: one unescaped quote here ends
  // the attribute and everything after it becomes markup.
  assert.ok(!html.includes('"weird"name"'), html);
  // Four quotes and no more: the two that delimit class and the two that
  // delimit data-link. A fifth is a quote that came out of the card.
  assert.equal((html.match(/"/g) ?? []).length, 4, html);
});

test("a known wiki link is a link, an unknown one is not clickable", () => {
  const html = renderMarkdown("[[fleet-ui]] and [[nowhere]]", cardNames);

  assert.ok(html.includes('<a class="wikilink" data-link="fleet-ui">fleet-ui</a>'), html);
  assert.ok(html.includes('<span class="wikilink wikilink-missing">nowhere</span>'), html);
  // The panel delegates on [data-link]; a missing card must carry no such
  // attribute or it would navigate to nothing.
  assert.equal(html.match(/data-link=/g).length, 1);
});

test("an alias and a heading resolve to the note the server extracted", () => {
  const html = renderMarkdown("[[fleet-ui|the panel]] and [[fleet-ui#log]]", cardNames);
  assert.ok(html.includes('data-link="fleet-ui">the panel</a>'), html);
  assert.ok(html.includes('data-link="fleet-ui">fleet-ui#log</a>'), html);
});

test("headings, lists, bold and inline code render", () => {
  const html = renderMarkdown("## Log\n\n- **done** and `code`\n- second", new Set());
  assert.ok(html.includes("<h3>Log</h3>"), html);
  assert.ok(html.includes("<ul>"), html);
  assert.equal(html.match(/<li>/g).length, 2);
  assert.ok(html.includes("<strong>done</strong>"), html);
  assert.ok(html.includes("<code>code</code>"), html);
  assert.ok(html.includes("</ul>"), html);
});

test("nothing at all renders as nothing", () => {
  assert.equal(renderMarkdown("", new Set()), "");
  assert.equal(renderMarkdown(undefined, new Set()), "");
  // The documentation section calls it with an empty set and no cards to link.
  assert.equal(renderMarkdown(null), "");
});

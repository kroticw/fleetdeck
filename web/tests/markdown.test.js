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
  // Six quotes and no more: the pairs delimiting type, class and data-link. A
  // seventh is a quote that came out of the card.
  assert.equal((html.match(/"/g) ?? []).length, 6, html);
});

test("a known wiki link is a link, an unknown one is not clickable", () => {
  const html = renderMarkdown("[[fleet-ui]] and [[nowhere]]", cardNames);

  assert.ok(
    html.includes('<button type="button" class="wikilink" data-link="fleet-ui">fleet-ui</button>'),
    html,
  );
  assert.ok(html.includes('<span class="wikilink wikilink-missing">nowhere</span>'), html);
  // The panel delegates on [data-link]; a missing card must carry no such
  // attribute or it would navigate to nothing.
  assert.equal(html.match(/data-link=/g).length, 1);
});

test("an alias and a heading resolve to the note the server extracted", () => {
  const html = renderMarkdown("[[fleet-ui|the panel]] and [[fleet-ui#log]]", cardNames);
  assert.ok(html.includes('data-link="fleet-ui">the panel</button>'), html);
  assert.ok(html.includes('data-link="fleet-ui">fleet-ui#log</button>'), html);
});

test("a link is reachable from the keyboard, a broken one is not a control", () => {
  const html = renderMarkdown("[[fleet-ui]] and [[nowhere]]", cardNames);
  // An <a> with no href is not focusable and not in the tab order: it would
  // look like a link and work only for a mouse.
  assert.ok(!/<a[\s>]/.test(html), `a link was rendered as an href-less anchor: ${html}`);
  assert.ok(html.includes('<button type="button" class="wikilink"'), html);
  // A card that does not exist has nothing to activate, so it is not a control
  // and must not take a tab stop.
  assert.ok(html.includes('<span class="wikilink wikilink-missing">'), html);
});

test("a code span wins over the constructs inside it", () => {
  const html = renderMarkdown("a `[[fleet-ui]]` and `**not bold**`", cardNames);
  assert.ok(html.includes("<code>[[fleet-ui]]</code>"), html);
  assert.ok(html.includes("<code>**not bold**</code>"), html);
  // The whole point: nothing inside backticks became a control.
  assert.ok(!html.includes("data-link"), html);
  assert.ok(!html.includes("<strong>"), html);
});

test("a backtick inside a link alias cannot produce crossed markup", () => {
  const html = renderMarkdown("[[fleet-ui|a`b]] and `c` and [[fleet-ui]]", cardNames);
  const opened = (html.match(/<button/g) ?? []).length;
  const closed = (html.match(/<\/button>/g) ?? []).length;
  assert.equal(opened, closed, `unbalanced markup: ${html}`);
  assert.equal((html.match(/<code>/g) ?? []).length, (html.match(/<\/code>/g) ?? []).length, html);
});

test("a fenced block does not open or close with a blank line", () => {
  // HTML drops a newline immediately after <pre>, never after <code>, so a
  // newline between the two shows up as an empty first line in every block.
  assert.equal(renderMarkdown("```\nx\n```", new Set()), "<pre><code>x</code></pre>");
  assert.equal(renderMarkdown("```sh\na\nb\n```", new Set()), "<pre><code>a\nb</code></pre>");
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

// --- tables ---
//
// The fleet's reports are full of them and none of them rendered: a table came
// out as literal vertical bars. The scope here is exactly what those reports
// contain — a header, the separator under it, body rows, and alignment by
// colons in the separator. Nothing else, because the source of these tables is
// one and is known.

const TABLE = [
  "| Сессия | Чем занята |",
  "| --- | --- |",
  "| 06a1f607 | оркестрация |",
  "| 80b7dc38 | панель карточки |",
].join("\n");

test("a table becomes a table, not a row of vertical bars", () => {
  const html = renderMarkdown(TABLE, new Set());
  assert.ok(html.includes("<table"), "the whole point");
  assert.equal((html.match(/<th\b/g) ?? []).length, 2, "two header cells");
  assert.equal((html.match(/<tr\b/g) ?? []).length, 3, "a header row and two body rows");
  assert.equal((html.match(/<td\b/g) ?? []).length, 4, "four body cells");
  assert.ok(!html.includes("| Сессия"), "and no bars left over");
});

test("a table's cells are rendered, so bold and code inside one work", () => {
  const html = renderMarkdown("| a | b |\n| --- | --- |\n| **bold** | `code` |", new Set());
  assert.ok(html.includes("<strong>bold</strong>"));
  assert.ok(html.includes("<code>code</code>"));
});

test("alignment comes from the colons in the separator", () => {
  const html = renderMarkdown("| l | c | r |\n| :--- | :---: | ---: |\n| 1 | 2 | 3 |", new Set());
  assert.ok(/class="[^"]*md-left/.test(html), "a leading colon aligns left");
  assert.ok(/class="[^"]*md-center/.test(html), "colons on both sides centre");
  assert.ok(/class="[^"]*md-right/.test(html), "a trailing colon aligns right");
});

test("a table scrolls inside its own box, never widening the page", () => {
  // Four columns in a narrow column is the normal case here, not the edge one.
  const html = renderMarkdown(TABLE, new Set());
  assert.ok(/<div class="md-table">\s*<table/.test(html), "the table is wrapped in something that can scroll");
});

// --- the control cases: text that LOOKS like a table must stay text ---

test("a single line with a bar is a paragraph, not a table", () => {
  const html = renderMarkdown("this | that", new Set());
  assert.ok(!html.includes("<table"), "one line cannot be a table: there is no separator under it");
  assert.ok(html.includes("this | that"));

  // With a separator underneath it, only the missing outer bars still say this
  // is not a table — so this half of the test holds when the other half cannot.
  const withSeparator = renderMarkdown("this | that\n| --- | --- |", new Set());
  assert.ok(!withSeparator.includes("<table"), "a row is fenced by bars on both sides or it is prose");
});

test("a header with no separator under it is not a table", () => {
  const html = renderMarkdown("| a | b |\nplain text follows", new Set());
  assert.ok(!html.includes("<table"), "the separator is what confirms a table, and there is none");
});

test("a separator with no header above it is not a table", () => {
  const html = renderMarkdown("| --- | --- |\n| 1 | 2 |", new Set());
  assert.ok(!html.includes("<table"), "a table has two ends, and this one has no beginning");
});

test("bars inside a fenced code block stay inside the code block", () => {
  const html = renderMarkdown("```\n| a | b |\n| --- | --- |\n```", new Set());
  assert.ok(!html.includes("<table"), "code is code, whatever it looks like");
  assert.ok(html.includes("| a | b |"));
});

test("a bar inside an inline code span does not start a table", () => {
  const html = renderMarkdown("use `a | b` in a pipe\n| --- | --- |", new Set());
  assert.ok(!html.includes("<table"));
});

test("prose about a table is prose", () => {
  const html = renderMarkdown("The column reads | Сессия | Чем занята | and that is the bug.", new Set());
  assert.ok(!html.includes("<table"));
  assert.ok(html.includes("Чем занята"));

  const followed = renderMarkdown(
    "The column reads | Сессия | Чем занята | and that is the bug.\n| --- | --- |",
    new Set(),
  );
  assert.ok(!followed.includes("<table"), "a sentence does not become a header because a separator follows it");
});

test("a table ends where its rows end, and what follows is not swallowed", () => {
  const html = renderMarkdown(`${TABLE}\n\nA sentence after the table.`, new Set());
  assert.ok(html.includes("<table"));
  assert.ok(html.includes("<p>A sentence after the table.</p>"), "finding the start is not finding the whole");
  assert.ok(html.indexOf("</table>") < html.indexOf("A sentence after"), "and the table closed before it");
});

test("a row with fewer cells than the header is still a row, not a dropped line", () => {
  const html = renderMarkdown("| a | b |\n| --- | --- |\n| only one |", new Set());
  assert.ok(html.includes("<table"));
  assert.ok(html.includes("only one"), "a short row is malformed, not invisible");
});

test("a cell cannot bring its own markup", () => {
  const html = renderMarkdown('| a |\n| --- |\n| <img src=x onerror=alert(1)> |', new Set());
  assert.ok(!html.includes("<img"), "cells go through the same escaping as everything else");
  assert.ok(html.includes("&lt;img src=x onerror=alert(1)&gt;"));
});

test("a separator has to contain a dash — colons alone are not one", () => {
  const html = renderMarkdown("| a | b |\n| : | : |\n| 1 | 2 |", new Set());
  assert.ok(!html.includes("<table"), "without a dash that line is just another row");
});

test("a separator narrower than its header does not make a table", () => {
  const html = renderMarkdown("| a | b |\n| --- |\n| 1 | 2 |", new Set());
  assert.ok(!html.includes("<table"), "a separator that does not match the header describes a different table");
});

test("a table is rendered once, and its rows are not read a second time", () => {
  const html = renderMarkdown(TABLE, new Set());
  assert.equal((html.match(/<table\b/g) ?? []).length, 1, "one table");
  assert.equal((html.match(/оркестрация/g) ?? []).length, 1, "and each row appears once");
  assert.ok(!html.includes("<p>| ---"), "the separator is consumed, not printed as a paragraph");
});

test("an empty cell is a cell, not a gap that shifts the row", () => {
  const html = renderMarkdown("| a | b | c |\n| --- | --- | --- |\n| 1 |  | 3 |", new Set());
  const row = html.slice(html.indexOf("<tbody>"));
  assert.equal((row.match(/<td\b/g) ?? []).length, 3, "three cells, one of them empty");
  assert.ok(row.includes("<td></td>"), "an empty cell keeps its place");
});

// --- paragraphs ---
//
// A card's paragraph is wrapped in the source, and every wrapped line was
// becoming its own block: one paragraph about Xray showed as four. In markdown
// a single newline continues a paragraph; a blank line starts a new one.

test("a paragraph wrapped across source lines is one paragraph", () => {
  const html = renderMarkdown("Про Xray отдельно: у XTLS все релизы\nпомечены как prerelease, поэтому\nrelease/latest отдаёт старый.", new Set());
  assert.equal((html.match(/<p>/g) ?? []).length, 1, "one paragraph, not three");
  assert.ok(html.includes("релизы помечены"), "the lines are joined by a space, not run together");
});

test("a blank line still starts a new paragraph", () => {
  // The control: if everything collapsed into one, this is not parsing, it is
  // deleting newlines.
  const html = renderMarkdown("первый абзац\nего продолжение\n\nвторой абзац", new Set());
  assert.equal((html.match(/<p>/g) ?? []).length, 2, "two paragraphs, split where the blank line is");
  assert.ok(html.includes("<p>первый абзац его продолжение</p>"));
  assert.ok(html.includes("<p>второй абзац</p>"));
});

test("several blank lines are one break, not several empty paragraphs", () => {
  const html = renderMarkdown("один\n\n\n\nдва", new Set());
  assert.equal((html.match(/<p>/g) ?? []).length, 2);
  assert.ok(!/<p>\s*<\/p>/.test(html), "no empty paragraphs");
});

test("a paragraph ends where another construct begins", () => {
  const html = renderMarkdown("текст абзаца\n# Заголовок\nещё текст", new Set());
  assert.ok(html.includes("<p>текст абзаца</p>"), "the heading closes the paragraph before it");
  assert.ok(/<h2>Заголовок<\/h2>/.test(html));
  assert.ok(html.includes("<p>ещё текст</p>"));
});

test("lines inside a fenced block keep their own newlines", () => {
  const html = renderMarkdown("```\nстрока один\nстрока два\n```", new Set());
  assert.ok(html.includes("строка один\nстрока два"), "code is not reflowed");
});

// --- ordered lists ---
//
// Bulleted lists already worked — checked by running the renderer, not assumed.
// Numbered ones did not: "1. Сессии ждут ответа" stayed a paragraph with a digit.

test("a numbered list becomes a list", () => {
  const html = renderMarkdown("1. Сессии ждут ответа\n2. Теряется контроль", new Set());
  assert.ok(html.includes("<ol>"), "an ordered list, not paragraphs with digits");
  assert.equal((html.match(/<li>/g) ?? []).length, 2);
  assert.ok(!html.includes("1."), "the marker belongs to the list, not to the text");
});

test("a numbered list may start at a number other than one", () => {
  // Real: a card numbers a stub as "0." on purpose.
  const html = renderMarkdown("0. Заготовка в цель не входит\n1. Остальное входит", new Set());
  assert.ok(/<ol start="0">/.test(html), "the list starts where the author started it");
});

test("an ordered list ends where its items end", () => {
  const html = renderMarkdown("1. первый\n2. второй\n\nОбычный текст.", new Set());
  assert.ok(html.includes("</ol>"), "the list closes");
  assert.ok(html.includes("<p>Обычный текст.</p>"), "and what follows is not swallowed");
  assert.ok(html.indexOf("</ol>") < html.indexOf("Обычный текст"));
});

test("a bulleted list and a numbered list are different lists", () => {
  const html = renderMarkdown("- маркер\n1. номер", new Set());
  assert.ok(html.includes("</ul>"), "the bulleted list closes when the numbering starts");
  assert.ok(html.includes("<ol"), "and the numbered one opens");
});

test("a bulleted list still works", () => {
  const html = renderMarkdown("- один\n- два", new Set());
  assert.equal((html.match(/<li>/g) ?? []).length, 2);
  assert.ok(html.includes("<ul>"));
});

// --- the control case, taken from the real board ---

test("a date at the start of a line is not a list item", () => {
  // Found by grepping the operator's own board: "07.09 в 12:42 Дарья…" is the
  // only line there that starts with digits and a dot and is not a list. What
  // separates them is the space after the dot, and that is the whole rule.
  const html = renderMarkdown("07.09 в 12:42 Дарья перевела тикет из Closed", new Set());
  assert.ok(!html.includes("<ol"), "a date is not a numbered list");
  assert.ok(html.includes("07.09 в 12:42"), "and it keeps its digits");
});

test("a version number at the start of a line is not a list item", () => {
  const html = renderMarkdown("3.4.3 — версия, до которой обновляем", new Set());
  assert.ok(!html.includes("<ol"));
  assert.ok(html.includes("3.4.3"));
});

test("a number with a dot but no space is not a list item", () => {
  const html = renderMarkdown("1.Слитно написанное не список", new Set());
  assert.ok(!html.includes("<ol"));
});

// --- what the mutation run found my tests could not see ---

test("a bulleted list after a numbered one closes it", () => {
  // The mirror of the test above. Checking one direction proved nothing about
  // the other: the code has two branches and only one of them was covered.
  const html = renderMarkdown("1. номер\n- маркер", new Set());
  assert.ok(html.includes("</ol>"), "the numbered list closes when the bullet starts");
  assert.ok(html.indexOf("</ol>") < html.indexOf("<ul>"));
});

test("a list after a paragraph is a new list", () => {
  // If the open-list flag is not cleared when the list closes, the second list
  // never opens: its items land loose between blocks.
  const html = renderMarkdown("- один\n\nтекст\n\n- два", new Set());
  assert.equal((html.match(/<ul>/g) ?? []).length, 2, "two lists, both opened");
  assert.equal((html.match(/<\/ul>/g) ?? []).length, 2, "and both closed");
});

test("a paragraph before a table stays before it", () => {
  // A buffered paragraph that is not flushed is not lost — it is printed after
  // whatever came next, which is worse: the text is on screen, in the wrong
  // place, and nothing looks broken.
  // No blank line between them, deliberately: with one, the paragraph is
  // already closed before the table is even looked at, and the test passes
  // whatever the table branch does. The first version of this test had one.
  const html = renderMarkdown("Вводная строка\n| a | b |\n| --- | --- |\n| 1 | 2 |", new Set());
  assert.ok(html.includes("<p>Вводная строка</p>"));
  assert.ok(html.indexOf("Вводная строка") < html.indexOf("md-table"), "before the table, not after");
});

test("a paragraph before a fenced block stays before it", () => {
  const html = renderMarkdown("Вводная строка\n```\nкод\n```", new Set());
  assert.ok(html.indexOf("Вводная строка") < html.indexOf("<pre>"), "before the code, not after");
});

test("paragraphs are separated in the output, not run together", () => {
  const html = renderMarkdown("абзац один\n\nабзац два", new Set());
  assert.ok(html.includes("<p>абзац один</p>\n\n<p>абзац два</p>"), "the blank line survives as a break");
});

// --- an item has two ends too ---
//
// Found on the operator's own board, not imagined: cards wrap their list items
// the way they wrap their prose, and the continuation line carries an indent.
// Every numbered list in 2026-09-07-obnovlenie-remnawave.md is written this way,
// and each continuation was closing the list and reopening it at the next item —
// one list of four items came out as three lists and two stray paragraphs.

test("an indented continuation belongs to the item above it", () => {
  const html = renderMarkdown("1. Обновить панель 3.3.2 → 3.4.3.\n   Перед этим — бэкап.", new Set());
  assert.equal((html.match(/<li>/g) ?? []).length, 1, "one item, not an item and a paragraph");
  assert.equal((html.match(/<ol/g) ?? []).length, 1, "and one list");
  assert.ok(html.includes("3.4.3. Перед этим"), "joined by a space");
  assert.ok(!html.includes("<p>"), "the continuation is not a paragraph");
});

test("a wrapped list keeps all its items in one list", () => {
  const html = renderMarkdown(
    "1. **Сначала панель, потом ноды.** Панель сейчас старее нод\n   (3.4.0) — это перекос.\n2. **Ноды по одной.** Порядок: Латвия → Москва →\n   Стокгольм.",
    new Set(),
  );
  assert.equal((html.match(/<ol/g) ?? []).length, 1, "one list, not one per item");
  assert.equal((html.match(/<li>/g) ?? []).length, 2);
});

test("bulleted items continue the same way", () => {
  const html = renderMarkdown("- Нашёл ломающее изменение — остановись и спроси, не\n  импровизируй.", new Set());
  assert.equal((html.match(/<li>/g) ?? []).length, 1);
  assert.ok(html.includes("не импровизируй."));
});

test("an unindented line after an item still ends the list", () => {
  // The control. Without the indent there is nothing to tell a continuation
  // from the paragraph that follows a list, and swallowing that paragraph into
  // the last item would be the same defect facing the other way.
  const html = renderMarkdown("- пункт\nОбычный текст после списка.", new Set());
  assert.ok(html.includes("</ul>"), "the list closes");
  assert.ok(html.includes("<p>Обычный текст после списка.</p>"), "and the text is its own paragraph");
});

test("markup split across a wrapped item still renders", () => {
  // A consequence of buffering the item rather than appending rendered halves:
  // bold that starts on one line and ends on the next is one bold run.
  const html = renderMarkdown("- **Это прод с живыми\n  пользователями.** Дальше текст.", new Set());
  assert.ok(html.includes("<strong>Это прод с живыми пользователями.</strong>"));
});


// The docs list grouped by the card a document belongs to, or by date alone,
// newest first or oldest first: the order the operator picks in the docs tab.

import { test } from "node:test";
import assert from "node:assert/strict";

import { docGroups, shortDate } from "../js/docsort.js";

const doc = (title, created) => ({ path: `/d/${title}`, title, created });
const DOCS = [
  doc("brief.md", "2026-11-02T10:00:00Z"),
  doc("log.md", "2026-11-05T10:00:00Z"),
  doc("loose.md", "2026-11-04T10:00:00Z"),
  doc("old.md", "2026-10-21T10:00:00Z"),
  doc("undated.md", undefined),
];
const VPN = { path: "/c/T-188.md", id: "T-188", title: "VPN client" };
const DEOB = { path: "/c/T-079.md", id: "T-079", title: "Deobfuscation" };
const cardOf = (d) => ({ "brief.md": VPN, "log.md": VPN, "old.md": DEOB })[d.title] ?? null;
const titles = (groups) => groups.map((g) => [g.card?.id ?? null, g.items.map((d) => d.title)]);

test("by task: a group per card, the card with the newest document first, the loose ones last", () => {
  assert.deepEqual(titles(docGroups(DOCS, cardOf, { by: "task", newestFirst: true })), [
    ["T-188", ["log.md", "brief.md"]],
    ["T-079", ["old.md"]],
    [null, ["loose.md", "undated.md"]],
  ]);
});

test("by task, oldest first: the groups and their documents turn round, the loose ones stay last", () => {
  assert.deepEqual(titles(docGroups(DOCS, cardOf, { by: "task", newestFirst: false })), [
    ["T-079", ["old.md"]],
    ["T-188", ["brief.md", "log.md"]],
    [null, ["undated.md", "loose.md"]],
  ]);
});

test("by date: one list, no headings, newest first", () => {
  const groups = docGroups(DOCS, cardOf, { by: "date", newestFirst: true });
  assert.equal(groups.length, 1);
  assert.equal(groups[0].card, undefined);
  assert.deepEqual(groups[0].items.map((d) => d.title), ["log.md", "loose.md", "brief.md", "old.md", "undated.md"]);
});

test("the date beside a document is its day and month, or nothing when unknown", () => {
  assert.equal(shortDate("2026-11-05T10:00:00Z"), "05.11");
  assert.equal(shortDate(undefined), "");
  assert.equal(shortDate("not a date"), "");
});

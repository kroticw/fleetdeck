// The panel's copy of the upload limits, checked against the server's.
//
// web/js/imagefile.js repeats maxImageBytes and the accepted media types from
// internal/server/image.go, so that a file too large or of the wrong kind is
// refused before it is uploaded rather than after. Repetition that drifts in
// silence is the defect this project keeps finding, so this reads the Go source
// and fails when the two disagree.
//
// Every extraction below fails loudly when it finds nothing. A regular expression
// over source breaks on a rename, and a test whose "not found" path quietly
// passes is worse than no test: it goes green with more confidence the further
// the two sides have drifted. The two failures are also worded differently on
// purpose — "could not read the server's value" and "the values disagree" are
// different repairs, and six months from now the message is all anyone has.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { MAX_IMAGE_BYTES, IMAGE_TYPES } from "../js/imagefile.js";

const GO_SOURCE = "../../internal/server/image.go";
const go = readFileSync(new URL(GO_SOURCE, import.meta.url), "utf8");

// maxImageBytes = 8 << 20 — captured as its two operands rather than as a
// literal, because that is how the constant is written and reformatting it into
// a decimal is exactly the kind of change this should survive.
function serverMaxImageBytes() {
  const match = /maxImageBytes\s*=\s*(\d+)\s*<<\s*(\d+)/.exec(go);
  assert.ok(
    match,
    `could not read maxImageBytes from ${GO_SOURCE}: the constant was renamed or its form changed, ` +
      "so this test can no longer compare anything and must not report success",
  );
  return Number(match[1]) * 2 ** Number(match[2]);
}

// The keys of the imageTypes map, which is the server's allowlist.
function serverImageTypes() {
  const block = /var imageTypes = map\[string\]string\{([\s\S]*?)\n\}/.exec(go);
  assert.ok(
    block,
    `could not read the imageTypes map from ${GO_SOURCE}: it was renamed or reshaped, ` +
      "so this test can no longer compare anything and must not report success",
  );
  const types = [...block[1].matchAll(/"(image\/[a-z0-9.+-]+)"\s*:/g)].map((m) => m[1]);
  assert.ok(
    types.length > 0,
    `read the imageTypes map from ${GO_SOURCE} but found no media types in it`,
  );
  return types;
}

test("the panel's size ceiling is the server's", () => {
  const server = serverMaxImageBytes();
  assert.equal(
    MAX_IMAGE_BYTES,
    server,
    `the values disagree: the panel refuses above ${MAX_IMAGE_BYTES} bytes while the server ` +
      `accepts up to ${server}. Whichever is right, a person is being told the wrong limit.`,
  );
});

test("the panel accepts exactly the types the server keeps", () => {
  const server = serverImageTypes();
  assert.deepEqual(
    [...IMAGE_TYPES].sort(),
    [...server].sort(),
    "the values disagree: the panel and the server accept different image types, so a file " +
      "is either refused here and welcome there, or sent there to be refused",
  );
});

// The Go source is read by path, and a path that stops resolving is its own kind
// of silent pass — the file could be moved and every test above would fail with
// a message about renames instead of about the move.
test("the server's source is where this test expects it", () => {
  assert.ok(
    go.includes("package server"),
    `${GO_SOURCE} does not look like the server package: this test is reading the wrong file`,
  );
});

// Pasting an image into one of the panel's own input boxes.
//
// This replaces a button that stood next to the input and, in the operator's
// word, was an "ужас": it took up room in the writing area for something that
// should simply happen. Copy a screenshot, put the caret in the box, press
// Cmd+V — the same gesture the terminal interface takes.
//
// Nothing here reads anybody's clipboard. A paste event is the browser handing
// the page what a person just pasted, at the moment they pasted it; the panel
// never asks and never sees the clipboard at any other time. That is the whole
// difference from the thing this project refused earlier — driving Ctrl+V into
// somebody else's terminal through the machine's clipboard, which would race a
// live human for a shared resource and overwrite whatever they had copied.
//
// The single most important property is what happens when there is no image:
// nothing at all. No preventDefault, no writing into the box, no error. Cmd+V
// on text is what a person does constantly and attaching an image is what they
// do occasionally, and a handler that swallowed the common case for the rare
// one would be a bad trade even if it worked.

import { IMAGE_TYPES, MAX_IMAGE_BYTES, detectImageType, toBase64 } from "./imagefile.js";
import { uploadSessionImage } from "./api.js";
import { t } from "./i18n.js";

// imagesIn pulls the images out of a paste, in the order the clipboard offers
// them, and returns an empty array for a paste that carries none.
//
// `items` rather than `files`: a screenshot copied from a screen-capture tool
// arrives as an item of kind "file" whose getAsFile() builds the File on
// demand, and some browsers leave `files` empty in exactly that case.
//
// A file that is not announced as an image is left alone entirely — pasting a
// zip into the box is not this handler's business, and swallowing it would
// break a paste the panel has no intention of doing anything with. An empty
// type is still taken: some clipboards leave it blank for a real screenshot,
// and the signature check downstream is what decides in that case. What the
// clipboard says is a filter, never the answer — a file claiming image/png with
// something else inside is still refused by its bytes.
function imagesIn(clipboardData) {
  if (!clipboardData) return [];
  const items = [...(clipboardData.items ?? [])];
  return items
    .filter((item) => item?.kind === "file")
    .filter((item) => !item.type || String(item.type).startsWith("image/"))
    .map((item) => item.getAsFile?.())
    .filter(Boolean);
}

const formatBytes = (bytes) => `${Math.round(bytes / (1024 * 1024))} MiB`;

/**
 * wireImagePaste makes `input` accept a pasted image, uploading it and putting
 * the resulting path into the box.
 *
 * `session` is a function, not a value, and is called at paste time. The
 * orchestrator column re-points its one textarea at a different session while
 * the node stays in place, so an id captured when this was wired would send a
 * later paste to the session that used to be pinned — quietly, and with every
 * appearance of success.
 *
 * Returns a function that removes the handler. A panel torn down and rebuilt
 * without it would leave one behind on every open, and a single paste would
 * upload the same image several times.
 */
export function wireImagePaste(input, session, { onError = () => {}, onNotice = () => {} } = {}) {
  const onPaste = (event) => {
    const files = imagesIn(event.clipboardData);
    if (files.length === 0) return;

    // From here on this is an image paste, so the browser must not also drop its
    // own idea of the clipboard's contents into the box — some of them paste a
    // file name as text.
    event.preventDefault?.();

    const target = session();
    if (!target) {
      // Nothing to attach it to. Uploading anyway would write a file under a
      // session id of "undefined", which nobody will ever look at.
      onError(t("image_no_session"));
      return;
    }

    // Sequential rather than parallel: the paths are appended in the order the
    // clipboard offered the images, and a race would shuffle them into an order
    // the operator did not choose.
    void (async () => {
      for (const file of files) {
        await attachOne(file, target, input, onError, onNotice);
      }
    })();
  };

  input.addEventListener("paste", onPaste);
  return () => input.removeEventListener("paste", onPaste);
}

async function attachOne(file, target, input, onError, onNotice) {
  // Size first, from the file's own metadata, so an oversized image is refused
  // without being read into memory at all.
  if (file.size > MAX_IMAGE_BYTES) {
    onError(`${t("image_too_large")} (${formatBytes(MAX_IMAGE_BYTES)})`);
    return;
  }

  let bytes;
  try {
    bytes = new Uint8Array(await file.arrayBuffer());
  } catch (err) {
    onError(err.message);
    return;
  }

  // The bytes, not the type the clipboard claimed. The server decides by the
  // signature, so this checks the same end — and it is a courtesy that saves a
  // doomed upload, never a substitute for the server's own check.
  if (detectImageType(bytes) === "") {
    onError(`${t("image_wrong_type")} (${IMAGE_TYPES.join(", ")})`);
    return;
  }

  try {
    const path = await uploadSessionImage(target, toBase64(bytes));
    // Appended on its own line: the operator is mid-sentence as often as not,
    // and an attachment is not a reason to lose what they were writing.
    const typed = input.value;
    input.value = typed === "" ? path : `${typed.replace(/\s*$/, "")}\n${path}`;
    onError("");
    onNotice(t("image_may_ask_permission"));
  } catch (err) {
    // Nothing was stored, so nothing goes into the box, and what was typed
    // stays exactly as it was.
    onError(err.message);
  }
}

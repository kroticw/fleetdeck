// A deliberately small markdown renderer: headings, unordered lists, fenced and
// inline code, bold, and Obsidian wiki links. A card is written to be read, not
// typeset, and vendoring a full implementation would be a dependency this
// project cannot justify.
//
// Everything here is a defence, not a convenience. Card bodies are written by
// autonomous agents into files on disk, and this function's output is inserted
// with innerHTML into a page that can type into a live Claude Code session. So
// the whole input is escaped once, up front, and every construct below is built
// from text that has already been through escapeHTML. Nothing that arrives in
// `text` can reach the DOM as markup.
//
// The escape set is &, <, > and ". The apostrophe is deliberately not escaped:
// every attribute this module emits is double-quoted, so an apostrophe cannot
// leave one, and the entity forms that would encode it (&#39;, &#x27;) contain a
// "#", which is the character that separates a wiki link's note name from its
// heading. Escaping it would corrupt link targets to buy nothing.

const ESCAPES = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" };

// Longest first, and &amp; last: the inverse of a single escaping pass, in the
// only order that cannot turn "&amp;lt;" back into "<".
const UNESCAPES = [
  ["&lt;", "<"],
  ["&gt;", ">"],
  ["&quot;", '"'],
  ["&amp;", "&"],
];

function escapeHTML(text) {
  return String(text ?? "").replace(/[&<>"]/g, (ch) => ESCAPES[ch]);
}

// unescapeHTML exists for one purpose: a wiki link's target has to be looked up
// in a set of note names that were never escaped. Its result is only ever used
// as a lookup key and never reaches the output.
function unescapeHTML(text) {
  let out = text;
  for (const [entity, ch] of UNESCAPES) out = out.split(entity).join(ch);
  return out;
}

const FENCE = /^(```|~~~)/;
const BULLET = /^[-*]\s+/;
const HEADING = /^(#{1,4})\s+(.*)$/;
// Matches internal/board's own linkRe, so the panel renders a link exactly where
// the server extracted one. A narrower pattern here would leave "[[note|alias]]"
// as literal text on a card whose backlinks the server had already resolved.
const WIKILINK = /\[\[([^[\]\r\n]+?)\]\]/g;
const CODE_SPAN = /`([^`]+)`/g;
const BOLD = /\*\*([^*]+)\*\*/g;

// linkParts splits a wiki link's inner text the way internal/board's
// trimLinkTarget does: everything up to the first "#" or "|" is the note name.
// What follows a "|" is the alias, which is what the reader should see.
function linkParts(inner) {
  const cut = inner.search(/[#|]/);
  const target = (cut < 0 ? inner : inner.slice(0, cut)).trim();
  const pipe = inner.indexOf("|");
  const label = pipe < 0 ? inner.trim() : inner.slice(pipe + 1).trim();
  return { target, label: label === "" ? target : label };
}

function inline(text, knownCards) {
  // Code spans are lifted out before anything else runs. Order alone would not
  // do it: a wiki link inside backticks would still be matched inside the
  // <code> a code-span pass had just produced, and a backtick inside a link's
  // alias would let a later code-span step across an <a> that was already
  // emitted and close it in the wrong place. Lifting them out first gives code
  // spans the precedence markdown gives them and keeps every construct built
  // from text that no other construct has touched.
  //
  // The placeholder is safe by construction rather than by hope: `text` has
  // already been through escapeHTML, so it holds no "<" at all, and "<0>"
  // cannot occur in it. Nothing emitted below matches <digits> either.
  const spans = [];
  const lifted = text.replace(CODE_SPAN, (_, code) => `<${spans.push(code) - 1}>`);

  return lifted
    .replace(WIKILINK, (whole, inner) => {
      const { target, label } = linkParts(inner);
      if (target === "" || !knownCards.has(unescapeHTML(target))) {
        // A link to a card that does not exist is shown as a link that does not
        // work: no data-link attribute, so the panel's click delegation never
        // sees it, and a class the stylesheet can mark as broken. A span, not a
        // control: there is nothing here to activate.
        return `<span class="wikilink wikilink-missing">${label === "" ? whole : label}</span>`;
      }
      // A button rather than an href-less <a>. An <a> with no href is not
      // focusable, is not in the tab order and is not announced as a link, so
      // it would look like a link and work only for a mouse. Following a link
      // here opens a panel rather than navigating, which is what a button is.
      return `<button type="button" class="wikilink" data-link="${target}">${label}</button>`;
    })
    .replace(BOLD, "<strong>$1</strong>")
    .replace(/<(\d+)>/g, (_, index) => `<code>${spans[Number(index)]}</code>`);
}

// renderMarkdown turns a card or documentation body into an HTML string.
//
// knownCards is a Set of note names — a card's file name without its directory
// and without ".md" — and decides only whether a wiki link is clickable. Callers
// with nothing to link against pass an empty Set; the signature is shared with
// the documentation section, which does exactly that.
export function renderMarkdown(text, knownCards) {
  const known = knownCards ?? new Set();
  const lines = escapeHTML(text).split("\n");
  const out = [];
  let inCode = false;
  let inList = false;
  let atCodeStart = false;

  // Closed onto the last line for the same reason, so a block does not end with
  // a blank line either.
  const closeCode = () => {
    out[out.length - 1] += "</code></pre>";
  };

  const closeList = () => {
    if (inList) {
      out.push("</ul>");
      inList = false;
    }
  };

  for (const raw of lines) {
    const line = raw.endsWith("\r") ? raw.slice(0, -1) : raw;
    if (FENCE.test(line)) {
      if (inCode) {
        closeCode();
      } else {
        closeList();
        out.push("<pre><code>");
        atCodeStart = true;
      }
      inCode = !inCode;
      continue;
    }
    if (inCode) {
      // Appended to the open <pre><code> rather than pushed as its own entry:
      // the join below puts a newline between entries, and HTML only ignores a
      // newline immediately after <pre>, never after <code>. Pushed, every
      // fenced block would open with a blank line.
      if (atCodeStart) {
        out[out.length - 1] += line;
        atCodeStart = false;
      } else {
        out.push(line);
      }
      continue;
    }
    if (BULLET.test(line)) {
      if (!inList) {
        out.push("<ul>");
        inList = true;
      }
      out.push(`<li>${inline(line.replace(BULLET, ""), known)}</li>`);
      continue;
    }
    closeList();
    const heading = line.match(HEADING);
    if (heading) {
      // A card's own "# title" is the panel's heading already, so the body's
      // headings start one level down and never collide with it.
      const level = heading[1].length + 1;
      out.push(`<h${level}>${inline(heading[2], known)}</h${level}>`);
      continue;
    }
    out.push(line.trim() === "" ? "" : `<p>${inline(line, known)}</p>`);
  }
  closeList();
  // An unterminated fence closes here rather than leaking an open <pre> into
  // whatever the caller appends after this string.
  if (inCode) closeCode();
  return out.join("\n");
}

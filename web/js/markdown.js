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
// A table row is a line fenced by vertical bars on both sides. Requiring both
// is what keeps prose out: "the column reads | a | b | and that is the bug" is a
// sentence, and a line that merely contains a bar is a line.
const TABLE_ROW = /^\s*\|(.*)\|\s*$/;

// The separator is the second end of the table, and a table is only a table
// when both ends are there. A header alone is a paragraph that happens to have
// bars in it; this line under it is what says otherwise.
const TABLE_SEPARATOR = /^\s*\|[\s:|-]*-[\s:|-]*\|\s*$/;

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
// cellsOf splits one table row into its cells. The fencing bars are dropped and
// everything between the inner ones is a cell, empty cells included: a blank
// cell is a value a table legitimately holds.
function cellsOf(line) {
  const inner = TABLE_ROW.exec(line);
  if (!inner) return null;
  return inner[1].split("|").map((cell) => cell.trim());
}

// alignmentsOf reads the colons in the separator. ":---" is left, "---:" is
// right, ":---:" is centre, and a bare "---" leaves the cell to the stylesheet.
function alignmentsOf(line) {
  return (cellsOf(line) ?? []).map((cell) => {
    const left = cell.startsWith(":");
    const right = cell.endsWith(":");
    if (left && right) return "md-center";
    if (right) return "md-right";
    if (left) return "md-left";
    return "";
  });
}

// renderTable turns a header, a separator and the rows under them into a table.
// The wrapper is not decoration: a table wider than its column has to scroll
// inside its own box, because a page that scrolls sideways moves every other
// column with it, and these columns are narrow.
function renderTable(header, alignments, rows, known) {
  const cell = (tag, text, index) => {
    const className = alignments[index] ? ` class="${alignments[index]}"` : "";
    return `<${tag}${className}>${inline(text, known)}</${tag}>`;
  };
  const head = `<tr>${header.map((text, i) => cell("th", text, i)).join("")}</tr>`;
  const body = rows
    .map((cells) => `<tr>${cells.map((text, i) => cell("td", text, i)).join("")}</tr>`)
    .join("");
  return `<div class="md-table"><table><thead>${head}</thead><tbody>${body}</tbody></table></div>`;
}

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

  const clean = (raw) => (raw.endsWith("\r") ? raw.slice(0, -1) : raw);

  for (let i = 0; i < lines.length; i += 1) {
    const line = clean(lines[i]);
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
    // A table is recognised by its first TWO lines, never by one: the header
    // and the separator under it. Finding a header alone is not finding a
    // table, and treating it as one would turn a sentence with bars in it into
    // a grid.
    const header = cellsOf(line);
    const next = i + 1 < lines.length ? clean(lines[i + 1]) : "";
    if (header && TABLE_SEPARATOR.test(next) && (cellsOf(next) ?? []).length === header.length) {
      closeList();
      const alignments = alignmentsOf(next);
      const rows = [];
      let j = i + 2;
      // The table ends where its rows end — the second end again. Everything
      // after that line belongs to whatever comes next and must not be eaten.
      while (j < lines.length) {
        const cells = cellsOf(clean(lines[j]));
        if (!cells) break;
        rows.push(cells);
        j += 1;
      }
      out.push(renderTable(header, alignments, rows, known));
      i = j - 1;
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

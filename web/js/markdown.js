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

// A numbered item, and the whole difficulty is telling one from a line that
// merely opens with digits. The space after the dot is the entire rule, and it
// was not invented here: grepping the operator's own board for lines starting
// with digits and a dot found 57, and exactly one of them is not a list item —
// "07.09 в 12:42 Дарья перевела тикет". What separates it from the rest is that
// nothing follows its dot but another digit. Requiring the space keeps dates and
// version numbers as the prose they are.
//
// The captured number is the list's starting value: a card that numbers a stub
// "0." means it, and renumbering from 1 would contradict text that refers to
// "пункт 0".
const ORDERED = /^(\d+)\.\s+/;

// A thematic break. It is not rendered as one — that is a construct this
// renderer does not have — but it does end the paragraph above it, which is
// what it did before paragraphs were joined at all. Without this, "---" glues
// itself to the line below and reads as a dash mid-sentence.
const BREAK = /^\s*([-*_])\1{2,}\s*$/;
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

// Exported with linkParts below for web/js/terminallinks.js, which finds the
// links a session prints into a live terminal: one rule for what a link is,
// wherever it is read.
export const WIKILINK = /\[\[([^[\]\r\n]+?)\]\]/g;
const CODE_SPAN = /`([^`]+)`/g;
const BOLD = /\*\*([^*]+)\*\*/g;

// linkParts splits a wiki link's inner text the way internal/board's
// trimLinkTarget does: everything up to the first "#" or "|" is the note name.
// What follows a "|" is the alias, which is what the reader should see.
export function linkParts(inner) {
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
  // Which list is open, if any: "ul", "ol", or null. Two kinds mean one has to
  // close the other — a bulleted line inside a numbered list is a new list, not
  // another item.
  let listTag = null;
  let atCodeStart = false;
  // Lines of the paragraph being accumulated. A paragraph in a card is wrapped
  // across several source lines, and in markdown a single newline continues it:
  // rendering each line as its own <p> broke one thought into four blocks with
  // a gap between each. A blank line, and every other construct, ends it.
  let para = [];
  // And the lines of the list item being accumulated, for the same reason: a
  // card wraps its items exactly as it wraps its prose, and the continuation
  // line carries an indent. Buffered rather than appended already-rendered,
  // so bold that opens on one line and closes on the next is one bold run.
  let item = null;

  // Closed onto the last line for the same reason, so a block does not end with
  // a blank line either.
  const closeCode = () => {
    out[out.length - 1] += "</code></pre>";
  };

  const flushItem = () => {
    if (item === null) return;
    out.push(`<li>${inline(item.join(" "), known)}</li>`);
    item = null;
  };

  const closeList = () => {
    flushItem();
    if (listTag) {
      out.push(`</${listTag}>`);
      listTag = null;
    }
  };

  // Joined with a space, not with nothing: the newline between two wrapped
  // lines stood for the space the author did not type, and dropping it runs
  // "релизы" into "помечены".
  const flushPara = () => {
    if (para.length === 0) return;
    out.push(`<p>${inline(para.join(" "), known)}</p>`);
    para = [];
  };

  // Every construct below begins by ending whatever was open. Kept together so
  // that adding a construct cannot forget one of the two.
  const closeBlocks = () => {
    flushPara();
    closeList();
  };

  // Opens a bulleted list, closing a numbered one first if that is what was
  // open. A numbered list opens inline below instead: it needs its own start
  // value, and hiding that behind a shared helper would only hide it.
  const openBullets = () => {
    if (listTag === "ul") {
      flushItem();
      return;
    }
    closeList();
    out.push("<ul>");
    listTag = "ul";
  };

  const clean = (raw) => (raw.endsWith("\r") ? raw.slice(0, -1) : raw);

  for (let i = 0; i < lines.length; i += 1) {
    const line = clean(lines[i]);
    if (FENCE.test(line)) {
      if (inCode) {
        closeCode();
      } else {
        closeBlocks();
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
    // An indented line inside an open list continues the item above it. The
    // indent is the whole rule and it is the item's second end: without one
    // there is nothing to tell a wrapped item from the paragraph that follows
    // the list, and swallowing that paragraph would be the same defect
    // reversed.
    if (item !== null && /^\s+\S/.test(line)) {
      const trimmed = line.trim();
      // …unless the indented line is itself an item. Nested lists are out of
      // scope, and leaving a nested item where it was is the lesser of the two
      // wrongs available: absorbed into the parent's text it reads as prose —
      // "parent - child - child2" — with its markers passing for dashes in a
      // sentence, and nothing on screen says a list was flattened.
      if (!BULLET.test(trimmed) && !ORDERED.test(trimmed)) {
        item.push(trimmed);
        continue;
      }
    }
    if (BULLET.test(line)) {
      flushPara();
      openBullets();
      item = [line.replace(BULLET, "")];
      continue;
    }
    const ordered = line.match(ORDERED);
    if (ordered) {
      flushPara();
      if (listTag === "ol") {
        flushItem();
      } else {
        closeList();
        // start is written only where it says something: a list beginning at 1
        // begins where a reader already assumes it does.
        // A number too large to be exact is written as an exponent, and
        // start="1e+23" is not an integer — the browser drops it. An attribute
        // that cannot mean anything is not written.
        const first = Number(ordered[1]);
        const explicit = Number.isSafeInteger(first) && first !== 1;
        out.push(explicit ? `<ol start="${first}">` : "<ol>");
        listTag = "ol";
      }
      item = [line.replace(ORDERED, "")];
      continue;
    }
    // A table is recognised by its first TWO lines, never by one: the header
    // and the separator under it. Finding a header alone is not finding a
    // table, and treating it as one would turn a sentence with bars in it into
    // a grid.
    const header = cellsOf(line);
    const next = i + 1 < lines.length ? clean(lines[i + 1]) : "";
    if (header && TABLE_SEPARATOR.test(next) && (cellsOf(next) ?? []).length === header.length) {
      closeBlocks();
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

    const heading = line.match(HEADING);
    if (heading) {
      // A card's own "# title" is the panel's heading already, so the body's
      // headings start one level down and never collide with it.
      closeBlocks();
      const level = heading[1].length + 1;
      out.push(`<h${level}>${inline(heading[2], known)}</h${level}>`);
      continue;
    }
    if (BREAK.test(line)) {
      closeBlocks();
      out.push(`<p>${inline(line.trim(), known)}</p>`);
      continue;
    }
    if (line.trim() === "") {
      closeBlocks();
      // One separator per run of blank lines, and none before the first block:
      // repeated empty entries would only pad the joined output.
      if (out.length > 0 && out[out.length - 1] !== "") out.push("");
      continue;
    }
    closeList();
    para.push(line);
  }
  closeBlocks();
  // An unterminated fence closes here rather than leaking an open <pre> into
  // whatever the caller appends after this string.
  if (inCode) closeCode();
  return out.join("\n");
}

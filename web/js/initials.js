// web/js/initials.js
// Two letters standing in for a session, for the folded sessions column.
//
// The folded column is a strip a few dozen pixels wide. A session's name does
// not fit in it and never will, so the strip carries a mark instead — and the
// mark's whole job is that a person can tell one session from another without
// unfolding anything.
//
// Which is harder than "take the first two letters", because a fleet's
// sessions are not named to be told apart that way. This machine's own fleet
// runs "fleetdeck: обновление из выпуска", "fleetdeck: колонка сессий — вид и
// подъём", "fleetdeck: разведка по разным агентам": the first nine characters
// are the project every one of them belongs to, and a mark taken off the front
// of the name reads "FL" on all of them. So:
//
//   • the prefix before the last ":" is dropped — it names the project, and
//     what follows is what makes this session itself;
//   • the mark is the initials of the first two words of what is left, the
//     ordinary convention for standing in for a name;
//   • and a mark that would be shared is moved along the name until it is not
//     (sessionMarks below), because "you can tell them apart" is the whole
//     requirement and two identical squares fail it outright.
//
// Nothing here is invented about a session. The mark is derived from the name
// the operator or the daemon gave it, and the full name is on the element's
// title for whoever wants the rest.

// A word is a run of letters or digits. Everything else — spaces, colons,
// dashes, the em-dashes these names are full of — separates them.
const WORDS = /[\p{L}\p{N}]+/gu;

// Only words that start with a letter are worth taking an initial from. A
// ticket number in the middle of a name ("BS-27572 fix") is a word by the rule
// above, and its first digit says nothing about which session this is — "B2"
// where "BF" was meant. Digits are kept as material for a single-word name,
// where there is nothing else to use.
const STARTS_WITH_LETTER = /^\p{L}/u;

function words(name) {
  const all = name.match(WORDS) ?? [];
  const lettered = all.filter((word) => STARTS_WITH_LETTER.test(word));
  return lettered.length > 0 ? lettered : all;
}

// displayName is the same fallback chain the unfolded row draws its name with:
// the operator's own label first, the daemon's name next. Kept identical on
// purpose — a mark built from a different name than the row shows would be a
// second answer to "what is this session called".
function displayName(session) {
  return session?.label || session?.name || "";
}

// distinguishing drops the project prefix. Only the part before the LAST colon
// goes: a name can carry more than one, and it is the deepest one that leaves
// the most specific remainder.
//
// A prefix that leaves nothing behind is not a prefix worth honouring — the
// whole name is then all there is, and it is used as it stands.
function distinguishing(name) {
  const cut = name.lastIndexOf(":");
  if (cut === -1) return name;
  const tail = name.slice(cut + 1);
  return tail.match(WORDS) ? tail : name;
}

// letters returns the words of a name as an array of their first characters,
// upper-cased: the raw material every mark below is cut from.
function letters(name) {
  return words(distinguishing(name)).map((word) => [...word][0].toUpperCase());
}

// markFrom builds one mark from a name and its nth alternative.
//
// alt 0 is the plain mark — the first two words' initials, or the first two
// characters of a single word. Each alt after it moves the SECOND letter one
// word further along, which is how two sessions whose names begin alike come
// out different. The first letter never moves: it is what makes the mark
// recognisable at all.
//
// An empty string means this name has no alternative that far along, and the
// caller falls back to the short id.
function markFrom(name, alt) {
  const parts = words(distinguishing(name));
  if (parts.length === 0) return "";
  const first = [...parts[0]][0].toUpperCase();

  if (parts.length === 1) {
    if (alt > 0) return "";
    // One word: its own second character, since there is no second word to
    // take an initial from. A single-character name stays a single character
    // rather than being padded with something that is not in it.
    const rest = [...parts[0]][1];
    return rest ? first + rest.toUpperCase() : first;
  }

  const second = letters(name)[alt + 1];
  return second ? first + second : "";
}

// fromShort is the last resort: the short id's own first two characters. It is
// never empty for a real session, and it is the id the operator would type in a
// terminal anyway.
function fromShort(short) {
  const id = String(short ?? "");
  return id ? id.slice(0, 2).toUpperCase() : "??";
}

// sessionMark is one session's mark, ignoring everyone else's.
//
// Use it when there is nothing to clash with — a test, a single row. The
// column itself uses sessionMarks below, which is this plus the one thing a
// mark on a strip of fifteen actually needs.
export function sessionMark(session) {
  return markFrom(displayName(session), 0) || fromShort(session?.short);
}

// sessionMarks gives every session in a list a mark no other session in it
// has, returned as short id -> mark.
//
// The clash resolution is deliberately order-independent. This column reorders
// itself constantly — a session that starts waiting floats to the top — and a
// mark that changed because its session moved would be worse than a clash: the
// square a person had learned to recognise would silently become another
// session's. So which of two clashing sessions keeps the plain mark is decided
// by their short ids, which never change, and not by where they happen to sit
// today.
export function sessionMarks(sessions) {
  const list = [...(sessions ?? [])];
  const byShort = new Map();

  // Grouped by what each would be called if it were alone. A group of one is
  // already settled; a group of more has to be walked.
  const groups = new Map();
  for (const session of list) {
    const plain = sessionMark(session);
    if (!groups.has(plain)) groups.set(plain, []);
    groups.get(plain).push(session);
  }

  const taken = new Set();
  for (const [plain, group] of groups) {
    if (group.length === 1) {
      byShort.set(group[0].short, plain);
      taken.add(plain);
    }
  }

  for (const [plain, group] of groups) {
    if (group.length === 1) continue;
    // By short id, so the answer does not depend on today's order.
    const ordered = [...group].sort((a, b) => String(a.short).localeCompare(String(b.short)));
    for (const session of ordered) {
      const name = displayName(session);
      let mark = "";
      for (let alt = 0; alt < 8 && !mark; alt++) {
        const candidate = markFrom(name, alt);
        if (candidate && !taken.has(candidate)) mark = candidate;
      }
      if (!mark) {
        // Nothing in the name settles it, so the short id does. Taken as it
        // is, even if another mark already reads the same: two sessions whose
        // names AND short ids both begin alike are as near identical as this
        // panel can see, and inventing a third thing to tell them apart would
        // be inventing something about a session. The title has their names.
        mark = fromShort(session.short);
      }
      byShort.set(session.short, mark);
      taken.add(mark);
    }
  }

  // Returned in the order they were given, so a caller rendering the list can
  // iterate either this or its own list and get the same sequence.
  const ordered = new Map();
  for (const session of list) ordered.set(session.short, byShort.get(session.short));
  return ordered;
}

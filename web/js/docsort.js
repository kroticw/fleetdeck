// The order of the docs list: grouped by the card a document belongs to, or one
// list by date, newest or oldest first. created is the server's (the file's
// birth time on macOS); a document without one sorts as the oldest.

const byDate = (newestFirst) => (a, b) => {
  const x = a.created ?? "";
  const y = b.created ?? "";
  // Equal dates keep the server's order (the sort is stable).
  if (x === y) return 0;
  return (x < y ? -1 : 1) * (newestFirst ? -1 : 1);
};

// docGroups orders docs. cardOf(doc) is the card a document belongs to, or
// null; by is "task" or "date". By task each card is a group, ordered by its
// newest document, and the documents of no card come last; by date there is
// one group with no card.
export function docGroups(docs, cardOf, { by = "task", newestFirst = true } = {}) {
  const order = byDate(newestFirst);
  if (by === "date") return [{ items: [...docs].sort(order) }];

  const groups = new Map();
  const loose = [];
  for (const doc of docs) {
    const card = cardOf(doc);
    if (!card) {
      loose.push(doc);
      continue;
    }
    if (!groups.has(card.path)) groups.set(card.path, { card, items: [] });
    groups.get(card.path).items.push(doc);
  }
  const out = [...groups.values()];
  for (const group of out) group.items.sort(order);
  // A group sorts by the document it shows first: its newest, or its oldest.
  out.sort((a, b) => order(a.items[0], b.items[0]));
  if (loose.length > 0) out.push({ card: null, items: loose.sort(order) });
  return out;
}

// shortDate is a document's day and month as the list shows it, "05.11".
export function shortDate(iso) {
  if (!iso) return "";
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return "";
  const pad = (n) => String(n).padStart(2, "0");
  return `${pad(when.getDate())}.${pad(when.getMonth() + 1)}`;
}

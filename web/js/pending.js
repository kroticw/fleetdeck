// A message drawn the moment it is sent, before the transcript has it.
//
// What a person waits for today is not the network. Measured from the keystroke
// to the text appearing on screen, the request itself takes about six
// milliseconds and the rest is waiting for the message to come back out of the
// session's transcript through a poll — a second in the common case, ten in the
// worst one seen. The panel had everything it needed to draw the message at
// once and drew nothing, so the box emptied and the thread stayed as it was,
// which reads as "it did not send".
//
// So the pane draws it immediately and this module keeps the promise honest.
// Three things go wrong with drawing ahead of the server, and each is answered
// here rather than in the two panes that would answer it differently:
//
//   • The send fails. Then the message never existed, and leaving it on screen
//     would tell a person their words are in the session when they are in
//     nobody's hands. `drop` removes it; the pane puts the text back in the box,
//     which is what both panes already do with text that failed to send.
//
//   • The real one arrives and the drawn one is still there. `merge` drops a
//     pending message when the incoming steps already carry it. It COUNTS
//     rather than remembering a set of texts, for the same reason the digest's
//     own de-duplication does: someone who typed "да" twice said it twice, and
//     a set would silently eat the second.
//
//   • It jumps when it is replaced. A pending message is by definition the
//     newest thing in the thread, so it is appended last and the real one lands
//     in the same place. Nothing moves at the swap.

// Text is compared trimmed, because that is what is sent: both panes trim
// before handing the message to the daemon, so the transcript holds the trimmed
// form and an untrimmed comparison would never match — leaving the drawn copy
// on screen beside the real one, which is the one outcome worse than the wait
// this exists to remove.
const key = (text) => String(text ?? "").trim();

/**
 * createPending keeps the messages a pane has drawn but the server has not
 * confirmed.
 *
 * It holds no DOM and no timers: a pane calls `add` when it sends, `drop` if
 * the send failed, and passes every list of steps through `merge` before
 * drawing. Which is why it can be shared — the two panes disagreeing about any
 * of this is exactly the class of defect this project keeps finding.
 */
export function createPending() {
  // In send order, which is also the order they are shown in.
  let waiting = [];
  let nextId = 1;

  return {
    // add returns a handle rather than the text, so a pane can drop exactly the
    // message it sent even when the same words were sent twice.
    add(text) {
      const id = nextId;
      nextId += 1;
      waiting.push({ id, text: String(text ?? ""), at: new Date().toISOString() });
      return id;
    },

    drop(id) {
      waiting = waiting.filter((m) => m.id !== id);
    },

    // Whether anything is waiting, for a pane that wants to know if a redraw is
    // worth doing at all.
    get size() {
      return waiting.length;
    },

    /**
     * merge returns the steps to draw: the server's own, followed by whatever
     * has been sent and has not come back yet.
     *
     * The incoming steps are never modified or reordered. A pending message
     * that the server now carries is forgotten here as a side effect — merge is
     * the only place that can know it has arrived, and making the caller ask
     * separately would be one more thing for two panes to do differently.
     */
    merge(steps) {
      const incoming = Array.isArray(steps) ? steps : [];

      // How many times each text appears among the steps the server sent, so a
      // repeat is matched once per copy rather than all at once.
      const available = new Map();
      for (const step of incoming) {
        if (step?.role !== "user") continue;
        const k = key(step.text);
        available.set(k, (available.get(k) ?? 0) + 1);
      }

      const stillWaiting = [];
      for (const message of waiting) {
        const k = key(message.text);
        const left = available.get(k) ?? 0;
        if (left > 0) {
          available.set(k, left - 1);
          continue; // it is on screen as the real thing now
        }
        stillWaiting.push(message);
      }
      waiting = stillWaiting;

      if (waiting.length === 0) return incoming;
      return [...incoming, ...waiting.map((m) => ({ role: "user", text: m.text, at: m.at, pending: true }))];
    },
  };
}

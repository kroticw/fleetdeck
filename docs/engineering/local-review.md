# Local review

The panel can review the branch a card's session works on: the operator reads the diff, leaves line comments, and sends them to the session as a round. Everything stays on the operator's machine. The only thing that leaves the panel is one line sent into the session, naming where the comments are and where to answer.

This note is for whoever changes the review. The user-facing description is in [getting started](../en/getting-started.md#reviewing-a-sessions-branch).

## The flow

1. The open card has **review** in its head (`web/js/card.js`). It is offered when the card has a number and a session.
2. The review opens as an overlay over the board, like the card and the reader. The diff is on top. The card's own session is docked under it (`createCardDock`), so the operator can read the agent's answers and talk to it without leaving the diff.
3. A comment is added on a line, or on a range of lines in one file and side. A new comment is a **draft** (`round` 0). A draft can be edited and deleted.
4. **send to the session** freezes every draft into a new round. The drafts get the round's number, the round records the branch head and where each comment stands at that head, and the session is told about it once.
5. The agent answers in `replies.md`. The answers appear under the comments they answer.
6. A sent comment can no longer be edited or deleted. It can be resolved, reopened and replied to.
7. A round that did not reach the session, or that nobody has answered, can be sent again from the review. One press sends one message. Nothing is retried on its own.

**card** and Escape go back to the card the review was opened from. An Escape inside a form or the docked session belongs to that form or session and does not leave the review.

## Storage

A card's review lives on the board, in `reviews/<card number>/`. The number survives a card's renames. The board template ignores `reviews/` in git (`plugin/templates/board/.gitignore`), so review notes never enter the board's history.

- **`comments.json` is the panel's.** Only the panel writes it (`internal/review/store.go`).
  - Top-level keys: `version`, `card`, `rev`, `nextId`, `comments` and `rounds`.
  - A comment carries `id`, `round` (0 for a draft), `replyTo`, `anchor`, `body`, `created` and `resolved`.
  - A round carries `n`, `sent`, `head`, `positions` (comment id to position) and `delivery`. An empty `delivery` means the message was delivered.
- **`replies.md` is the agent's.** The panel never writes it. Each answer is a section:

  ```markdown
  ## c3
  status: fixed
  commit: 4e1d0aa

  What was done, or why not.
  ```

  - `status` is `fixed`, `declined` or `question`, and `commit` is optional.
  - An answer in a later round is a new section below the earlier ones.
  - The board's `scripts/validate_review.py` checks this format and that every id it answers exists in `comments.json`.
  - The panel reads the file as possibly half written: a last section with no trailing newline is shown as partial.

**Concurrent writes.** Every write to `comments.json` names the revision it was made against. `review.Update` re-reads the file and refuses a write made against another revision (`ErrStale`, answered as 409). Otherwise it applies the change, bumps `rev` and writes through a temporary file and a rename. Two tabs editing the same review therefore cannot overwrite each other. Writers inside one panel process are also serialised per directory.

## Where the diff is read

`cmd/fleetdeck/review.go` (`reviewWorkdir`) decides which working tree to read, in this order:

1. The card's `worktree` field. It must be an absolute path. The agent writes it when it enters a working tree (the card-keeping skill), and the panel never writes it.
2. Claude Code's job record for the session: `worktreePath`, the working tree the session entered after it started. Its `cwd` never follows it ([the job store](../protocol/claude-jobs-store.md)).
3. The same record's `cwd`.

Otherwise the review is refused with a message naming the missing `worktree` field. The card's `repo` field is never used: it names a repository, and one machine holds several checkouts of it.

**Git** (`internal/review/git.go`). Every call is read-only and has a timeout. The git environment is pinned:

- no pager;
- no prompt;
- no optional locks;
- no external diff or textconv;
- quoted paths off;
- the C locale.

Commits and paths that come from the page are passed after `--end-of-options`.

**The base.**

- The default branch comes from `origin/HEAD`. When `origin/HEAD` is not set, the base is whichever one of `origin/main` and `origin/master` exists; with neither or both, the review is refused.
- The base is the merge base of `HEAD` with that remote branch. When a local branch of the same name exists, its merge base is also considered, and the later of the two wins.

**Uncommitted changes** (`git status`) are listed by path, with a note that the review works on committed code. They take no comments: a tree the agent is still editing has nothing stable to anchor to.

**Size limits.**

- A file over 3000 changed lines is folded: its path is shown, its lines are not.
- Expanded context comes 20 lines at a time, at most 500 lines per request.

## Anchors and positions

A comment is anchored to the code it was left on, not to a line number (`internal/review/trace.go`). The anchor holds:

- the commit;
- the path;
- the side (`new` or `old`);
- the line range;
- the exact text of those lines;
- the two lines before and after them.

When the branch moves, `review.Place` traces each anchor to the new head:

1. It reads the diff from the anchor's commit to the head, with no context lines and ignoring whitespace changes. One diff is computed per commit pair and shared by all anchors on it.
2. It walks the hunks that touch the anchor's range.

The result is one of five states:

- `in_place` — nothing touched the range, which may have shifted;
- `changed` — a hunk rewrote part of it;
- `deleted` — the range is gone, and the position says after which line;
- `file_gone` — the file was deleted;
- `unavailable` — the diff itself could not be computed, for example because the commit is gone.

`Freeze` is the one place a derived value is stored: a round keeps the positions computed at its head. They are a hint. **The anchor's text is the truth**, and the card-keeping skill tells the agent the same.

Not implemented: finding a moved block by its text, a side-by-side view, syntax highlighting, and choosing the base by hand.

## Routes

All routes are under the fleet the page names. Each first checks that the card is on that fleet's board and can be read:

- 403: the card is outside the board;
- 404: no such card;
- 422: the card cannot be read or has no number.

A working tree git cannot read answers 424.

| Route | What it does |
| --- | --- |
| `GET /api/review?card=` | The whole view: the diff against the base, comments placed on it, rounds, replies, uncommitted paths |
| `GET /api/review/lines?card=&commit=&path=&from=&to=` | Unchanged lines of one file at one commit, for expanding context around a hunk |
| `POST /api/review/comments` | Add a draft |
| `PATCH /api/review/comments` | Edit a draft's text |
| `DELETE /api/review/comments?card=&rev=&id=` | Delete a draft |
| `POST /api/review/resolve` | Resolve or reopen a comment |
| `POST /api/review/send` | Freeze the drafts into a round and tell the session, in the page's `lang` |
| `POST /api/review/notify` | Tell the session about an existing round once more, in the page's `lang` |

Writes answer the file as it now stands. A write against a stale revision answers 409, and a draft-only operation on a sent comment answers 400. `send` and `notify` run on a context of their own with a two-minute bound, so a page closed mid-request does not cut a round or a message in half.

## The page

`web/js/review.js` builds every piece of diff, comment and reply text with `textContent`, never `innerHTML`. All of it is text an agent wrote, on a page that can type into a live session.

- **Code size.** The diff's code has its own A−/px/A+ controls. They use the same steps and bounds as the terminal, are stored under `fleetdeck-review-font` and default to 16 px.
- **Long diffs.** Each file is a `content-visibility: auto` box, so a file off screen is neither laid out nor painted. The page keeps each file's last drawn height as its intrinsic size, so the files below do not move when one is redrawn.
- **Expanding context.** Context around a hunk is expanded 20 lines at a time or the whole gap at once. Expanded lines can take comments like any other line.

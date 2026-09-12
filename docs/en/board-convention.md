# Board convention

The fleet board is a directory of markdown files, each one a "card" for one task, with YAML frontmatter at the top and free-form markdown in the body. Cards live in a git repository, and the panel commits its own edits to that repository. This page describes the file format, who is allowed to write what, and two rough edges that are worth knowing about before you rely on the board: a gap between what two different pieces of code enforce, and a small, accepted race in how the panel writes.

## An example card

```markdown
---
zone: unplanned
stage: active
progress: 20
session: a1b2c3
repo: fleetdeck
created: 2026-09-10
---

# Add a health check endpoint

## Context

The load balancer needs a lightweight endpoint to confirm the service is up.
See issue #42 for the original request.

## Log

- 2026-09-10: started, reading the existing router setup.
```

## Frontmatter fields

The fields below and their allowed values are the schema enforced by the board's validator script, `plugin/templates/board/scripts/validate_cards.py`. The Go code that the panel uses to read and write cards enforces a smaller part of this schema; see "Two different levels of strictness" further down for exactly where the two disagree.

| Field | Type | Required | Allowed values |
| --- | --- | --- | --- |
| `id` | string | yes | the card's permanent number, `T-` and at least three digits (`T-018`) |
| `zone` | string | yes | one of `urgent`, `unplanned`, `planned`, `niceToHave` |
| `stage` | string | yes | one of `new`, `active`, `review`, `done`, `blocked` |
| `progress` | integer | yes | one of `0`, `10`, `20`, `40`, `60`, `80`, `100` |
| `created` | string | yes | a date in `YYYY-MM-DD` format |
| `session` | string | required once `stage` is `active`, `review`, `done`, or `blocked` | 6 to 12 hexadecimal characters |
| `repo` | string | no | any string; not otherwise validated |

The validator also tolerates four fields that Obsidian's own property panel can add on its own — `tags`, `aliases`, `cssclasses`, `cssclass` — without treating them as unknown. Any other field name is rejected.

### The card's number

`id` is the one identifier a person can read off the panel, say out loud, and hand back to a script to reach the same file — which is what a board's `scripts/card_path.py` resolves. It is permanent: unlike `session`, which is the short id of whichever session is working the card and changes with every run, a card's number is assigned once, when the card is created, and is what the card is referred to by afterwards.

The panel shows it wherever a card appears — on the card in its column, in the open card beside `stage` and `progress`, and on the button that opens a session's card in the session list — as a monospaced chip, which is what keeps it from being mistaken for the session's short id sitting next to it.

A number is handed out in exactly two places, and both claim it the same way — an empty marker file `.ids/T-NNN` created with `O_EXCL`: the board's `scripts/new_card.py` and the panel's new-card button. One claim rather than two independent counters: cards are started in parallel by the operator, by the orchestrator and by the sessions themselves, and "read the maximum, add one" gives one number to two cards when two of them start at once. The marker is never removed — not when a card is renamed, not when it moves to the archive — so a number can neither be released nor reused.

A card with no `id` is one created by hand past both paths, or inherited from a board older than the numbers: the validator rejects it, while the panel reads and shows it anyway, saying in words that the card has no number rather than leaving a blank where one would be. Cards created before the scheme existed are given their numbers by `scripts/backfill_ids.py`. A card whose frontmatter does not parse says nothing about its number at all — nothing is known about it.

## Cross-field rules

Two rules connect `stage`, `progress`, and `session` to each other, and both are enforced on both the panel's writing code and the validator script:

- Setting `stage` to `done` requires `progress` to already be, or to also be set to, `100`. The panel's write path refuses with `cannot set stage to done while progress is %d: the board requires progress 100 at stage done`; conversely, setting `progress` to anything but `100` while `stage` is `done` is refused with `cannot set progress to %s while stage is done: the board requires progress 100 at stage done`. The validator script enforces the same rule and reports it (in Russian, its only output language) as `при stage done значение progress обязано быть 100`.
- Setting `stage` to `active`, `review`, `done`, or `blocked` requires `session` to be non-empty — the panel refuses with `cannot set stage to %s while session is empty: the board requires a session at stage %s`. The validator script's message explains why: `при stage {stage} поле session обязано быть заполнено: оно единственное связывает карточку с сессией` ("session must be filled in at this stage: it is the only field that connects the card to a session").

## Who writes what

The board splits ownership by who is writing:

- The panel writes exactly two fields: `stage` and `progress`. Its write path accepts no other field name — asking it to write `zone`, `session`, `repo`, or `created` is refused as an unwritable field, reported as `field is not writable: <field>`.
- The body of the card — its heading, its context section, and its log — belongs to the agents working on the task. The panel never edits the body.
- The `session` field is filled in by the orchestrator when it starts a session for a card, not by the panel and not by the agent working the task.
- `repo`, `created`, and `zone` are not written by the panel, and are not documented as belonging to the agent either. In practice they are set once, by whoever creates the card from the template, and left alone.

## How the title and links are extracted

A card's title and its `[[links]]` to other notes are read out of the body, not out of the frontmatter, and the extraction has two deliberate blind spots:

- Before looking for a title or a link, the body is scanned for fenced code blocks (opened with three backticks or three tildes) and everything inside a fence is blanked out first. A `# heading`-looking line or a `[[link]]`-looking string that only appears inside an example code block is not picked up as the card's real title or a real link. This keeps a card's title and links tied to what the card is actually about, rather than to whatever happens to appear in a quoted snippet.
- A fence that is opened but never closed blanks out everything from the opening marker to the end of the file. Anything after an unterminated fence, including a real title or a real link, is silently lost. Close every fence you open.

With the fenced regions blanked out, the title is the first line matching a level-1 markdown heading (`# ...`) anywhere in the body — normally the card template's own heading line. Every `[[...]]` reference is collected as a link; a link with a heading anchor (`[[note#heading]]`) or an alias (`[[note|alias]]`) is recorded by its bare target, `note`, with the anchor or alias stripped. The card's stored body text is always the untouched original file content — the blanking described here is only a working copy used to find the title and the links, and is never written back or shown as the card's body.

## What makes a card unreadable

A card fails to parse in exactly two situations: the file does not start with a frontmatter block at all (no `---` fence at the very top), or the frontmatter is present but is not valid YAML. Either way, the card is not dropped from the board: it is kept, marked with a parse error, and shown as unreadable rather than silently disappearing. A file that cannot even be read from disk — permission denied, or deleted between listing the directory and reading the file — is handled the same way, as a card carrying an error rather than as a reason to abort scanning the rest of the board.

One broken card never hides the others: the rest of the board is scanned and reported normally. A broken card is also excluded, on both sides of a comparison, when the panel checks which cards changed stage since the last time it looked — so a half-written card cannot trigger a false "moved to blocked" or "moved to review" notification.

## Two different levels of strictness

The Go code the panel uses to read cards (`internal/board`) and the Python validator script (`plugin/templates/board/scripts/validate_cards.py`) read the same file format, but they do not check the same things. Passing one is not the same as passing the other.

| Check | Go reader (`ParseCard`) | Go writer (`SetField`) | Validator script |
| --- | --- | --- | --- |
| Unknown frontmatter field | ignored | ignored | rejected |
| `id` | read and shown; absence shown as "no number" | written once, when the card is created, and never again | required; the format, the match with the file name, the absence of duplicates and the claim in the registry are all checked |
| Required fields present | not checked | not checked | required: `zone`, `stage`, `progress`, `created` |
| `zone` is one of the four allowed values | not checked | not checked | enforced |
| `created` matches `YYYY-MM-DD` | not checked | not checked | enforced |
| `session` matches the hex-character pattern | not checked | not checked | enforced |
| `stage` is one of the five allowed values | not checked | enforced | enforced |
| `progress` is one of the seven allowed values | not checked | enforced | enforced |
| `stage: done` requires `progress: 100`, and vice versa | not checked | enforced | enforced |
| A started stage requires a non-empty `session` | not checked | enforced | enforced |

A card the panel starts (`CreateCard`, behind the **+ card** button) is the one thing the Go code writes whole, and it is written to pass the validator script: exactly `zone` — one of the four allowed values, checked — `stage: new`, `progress: 0` and `created`, and a title. A test runs the validator script itself on such a card in each of the four zones.

The Go reader is a lenient reader and a surgical writer of two fields; it was not built to be a schema gate. The validator script is the strict gate, and it has to be run on purpose — by a person or by an agent — since nothing in the Go code calls it. A card that the panel reads without complaint can still fail the validator script, and a card that fails the validator script can still be read and have its `stage` or `progress` field updated by the panel without any warning that something else about it is malformed.

## The card-write race

The panel's write is designed to be surgical: it re-reads the file immediately before writing, changes only the one field it was asked to change, and leaves the rest of the file untouched, byte for byte. The write itself is atomic — a temporary file is written next to the original and then renamed over it — so a reader can never see a half-written card.

This narrows the race with an agent appending a line to the same card's log, but it does not close it. The panel's re-read happens some time before its rename, and that window is not zero. A log line an agent appends inside that window is silently lost, even though the file is never left in a broken state. Measured under artificial pressure, this happens at roughly one loss per two hundred iterations.

No lock was added to close this window, and that is a deliberate choice, not an oversight. The agents that append to a card do so with an ordinary file edit and take no file lock at all. A lock on the panel's side alone would guard against nothing — the agents would not be waiting on it — while looking, in the code, like protection that isn't actually there. Actually closing this race requires changing the convention on both sides: either agents stop editing cards directly and go through the panel instead, or both the panel and the agents honestly take the same lock. Either change affects how the whole fleet works, not just the panel, and neither has been made. The accepted cost is a rare lost log line, in exchange for the board staying a set of ordinary markdown files that anyone, or anything, can edit directly.

## What a failed commit means

After the panel writes a field, it commits that one file to git with a message describing the change. If that commit fails, the field has already been written to the file — the write happens first, and the commit is a separate step afterward. A commit error does not mean the edit did not take effect; it means the edit is sitting in the working tree without a matching commit. Re-applying the same edit in response to a commit error would be redundant, not corrective.

One way a commit can fail is by waiting on a commit-signing passphrase prompt with nobody there to answer it. The panel does not wait indefinitely for this: each git operation is bounded, and a commit that is still not done well past that bound fails with an error naming a signing prompt as the likely cause, rather than hanging.

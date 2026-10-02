#!/usr/bin/env python3
"""Check an agent's replies to a review before saying it has answered.

The file is the agent's alone; the panel only reads it. Each answer is a
section "## c<number>", then "status: fixed|declined|question", an optional
"commit: <sha>", a blank line, and free text. An answer to the same comment
in a later round is a new section below; earlier ones are never edited.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

STATUSES = ("fixed", "declined", "question")
ID_RE = re.compile(r"^c\d+$")
FENCE_RE = re.compile(r"^(```+|~~~+)")
# An attempted reply heading: some #s, the comment id, nothing else on the
# line. Matches "##c3", "### c3", "#c3" as well as the one correct spelling,
# "## c3" — code (a shebang, a C #include, an issue reference, a Rust
# attribute) never looks like this.
ATTEMPTED_HEADING_RE = re.compile(r"^#+\s*c\d+\s*$")
VALID_HEADING_RE = re.compile(r"^## c\d+$")


def validate_replies(text: str, comments: Path | None = None) -> list[str]:
    known = None
    if comments is not None and comments.exists():
        known = {c.get("id") for c in json.loads(comments.read_text(encoding="utf-8")).get("comments", [])}
    errors = []
    # A heading meant for a comment id but glued or over-hashed ("##c3",
    # "### c3") would be read as text of the section above it, and the
    # comment it meant to answer as unanswered. Only lines that actually look
    # like an attempted "c<number>" heading are checked — a shebang, a C
    # #include, an issue reference, a fenced code block, all start with '#'
    # but never mean a heading. A fence's own content is skipped outright.
    fence = None
    for n, line in enumerate(text.splitlines(), 1):
        m = FENCE_RE.match(line.strip())
        if m:
            marker = m.group(1)[0]
            fence = None if fence == marker else (fence or marker)
            continue
        if fence:
            continue
        if ATTEMPTED_HEADING_RE.match(line) and not VALID_HEADING_RE.match(line):
            errors.append(f"line {n} {line!r}: a heading is '## c<number>', with one space after '##'")
    sections = ("\n" + text).split("\n## ")[1:]
    for section in sections:
        head, _, body = section.partition("\n")
        cid = head.strip()
        if not ID_RE.match(cid):
            errors.append(f"heading {head!r}: must be a comment id, c<number>, as in comments.json")
            continue
        if known is not None and cid not in known:
            errors.append(f"{cid}: comments.json has no such comment")
        fields, _, _ = body.partition("\n\n")
        status = None
        for line in fields.splitlines():
            key, sep, value = line.partition(":")
            if sep and key.strip() == "status":
                status = value.strip()
        if status is None:
            errors.append(f"{cid}: no status line; write status: {'|'.join(STATUSES)}")
        elif status not in STATUSES:
            errors.append(f"{cid}: status {status!r} is not one of {', '.join(STATUSES)}")
    return errors


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("usage: validate_review.py <reviews/T-NNN/replies.md>", file=sys.stderr)
        return 2
    replies = Path(argv[1])
    errors = validate_replies(replies.read_text(encoding="utf-8"), replies.with_name("comments.json"))
    for e in errors:
        print(e)
    if errors:
        return 1
    print("replies are valid")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

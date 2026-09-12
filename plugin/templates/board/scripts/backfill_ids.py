#!/usr/bin/env python3
"""Раздача идентификаторов карточкам, заведённым до появления идентификаторов.

Три шага, каждый идемпотентен — скрипт можно прогонять сколько угодно раз:

1. Карточке без поля `id` захватывается номер в реестре и вписывается в
   frontmatter первой строкой. Порядок раздачи — по дате заведения, чтобы
   номера шли примерно в хронологии.
2. Карточка, чьё имя файла ещё не начинается с её идентификатора,
   переименовывается.
3. Ссылки `[[имя]]` во всём волте переписываются на новые имена.

Шаг 2 по умолчанию не трогает карточки в работе: переименовать файл под
сессией, которая в него пишет, — потерять её следующую запись. Такие карточки
переименовываются отдельным прогоном, когда сессии закончат.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

from new_card import ID_FORMAT, IDS_DIR, claim_number, next_number
from validate_cards import FILENAME_ID_RE, parse_frontmatter

WIKILINK_RE = re.compile(r"\[\[([^\[\]]+?)\]\]")
FENCE_RE = re.compile(r"^\s*(```|~~~)")
# Ни `active`, ни `review` не значат «файл свободен»: сессия в review жива и
# дописывает лог после того, как оператор принял работу. Переименование под
# ней теряет эту запись молча — на живой доске так уже терялась.
SKIP_BY_DEFAULT = ("active", "review")


def cards_of(board: Path) -> list[Path]:
    """Карточки доски в порядке имени файла."""
    return sorted((board / "cards").glob("*.md"))


def assign_missing(board: Path) -> dict[str, str]:
    """Вписать `id` карточкам, у которых его нет. Возвращает имя файла → id."""
    pending = []
    for path in cards_of(board):
        fields = parse_frontmatter(path.read_text(encoding="utf-8"))
        if fields is not None and not fields.get("id"):
            pending.append((fields.get("created", ""), path))
    pending.sort(key=lambda item: (item[0], item[1].name))

    assigned: dict[str, str] = {}
    for _, path in pending:
        card_id = ID_FORMAT.format(claim_number(board, next_number(board)))
        text = path.read_text(encoding="utf-8")
        path.write_text(text.replace("---\n", f"---\nid: {card_id}\n", 1), encoding="utf-8")
        assigned[path.name] = card_id
    return assigned


def rename_pending(
    board: Path,
    skip_stages: tuple[str, ...] = SKIP_BY_DEFAULT,
    force: tuple[str, ...] = (),
) -> dict[str, str]:
    """Переименовать карточки, чьё имя ещё не несёт идентификатора.

    `force` — номера, которые переименовываются несмотря на стадию: их называет
    тот, кто точно знает, что в карточку сейчас никто не пишет.
    """
    renames: dict[str, str] = {}
    for path in cards_of(board):
        fields = parse_frontmatter(path.read_text(encoding="utf-8")) or {}
        card_id = fields.get("id")
        if not card_id or FILENAME_ID_RE.match(path.name):
            continue
        if fields.get("stage") in skip_stages and card_id not in force:
            continue
        target = path.with_name(f"{card_id}-{path.name}")
        path.rename(target)
        renames[path.name] = target.name
    return renames


def rewrite_links(text: str, renames: dict[str, str]) -> str:
    """Переписать ссылки, не трогая примеры внутри бэктиков и блоков кода."""
    by_stem = {Path(old).stem: Path(new).stem for old, new in renames.items()}

    def replace(match: re.Match[str]) -> str:
        # Ссылка может нести раздел и подпись: [[имя#раздел|подпись]].
        target, _, tail = match.group(1).partition("#")
        if not tail:
            target, _, tail = match.group(1).partition("|")
            tail = f"|{tail}" if tail else ""
        else:
            tail = f"#{tail}"
        renamed = by_stem.get(target.strip())
        return f"[[{renamed}{tail}]]" if renamed else match.group(0)

    result: list[str] = []
    in_fence = False
    for line in text.splitlines(keepends=True):
        if FENCE_RE.match(line):
            in_fence = not in_fence
            result.append(line)
            continue
        if in_fence:
            result.append(line)
            continue
        # Нечётные куски — то, что внутри бэктиков: там ссылки ненастоящие.
        parts = re.split(r"(`[^`]*`)", line)
        result.append(
            "".join(
                part if index % 2 else WIKILINK_RE.sub(replace, part)
                for index, part in enumerate(parts)
            )
        )
    return "".join(result)


def fix_links(board: Path, renames: dict[str, str]) -> list[str]:
    """Переписать ссылки во всём волте. Возвращает изменённые файлы."""
    if not renames:
        return []
    changed: list[str] = []
    for path in sorted(board.rglob("*.md")):
        if any(part.startswith(".") for part in path.relative_to(board).parts):
            continue
        text = path.read_text(encoding="utf-8")
        updated = rewrite_links(text, renames)
        if updated != text:
            path.write_text(updated, encoding="utf-8")
            changed.append(str(path.relative_to(board)))
    return changed


def main(argv: list[str]) -> int:
    """Прогнать раздачу, переименование и починку ссылок."""
    parser = argparse.ArgumentParser(description="Раздать идентификаторы карточкам доски.")
    parser.add_argument(
        "--board", default=str(Path(__file__).resolve().parent.parent), help="корень доски"
    )
    parser.add_argument(
        "--rename-busy",
        action="store_true",
        help="переименовывать и карточки в работе и на ревью (только когда сессии сняты)",
    )
    parser.add_argument(
        "--also",
        nargs="*",
        default=[],
        metavar="T-NNN",
        help="переименовать и эти номера, несмотря на стадию",
    )
    args = parser.parse_args(argv[1:])
    board = Path(args.board)
    (board / IDS_DIR).mkdir(exist_ok=True)

    assigned = assign_missing(board)
    for name, card_id in assigned.items():
        print(f"{card_id}  {name}")

    renames = rename_pending(
        board, () if args.rename_busy else SKIP_BY_DEFAULT, tuple(args.also)
    )
    for old, new in renames.items():
        print(f"переименована: {old} -> {new}")

    for name in fix_links(board, renames):
        print(f"поправлены ссылки: {name}")

    skipped = [p.name for p in cards_of(board) if not FILENAME_ID_RE.match(p.name)]
    print(f"\nроздано: {len(assigned)}, переименовано: {len(renames)}")
    if skipped:
        print(f"ждут переименования ({len(skipped)}): {', '.join(skipped)}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

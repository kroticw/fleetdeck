#!/usr/bin/env python3
"""Путь к карточке по её номеру.

Нужен всем, кто держит карточку открытой дольше одного действия. Имя файла
карточки может измениться под работающей сессией — так уже было: карточку
переименовали, пока сессия писала в неё лог, и запись ушла по старому пути,
в файл, которого через секунду не стало.

Номер же не меняется никогда. Поэтому адрес карточки — номер, а путь берётся
этим скриптом непосредственно перед обращением к файлу:

    python3 scripts/card_path.py T-018

Поиск идёт по полю `id`, а не по имени файла: так находится и карточка,
которую ещё не переименовали.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

NUMBER_RE = re.compile(r"^(?:[Tt]-)?(\d{1,6})$")
SEARCHED = ("cards", "archive")


def normalize(spoken: str) -> str | None:
    """Привести сказанное к каноническому виду: «T-13», «t-13», «13» → «T-013»."""
    match = NUMBER_RE.match(spoken.strip())
    return f"T-{int(match.group(1)):03d}" if match else None


def find_card(board: Path, wanted: str) -> Path | None:
    """Найти карточку по номеру. Доска важнее архива: там живая версия."""
    card_id = normalize(wanted)
    if card_id is None:
        return None
    needle = f"id: {card_id}"
    for directory in SEARCHED:
        for path in sorted((board / directory).glob("*.md")):
            try:
                head = path.read_text(encoding="utf-8")[:512]
            except (OSError, UnicodeDecodeError):
                continue
            if any(line.strip().strip("\"'") == needle for line in head.splitlines()):
                return path
    return None


def main(argv: list[str]) -> int:
    """Напечатать путь к карточке или объяснить, почему её нет."""
    parser = argparse.ArgumentParser(description="Найти карточку доски по номеру.")
    parser.add_argument("identifier", help="номер карточки: T-018, T-18 или 18")
    parser.add_argument(
        "--board", default=str(Path(__file__).resolve().parent.parent), help="корень доски"
    )
    args = parser.parse_args(argv[1:])

    if normalize(args.identifier) is None:
        print(f"не похоже на номер карточки: {args.identifier!r}", file=sys.stderr)
        return 2

    path = find_card(Path(args.board), args.identifier)
    if path is None:
        print(f"карточка {normalize(args.identifier)} не найдена", file=sys.stderr)
        return 1

    print(path)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

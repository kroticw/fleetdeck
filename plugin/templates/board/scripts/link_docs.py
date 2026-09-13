#!/usr/bin/env python3
"""Переписать пути к документам в карточках на ссылки [[имя]].

Панель открывает документ из карточки только по ссылке. Путь, записанный
текстом, человек читает глазами и ищет файл сам. Валидатор такой путь
отвергает (см. validate_doc_paths), а этот скрипт приводит к ссылкам то, что
было записано до появления правила:

    python3 scripts/link_docs.py            # показать, что поменяется
    python3 scripts/link_docs.py --write    # записать

Переписывается ровно то, что отвергает валидатор: путь к существующему .md под
каталогом docs, записанный голым или в бэктиках целиком. Бэктики уходят вместе
с путём: ссылка внутри кода не работает ни в Obsidian, ни в панели. Путь внутри
команды, в блоке кода, к несуществующему файлу и к снимку остаётся как был.
Скрипт идемпотентен.

Карточку в это время могут писать живые сессии. Поэтому файл перечитывается
непосредственно перед записью и заменяется одним переименованием: запись поверх
копии, прочитанной раньше, потеряла бы их строку лога.
"""

from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path

from validate_cards import doc_links, find_doc_paths

# Сколько раз перечитать карточку, которая меняется прямо во время прогона.
ATTEMPTS = 3


def link_text(text: str, root: Path, links: dict[Path, str] | None = None) -> tuple[str, int]:
    """Текст с путями, заменёнными на ссылки, и число замен."""
    found = find_doc_paths(text, root, links)
    for start, end, _, link in reversed(found):
        text = text[:start] + f"[[{link}]]" + text[end:]
    return text, len(found)


def link_card(path: Path, root: Path, links: dict[Path, str], write: bool) -> list[tuple[str, str]]:
    """Переписать одну карточку. Вернуть пары «как было записано — ссылка»."""
    for _ in range(ATTEMPTS):
        before = path.read_text(encoding="utf-8")
        found = [(written, link) for _, _, written, link in find_doc_paths(before, root, links)]
        if not found or not write:
            return found
        after, _ = link_text(before, root, links)
        if path.read_text(encoding="utf-8") != before:
            continue
        staged = path.with_name(f".{path.name}.link_docs")
        staged.write_text(after, encoding="utf-8")
        os.replace(staged, path)
        return found
    raise RuntimeError(f"{path.name}: карточка менялась во время каждой из {ATTEMPTS} попыток")


def main(argv: list[str]) -> int:
    """Пройти карточки доски и показать или записать замены."""
    parser = argparse.ArgumentParser(description="Переписать пути к документам в ссылки [[имя]].")
    parser.add_argument(
        "--board", default=str(Path(__file__).resolve().parent.parent), help="корень доски"
    )
    parser.add_argument("--write", action="store_true", help="записать замены, а не только показать")
    args = parser.parse_args(argv[1:])
    board = Path(args.board)
    links = doc_links(board)

    cards = 0
    paths = 0
    for path in sorted((board / "cards").glob("*.md")):
        found = link_card(path, board, links, args.write)
        if not found:
            continue
        cards += 1
        paths += len(found)
        for written, link in found:
            print(f"{path.name}: {written} -> [[{link}]]")

    print(f"\nкарточек: {cards}, путей: {paths}")
    if paths and not args.write:
        print("ничего не записано: это прогон без --write")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

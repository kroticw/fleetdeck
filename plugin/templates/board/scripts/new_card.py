#!/usr/bin/env python3
"""Заведение карточки доски с выдачей сквозного идентификатора.

Карточки заводятся параллельно: оператором, оркестратором и самими сессиями,
иногда одновременно. Поэтому номер не «берётся», а **захватывается**: файл
создаётся через O_EXCL, и захват имени — это одна атомарная операция ядра, а
не «проверил, потом записал». Проигравший в гонке получает FileExistsError и
берёт следующий номер. Ни общего счётчика, ни lock-файла для этого не нужно.

Захватывается при этом не имя карточки, а пустой файл-маркер `.ids/T-012` —
только номер и ничего больше. Имя карточки содержит ещё и slug, и две
карточки с разными slug'ами заняли бы один номер, не столкнувшись именами:
O_EXCL по имени карточки такую гонку пропускает (проверено стрессом на 20
процессах — три дубля из двадцати). У номера же маркер ровно один.

Маркер не удаляется никогда: ни когда карточку переименовали, ни когда она
уехала в архив. Освободить номер нельзя, и переиспользовать его тоже.
Маркер без карточки — это след упавшего заведения, он безвреден; карточка
без маркера — заведённая мимо этого скрипта, и валидатор её ловит.

Руками карточки не заводят: заведённая мимо этого скрипта карточка либо
останется без номера, либо повторит чужой.
"""

from __future__ import annotations

import argparse
import re
import sys
from datetime import date
from pathlib import Path

ID_FORMAT = "T-{:03d}"
# Номер в начале имени: и у готовой карточки «T-012-дата-slug.md»,
# и у заготовки «T-012.md», которой номер захватывается.
FILENAME_ID_RE = re.compile(r"^T-(\d{3}|[1-9]\d{3,})(?:-|\.md$)")
FRONTMATTER_ID_RE = re.compile(r"^id:\s*[\"']?T-(\d{3,})[\"']?\s*$", re.MULTILINE)
ZONES = ("urgent", "unplanned", "planned", "niceToHave")
SEARCHED = ("cards", "archive")
# Реестр захваченных номеров. Скрытый: Obsidian не показывает его в дереве,
# а смотреть там нечего — файлы пустые, значение несёт только имя.
IDS_DIR = ".ids"

TEMPLATE = """---
id: {card_id}
zone: {zone}
stage: new
progress: 0
session: ""
{repo_line}created: {created}
---

# {title}

## Постановка

## Приёмка

## Лог

- {created} — заведена
"""


def card_number(path: Path) -> int | None:
    """Номер карточки: из имени файла, а если имя ещё старое — из frontmatter."""
    match = FILENAME_ID_RE.match(path.name)
    if match:
        return int(match.group(1))
    try:
        head = path.read_text(encoding="utf-8")[:512]
    except (OSError, UnicodeDecodeError):
        return None
    match = FRONTMATTER_ID_RE.search(head)
    return int(match.group(1)) if match else None


def taken_numbers(board: Path) -> set[int]:
    """Все занятые номера: реестр плюс сами карточки на доске и в архиве.

    Реестр — главный источник, карточки читаются ради номеров, розданных до
    появления реестра. Архив обязателен: карточка уезжает туда вместе со
    своим номером, и без него максимум просядет и номер переиспользуется.
    """
    numbers = {
        int(path.name.removeprefix("T-"))
        for path in (board / IDS_DIR).glob("T-*")
        if path.name.removeprefix("T-").isdigit()
    }
    numbers.update(
        number
        for directory in SEARCHED
        for path in sorted((board / directory).glob("*.md"))
        if (number := card_number(path)) is not None
    )
    return numbers


def next_number(board: Path) -> int:
    """Следующий свободный номер."""
    return max(taken_numbers(board), default=0) + 1


def claim_number(board: Path, start: int) -> int:
    """Захватить свободный номер, начиная со start. Возвращает захваченный."""
    registry = board / IDS_DIR
    registry.mkdir(exist_ok=True)
    number = start
    while True:
        try:
            (registry / ID_FORMAT.format(number)).touch(exist_ok=False)
        except FileExistsError:
            # Номер увели между сканированием и захватом — берём следующий.
            number += 1
            continue
        return number


def create_card(
    board: Path,
    *,
    title: str,
    slug: str,
    zone: str,
    created: str,
    repo: str = "",
    start: int | None = None,
) -> Path:
    """Создать карточку, захватив свободный номер. Возвращает путь к файлу."""
    number = claim_number(board, start if start is not None else next_number(board))
    card_id = ID_FORMAT.format(number)
    path = board / "cards" / f"{card_id}-{created}-{slug}.md"
    path.write_text(
        TEMPLATE.format(
            card_id=card_id,
            zone=zone,
            repo_line=f"repo: {repo}\n" if repo else "",
            created=created,
            title=title,
        ),
        encoding="utf-8",
    )
    return path


def main(argv: list[str]) -> int:
    """Разобрать аргументы и завести карточку."""
    parser = argparse.ArgumentParser(description="Завести карточку на доске флота.")
    parser.add_argument("--title", required=True, help="заголовок карточки")
    parser.add_argument("--slug", required=True, help="slug для имени файла, латиницей")
    parser.add_argument("--zone", required=True, choices=ZONES, help="зона доски")
    parser.add_argument("--repo", default="", help="путь репозитория от домашнего каталога")
    parser.add_argument("--created", default=date.today().isoformat(), help="дата YYYY-MM-DD")
    parser.add_argument(
        "--board",
        default=str(Path(__file__).resolve().parent.parent),
        help="корень доски",
    )
    args = parser.parse_args(argv[1:])

    path = create_card(
        Path(args.board),
        title=args.title,
        slug=args.slug,
        zone=args.zone,
        created=args.created,
        repo=args.repo,
    )
    print(path)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

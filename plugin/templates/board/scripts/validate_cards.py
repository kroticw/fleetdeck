#!/usr/bin/env python3
"""Проверка схемы карточек доски флота агентов.

Внешних зависимостей нет: схема плоская, поэтому frontmatter разбирается
вручную, без PyYAML.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ZONES = ("urgent", "unplanned", "planned", "niceToHave")
STAGES = ("new", "active", "review", "done", "blocked")
STARTED_STAGES = ("active", "review", "done", "blocked")
PROGRESS_VALUES = (0, 10, 20, 40, 60, 80, 100)
REQUIRED_FIELDS = ("id", "zone", "stage", "progress", "created")
KNOWN_FIELDS = ("id", "zone", "stage", "progress", "session", "repo", "created")
# Панель свойств Obsidian сама дописывает эти поля во frontmatter карточки.
# Они не часть схемы доски, но валидатор не вправе их удалять — терпим.
OBSIDIAN_FIELDS = ("tags", "aliases", "cssclasses", "cssclass")

DATE_RE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
# Наблюдались идентификаторы и в шесть, и в восемь шестнадцатеричных символов;
# диапазон намеренно шире наблюдений, чтобы валидатор не падал на смене формата.
SESSION_RE = re.compile(r"^[0-9a-fA-F]{6,12}$")
# Идентификатор карточки: постоянный префикс и сквозной номер по всей доске.
# Ведущие нули — чтобы карточки сортировались по номеру, а не лексикографически.
# Знаков не меньше трёх, но и не ровно три: на доске бывает до тридцати карточек
# в день, тысячный номер — вопрос месяца, и упереться в формат нельзя.
# Написание при этом ровно одно: три знака с ведущими нулями, дальше — без них.
# Иначе «T-0001» и «T-001» стали бы двумя записями одного номера.
ID_RE = re.compile(r"^T-(?:\d{3}|[1-9]\d{3,})$")
# Тот же идентификатор в начале имени файла. Имя — главное место: на захвате
# имени через O_EXCL держится выдача номеров без гонки, см. scripts/new_card.py.
FILENAME_ID_RE = re.compile(r"^(T-(?:\d{3}|[1-9]\d{3,}))-")
# Ссылка [[заметка]], [[заметка#раздел]], [[заметка|подпись]].
WIKILINK_RE = re.compile(r"\[\[([^\[\]]+?)\]\]")
FENCE_RE = re.compile(r"^\s*(```|~~~)")
# Документ, записанный путём: docs/reports/x.md, ~/obsidian/board/docs/reports/x.md.
# Группа — путь внутри каталога docs, по нему документ и ищется.
DOC_PATH_RE = re.compile(
    r"(?<![\w./~\[-])(?:~/|/)?(?:[\w.-]+/)*?docs/((?:[\w.-]+/)*[\w.-]+\.md)(?![\w/-])"
)
CODE_SPAN_RE = re.compile(r"`([^`]*)`")
DOCS_DIR = "docs"
# Реестр захваченных номеров, который ведёт scripts/new_card.py.
IDS_DIR = ".ids"


def strip_quotes(value: str) -> str:
    """Снять парные обрамляющие кавычки: Obsidian ставит их сам."""
    if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
        return value[1:-1]
    return value


def parse_frontmatter(text: str) -> dict[str, str] | None:
    """Разобрать плоский frontmatter. None, если блока нет или он не закрыт."""
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        return None
    fields: dict[str, str] = {}
    for line in lines[1:]:
        if line.strip() == "---":
            return fields
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        key, separator, value = line.partition(":")
        if not separator:
            continue
        fields[key.strip()] = strip_quotes(value.strip())
    return None


def validate_card(name: str, text: str) -> list[str]:
    """Вернуть список ошибок карточки. Пустой список означает, что всё в порядке."""
    fields = parse_frontmatter(text)
    if fields is None:
        return [f"{name}: нет корректного блока frontmatter"]

    errors: list[str] = []

    for key in REQUIRED_FIELDS:
        if not fields.get(key):
            errors.append(f"{name}: не заполнено обязательное поле {key}")

    for key in fields:
        if key not in KNOWN_FIELDS and key not in OBSIDIAN_FIELDS:
            errors.append(
                f"{name}: неизвестное поле frontmatter {key!r}, "
                f"схема допускает только {KNOWN_FIELDS}"
            )

    zone = fields.get("zone")
    if zone and zone not in ZONES:
        errors.append(f"{name}: недопустимая zone {zone!r}, ожидалось одно из {ZONES}")

    stage = fields.get("stage")
    if stage and stage not in STAGES:
        errors.append(f"{name}: недопустимая stage {stage!r}, ожидалось одно из {STAGES}")

    progress: int | None = None
    raw_progress = fields.get("progress")
    if raw_progress:
        try:
            progress = int(raw_progress)
        except ValueError:
            errors.append(f"{name}: progress не число: {raw_progress!r}")
        else:
            if progress not in PROGRESS_VALUES:
                errors.append(
                    f"{name}: недопустимый progress {progress}, "
                    f"ожидалось одно из {PROGRESS_VALUES}"
                )

    if stage == "done" and progress != 100:
        errors.append(f"{name}: при stage done значение progress обязано быть 100")

    created = fields.get("created")
    if created and not DATE_RE.match(created):
        errors.append(f"{name}: created не в формате YYYY-MM-DD: {created!r}")

    card_id = fields.get("id")
    if card_id and not ID_RE.match(card_id):
        errors.append(f"{name}: id не в формате T-NNN: {card_id!r}")
    filename_match = FILENAME_ID_RE.match(name)
    if filename_match and card_id and filename_match.group(1) != card_id:
        errors.append(
            f"{name}: id {card_id!r} разошёлся с идентификатором "
            f"в имени файла {filename_match.group(1)!r}"
        )

    session = fields.get("session")
    if session and not SESSION_RE.match(session):
        errors.append(f"{name}: session не похож на short id: {session!r}")
    if not session and stage in STARTED_STAGES:
        errors.append(
            f"{name}: при stage {stage} поле session обязано быть заполнено: "
            "оно единственное связывает карточку с сессией"
        )

    return errors


def strip_code(text: str) -> str:
    """Вырезать код: в примерах внутри бэктиков ссылки ненастоящие."""
    lines: list[str] = []
    in_fence = False
    for line in text.splitlines():
        if FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        lines.append("" if in_fence else re.sub(r"`[^`]*`", "", line))
    return "\n".join(lines)


def vault_root(target: Path) -> Path:
    """Корень волта: ближайший каталог вверх с .obsidian или .git."""
    start = target if target.is_dir() else target.parent
    current = start.resolve()
    for _ in range(3):
        if (current / ".obsidian").is_dir() or (current / ".git").is_dir():
            return current
        current = current.parent
    return start


def docs_dirs(root: Path) -> list[Path]:
    """Каталоги документов волта: docs внутри доски и docs рядом с ней.

    Живая доска держит разборы в <доска>/docs, а рабочий каталог fleetdeck
    раскладывает <корень>/board и <корень>/docs рядом. Ссылка из карточки на
    разбор должна находиться в обоих случаях, иначе на свежем рабочем каталоге
    каждая такая ссылка «никуда не ведёт».
    """
    found: list[Path] = []
    for candidate in (root / DOCS_DIR, root.parent / DOCS_DIR):
        if candidate.is_dir() and all(candidate.resolve() != d.resolve() for d in found):
            found.append(candidate)
    return found


def is_inside(path: Path, root: Path) -> bool:
    """Лежит ли path внутри root, с учётом символических ссылок."""
    try:
        path.resolve().relative_to(root.resolve())
    except ValueError:
        return False
    return True


def vault_notes(root: Path) -> list[tuple[Path, tuple[str, ...]]]:
    """Заметки волта: файл и части его имени без .md.

    Части считаются от корня волта, а у каталога docs рядом с доской — от
    каталога, в котором лежат оба: так имя разбора одинаково в обеих раскладках.
    """
    scans = [(root, root)] + [(d.parent, d) for d in docs_dirs(root) if not is_inside(d, root)]
    notes: list[tuple[Path, tuple[str, ...]]] = []
    for base, scanned in scans:
        for path in scanned.rglob("*.md"):
            parts = path.relative_to(base).with_suffix("").parts
            if any(part.startswith(".") for part in parts):
                continue
            notes.append((path.resolve(), parts))
    return notes


def tails(parts: tuple[str, ...]) -> list[str]:
    """Хвосты пути от самого короткого: «x», «reports/x», «docs/reports/x»."""
    return ["/".join(parts[start:]) for start in range(len(parts) - 1, -1, -1)]


def vault_names(root: Path) -> set[str]:
    """Имена заметок волта так, как их видит Obsidian: путь и его хвосты."""
    return {name for _, parts in vault_notes(root) for name in tails(parts)}


def doc_links(root: Path) -> dict[Path, str]:
    """Имя для ссылки на каждый документ: самый короткий хвост, единственный в волте."""
    notes = vault_notes(root)
    counts: dict[str, int] = {}
    for _, parts in notes:
        for name in tails(parts):
            counts[name] = counts.get(name, 0) + 1
    dirs = [d.resolve() for d in docs_dirs(root)]
    links: dict[Path, str] = {}
    for path, parts in notes:
        if not any(is_inside(path, d) for d in dirs):
            continue
        names = tails(parts)
        links[path] = next((name for name in names if counts[name] == 1), names[-1])
    return links


def find_doc_paths(
    text: str, root: Path, links: dict[Path, str] | None = None
) -> list[tuple[int, int, str, str]]:
    """Документы, записанные путём: (начало, конец, как записано, имя для ссылки).

    Путём считается ровно два написания: голый путь и код в бэктиках, который
    целиком состоит из пути. Путь внутри команды, в блоке кода, к файлу, которого
    под docs нет (документация репозитория задачи), и к снимку — не документ доски.
    """
    if links is None:
        links = doc_links(root)
    dirs = docs_dirs(root)

    def link_for(inner: str) -> str | None:
        for directory in dirs:
            candidate = directory / inner
            if candidate.is_file():
                return links.get(candidate.resolve())
        return None

    found: list[tuple[int, int, str, str]] = []
    offset = 0
    in_fence = False
    for line in text.splitlines(keepends=True):
        start_of_line = offset
        offset += len(line)
        if FENCE_RE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        hits: list[tuple[int, int, str, str]] = []
        masked = line
        for span in CODE_SPAN_RE.finditer(line):
            whole = DOC_PATH_RE.fullmatch(span.group(1).strip())
            link = link_for(whole.group(1)) if whole else None
            if link:
                hits.append((span.start(), span.end(), span.group(0), link))
            masked = masked[: span.start()] + " " * len(span.group(0)) + masked[span.end() :]
        for existing in WIKILINK_RE.finditer(masked):
            masked = masked[: existing.start()] + " " * len(existing.group(0)) + masked[existing.end() :]
        for bare in DOC_PATH_RE.finditer(masked):
            link = link_for(bare.group(1))
            if link:
                hits.append((bare.start(), bare.end(), bare.group(0), link))
        found.extend(
            (start_of_line + s, start_of_line + e, written, link)
            for s, e, written, link in sorted(hits)
        )
    return found


def validate_doc_paths(
    name: str, text: str, root: Path, links: dict[Path, str] | None = None
) -> list[str]:
    """Документ доски, записанный путём, — ошибка: панель открывает только ссылку."""
    return [
        f"{name}: документ записан путём {written} — оформи ссылкой [[{link}]]"
        for _, _, written, link in find_doc_paths(text, root, links)
    ]


def read_registry(root: Path) -> set[str] | None:
    """Захваченные номера. None, если реестра ещё нет и проверять нечем."""
    registry = root / IDS_DIR
    if not registry.is_dir():
        return None
    return {path.name for path in registry.iterdir() if not path.name.startswith(".")}


def validate_collection(
    cards: list[tuple[str, str]], vault: set[str], registry: set[str] | None = None
) -> list[str]:
    """Проверки, которые не помещаются в одну карточку: дубли, реестр, ссылки."""
    errors: list[str] = []

    seen: dict[str, str] = {}
    for name, text in cards:
        fields = parse_frontmatter(text) or {}
        card_id = fields.get("id")
        if not card_id:
            continue
        if card_id in seen:
            errors.append(f"{name}: id {card_id} уже занят карточкой {seen[card_id]}")
        else:
            seen[card_id] = name
        if registry is not None and card_id not in registry:
            errors.append(
                f"{name}: id {card_id} не захвачен в реестре {IDS_DIR}/ — "
                "карточка заведена мимо scripts/new_card.py"
            )

    for name, text in cards:
        for raw in WIKILINK_RE.findall(strip_code(text)):
            target = raw.split("|")[0].split("#")[0].strip()
            if target and target not in vault:
                errors.append(f"{name}: ссылка [[{target}]] никуда не ведёт")

    return errors


def main(argv: list[str]) -> int:
    """Проверить карточки. Аргументом можно передать каталог или один файл."""
    if len(argv) > 1:
        target = Path(argv[1])
    else:
        target = Path(__file__).resolve().parent.parent / "cards"

    if target.is_dir():
        paths = sorted(target.glob("*.md"))
    elif target.is_file():
        paths = [target]
    else:
        print(f"карточки не найдены по пути: {target}", file=sys.stderr)
        return 2

    if not paths:
        # Пустой каталог — не успех проверки: проверять было нечего. Отвечать
        # «все карточки валидны» здесь значит врать с видом достоверности.
        # Ошибкой это тоже не является: свежая доска пуста по построению.
        print(f"карточек не найдено: {target}")
        return 0

    errors: list[str] = []
    cards: list[tuple[str, str]] = []
    for path in paths:
        try:
            text = path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as exc:
            errors.append(f"{path.name}: файл не прочитан: {exc}")
            continue
        cards.append((path.name, text))
        errors.extend(validate_card(path.name, text))

    root = vault_root(target)
    errors.extend(validate_collection(cards, vault_names(root), read_registry(root)))
    links = doc_links(root)
    for name, text in cards:
        errors.extend(validate_doc_paths(name, text, root, links))

    for error in errors:
        print(error)

    if errors:
        print(f"\nвсего ошибок: {len(errors)}")
        return 1

    pending = sum(1 for name, _ in cards if not FILENAME_ID_RE.match(name))
    if pending:
        print(f"без идентификатора в имени файла: {pending}")

    print("все карточки валидны")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

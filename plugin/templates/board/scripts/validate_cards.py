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
REQUIRED_FIELDS = ("zone", "stage", "progress", "created")
KNOWN_FIELDS = ("zone", "stage", "progress", "session", "repo", "created")
# Панель свойств Obsidian сама дописывает эти поля во frontmatter карточки.
# Они не часть схемы доски, но валидатор не вправе их удалять — терпим.
OBSIDIAN_FIELDS = ("tags", "aliases", "cssclasses", "cssclass")

DATE_RE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
# Наблюдались идентификаторы и в шесть, и в восемь шестнадцатеричных символов;
# диапазон намеренно шире наблюдений, чтобы валидатор не падал на смене формата.
SESSION_RE = re.compile(r"^[0-9a-fA-F]{6,12}$")


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

    session = fields.get("session")
    if session and not SESSION_RE.match(session):
        errors.append(f"{name}: session не похож на short id: {session!r}")
    if not session and stage in STARTED_STAGES:
        errors.append(
            f"{name}: при stage {stage} поле session обязано быть заполнено: "
            "оно единственное связывает карточку с сессией"
        )

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

    errors: list[str] = []
    for path in paths:
        try:
            text = path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as exc:
            errors.append(f"{path.name}: файл не прочитан: {exc}")
            continue
        errors.extend(validate_card(path.name, text))

    for error in errors:
        print(error)

    if errors:
        print(f"\nвсего ошибок: {len(errors)}")
        return 1

    print("все карточки валидны")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

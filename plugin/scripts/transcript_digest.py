#!/usr/bin/env python3
"""Выжимка из транскрипта сессии Claude Code.

Сырой `.jsonl` сессии — это мегабайты, из которых на смысл работают проценты:
остальное занимают результаты вызовов инструментов. Скрипт оставляет то, по
чему видно ход работы: реплики человека, текст ответов агента и перечень
запущенных команд без их вывода.

Нужен, когда сессию собираются закрыть, а знание из неё хочется сохранить.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

NOISE_PREFIXES = (
    "<local-command",
    "<command-name",
    "<command-message",
    "Caveat:",
    "Base directory for this skill:",
    "[Request interrupted",
)


def texts(content) -> list[str]:
    """Достать текстовые блоки из поля content, каким бы оно ни пришло."""
    if isinstance(content, str):
        return [content]
    if not isinstance(content, list):
        return []
    out = []
    for block in content:
        if isinstance(block, dict) and block.get("type") == "text":
            out.append(block.get("text", ""))
    return out


def commands(content) -> list[str]:
    """Достать команды из вызовов инструментов, без их вывода."""
    if not isinstance(content, list):
        return []
    out = []
    for block in content:
        if not isinstance(block, dict) or block.get("type") != "tool_use":
            continue
        name = block.get("name", "?")
        inp = block.get("input") or {}
        if name == "Bash":
            out.append("$ " + str(inp.get("command", "")).strip())
        else:
            hint = inp.get("file_path") or inp.get("pattern") or inp.get("url") or ""
            out.append(f"[{name}] {hint}".rstrip())
    return out


def is_noise(text: str) -> bool:
    stripped = text.lstrip()
    return not stripped or stripped.startswith(NOISE_PREFIXES)


def digest(path: Path, max_chars: int, with_commands: bool) -> str:
    parts: list[str] = []
    for line in path.open(encoding="utf-8", errors="replace"):
        try:
            record = json.loads(line)
        except ValueError:
            continue
        role = record.get("type")
        if role not in ("user", "assistant"):
            continue
        content = (record.get("message") or {}).get("content")
        for text in texts(content):
            if is_noise(text):
                continue
            who = "ОПЕРАТОР" if role == "user" else "АГЕНТ"
            parts.append(f"\n### {who}\n{text.strip()[:max_chars]}")
        if with_commands and role == "assistant":
            for cmd in commands(content):
                parts.append(f"    {cmd[:300]}")
    return "\n".join(parts)


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("transcript", help="путь к .jsonl или short id сессии")
    ap.add_argument("--max-chars", type=int, default=4000,
                    help="обрезать каждую реплику до N символов (по умолчанию 4000)")
    ap.add_argument("--no-commands", action="store_true",
                    help="не показывать запущенные команды")
    args = ap.parse_args(argv[1:])

    path = Path(args.transcript)
    if not path.is_file():
        root = Path.home() / ".claude/projects"
        found = next(root.rglob(f"{args.transcript}*.jsonl"), None)
        if found is None:
            print(f"транскрипт не найден: {args.transcript}", file=sys.stderr)
            return 2
        path = found

    print(f"# Выжимка: {path.name}\n")
    print(digest(path, args.max_chars, not args.no_commands))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

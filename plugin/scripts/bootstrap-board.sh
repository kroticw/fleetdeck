#!/usr/bin/env bash
# Разворачивает каталог доски флота на новой машине.
#
# Доска — локальный git-репозиторий с карточками задач. Каждая машина держит
# свою: поле session в карточке указывает на short id локального демона, и на
# другой машине этот идентификатор не значит ничего. Общая доска на два
# компьютера дала бы смесь двух флотов с нерезолвимыми ссылками.
#
# Использование:
#   bootstrap-board.sh [каталог]        по умолчанию ~/fleetdeck/board
#
# Ту же доску из того же шаблона делает сам fleetdeck: `fleetdeck init` или
# первый запуск приложения, вместе с конфигурацией и разрешением на запись.
# Этот скрипт — для машины, где fleetdeck не стоит.
#
# Скрипт рассчитан на системный bash 3.2 в macOS: без mapfile, без
# конструкции «условие и команда» последней строкой, с явным шаблоном mktemp.
set -euo pipefail

target="${1:-$HOME/fleetdeck/board}"
here="$(cd "$(dirname "$0")" && pwd -P)"
template="$here/../templates/board"

if [ ! -d "$template" ]; then
    printf 'не найден шаблон доски: %s\n' "$template" >&2
    exit 1
fi

if [ -e "$target" ] && [ -n "$(ls -A "$target" 2>/dev/null || true)" ]; then
    printf 'каталог не пуст: %s\n' "$target" >&2
    printf 'разверните доску в пустой каталог или уберите существующий вручную\n' >&2
    exit 1
fi

mkdir -p "$target"

# Копируем содержимое шаблона вместе со скрытыми файлами. Точка на конце
# источника переносит именно содержимое, а не сам каталог.
cp -R "$template/." "$target/"
# Циклом, а не списком имён: скрипты доски добавляются, и список, набранный
# руками, отстаёт молча — новый скрипт приезжает на доску без бита исполнения,
# и это выясняется только в момент, когда его пытаются запустить.
for script in "$target"/scripts/*.py; do
    chmod +x "$script"
done

cd "$target"

if [ ! -d .git ]; then
    git init --quiet --initial-branch=master
fi

printf 'доска развёрнута: %s\n' "$target"

# Проверяем, что развёрнутое действительно работает, а не просто скопировалось.
printf '\nпроверка валидатора: '
python3 scripts/validate_cards.py

printf 'проверка тестов:    '
# discover, а не перечисление модулей: скрипты доски добавляются, и модуль,
# забытый в списке, молча остаётся непроверенным.
( cd scripts && python3 -m unittest discover --pattern 'test_*.py' 2>&1 | tail -1 )

cat <<NEXT

Осталось два шага, их скрипт за вас не сделает.

1. Разрешить агентам писать в доску. В ~/.claude/settings.json, в
   permissions.additionalDirectories, добавить путь:

     $target

   Без этого агент не сможет вести свою карточку: доска лежит вне его
   рабочего каталога.

2. Перезапустить Claude Code, чтобы настройка подхватилась.

Виды доска не хранит: срезы «в работе», «на ревью» и «всё» рисует пульт
fleetdeck, читая эти же карточки.

Первый коммит доски скрипт не делает намеренно: подпись GPG попросит PIN, а
скрипт может идти без человека. Зафиксируйте развёрнутое сами либо оставьте
первой уборке флота — она коммитит доску своим шагом.

Ещё понадобится MCP управления сессиями, если он не поставлен:

  git clone https://github.com/kvaps/claude-agents-mcp ~/opensource/claude-agents-mcp
  cd ~/opensource/claude-agents-mcp
  go build -o claude-agents-mcp ./cmd/claude-agents-mcp
  claude mcp add --scope user claude-agents -- ~/opensource/claude-agents-mcp/claude-agents-mcp
NEXT

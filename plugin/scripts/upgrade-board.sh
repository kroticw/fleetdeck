#!/usr/bin/env bash
# Подтягивает в существующую доску изменения шаблона.
#
# Доска — разовая копия шаблона: обновление плагина обновляет скилы, но доску
# не трогает. Этот скрипт закрывает разрыв, не затирая то, что оператор
# настроил у себя.
#
# Что делает:
#   - недостающие файлы копирует;
#   - скрипты в scripts/ обновляет: они каноничны и живут в шаблоне;
#   - про остальные разошедшиеся файлы только сообщает, решает человек.
#
# Использование:
#   upgrade-board.sh [каталог]        по умолчанию ~/fleetdeck/board
#
# Доске, которая лежит в другом месте, каталог передают аргументом: он
# записан в board.path файла ~/.config/fleetdeck/config.yaml.
set -euo pipefail

target="${1:-$HOME/fleetdeck/board}"
here="$(cd "$(dirname "$0")" && pwd -P)"
template="$here/../templates/board"

if [ ! -d "$template" ]; then
    printf 'не найден шаблон доски: %s\n' "$template" >&2
    exit 1
fi
if [ ! -d "$target/cards" ]; then
    printf 'не похоже на каталог доски: %s\n' "$target" >&2
    printf 'для новой доски используйте bootstrap-board.sh\n' >&2
    exit 1
fi

changed=0

# Недостающие файлы и обновление каноничных скриптов.
while IFS= read -r rel; do
    src="$template/$rel"
    dst="$target/$rel"
    if [ ! -e "$dst" ]; then
        mkdir -p "$(dirname "$dst")"
        cp "$src" "$dst"
        printf 'добавлен файл:   %s\n' "$rel"
        changed=$((changed + 1))
        continue
    fi
    if cmp -s "$src" "$dst"; then
        continue
    fi
    case "$rel" in
        scripts/*.py)
            cp "$src" "$dst"
            printf 'обновлён скрипт: %s\n' "$rel"
            changed=$((changed + 1))
            ;;
        *)
            printf 'разошёлся, не трогаю: %s\n' "$rel"
            ;;
    esac
done < <(cd "$template" && find . -type f ! -name .gitkeep | sed 's|^\./||' | sort)

if [ "$changed" -eq 0 ]; then
    printf 'доска уже соответствует шаблону: %s\n' "$target"
    exit 0
fi

# Доказываем, что обновлённое работает, а не просто скопировалось.
cd "$target"
printf '\nпроверка валидатора: '
python3 scripts/validate_cards.py
printf 'проверка тестов:    '
( cd scripts && python3 -m unittest test_validate_cards 2>&1 | tail -1 )

printf '\nизменения не закоммичены: подпись попросит PIN, а скрипт может идти\n'
printf 'без человека. Зафиксируйте сами либо оставьте ближайшей уборке флота.\n'

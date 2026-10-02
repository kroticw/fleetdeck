"""Тесты валидатора карточек доски."""

import contextlib
import io
import tempfile
import unittest
from pathlib import Path

from validate_cards import (
    main,
    parse_frontmatter,
    validate_card,
    validate_collection,
    validate_doc_frontmatter,
    validate_doc_paths,
    vault_names,
)

VALID = """---
id: T-001
zone: unplanned
stage: active
progress: 20
session: ac43ee
repo: example/project
created: 2026-09-05
---

# Карточка

## Контекст

Текст постановки.
"""


def card(**overrides):
    """Собрать карточку, заменив отдельные поля валидного образца."""
    head, _, body = VALID.partition("---\n\n")
    fields = {}
    for line in head.splitlines():
        if line.strip() in ("---", ""):
            continue
        key, _, value = line.partition(":")
        fields[key.strip()] = value.strip()
    fields.update(overrides)
    lines = ["---"]
    for key, value in fields.items():
        if value is None:
            continue
        lines.append(f"{key}: {value}")
    lines.append("---")
    return "\n".join(lines) + "\n\n" + body


class ParseFrontmatterTest(unittest.TestCase):
    def test_parses_flat_fields(self):
        fields = parse_frontmatter(VALID)
        self.assertEqual(fields["zone"], "unplanned")
        self.assertEqual(fields["progress"], "20")

    def test_returns_none_without_block(self):
        self.assertIsNone(parse_frontmatter("# Просто заголовок\n"))

    def test_returns_none_when_block_not_closed(self):
        self.assertIsNone(parse_frontmatter("---\nzone: urgent\n"))

    def test_strips_surrounding_double_quotes(self):
        fields = parse_frontmatter('---\nzone: "unplanned"\n---\n')
        self.assertEqual(fields["zone"], "unplanned")

    def test_strips_surrounding_single_quotes(self):
        fields = parse_frontmatter("---\nsession: '12e34'\n---\n")
        self.assertEqual(fields["session"], "12e34")

    def test_keeps_unpaired_quote(self):
        fields = parse_frontmatter('---\nrepo: "хвост\n---\n')
        self.assertEqual(fields["repo"], '"хвост')


class ValidateCardTest(unittest.TestCase):
    def test_valid_card_has_no_errors(self):
        self.assertEqual(validate_card("ok.md", VALID), [])

    def test_missing_frontmatter_is_error(self):
        errors = validate_card("bad.md", "# Без frontmatter\n")
        self.assertEqual(len(errors), 1)
        self.assertIn("frontmatter", errors[0])

    def test_missing_required_field_is_error(self):
        errors = validate_card("bad.md", card(created=None))
        self.assertTrue(any("created" in e for e in errors))

    def test_unknown_zone_is_error(self):
        errors = validate_card("bad.md", card(zone="urgent-ish"))
        self.assertTrue(any("zone" in e for e in errors))

    def test_unknown_stage_is_error(self):
        errors = validate_card("bad.md", card(stage="in_progress"))
        self.assertTrue(any("stage" in e for e in errors))

    def test_progress_outside_convention_is_error(self):
        errors = validate_card("bad.md", card(progress="45"))
        self.assertTrue(any("progress" in e for e in errors))

    def test_progress_not_a_number_is_error(self):
        errors = validate_card("bad.md", card(progress="почти"))
        self.assertTrue(any("progress" in e for e in errors))

    def test_done_requires_full_progress(self):
        errors = validate_card("bad.md", card(stage="done", progress="80"))
        self.assertTrue(any("done" in e for e in errors))

    def test_done_with_full_progress_is_valid(self):
        self.assertEqual(validate_card("ok.md", card(stage="done", progress="100")), [])

    def test_bad_created_format_is_error(self):
        errors = validate_card("bad.md", card(created="05.09.2026"))
        self.assertTrue(any("created" in e for e in errors))

    def test_bad_session_is_error(self):
        errors = validate_card("bad.md", card(session="не-хекс"))
        self.assertTrue(any("session" in e for e in errors))

    def test_empty_session_is_allowed_when_stage_is_new(self):
        sample = card(stage="new", progress="0", session="")
        self.assertEqual(validate_card("ok.md", sample), [])

    def test_empty_session_is_error_after_start(self):
        started = (("active", "20"), ("review", "80"), ("done", "100"), ("blocked", "20"))
        for stage, progress in started:
            with self.subTest(stage=stage):
                sample = card(stage=stage, progress=progress, session="")
                errors = validate_card("bad.md", sample)
                self.assertTrue(any("session" in e for e in errors))

    def test_quoted_values_are_valid(self):
        sample = card(zone='"unplanned"', stage='"active"', session='"12e345"')
        self.assertEqual(validate_card("ok.md", sample), [])

    def test_uppercase_session_is_valid(self):
        self.assertEqual(validate_card("ok.md", card(session="AC43EE")), [])

    def test_worktree_is_a_known_field(self):
        self.assertEqual(validate_card("ok.md", card(worktree="/Users/x/repo/.claude/worktrees/a")), [])

    def test_worktree_must_be_absolute(self):
        errors = validate_card("bad.md", card(worktree="repo/.claude/worktrees/a"))
        self.assertTrue(any("worktree" in e for e in errors), errors)

    def test_unknown_field_is_error(self):
        errors = validate_card("bad.md", card(pinned="true"))
        self.assertTrue(any("pinned" in e for e in errors))

    def test_obsidian_tags_field_is_valid(self):
        self.assertEqual(validate_card("ok.md", card(tags="foo")), [])

    def test_obsidian_aliases_field_is_valid(self):
        self.assertEqual(validate_card("ok.md", card(aliases="foo")), [])

    def test_obsidian_cssclasses_field_is_valid(self):
        self.assertEqual(validate_card("ok.md", card(cssclasses="foo")), [])

    def test_pinned_field_is_still_an_error(self):
        errors = validate_card("bad.md", card(pinned="true"))
        self.assertTrue(any("pinned" in e for e in errors))


class CardIdTest(unittest.TestCase):
    def test_missing_id_is_error(self):
        errors = validate_card("bad.md", card(id=None))
        self.assertTrue(any("id" in e for e in errors))

    def test_bad_id_format_is_error(self):
        for bad in ("T-1", "T-0001", "FD-001", "t-001", "T001", "001"):
            with self.subTest(id=bad):
                errors = validate_card("bad.md", card(id=bad))
                self.assertTrue(any("id" in e for e in errors))

    def test_id_matching_filename_is_valid(self):
        sample = card(id="T-042")
        self.assertEqual(validate_card("T-042-2026-09-05-slug.md", sample), [])

    def test_id_diverging_from_filename_is_error(self):
        errors = validate_card("T-042-2026-09-05-slug.md", card(id="T-007"))
        self.assertTrue(any("имен" in e for e in errors))

    def test_legacy_filename_without_id_is_not_an_error(self):
        self.assertEqual(validate_card("2026-09-05-slug.md", card(id="T-007")), [])

    def test_quoted_id_is_valid(self):
        self.assertEqual(validate_card("T-042-x.md", card(id='"T-042"')), [])


class CollectionTest(unittest.TestCase):
    def test_duplicate_id_is_error(self):
        cards = [("a.md", card(id="T-005")), ("b.md", card(id="T-005"))]
        errors = validate_collection(cards, vault={})
        self.assertTrue(any("T-005" in e for e in errors))

    def test_distinct_ids_are_valid(self):
        cards = [("a.md", card(id="T-005")), ("b.md", card(id="T-006"))]
        self.assertEqual(validate_collection(cards, vault={}), [])

    def test_id_missing_from_registry_is_error(self):
        """Карточка, заведённая мимо new_card.py, номера не захватывала."""
        errors = validate_collection([("a.md", card(id="T-005"))], vault=set(), registry={"T-004"})
        self.assertTrue(any("реестр" in e for e in errors))

    def test_id_present_in_registry_is_valid(self):
        cards = [("a.md", card(id="T-005"))]
        self.assertEqual(validate_collection(cards, vault=set(), registry={"T-005"}), [])

    def test_registry_is_not_checked_when_absent(self):
        cards = [("a.md", card(id="T-005"))]
        self.assertEqual(validate_collection(cards, vault=set(), registry=None), [])

    def test_broken_wikilink_is_error(self):
        body = card(id="T-005") + "\nсм. [[нет-такой-заметки]]\n"
        errors = validate_collection([("a.md", body)], vault={"a"})
        self.assertTrue(any("нет-такой-заметки" in e for e in errors))

    def test_resolvable_wikilink_is_valid(self):
        body = card(id="T-005") + "\nсм. [[T-006-другая]]\n"
        self.assertEqual(validate_collection([("a.md", body)], vault={"T-006-другая"}), [])

    def test_wikilink_with_alias_and_heading_resolves(self):
        body = card(id="T-005") + "\n[[T-006-другая#Лог|вон та]]\n"
        self.assertEqual(validate_collection([("a.md", body)], vault={"T-006-другая"}), [])

    def test_wikilink_inside_inline_code_is_ignored(self):
        body = card(id="T-005") + "\nссылка вида `[[имя]]` по имени файла\n"
        self.assertEqual(validate_collection([("a.md", body)], vault=set()), [])

    def test_wikilink_inside_fenced_block_is_ignored(self):
        body = card(id="T-005") + "\n```text\n[[имя]]\n```\n"
        self.assertEqual(validate_collection([("a.md", body)], vault=set()), [])


class MainTest(unittest.TestCase):
    def run_main(self, *args):
        """Прогнать main, вернув код возврата и весь его вывод."""
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = main(["validate_cards.py", *args])
        return code, out.getvalue() + err.getvalue()

    def test_empty_directory_reports_that_there_are_no_cards(self):
        """Пустому каталогу отвечать «все карточки валидны» нельзя.

        Проверять было нечего, а отчёт выглядит как успешная проверка —
        враньё с видом достоверности. Пустая доска сама по себе нормальна:
        сразу после развёртывания карточек ещё нет. Поэтому код возврата
        остаётся нулевым, меняется только то, что валидатор говорит.
        """
        with tempfile.TemporaryDirectory() as tmp:
            code, output = self.run_main(tmp)
        self.assertEqual(code, 0)
        self.assertNotIn("все карточки валидны", output)
        self.assertIn("карточек не найдено", output)

    def test_directory_with_broken_card_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / "ok.md").write_text(VALID, encoding="utf-8")
            (Path(tmp) / "broken.md").write_text("# без frontmatter\n", encoding="utf-8")
            code, output = self.run_main(tmp)
        self.assertEqual(code, 1)
        self.assertIn("broken.md", output)
        self.assertNotIn("ok.md", output)

    def test_reports_cards_without_id_in_filename(self):
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / "2026-09-05-slug.md").write_text(VALID, encoding="utf-8")
            code, output = self.run_main(tmp)
        self.assertEqual(code, 0)
        self.assertIn("без идентификатора в имени файла: 1", output)

    def test_says_nothing_when_every_filename_carries_id(self):
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / "T-001-2026-09-05-slug.md").write_text(VALID, encoding="utf-8")
            code, output = self.run_main(tmp)
        self.assertEqual(code, 0)
        self.assertNotIn("без идентификатора", output)

    def test_missing_path_returns_two(self):
        with tempfile.TemporaryDirectory() as tmp:
            code, output = self.run_main(str(Path(tmp) / "нет-такого"))
        self.assertEqual(code, 2)
        self.assertIn("нет-такого", output)

    def test_single_file_checks_only_that_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            mine = Path(tmp) / "моя.md"
            mine.write_text(VALID, encoding="utf-8")
            (Path(tmp) / "чужая.md").write_text("# без frontmatter\n", encoding="utf-8")
            code, output = self.run_main(str(mine))
        self.assertEqual(code, 0)
        self.assertNotIn("чужая.md", output)

    def test_undecodable_file_does_not_stop_the_run(self):
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp) / "binary.md").write_bytes(b"---\nzone: \xff\xfe\n---\n")
            (Path(tmp) / "broken.md").write_text("# без frontmatter\n", encoding="utf-8")
            code, output = self.run_main(tmp)
        self.assertEqual(code, 1)
        self.assertIn("binary.md", output)
        self.assertIn("broken.md", output)


def board_with_report(tmp: str) -> Path:
    """Доска с одним разбором в docs/reports, корень волта помечен .git."""
    root = Path(tmp) / "board"
    for name in ("cards", ".git", "docs/reports"):
        (root / name).mkdir(parents=True)
    (root / "docs" / "reports" / "2026-09-12-report.md").write_text("# Разбор\n", encoding="utf-8")
    return root


class DocPathTest(unittest.TestCase):
    """Документ, записанный путём, а не ссылкой, панель открыть не может."""

    def test_bare_path_to_a_document_is_error_naming_the_link(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            errors = validate_doc_paths("a.md", "разбор docs/reports/2026-09-12-report.md\n", root)
            self.assertEqual(len(errors), 1)
            self.assertIn("[[2026-09-12-report]]", errors[0])

    def test_code_span_holding_only_the_path_is_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            errors = validate_doc_paths("a.md", "разбор `docs/reports/2026-09-12-report.md`\n", root)
            self.assertEqual(len(errors), 1)

    def test_path_through_the_home_directory_is_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            text = "`~/obsidian/board/docs/reports/2026-09-12-report.md`\n"
            self.assertEqual(len(validate_doc_paths("a.md", text, root)), 1)

    def test_path_to_a_document_that_does_not_exist_is_not_error(self):
        # Карточки называют и документацию репозитория задачи: её на доске нет.
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            text = "поправлен `docs/engineering/live-terminal.md`\n"
            self.assertEqual(validate_doc_paths("a.md", text, root), [])

    def test_path_inside_a_fenced_block_is_not_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            text = "```bash\ncat docs/reports/2026-09-12-report.md\n```\n"
            self.assertEqual(validate_doc_paths("a.md", text, root), [])

    def test_code_span_holding_a_command_is_not_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            text = "`wc -l docs/reports/2026-09-12-report.md`\n"
            self.assertEqual(validate_doc_paths("a.md", text, root), [])

    def test_snapshot_is_not_a_document(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            (root / "docs" / "reports" / "shot.png").write_bytes(b"")
            self.assertEqual(validate_doc_paths("a.md", "docs/reports/shot.png\n", root), [])

    def test_link_to_a_document_is_not_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.assertEqual(validate_doc_paths("a.md", "[[2026-09-12-report]]\n", root), [])

    def test_documents_next_to_the_board_are_notes_of_the_vault(self):
        # Раскладка рабочего каталога fleetdeck: <root>/board и <root>/docs рядом,
        # и ссылка из карточки на разбор не должна считаться битой.
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "board"
            (root / "cards").mkdir(parents=True)
            (root / ".git").mkdir()
            (Path(tmp) / "docs" / "reports").mkdir(parents=True)
            (Path(tmp) / "docs" / "reports" / "design.md").write_text("# Дизайн\n", encoding="utf-8")
            names = vault_names(root)
            self.assertIn("design", names)
            self.assertIn("reports/design", names)

    def test_main_fails_on_a_card_with_a_document_path(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            (root / "cards" / "T-001-card.md").write_text(
                card(id="T-001") + "\nразбор docs/reports/2026-09-12-report.md\n", encoding="utf-8"
            )
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                code = main(["validate_cards.py", str(root / "cards")])
            self.assertEqual(code, 1)
            self.assertIn("[[2026-09-12-report]]", out.getvalue())


class DocFrontmatterTest(unittest.TestCase):
    """Документ может назвать сессию-автора во frontmatter; панель откроет её рядом."""

    def write(self, root: Path, text: str, name: str = "2026-09-12-report.md") -> None:
        (root / "docs" / "reports" / name).write_text(text, encoding="utf-8")

    def test_document_without_frontmatter_is_valid(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.assertEqual(validate_doc_frontmatter(root), [])

    def test_session_in_the_short_id_format_is_valid(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "---\ndate: 2026-09-12\nsession: e62e1d58\ncards: [T-090]\n---\n# Разбор\n")
            self.assertEqual(validate_doc_frontmatter(root), [])

    def test_session_not_like_a_short_id_is_error_naming_the_document(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "---\nsession: оркестр\n---\n# Разбор\n")
            errors = validate_doc_frontmatter(root)
            self.assertEqual(len(errors), 1)
            self.assertIn("reports/2026-09-12-report.md", errors[0])
            self.assertIn("оркестр", errors[0])

    def test_empty_session_is_error(self):
        # Пустое поле панель не отличит от отсутствующего, а автор думал, что назвал себя.
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "---\nsession:\n---\n# Разбор\n")
            self.assertEqual(len(validate_doc_frontmatter(root)), 1)

    def test_unclosed_frontmatter_is_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "---\nsession: e62e1d58\n# Разбор\n")
            self.assertEqual(len(validate_doc_frontmatter(root)), 1)

    def test_a_longer_rule_at_the_top_is_not_a_frontmatter(self):
        # Только строка ровно из трёх дефисов открывает фронтматтер; линия из
        # четырёх — просто линия.
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "----\n\n# Разбор\n")
            self.assertEqual(validate_doc_frontmatter(root), [])

    def test_other_fields_are_free(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            self.write(root, "---\nprs: [143, 146]\nrepo: opensource/fleetdeck\n---\n# Разбор\n")
            self.assertEqual(validate_doc_frontmatter(root), [])

    def test_documents_next_to_the_board_are_checked(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "board"
            (root / "cards").mkdir(parents=True)
            (root / ".git").mkdir()
            (Path(tmp) / "docs").mkdir()
            (Path(tmp) / "docs" / "design.md").write_text("---\nsession: x\n---\n", encoding="utf-8")
            self.assertEqual(len(validate_doc_frontmatter(root)), 1)

    def test_main_on_the_cards_directory_reports_the_document(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            (root / "cards" / "T-001-card.md").write_text(card(id="T-001"), encoding="utf-8")
            self.write(root, "---\nsession: nope\n---\n")
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                code = main(["validate_cards.py", str(root / "cards")])
            self.assertEqual(code, 1)
            self.assertIn("session документа", out.getvalue())

    def test_main_on_one_card_does_not_check_documents(self):
        # Сессия проверяет свою карточку; чужой документ ей чинить нельзя.
        with tempfile.TemporaryDirectory() as tmp:
            root = board_with_report(tmp)
            path = root / "cards" / "T-001-card.md"
            path.write_text(card(id="T-001"), encoding="utf-8")
            self.write(root, "---\nsession: nope\n---\n")
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(main(["validate_cards.py", str(path)]), 0)


if __name__ == "__main__":
    unittest.main()

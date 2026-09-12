"""Тесты валидатора карточек доски."""

import contextlib
import io
import tempfile
import unittest
from pathlib import Path

from validate_cards import main, parse_frontmatter, validate_card, validate_collection

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


if __name__ == "__main__":
    unittest.main()

"""Тесты переписывания путей к документам в ссылки."""

import contextlib
import io
import tempfile
import unittest
from pathlib import Path

from link_docs import link_text, main
from validate_cards import validate_doc_paths, vault_names

CARD = """---
id: T-001
zone: planned
stage: active
progress: 20
session: "ab12cd34"
created: 2026-09-13
---

# Карточка

{body}
"""


def board(tmp: str) -> Path:
    root = Path(tmp) / "board"
    for name in ("cards", ".git", "docs/reports"):
        (root / name).mkdir(parents=True)
    (root / "docs" / "reports" / "2026-09-12-report.md").write_text("# Разбор\n", encoding="utf-8")
    return root


def put(root: Path, body: str, name: str = "T-001-2026-09-13-card.md") -> Path:
    path = root / "cards" / name
    path.write_text(CARD.format(body=body), encoding="utf-8")
    return path


class LinkTextTest(unittest.TestCase):
    def test_code_span_holding_the_path_becomes_a_link_without_backticks(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            text, count = link_text("разбор `docs/reports/2026-09-12-report.md`, дальше", root)
            self.assertEqual(text, "разбор [[2026-09-12-report]], дальше")
            self.assertEqual(count, 1)

    def test_bare_path_becomes_a_link_and_keeps_the_punctuation_after_it(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            text, _ = link_text("разбор docs/reports/2026-09-12-report.md; жду", root)
            self.assertEqual(text, "разбор [[2026-09-12-report]]; жду")

    def test_path_through_the_home_directory_becomes_a_link(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            text, _ = link_text("`~/obsidian/board/docs/reports/2026-09-12-report.md`.", root)
            self.assertEqual(text, "[[2026-09-12-report]].")

    def test_path_to_a_document_that_does_not_exist_is_left_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            body = "в репозитории `docs/engineering/live-terminal.md` и docs/ru/orchestrator.md"
            self.assertEqual(link_text(body, root), (body, 0))

    def test_code_span_holding_more_than_the_path_is_left_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            body = "`grep -c x docs/reports/2026-09-12-report.md`"
            self.assertEqual(link_text(body, root), (body, 0))

    def test_fenced_block_is_left_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            body = "```bash\ncat docs/reports/2026-09-12-report.md\n```\n"
            self.assertEqual(link_text(body, root), (body, 0))

    def test_snapshot_next_to_a_report_is_not_a_document(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            (root / "docs" / "reports" / "shot.png").write_bytes(b"")
            body = "снимок docs/reports/shot.png"
            self.assertEqual(link_text(body, root), (body, 0))

    def test_existing_link_is_not_linked_twice(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            body = "см. [[2026-09-12-report]]"
            self.assertEqual(link_text(body, root), (body, 0))

    def test_same_name_in_two_directories_gets_the_shortest_unique_name(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            (root / "docs" / "old").mkdir()
            (root / "docs" / "old" / "2026-09-12-report.md").write_text("# Старый\n", encoding="utf-8")
            text, _ = link_text("docs/reports/2026-09-12-report.md", root)
            self.assertEqual(text, "[[reports/2026-09-12-report]]")

    def test_documents_next_to_the_board_are_found_too(self):
        # Раскладка рабочего каталога fleetdeck: <root>/board и <root>/docs рядом.
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "board"
            (root / "cards").mkdir(parents=True)
            (root / ".git").mkdir()
            (Path(tmp) / "docs").mkdir()
            (Path(tmp) / "docs" / "design.md").write_text("# Дизайн\n", encoding="utf-8")
            text, _ = link_text("дизайн в docs/design.md", root)
            self.assertEqual(text, "дизайн в [[design]]")

    def test_rewritten_card_satisfies_the_validator(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            body = "разбор `docs/reports/2026-09-12-report.md` и ещё docs/reports/2026-09-12-report.md"
            text, count = link_text(CARD.format(body=body), root)
            self.assertEqual(count, 2)
            self.assertEqual(validate_doc_paths("card.md", text, root), [])
            self.assertIn("2026-09-12-report", vault_names(root))


class MainTest(unittest.TestCase):
    def run_main(self, *args):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = main(["link_docs.py", *args])
        return code, out.getvalue()

    def test_without_write_nothing_changes_on_disk(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "разбор `docs/reports/2026-09-12-report.md`")
            before = path.read_text(encoding="utf-8")
            code, out = self.run_main("--board", str(root))
            self.assertEqual(code, 0)
            self.assertEqual(path.read_text(encoding="utf-8"), before)
            self.assertIn(path.name, out)

    def test_write_rewrites_and_a_second_run_finds_nothing(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "разбор `docs/reports/2026-09-12-report.md`")
            self.run_main("--board", str(root), "--write")
            self.assertIn("разбор [[2026-09-12-report]]", path.read_text(encoding="utf-8"))
            _, out = self.run_main("--board", str(root), "--write")
            self.assertNotIn(path.name, out)

    def test_frontmatter_is_kept_byte_for_byte(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "docs/reports/2026-09-12-report.md")
            head = path.read_text(encoding="utf-8").split("# Карточка")[0]
            self.run_main("--board", str(root), "--write")
            self.assertTrue(path.read_text(encoding="utf-8").startswith(head))


if __name__ == "__main__":
    unittest.main()

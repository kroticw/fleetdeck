"""Тесты раздачи идентификаторов уже существующим карточкам."""

import tempfile
import unittest
from pathlib import Path

from backfill_ids import assign_missing, fix_links, rename_pending

HEAD = """---
{id_line}zone: planned
stage: {stage}
progress: {progress}
session: "ab12cd34"
created: {created}
---

# Карточка

{body}
"""


def board(tmp: str) -> Path:
    root = Path(tmp)
    for name in ("cards", "archive", ".ids"):
        (root / name).mkdir()
    return root


def put(root, name, *, card_id=None, stage="done", progress="100", created="2026-09-12", body=""):
    path = root / "cards" / name
    path.write_text(
        HEAD.format(
            id_line=f"id: {card_id}\n" if card_id else "",
            stage=stage,
            progress=progress,
            created=created,
            body=body,
        ),
        encoding="utf-8",
    )
    return path


class AssignMissingTest(unittest.TestCase):
    def test_assigns_in_order_of_creation(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root, "b.md", created="2026-09-05")
            put(root, "a.md", created="2026-09-11")
            assigned = assign_missing(root)
            self.assertEqual(assigned, {"b.md": "T-001", "a.md": "T-002"})

    def test_writes_id_as_the_first_frontmatter_line(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "a.md")
            assign_missing(root)
            self.assertEqual(path.read_text(encoding="utf-8").splitlines()[1], "id: T-001")

    def test_keeps_the_rest_of_the_card_byte_for_byte(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "a.md", body="Текст\n\n## Лог\n- запись")
            before = path.read_text(encoding="utf-8")
            assign_missing(root)
            self.assertEqual(path.read_text(encoding="utf-8"), before.replace("---\n", "---\nid: T-001\n", 1))

    def test_is_idempotent(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root, "a.md")
            assign_missing(root)
            self.assertEqual(assign_missing(root), {})

    def test_claims_every_number_in_the_registry(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root, "a.md")
            put(root, "b.md", created="2026-09-13")
            assign_missing(root)
            self.assertEqual({p.name for p in (root / ".ids").iterdir()}, {"T-001", "T-002"})


class RenamePendingTest(unittest.TestCase):
    def test_renames_card_whose_name_lacks_the_id(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root, "2026-09-12-slug.md", card_id="T-004")
            renames = rename_pending(root)
            self.assertEqual(renames, {"2026-09-12-slug.md": "T-004-2026-09-12-slug.md"})
            self.assertTrue((root / "cards" / "T-004-2026-09-12-slug.md").exists())

    def test_leaves_already_renamed_card_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root, "T-004-2026-09-12-slug.md", card_id="T-004")
            self.assertEqual(rename_pending(root), {})

    def test_skips_cards_held_by_a_live_session(self):
        """Переименовать файл под пишущей сессией — потерять её следующую запись."""
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root, "2026-09-12-live.md", card_id="T-004", stage="active", progress="20")
            self.assertEqual(rename_pending(root, skip_stages=("active",)), {})
            self.assertTrue((root / "cards" / "2026-09-12-live.md").exists())

    def test_skips_cards_on_review_by_default(self):
        """Review — не «свободна»: сессия жива и дописывает после приёмки."""
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root, "2026-09-12-r.md", card_id="T-004", stage="review", progress="80")
            self.assertEqual(rename_pending(root), {})

    def test_force_renames_a_named_number_regardless_of_stage(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root, "2026-09-12-a.md", card_id="T-004", stage="active", progress="20")
            put(root, "2026-09-12-b.md", card_id="T-005", stage="active", progress="20")
            renames = rename_pending(root, force=("T-004",))
            self.assertEqual(renames, {"2026-09-12-a.md": "T-004-2026-09-12-a.md"})

    def test_renames_live_card_when_not_skipped(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root, "2026-09-12-live.md", card_id="T-004", stage="active", progress="20")
            self.assertEqual(len(rename_pending(root, skip_stages=())), 1)


class FixLinksTest(unittest.TestCase):
    def test_rewrites_link_to_a_renamed_card(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "a.md", card_id="T-001", body="см. [[2026-09-12-slug]]")
            fix_links(root, {"2026-09-12-slug.md": "T-004-2026-09-12-slug.md"})
            self.assertIn("[[T-004-2026-09-12-slug]]", path.read_text(encoding="utf-8"))

    def test_keeps_alias_and_heading(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "a.md", card_id="T-001", body="[[2026-09-12-slug#Лог|вон та]]")
            fix_links(root, {"2026-09-12-slug.md": "T-004-2026-09-12-slug.md"})
            self.assertIn("[[T-004-2026-09-12-slug#Лог|вон та]]", path.read_text(encoding="utf-8"))

    def test_leaves_unrelated_links_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "a.md", card_id="T-001", body="[[другая-заметка]]")
            fix_links(root, {"2026-09-12-slug.md": "T-004-2026-09-12-slug.md"})
            self.assertIn("[[другая-заметка]]", path.read_text(encoding="utf-8"))

    def test_leaves_examples_in_code_alone(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "a.md", card_id="T-001", body="ссылка `[[2026-09-12-slug]]` в тексте")
            fix_links(root, {"2026-09-12-slug.md": "T-004-2026-09-12-slug.md"})
            self.assertIn("`[[2026-09-12-slug]]`", path.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()

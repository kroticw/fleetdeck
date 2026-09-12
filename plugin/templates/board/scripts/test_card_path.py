"""Тесты поиска карточки по её номеру."""

import tempfile
import unittest
from pathlib import Path

from card_path import find_card, normalize

CARD = """---
id: {card_id}
zone: planned
stage: new
progress: 0
created: 2026-09-12
---

# Карточка
"""


def board(tmp: str) -> Path:
    root = Path(tmp)
    for name in ("cards", "archive"):
        (root / name).mkdir()
    return root


def put(root: Path, where: str, name: str, card_id: str) -> Path:
    path = root / where / name
    path.write_text(CARD.format(card_id=card_id), encoding="utf-8")
    return path


class NormalizeTest(unittest.TestCase):
    def test_pads_a_spoken_number(self):
        """Оператор говорит «T-13», в карточке написано T-013."""
        self.assertEqual(normalize("T-13"), "T-013")

    def test_keeps_a_full_identifier(self):
        self.assertEqual(normalize("T-013"), "T-013")

    def test_accepts_lowercase(self):
        self.assertEqual(normalize("t-13"), "T-013")

    def test_accepts_a_bare_number(self):
        self.assertEqual(normalize("13"), "T-013")

    def test_rejects_nonsense(self):
        for bad in ("", "T-", "FD-13", "тринадцать"):
            with self.subTest(value=bad):
                self.assertIsNone(normalize(bad))


class FindCardTest(unittest.TestCase):
    def test_finds_a_card_on_the_board(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "cards", "T-013-2026-09-12-slug.md", "T-013")
            self.assertEqual(find_card(root, "T-013"), path)

    def test_finds_a_card_whose_filename_is_still_old(self):
        """Ровно тот случай, ради которого поиск идёт по полю, а не по имени."""
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "cards", "2026-09-12-slug.md", "T-013")
            self.assertEqual(find_card(root, "T-013"), path)

    def test_finds_an_archived_card(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "archive", "T-002-old.md", "T-002")
            self.assertEqual(find_card(root, "T-002"), path)

    def test_prefers_the_board_over_the_archive(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            live = put(root, "cards", "T-002-live.md", "T-002")
            put(root, "archive", "T-002-old.md", "T-002")
            self.assertEqual(find_card(root, "T-002"), live)

    def test_returns_none_when_there_is_no_such_card(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.assertIsNone(find_card(board(tmp), "T-999"))

    def test_accepts_a_spoken_number(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "cards", "T-013-2026-09-12-slug.md", "T-013")
            self.assertEqual(find_card(root, "T-13"), path)


if __name__ == "__main__":
    unittest.main()


class FourDigitLookupTest(unittest.TestCase):
    def test_normalizes_a_four_digit_number(self):
        self.assertEqual(normalize("T-1234"), "T-1234")

    def test_finds_a_four_digit_card(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = put(root, "cards", "T-1234-2026-09-12-slug.md", "T-1234")
            self.assertEqual(find_card(root, "1234"), path)

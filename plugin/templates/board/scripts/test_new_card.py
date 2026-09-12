"""Тесты выдачи идентификаторов новым карточкам."""

import tempfile
import unittest
from pathlib import Path

from new_card import IDS_DIR, claim_number, create_card, next_number

CARD = """---
id: {card_id}
zone: planned
stage: new
progress: 0
created: 2026-09-12
---

# Что-то
"""


def board(tmp: str) -> Path:
    """Собрать пустую доску: каталоги cards/ и archive/."""
    root = Path(tmp)
    (root / "cards").mkdir()
    (root / "archive").mkdir()
    return root


def mark(root: Path, *card_ids: str) -> None:
    """Захватить номера в реестре, как это делает скрипт."""
    (root / IDS_DIR).mkdir(exist_ok=True)
    for card_id in card_ids:
        (root / IDS_DIR / card_id).touch()


def put(directory: Path, name: str, card_id: str) -> None:
    """Положить карточку с заданными именем файла и идентификатором."""
    directory.mkdir(parents=True, exist_ok=True)
    (directory / name).write_text(CARD.format(card_id=card_id), encoding="utf-8")


class NextNumberTest(unittest.TestCase):
    def test_empty_board_starts_at_one(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.assertEqual(next_number(board(tmp)), 1)

    def test_takes_maximum_from_cards(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root / "cards", "T-007-a.md", "T-007")
            self.assertEqual(next_number(root), 8)

    def test_takes_maximum_from_archive_too(self):
        """Карточка уезжает в архив, и номер обязан уехать вместе с ней."""
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root / "cards", "T-002-a.md", "T-002")
            put(root / "archive", "T-031-b.md", "T-031")
            self.assertEqual(next_number(root), 32)

    def test_reads_id_from_frontmatter_when_filename_is_legacy(self):
        """Ещё не переименованная карточка всё равно держит свой номер занятым."""
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            put(root / "cards", "2026-09-12-legacy.md", "T-019")
            self.assertEqual(next_number(root), 20)

    def test_registry_holds_the_number_after_the_card_is_gone(self):
        """Маркер вечен: карточку хоть удали, номер переиспользован не будет."""
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            mark(root, "T-001", "T-002", "T-003")
            self.assertEqual(next_number(root), 4)

    def test_ignores_files_without_any_id(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            (root / "archive" / "AGENTS-ARCHIVE.md").write_text("# реестр\n", encoding="utf-8")
            self.assertEqual(next_number(root), 1)


class CreateCardTest(unittest.TestCase):
    def make(self, root: Path, slug: str = "slug", **kwargs) -> Path:
        return create_card(
            root, title="Заголовок", slug=slug, zone="planned", created="2026-09-12", **kwargs
        )

    def test_creates_file_named_by_identifier(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = self.make(root)
            self.assertEqual(path.name, "T-001-2026-09-12-slug.md")

    def test_second_card_gets_next_number(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            self.make(root, slug="first")
            self.assertEqual(self.make(root, slug="second").name, "T-002-2026-09-12-second.md")

    def test_taken_marker_pushes_to_the_next_number(self):
        """Сердце выдачи: номер захвачен — берём следующий, а не падаем."""
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            mark(root, "T-001", "T-002")
            path = self.make(root, start=1)
            self.assertEqual(path.name, "T-003-2026-09-12-slug.md")

    def test_creating_a_card_marks_its_number(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            self.make(root)
            self.assertTrue((root / IDS_DIR / "T-001").exists())

    def test_marker_survives_a_card_with_a_different_slug(self):
        """Тот же номер с другим slug'ом — та самая гонка, которую ловил стресс."""
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            first = self.make(root, slug="один")
            second = self.make(root, slug="два")
            self.assertNotEqual(first.name.split("-2026")[0], second.name.split("-2026")[0])

    def test_claim_is_granted_once(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            self.assertEqual(claim_number(root, 5), 5)
            self.assertEqual(claim_number(root, 5), 6)

    def test_written_card_passes_the_validator(self):
        from validate_cards import validate_card, validate_collection

        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            path = self.make(root, repo="opensource/fleetdeck")
            text = path.read_text(encoding="utf-8")
            self.assertEqual(validate_card(path.name, text), [])
            self.assertEqual(validate_collection([(path.name, text)], vault=set()), [])

    def test_body_carries_title_and_log(self):
        with tempfile.TemporaryDirectory() as tmp:
            text = self.make(board(tmp)).read_text(encoding="utf-8")
            self.assertIn("# Заголовок", text)
            self.assertIn("## Лог", text)


if __name__ == "__main__":
    unittest.main()


class FourDigitNumbersTest(unittest.TestCase):
    """При тридцати карточках в день тысячный номер — вопрос месяца."""

    def test_number_grows_past_three_digits(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            mark(root, "T-999")
            path = create_card(
                root, title="з", slug="slug", zone="planned", created="2026-09-12"
            )
            self.assertEqual(path.name, "T-1000-2026-09-12-slug.md")

    def test_four_digit_marker_is_counted(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            mark(root, "T-1000")
            self.assertEqual(next_number(root), 1001)

    def test_four_digit_id_passes_the_validator(self):
        from validate_cards import validate_card

        with tempfile.TemporaryDirectory() as tmp:
            root = board(tmp)
            mark(root, "T-1233")
            path = create_card(
                root, title="з", slug="slug", zone="planned", created="2026-09-12", start=1234
            )
            self.assertEqual(validate_card(path.name, path.read_text(encoding="utf-8")), [])

"""Tests for the review reply validator."""

import tempfile
import unittest
from pathlib import Path

from validate_review import validate_replies

GOOD = "# Ответы на ревью T-057\n\n## c3\nstatus: fixed\ncommit: 4e1d0aa\n\nОбернул.\n"


class ValidateRepliesTest(unittest.TestCase):
    def test_a_good_file_passes(self):
        self.assertEqual(validate_replies(GOOD), [])

    def test_an_unknown_status_is_named(self):
        errors = validate_replies("## c3\nstatus: done\n\nx\n")
        self.assertTrue(any("status" in e for e in errors), errors)

    def test_a_heading_that_is_not_a_comment_id_is_named(self):
        errors = validate_replies("## третье замечание\nstatus: fixed\n\nx\n")
        self.assertTrue(any("c<number>" in e for e in errors), errors)

    def test_a_section_without_a_status_is_named(self):
        errors = validate_replies("## c3\n\nx\n")
        self.assertTrue(any("status" in e for e in errors), errors)

    def test_an_id_the_operator_never_wrote_is_named_when_comments_are_given(self):
        with tempfile.TemporaryDirectory() as d:
            comments = Path(d) / "comments.json"
            comments.write_text('{"comments": [{"id": "c1"}]}', encoding="utf-8")
            errors = validate_replies("## c9\nstatus: fixed\n\nx\n", comments)
        self.assertTrue(any("c9" in e for e in errors), errors)

    def test_a_malformed_heading_glued_to_the_section_above_is_named(self):
        for bad in ("##c4", "### c4", "#c4"):
            with self.subTest(bad=bad):
                errors = validate_replies(GOOD + "\n" + bad + "\nstatus: fixed\n\nx\n")
                self.assertTrue(any(bad in e for e in errors), errors)

    def test_lines_that_only_start_with_hash_are_not_flagged_as_headings(self):
        for text in (
            "#!/bin/sh\necho hi\n",
            "#include <stdio.h>\n",
            "#123 is the issue\n",
            "#[derive(Debug)]\nstruct S;\n",
        ):
            with self.subTest(text=text):
                errors = validate_replies(GOOD + "\n" + text)
                self.assertEqual(errors, [], errors)

    def test_a_heading_like_line_inside_a_fence_is_ignored(self):
        for fence in ("```", "~~~"):
            with self.subTest(fence=fence):
                text = GOOD + "\n" + fence + "\n### Why\n##c3\n" + fence + "\n"
                errors = validate_replies(text)
                self.assertEqual(errors, [], errors)


if __name__ == "__main__":
    unittest.main()

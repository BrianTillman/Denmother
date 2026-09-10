import subprocess
import tempfile
import unittest
from pathlib import Path

from check_public_source import check


class PublicSourceTests(unittest.TestCase):
    def test_ignored_file_cannot_be_shipped_or_linked(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            (root / "scripts").mkdir()
            (root / ".gitignore").write_text("private.md\n")
            (root / "private.md").write_text("Local development record\n")
            (root / "README.md").write_text("[record](private.md)\n")
            (root / "scripts/release-assets.txt").write_text("private.md\n")
            errors = check(root)
            self.assertEqual(len(errors), 2, errors)
            self.assertTrue(any("release asset" in error for error in errors))
            self.assertTrue(any("link target" in error for error in errors))

    def test_public_relative_links_and_external_links(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            (root / "scripts").mkdir()
            (root / "docs").mkdir()
            (root / "README.md").write_text("[guide](docs/guide.md#usage)\n")
            (root / "docs/guide.md").write_text(
                "[home](../README.md) [web](https://example.org)\n"
                "```md\n[example](missing.md)\n```\n"
            )
            (root / "scripts/release-assets.txt").write_text("README.md\ndocs/guide.md\n")
            self.assertEqual(check(root), [])

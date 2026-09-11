import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from acceptance import require_test_events
import precommit


class RequiredAcceptanceTests(unittest.TestCase):
    def test_missing_skipped_and_incomplete_runs_are_failures(self):
        for events in ([], [{"Action": "pass", "Package": "cmd"}],
                       [{"Action": "skip", "Test": "Required"}],
                       [{"Action": "pass", "Test": "Required"}]):
            with self.subTest(events=events), self.assertRaises(RuntimeError):
                require_test_events(events, "Required", 2)

    def test_skipped_subtest_cannot_hide_under_passing_parent(self):
        with self.assertRaises(RuntimeError):
            require_test_events([{"Action": "skip", "Test": "Required/case"},
                                 {"Action": "pass", "Test": "Required"}], "Required", 1)

    def test_every_repetition_must_pass(self):
        require_test_events([{"Action": "pass", "Test": "Required"}] * 5, "Required", 5)
        with self.assertRaises(RuntimeError):
            require_test_events([], "Required", 0)


class SnapshotTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "source with spaces"
        self.root.mkdir()
        self.patch = patch.object(precommit, "ROOT", self.root)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        precommit.git("init", "-q")
        precommit.git("config", "core.hooksPath", "/dev/null")
        (self.root / ".gitignore").write_text("private.txt\n")
        (self.root / "file.txt").write_text("staged\n")
        precommit.git("add", ".")

    def destination(self, name="snapshot"):
        path = Path(self.temp.name) / name
        path.mkdir()
        return path

    def test_staged_snapshot_excludes_unstaged_fixes_and_private_files(self):
        before = (self.root / ".git/index").read_bytes()
        (self.root / "file.txt").write_text("unstaged fix\n")
        (self.root / "extra.txt").write_text("untracked\n")
        (self.root / "private.txt").write_text("private\n")
        target = self.destination()
        precommit.snapshot(target)
        self.assertEqual((target / "file.txt").read_text(), "staged\n")
        self.assertFalse((target / "extra.txt").exists())
        self.assertFalse((target / "private.txt").exists())
        self.assertEqual((self.root / "file.txt").read_text(), "unstaged fix\n")
        self.assertEqual((self.root / ".git/index").read_bytes(), before)

    def test_alternate_commit_index_is_honored_and_not_inherited(self):
        alternate = Path(self.temp.name) / "alternate-index"
        alternate.write_bytes((self.root / ".git/index").read_bytes())
        with patch.dict(os.environ, {"GIT_INDEX_FILE": str(alternate),
                                    "HASS_DEV_URL": "http://unrelated.invalid",
                                    "DM_DEV_HA_IMAGE": "unrelated-image"}):
            (self.root / "file.txt").write_text("alternate staged value\n")
            precommit.git("add", "file.txt")
            before = alternate.read_bytes()
            target = self.destination()
            env = precommit.snapshot(target)
            self.assertNotIn("GIT_INDEX_FILE", env)
            self.assertNotIn("HASS_DEV_URL", env)
            self.assertNotIn("DM_DEV_HA_IMAGE", env)
            self.assertEqual((target / "file.txt").read_text(), "alternate staged value\n")
            self.assertEqual(alternate.read_bytes(), before)
        target = self.destination("normal")
        precommit.snapshot(target)
        self.assertEqual((target / "file.txt").read_text(), "staged\n")

    def test_worktree_preview_includes_new_public_files_and_deletions(self):
        (self.root / "file.txt").unlink()
        (self.root / "new.txt").write_text("new\n")
        (self.root / "private.txt").write_text("private\n")
        target = self.destination()
        precommit.snapshot(target, worktree=True)
        self.assertFalse((target / "file.txt").exists())
        self.assertEqual((target / "new.txt").read_text(), "new\n")
        self.assertFalse((target / "private.txt").exists())

    def test_staged_failure_blocks_gate_despite_unstaged_success(self):
        scripts = self.root / "scripts"
        scripts.mkdir()
        gate = scripts / "precommit.py"
        gate.write_text("import sys\nprint('failed staged check')\nsys.exit(7)\n")
        precommit.git("add", "scripts/precommit.py")
        gate.write_text("import sys\nsys.exit(0)\n")
        target = self.destination()
        with patch.object(sys, "argv", ["precommit.py"]), \
                patch.object(precommit.tempfile, "mkdtemp", return_value=str(target)):
            with self.assertRaises(subprocess.CalledProcessError) as raised:
                precommit.main()
        self.assertEqual(raised.exception.returncode, 7)
        self.assertTrue(target.exists(), "failure evidence must be retained")
        self.assertIn("failed staged check", (target / "precommit.log").read_text())

    def test_install_preserves_an_existing_hook(self):
        with self.assertRaisesRegex(RuntimeError, "existing Git hook"):
            precommit.install()
        self.assertEqual(precommit.git("config", "--get", "core.hooksPath"), "/dev/null")

    def test_installed_hook_blocks_git_commit_when_gate_fails(self):
        precommit.git("config", "--unset", "core.hooksPath")
        hooks = self.root / ".githooks"
        hooks.mkdir()
        hook_source = Path(__file__).resolve().parent.parent / ".githooks/pre-commit"
        (hooks / "pre-commit").write_text(hook_source.read_text())
        scripts = self.root / "scripts"
        scripts.mkdir()
        (scripts / "precommit.py").write_text("import sys\nprint('required gate failed')\nsys.exit(23)\n")
        precommit.git("add", ".")
        precommit.install()
        result = subprocess.run(
            ["git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
             "-c", "commit.gpgSign=false", "commit", "-qm", "Must be blocked"],
            cwd=self.root, text=True, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("required gate failed", result.stdout + result.stderr)
        result = subprocess.run(["git", "rev-parse", "--verify", "HEAD"], cwd=self.root,
                                capture_output=True)
        self.assertNotEqual(result.returncode, 0, "failed gate must not create a commit")


if __name__ == "__main__":
    unittest.main()

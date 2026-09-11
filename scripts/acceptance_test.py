import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import sys
import unittest
from unittest.mock import patch

import acceptance


class RequiredTests(unittest.TestCase):
    def test_expected_diagnostic_failure_must_really_fail(self):
        with contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(RuntimeError, "expected 1"):
                acceptance.run(sys.executable, "-c", "pass", expected_exit=1)
            output = acceptance.run(sys.executable, "-c", "print('missing lamp'); raise SystemExit(1)", expected_exit=1)
            self.assertIn("missing lamp", output)

    def test_missing_skipped_and_partial_runs_fail(self):
        for events in ([], [{"Action": "pass", "Package": "cmd"}],
                       [{"Action": "skip", "Test": "Required"}],
                       [{"Action": "pass", "Test": "Required"}]):
            with self.subTest(events=events), self.assertRaises(RuntimeError):
                acceptance.require_test_events(events, "Required", 2)

    def test_skipped_subtest_fails_even_if_parent_passes(self):
        with self.assertRaises(RuntimeError):
            acceptance.require_test_events([{"Action": "skip", "Test": "Required/case"},
                                            {"Action": "pass", "Test": "Required"}], "Required", 1)

    def test_each_named_test_and_repetition_must_execute(self):
        acceptance.require_test_events([{"Action": "pass", "Test": "A"},
                                        {"Action": "pass", "Test": "B"}], ["A", "B"], 1)
        with self.assertRaises(RuntimeError):
            acceptance.require_test_events([{"Action": "pass", "Test": "A"}], ["A", "B"], 1)
        for names, count in (([], 1), (["A"], 0)):
            with self.assertRaises(RuntimeError):
                acceptance.require_test_events([], names, count)

    def test_missing_yaml_blocks_source_before_tests_can_skip(self):
        with patch.object(acceptance.importlib.util, "find_spec", return_value=None), \
                patch.object(acceptance, "require"), patch.object(acceptance, "run") as run:
            with self.assertRaisesRegex(acceptance.PrerequisiteError, "PyYAML"):
                acceptance.source()
            run.assert_not_called()


class ReportTests(unittest.TestCase):
    def test_blocked_dependency_is_incomplete_and_other_checks_continue(self):
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
            report = acceptance.Report(Path(directory), ["source", "runtime", "browser"], "preflight")
            def unavailable():
                raise acceptance.PrerequisiteError("Docker is unavailable")
            report.check("source", unavailable)
            report.check("runtime", lambda: self.fail("dependency should prevent execution"), ("source",))
            report.check("browser", lambda: print("browser checked"))
            self.assertEqual(report.finish(), 1)
            data = json.loads((Path(directory) / "report.json").read_text())
            self.assertEqual(data["status"], "incomplete")
            self.assertEqual([check["status"] for check in data["checks"]], ["blocked", "not_run", "passed"])
            self.assertIn("browser checked", (Path(directory) / "browser.log").read_text())

    def test_failure_and_interruption_never_report_success(self):
        for exception in (RuntimeError("broken"), KeyboardInterrupt()):
            with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
                report = acceptance.Report(Path(directory), ["runtime"], "runtime")
                def fail():
                    raise exception
                if isinstance(exception, KeyboardInterrupt):
                    with self.assertRaises(KeyboardInterrupt):
                        report.check("runtime", fail)
                else:
                    report.check("runtime", fail)
                self.assertEqual(report.finish(), 1)
                self.assertEqual(report.data["status"], "failed")

    def test_only_completed_passes_produce_success(self):
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
            report = acceptance.Report(Path(directory), ["one", "two"], "preflight")
            report.check("one", lambda: None)
            self.assertEqual(report.finish(), 1)
            report.check("two", lambda: None)
            self.assertEqual(report.finish(), 0)


class RuntimeTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        for name in ("quickstart", "automations", "dashboard"):
            config = self.root / "examples" / name / "ha-config"
            config.mkdir(parents=True)
            (config / "configuration.yaml").write_text("input_boolean: {}\n")
            automation = config / "automations/presence/motion_lamp.yaml"
            automation.parent.mkdir(parents=True)
            automation.write_text("entity_id: input_boolean.study_lamp\n")
            private = config / ".storage"
            private.mkdir()
            (private / "auth").write_text("must not copy")
            (config.parent / ".denmother.yaml").write_text("dev_fixtures: fixtures.json\n")
            (config.parent / "fixtures.json").write_text('{"entities":{}}')
            (config.parent / "scenarios.json").write_text('{"warm":{}}')
        (self.root / "dm").touch()
        for target, value in (("ROOT", self.root), ("BINARY", self.root / "dm")):
            patcher = patch.object(acceptance, target, value)
            patcher.start()
            self.addCleanup(patcher.stop)
        patcher = patch.object(acceptance, "docker_prerequisites")
        patcher.start()
        self.addCleanup(patcher.stop)
        patcher = patch.object(acceptance, "require")
        patcher.start()
        self.addCleanup(patcher.stop)

    def test_each_version_uses_fresh_state_and_cleans_its_own_volumes(self):
        configs, commands = [], []
        def run(*args, env=None, expected_exit=0):
            commands.append(args)
            if "up" in args:
                config = Path(args[args.index("--config") + 1])
                self.assertFalse((config / ".storage").exists())
                self.assertEqual((config.parent / ".denmother.yaml").read_text(), "dev_fixtures: fixtures.json\n")
                self.assertEqual((config.parent / "fixtures.json").read_text(), '{"entities":{}}')
                self.assertEqual((config.parent / "scenarios.json").read_text(), '{"warm":{}}')
                self.assertNotIn("HASS_DEV_URL", env)
                self.assertNotIn("DM_DEV_HOST_REPO_ROOT", env)
                configs.append(config)
                compose = config.parent / ".devcontainer/worktrees/fixture/docker-compose.yml"
                compose.parent.mkdir(parents=True)
                compose.touch()
                self.assertEqual((config / "configuration.yaml").read_text(), "input_boolean: {}\n")
            if expected_exit:
                return "study_missing_lamp"
        with patch.object(acceptance, "run", side_effect=run), \
                patch.object(acceptance, "required_go_test"), patch.object(acceptance, "storage"), \
                patch.dict(os.environ, {"HASS_DEV_URL": "http://unrelated.invalid", "DM_DEV_HOST_REPO_ROOT": "/private"}):
            for version in acceptance.CONFIG["home_assistant"]:
                acceptance.runtime(version, self.root / "evidence" / version)
        self.assertEqual(len(set(configs)), 3 * len(acceptance.CONFIG["home_assistant"]))
        self.assertTrue(all(not config.exists() for config in configs))
        cleanup = [args for args in commands if args[0] == "docker"]
        self.assertEqual(len(cleanup), len(configs))
        self.assertTrue(all("--volumes" in args for args in cleanup))
        self.assertTrue((self.root / "examples/dashboard/ha-config/.storage/auth").exists())

    def test_failure_preserves_evidence_and_still_cleans(self):
        paths = []
        def run(*args, env=None):
            if "up" in args:
                config = Path(args[args.index("--config") + 1])
                paths.append(config)
                artifact = config.parent / "artifacts/dashboard-render/view/result.json"
                artifact.parent.mkdir(parents=True)
                screenshot = artifact.with_suffix(".png")
                screenshot.write_bytes(b"fixture")
                artifact.write_text(json.dumps({"failure": "expected", "views": [{"screenshot": str(screenshot)}]}))
                raise RuntimeError("startup failed")
        with patch.object(acceptance, "run", side_effect=run), \
                patch.object(acceptance, "cleanup_runtime") as cleanup:
            with self.assertRaisesRegex(RuntimeError, "startup failed"):
                acceptance.runtime(acceptance.CONFIG["home_assistant"][0], self.root / "evidence")
            cleanup.assert_called_once()
        self.assertFalse(paths[0].exists())
        self.assertTrue((self.root / "evidence/quickstart/view/result.json").exists())
        saved = json.loads((self.root / "evidence/quickstart/view/result.json").read_text())
        self.assertEqual(Path(saved["views"][0]["screenshot"]).read_bytes(), b"fixture")

    def test_cleanup_attempts_every_project_even_when_one_fails(self):
        for name in ("a", "b"):
            compose = self.root / name / ".devcontainer/worktrees/project/docker-compose.yml"
            compose.parent.mkdir(parents=True)
            compose.touch()
        with patch.object(acceptance, "run", side_effect=[OSError("cleanup failed"), None]) as run:
            with self.assertRaisesRegex(RuntimeError, "cleanup failed"):
                acceptance.cleanup_runtime(self.root, {})
            self.assertEqual(run.call_count, 2)


if __name__ == "__main__":
    unittest.main()

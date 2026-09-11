"""Local preflight and CI acceptance using the same checks and HA versions."""

import argparse
import contextlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent
CONFIG = json.loads((ROOT / "scripts/acceptance.json").read_text())
BINARY = ROOT / "dm"


class PrerequisiteError(RuntimeError):
    pass


def require(*programs):
    missing = [program for program in programs if not shutil.which(program)]
    if missing:
        raise PrerequisiteError("missing prerequisite(s): " + ", ".join(missing))


def require_python_yaml():
    if importlib.util.find_spec("yaml") is None:
        raise PrerequisiteError("PyYAML is required; install python3-yaml in the devcontainer")


def run(*args, env=None, expected_exit=0):
    command = list(map(str, args))
    print("+ " + " ".join(command), flush=True)
    output = []
    with subprocess.Popen(command, cwd=ROOT, env=env, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, text=True) as child:
        for line in child.stdout:
            print(line, end="", flush=True)
            if expected_exit:
                output.append(line)
        if child.wait() != expected_exit:
            raise RuntimeError(f"command exited {child.returncode}, expected {expected_exit}: {' '.join(command)}")
    return "".join(output)


def require_test_events(events, names, count):
    if isinstance(names, str):
        names = [names]
    if count < 1 or not names:
        raise RuntimeError("required tests and repetition count must be nonempty and positive")
    skipped = [e["Test"] for e in events if e.get("Action") == "skip" and "Test" in e]
    for name in names:
        passed = sum(e.get("Test") == name and e.get("Action") == "pass" for e in events)
        if passed != count or skipped:
            raise RuntimeError(f"required {name}: {passed}/{count} passes; skipped tests: {skipped}")


def required_go_test(names, count=1, env=None, package="./cmd"):
    if isinstance(names, str):
        names = [names]
    pattern = "^(" + "|".join(re.escape(name) for name in names) + ")$"
    command = ["go", "test", "-json", "-race", package, "-run", pattern, f"-count={count}"]
    print("+ " + " ".join(command), flush=True)
    events = []
    with subprocess.Popen(command, cwd=ROOT, env=env, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, text=True) as child:
        for line in child.stdout:
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                print(line, end="", flush=True)
                continue
            events.append(event)
            if "Output" in event:
                print(event["Output"], end="", flush=True)
        if child.wait():
            raise subprocess.CalledProcessError(child.returncode, command)
    require_test_events(events, names, count)


def source():
    require("go", "gofmt", "git", "python3")
    require_python_yaml()
    names = subprocess.check_output(["git", "ls-files", "--cached", "--others",
                                     "--exclude-standard", "-z", "--", "*.go"], cwd=ROOT)
    files = [os.fsdecode(name) for name in names.split(b"\0") if name and (ROOT / os.fsdecode(name)).is_file()]
    unformatted = subprocess.check_output(["gofmt", "-l", *sorted(set(files))], cwd=ROOT, text=True).strip()
    if unformatted:
        raise RuntimeError("run gofmt on:\n" + unformatted)
    run("go", "build", "-o", BINARY, ".")
    # Ensure preparation tests actually execute, including all their subtests.
    tests = re.findall(r"^func (TestPortableAssets\w+)\(", (ROOT / "cmd/dev_assets_test.go").read_text(), re.M)
    required_go_test(tests)
    run(BINARY, "release", "check", "--json")
    run("python3", "-B", "scripts/check_public_source.py")


def docker_prerequisites():
    require("docker")
    try:
        run("docker", "info", "--format", "{{.ServerVersion}}")
        run("docker", "compose", "version")
    except RuntimeError as error:
        raise PrerequisiteError("a reachable Docker daemon and Compose v2 are required") from error


def browser(with_deps=False):
    require("go", "node", "npm")
    source_text = (ROOT / "cmd/dev_dashboard.go").read_text()
    match = re.search(r'const dashboardRenderPlaywrightVersion = "([0-9.]+)"', source_text)
    if not match:
        raise RuntimeError("cannot determine the renderer's Playwright version")
    version = match[1]
    cache = Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache"))
    runner = (cache / "denmother" / f"playwright-{version}").resolve()
    metadata = runner / "node_modules/playwright/package.json"
    if not metadata.exists() or json.loads(metadata.read_text()).get("version") != version:
        run("npm", "install", "--prefix", runner, "--no-audit", "--no-fund", f"playwright@{version}")
    cli = runner / "node_modules/playwright/cli.js"
    run("node", cli, "install", *(["--with-deps"] if with_deps else []), "chromium")
    env = dict(os.environ, DENMOTHER_TEST_PLAYWRIGHT_DIR=str(runner))
    required_go_test("TestDashboardBrowserAcceptance", env=env)


def storage(config, env=None):
    config = Path(config).resolve()
    if not (config / "configuration.yaml").is_file():
        raise RuntimeError(f"missing live dashboard config: {config}")
    env = dict(env or os.environ, DENMOTHER_TEST_STORAGE_CONFIG=str(config))
    required_go_test("TestStorageDashboardLiveAcceptance", CONFIG["storage_repetitions"], env)


def runtime_environment(version):
    env = dict(os.environ)
    # Acceptance always targets its own synthetic configuration and image.
    for name in list(env):
        if name.startswith(("HASS_", "DM_", "DENMOTHER_TEST_", "DENMOTHER_RUNTIME_TEST_")):
            env.pop(name)
    env["DM_DEV_HA_IMAGE"] = f"ghcr.io/home-assistant/home-assistant:{version}"
    return env


def copy_runtime_evidence(directory, evidence):
    for project in directory.iterdir():
        renders = project / "artifacts/dashboard-render"
        if renders.is_dir():
            destination = evidence / project.name
            shutil.copytree(renders, destination, dirs_exist_ok=True,
                            ignore=shutil.ignore_patterns("playwright-runner", "node_modules"))
            # The temporary runtime is removed; saved reports must point to the
            # preserved screenshots rather than the deleted originals.
            def relocate(value):
                if isinstance(value, dict):
                    screenshot = value.get("screenshot")
                    if isinstance(screenshot, str) and screenshot.startswith(str(renders) + os.sep):
                        value["screenshot"] = str(destination / Path(screenshot).relative_to(renders))
                    for child in value.values():
                        relocate(child)
                elif isinstance(value, list):
                    for child in value:
                        relocate(child)
            for path in destination.rglob("*.json"):
                try:
                    value = json.loads(path.read_text())
                except (UnicodeError, json.JSONDecodeError):
                    continue  # Keep partial failure evidence verbatim.
                relocate(value)
                path.write_text(json.dumps(value, indent=2) + "\n")


def cleanup_runtime(directory, env):
    errors = []
    # Every compose project here was created in this invocation's fresh directory.
    for compose in sorted(directory.glob("*/.devcontainer/worktrees/*/docker-compose.yml")):
        try:
            run("docker", "compose", "-p", compose.parent.name, "-f", compose,
                "down", "--volumes", "--remove-orphans", env=env)
        except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
            errors.append(str(error))
    if errors:
        raise RuntimeError("runtime cleanup failed: " + "; ".join(errors))


def runtime(version, evidence):
    require("go", "node", "npm")
    docker_prerequisites()
    if not BINARY.is_file():
        raise PrerequisiteError("build ./dm first, or run the full preflight")
    env = runtime_environment(version)
    directory = Path(tempfile.mkdtemp(prefix=f"denmother-acceptance-{version}-"))
    errors = []
    try:
        for name in ("quickstart", "automations", "dashboard"):
            project = directory / name
            # Copy only public example inputs; never reuse runtime state or volumes.
            for child in ("ha-config", "docs"):
                origin = ROOT / "examples" / name / child
                if origin.is_dir():
                    shutil.copytree(origin, project / child,
                                    ignore=shutil.ignore_patterns(".*", "artifacts", "node_modules", "__pycache__"))
            for filename in (".denmother.yaml", "fixtures.json", "scenarios.json"):
                origin = ROOT / "examples" / name / filename
                if origin.is_file():
                    shutil.copy2(origin, project / filename)
            config = project / "ha-config"
            run(BINARY, "dev", "up", "--config", config, "--json", env=env)
            if name != "dashboard":
                run(BINARY, "--config", config, "--json", env=env)
                run(BINARY, "test", "--config", config, "--json", env=env)
            if name == "quickstart":
                automation = config / "automations/presence/motion_lamp.yaml"
                automation.write_text(automation.read_text().replace("input_boolean.study_lamp", "input_boolean.study_missing_lamp"))
                output = run(BINARY, "--config", config, "--no-schema", env=env, expected_exit=1)
                if "study_missing_lamp" not in output:
                    raise RuntimeError("intentional missing-entity check did not report the missing lamp")
            if name == "automations":
                required_go_test("TestNativeRuntimeTargetsAndCleanup", env=dict(
                    env, DENMOTHER_RUNTIME_TEST_CONFIG=str(config)), package="./internal/hatest")
            if name == "dashboard":
                run(BINARY, "dev", "dashboard", "denmother-demo", "--config", config,
                    "--render", "--require-running", "--json", env=env)
                storage(config, env)
                run(BINARY, "dev", "scenario", "warm", "--config", config, "--json", env=env)
                run(BINARY, "dev", "dashboard", "denmother-demo", "--config", config,
                    "--render", "--view", "details", "--require-running", "--json", env=env)
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        errors.append(str(error))
    finally:
        try:
            copy_runtime_evidence(directory, evidence)
        except OSError as error:
            errors.append(f"could not preserve render evidence: {error}")
        try:
            cleanup_runtime(directory, env)
        except (OSError, RuntimeError) as error:
            errors.append(str(error))
            print(f"Runtime files retained for cleanup: {directory}", flush=True)
        else:
            shutil.rmtree(directory)
    if errors:
        raise RuntimeError("; ".join(errors))


def package():
    run(BINARY, "release", "build", "--version", "0.1.0-rc.1",
        "--homebrew-repository", "BrianTillman/Denmother")


def homebrew(archives, native=False):
    archives = Path(archives).resolve()
    if not (archives / "homebrew/Formula/denmother.rb").is_file():
        raise PrerequisiteError(f"missing generated formula in {archives}")
    script = ROOT / "scripts/homebrew-acceptance.sh"
    if native:
        require("brew", "bash")
        run("bash", script, archives)
    else:
        docker_prerequisites()
        run("docker", "run", "--rm", "--init", "--platform", "linux/amd64",
            "--mount", f"type=bind,src={archives},dst=/archives,readonly",
            "--mount", f"type=bind,src={script},dst=/homebrew-acceptance.sh,readonly",
            CONFIG["homebrew_image"], "bash", "/homebrew-acceptance.sh", "/archives")


class Tee:
    def __init__(self, *streams):
        self.streams = streams

    def write(self, value):
        for stream in self.streams:
            stream.write(value)
        return len(value)

    def flush(self):
        for stream in self.streams:
            stream.flush()


class Report:
    def __init__(self, directory, names, scope):
        self.directory = directory.resolve()
        self.directory.mkdir(parents=True, exist_ok=True)
        self.data = {"schema_version": "denmother.preflight.v1", "scope": scope,
                     "status": "incomplete", "checks": [
                         {"name": name, "status": "not_run"} for name in names],
                     "coverage": "Local Homebrew uses Linux amd64; native macOS Homebrew and the OS/CPU matrix run in CI."}
        self.write()

    def write(self):
        (self.directory / "report.json").write_text(json.dumps(self.data, indent=2) + "\n")

    def check(self, name, function, dependencies=()):
        entry = next(item for item in self.data["checks"] if item["name"] == name)
        statuses = {item["name"]: item["status"] for item in self.data["checks"]}
        if any(statuses.get(dep) != "passed" for dep in dependencies):
            entry["detail"] = "required earlier check did not pass: " + ", ".join(dependencies)
            self.write()
            return
        started = time.monotonic()
        logfile = self.directory / (name.replace(":", "-") + ".log")
        entry["log"] = str(logfile)
        entry["status"] = "running"
        self.write()
        try:
            with logfile.open("w") as log, contextlib.redirect_stdout(Tee(sys.stdout, log)), contextlib.redirect_stderr(Tee(sys.stderr, log)):
                function()
            entry["status"] = "passed"
        except PrerequisiteError as error:
            entry.update(status="blocked", detail=str(error))
        except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
            entry.update(status="failed", detail=str(error))
        except BaseException:
            entry.update(status="failed", detail="interrupted")
            raise
        finally:
            entry["duration_seconds"] = round(time.monotonic() - started, 2)
            self.write()

    def finish(self):
        statuses = {item["status"] for item in self.data["checks"]}
        self.data["status"] = "passed" if statuses == {"passed"} else "failed" if "failed" in statuses else "incomplete"
        self.write()
        for item in self.data["checks"]:
            print(f"{item['status'].upper():8} {item['name']}" + (": " + item["detail"] if "detail" in item else ""))
        print(f"Report: {self.directory / 'report.json'}")
        print(self.data["coverage"])
        return 0 if self.data["status"] == "passed" else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("matrix")
    preflight = commands.add_parser("preflight", help="Run source, browser, all HA versions, packaging and Linux Homebrew")
    preflight.add_argument("--with-deps", action="store_true", help="Let Playwright install Chromium's system libraries")
    preflight.add_argument("--report-dir", type=Path, default=ROOT / "artifacts/preflight")
    commands.add_parser("source")
    commands.add_parser("browser").add_argument("--with-deps", action="store_true")
    commands.add_parser("storage").add_argument("--config", required=True)
    live = commands.add_parser("runtime")
    live.add_argument("--ha", choices=CONFIG["home_assistant"], help="One HA version; defaults to the complete CI matrix")
    live.add_argument("--report-dir", type=Path, default=ROOT / "artifacts/preflight")
    brew = commands.add_parser("homebrew")
    brew.add_argument("--archives", default="dist")
    brew.add_argument("--native", action="store_true", help="Use Homebrew on a disposable native CI runner")
    args = parser.parse_args()
    if args.command == "matrix":
        print(json.dumps(CONFIG["home_assistant"]))
    elif args.command == "source":
        source()
    elif args.command == "browser":
        browser(args.with_deps)
    elif args.command == "storage":
        storage(args.config)
    elif args.command == "homebrew":
        homebrew(args.archives, args.native)
    else:
        versions = CONFIG["home_assistant"] if args.command == "preflight" or args.ha is None else [args.ha]
        runtime_names = ["runtime:" + version for version in versions]
        names = ["source", "browser", *runtime_names, "package", "homebrew"] if args.command == "preflight" else runtime_names
        directory = args.report_dir / (time.strftime("%Y%m%dT%H%M%S") + "-" + str(os.getpid()))
        report = Report(directory, names, args.command)
        try:
            if args.command == "preflight":
                report.check("source", source)
                report.check("browser", lambda: browser(args.with_deps))
            for version in versions:
                report.check("runtime:" + version, lambda version=version: runtime(version, report.directory / version),
                             ("source", "browser") if args.command == "preflight" else ())
            if args.command == "preflight":
                report.check("package", package, ("source",))
                report.check("homebrew", lambda: homebrew(ROOT / "dist"), ("package",))
        finally:
            result = report.finish()
        return result
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        sys.exit(f"Acceptance failed: {error}")

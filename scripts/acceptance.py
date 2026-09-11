"""Required browser and Homebrew acceptance, shared by local checks and CI."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
CONFIG = json.loads((ROOT / "scripts/acceptance.json").read_text())


def run(*args, env=None):
    print("+ " + " ".join(map(str, args)), flush=True)
    subprocess.run(list(map(str, args)), cwd=ROOT, env=env, check=True)


def require_test_events(events, name, count):
    if count < 1:
        raise RuntimeError("required test repetition count must be positive")
    passed = sum(e.get("Test") == name and e.get("Action") == "pass" for e in events)
    skipped = [e["Test"] for e in events if e.get("Action") == "skip" and "Test" in e]
    if passed != count or skipped:
        raise RuntimeError(f"required {name}: {passed}/{count} passes; skipped tests: {skipped}")


def required_go_test(name, count, env):
    command = ["go", "test", "-json", "-race", "./cmd", "-run", f"^{name}$", f"-count={count}"]
    print("+ " + " ".join(command), flush=True)
    events = []
    with subprocess.Popen(command, cwd=ROOT, env=env, stdout=subprocess.PIPE, text=True) as child:
        for line in child.stdout:
            event = json.loads(line)
            events.append(event)
            if "Output" in event:
                print(event["Output"], end="", flush=True)
        if child.wait():
            raise subprocess.CalledProcessError(child.returncode, command)
    require_test_events(events, name, count)


def browser(with_deps=False):
    # Use the renderer's pin rather than maintaining a second Playwright version.
    source = (ROOT / "cmd/dev_dashboard.go").read_text()
    version = re.search(r'const dashboardRenderPlaywrightVersion = "([^"]+)"', source)[1]
    cache = Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache"))
    runner = cache / "denmother" / f"playwright-{version}"
    metadata = runner / "node_modules/playwright/package.json"
    if not metadata.exists() or json.loads(metadata.read_text()).get("version") != version:
        run("npm", "install", "--prefix", runner, "--no-audit", "--no-fund", f"playwright@{version}")
    cli = runner / "node_modules/playwright/cli.js"
    run("node", cli, "install", *(["--with-deps"] if with_deps else []), "chromium")
    env = dict(os.environ, DENMOTHER_TEST_PLAYWRIGHT_DIR=str(runner))
    required_go_test("TestDashboardBrowserAcceptance", 1, env)


def storage(config):
    config = Path(config).resolve()
    if not (config / "configuration.yaml").is_file():
        raise RuntimeError(f"missing live dashboard config: {config}")
    env = dict(os.environ, DENMOTHER_TEST_STORAGE_CONFIG=str(config))
    required_go_test("TestStorageDashboardLiveAcceptance", CONFIG["storage_repetitions"], env)


def homebrew(archives, native=False):
    archives = Path(archives).resolve()
    if not (archives / "homebrew/Formula/denmother.rb").is_file():
        raise RuntimeError(f"missing generated formula in {archives}")
    script = ROOT / "scripts/homebrew-acceptance.sh"
    if native:
        run("bash", script, archives)
    else:
        # A disposable Linux Homebrew keeps the developer's installed packages intact.
        run("docker", "run", "--rm", "--init", "--platform", "linux/amd64",
            "--mount", f"type=bind,src={archives},dst=/archives,readonly",
            "--mount", f"type=bind,src={script},dst=/homebrew-acceptance.sh,readonly",
            CONFIG["homebrew_image"], "bash", "/homebrew-acceptance.sh", "/archives")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("browser").add_argument("--with-deps", action="store_true")
    commands.add_parser("storage").add_argument("--config", required=True)
    brew = commands.add_parser("homebrew")
    brew.add_argument("--archives", default="dist")
    brew.add_argument("--native", action="store_true", help="Use Homebrew on a disposable native CI runner")
    commands.add_parser("matrix")
    args = parser.parse_args()
    if args.command == "browser":
        browser(args.with_deps)
    elif args.command == "storage":
        storage(args.config)
    elif args.command == "homebrew":
        homebrew(args.archives, args.native)
    else:
        print(json.dumps(CONFIG["home_assistant"]))


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        sys.exit(f"Acceptance failed: {error}")

"""Check the exact proposed commit in an isolated checkout, without stashing edits."""

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def git(*args, root=None, env=None):
    return subprocess.check_output(["git", *args], cwd=root or ROOT, env=env).decode().strip()


def snapshot(destination, worktree=False):
    # checkout-index honors Git's alternate index during partial/pathspec commits.
    if worktree:
        names = subprocess.check_output(
            ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=ROOT)
        for name in set(filter(None, names.split(b"\0"))):
            source = ROOT / os.fsdecode(name)
            target = destination / os.fsdecode(name)
            if source.is_symlink():
                target.parent.mkdir(parents=True, exist_ok=True)
                target.symlink_to(os.readlink(source))
            elif source.is_file():
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, target)
    else:
        git("checkout-index", "--all", f"--prefix={destination}{os.sep}")
    # Git exports repository/index variables to hooks. Do not let them redirect
    # snapshot commands (or tests that create repos) back into the user's repo.
    env = os.environ.copy()
    for name in git("rev-parse", "--local-env-vars").splitlines():
        env.pop(name, None)
    for name in list(env):
        if name.startswith(("HASS_", "DM_", "DENMOTHER_TEST_", "DENMOTHER_RUNTIME_TEST_")):
            env.pop(name)
    git("init", "-q", root=destination, env=env)
    git("config", "core.hooksPath", "/dev/null", root=destination, env=env)
    git("add", "--all", root=destination, env=env)
    git("-c", "user.name=Denmother checks", "-c", "user.email=checks@example.invalid",
        "-c", "commit.gpgSign=false", "commit", "-qm", "Acceptance snapshot", root=destination, env=env)
    return env


def check():
    from acceptance import CONFIG, browser, homebrew, run, storage

    for program in ("go", "gofmt", "git", "python3", "node", "npm", "docker", "bash"):
        if not shutil.which(program):
            raise RuntimeError(f"missing prerequisite: {program}; see CONTRIBUTING.md")
    run("docker", "info", "--format", "{{.ServerVersion}}")
    run("docker", "compose", "version")
    go_files = git("ls-files", "*.go").splitlines()
    unformatted = subprocess.check_output(["gofmt", "-l", *go_files], cwd=ROOT).decode().strip()
    if unformatted:
        raise RuntimeError("run gofmt on:\n" + unformatted)
    run("go", "build", "-o", "dm", ".")
    run("./dm", "release", "check")
    run("python3", "-B", "scripts/check_public_source.py")
    browser()
    run("./dm", "release", "build", "--version", "0.1.0-rc.1",
        "--homebrew-repository", "BrianTillman/Denmother")
    homebrew(ROOT / "dist")
    config = ROOT / "examples/dashboard/ha-config"
    for version in CONFIG["home_assistant"]:
        env = dict(os.environ, DM_DEV_HA_IMAGE=f"ghcr.io/home-assistant/home-assistant:{version}")
        try:
            run("./dm", "dev", "up", "--config", config, "--json", env=env)
            run("./dm", "dev", "dashboard", "denmother-demo", "--config", config,
                "--render", "--require-running", "--json", env=env)
            storage(config)
            run("./dm", "dev", "scenario", "warm", "--config", config, "--json", env=env)
            run("./dm", "dev", "dashboard", "denmother-demo", "--config", config,
                "--render", "--view", "details", "--require-running", "--json", env=env)
        finally:
            run("./dm", "dev", "down", "--config", config, "--json", env=env)


def install():
    configured = subprocess.run(["git", "config", "--get", "core.hooksPath"], cwd=ROOT,
                                capture_output=True, text=True).stdout.strip()
    hook = Path(git("rev-parse", "--git-path", "hooks/pre-commit"))
    if not hook.is_absolute():
        hook = ROOT / hook
    if (configured and configured != ".githooks") or (not configured and hook.exists()):
        raise RuntimeError("an existing Git hook is configured; integrate scripts/precommit.py before replacing it")
    (ROOT / ".githooks/pre-commit").chmod(0o755)
    git("config", "--local", "core.hooksPath", ".githooks")
    print("Installed: commits now require staged source, Homebrew, browser, and live dashboard checks.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--install", action="store_true")
    mode.add_argument("--worktree", action="store_true", help="Check current public files before staging")
    mode.add_argument("--check-snapshot", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.install:
        install()
    elif args.check_snapshot:
        check()
    else:
        directory = Path(tempfile.mkdtemp(prefix="denmother-precommit-"))
        try:
            env = snapshot(directory, args.worktree)
            print(f"Checking {'working tree' if args.worktree else 'staged files'} in {directory}", flush=True)
            command = [sys.executable, "-B", "scripts/precommit.py", "--check-snapshot"]
            with (directory / "precommit.log").open("w") as log, subprocess.Popen(
                    command, cwd=directory, env=env, stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT, text=True) as child:
                for line in child.stdout:
                    print(line, end="", flush=True)
                    log.write(line)
                    log.flush()
                if child.wait():
                    raise subprocess.CalledProcessError(child.returncode, command)
        except BaseException:
            print(f"Commit checks failed; snapshot and diagnostics retained at {directory}", file=sys.stderr)
            raise
        else:
            shutil.rmtree(directory)
            print("All pre-commit checks passed.")


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        sys.exit(f"Pre-commit blocked: {error}")

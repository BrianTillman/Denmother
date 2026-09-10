#!/usr/bin/env python3
"""Prepare isolated projects and synthetic task fixtures for adoption evaluations."""
import argparse
import json
from pathlib import Path
import subprocess


def prepare(dm, directory):
    cases = json.loads(Path(__file__).with_name("cases.json").read_text())
    directory.mkdir(parents=True, exist_ok=False)
    for case in cases:
        root = directory / case["id"]
        result = subprocess.run(
            [str(dm), "init", "--directory", str(root), "--agent", "--skill", "--json"],
            capture_output=True, text=True, check=True,
        )
        json.loads(result.stdout)
        automation = root / "ha-config/automations/presence/motion_lamp.yaml"
        inventory = root / "docs/reference/entity-list.txt"
        if case["id"] == "broken-entity":
            automation.write_text(automation.read_text().replace("input_boolean.study_lamp", "input_boolean.study_missing_lamp"))
        elif case["id"] == "author-test":
            (root / "ha-config/tests/presence/motion_lamp_test.yaml").unlink()
        elif case["id"] == "incomplete-verification":
            inventory.unlink()
        elif case["id"] == "trace-failure":
            (root / "artifacts").mkdir(exist_ok=True)
            fixture = {
                "fixture": True,
                "automation": "automation.study_motion_lamp",
                "target": {"instance": "development", "url": "http://127.0.0.1:8123"},
                "baseline_run_ids": ["old-success"],
                "fresh_run_id": "fresh-failure",
                "traces": [
                    {"run_id": "old-success", "state": "stopped", "script_execution": "finished"},
                    {"run_id": "fresh-failure", "state": "stopped", "script_execution": "error", "trace": {"action/0": [{"error": "NoneType has no attribute 'state'"}]}},
                ],
            }
            (root / "artifacts/failure.json").write_text(json.dumps(fixture, indent=2) + "\n")
        task = (case["prompt"] + "\n\nBinary: " + str(dm)
                + "\nProject directory: " + str(root)
                + "\nSkill: .agents/skills/denmother/SKILL.md\n"
                + "Keep all changes in this task directory. Do not start Docker, contact HA, install dependencies, or publish anything. "
                + "Write REPORT.md with commands actually run, outcomes, evidence, and remaining uncertainty.\n")
        (root / "TASK.md").write_text(task)
    print(json.dumps({"directory": str(directory), "cases": [case["id"] for case in cases]}, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dm", type=Path, required=True)
    parser.add_argument("--directory", type=Path, required=True, help="New directory outside the source checkout")
    args = parser.parse_args()
    prepare(args.dm.resolve(), args.directory.resolve())

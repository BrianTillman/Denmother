#!/usr/bin/env python3
"""Record task metadata and validate completed evaluation evidence."""
import argparse
from contextlib import contextmanager
import os
import uuid
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import subprocess
import time


def now():
    return datetime.now(timezone.utc).isoformat()


@contextmanager
def record_lock(project):
    lock = project / '.evaluation.lock'
    deadline = time.monotonic() + 30
    while True:
        try:
            descriptor = os.open(lock, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            os.close(descriptor)
            break
        except FileExistsError:
            if time.monotonic() >= deadline:
                raise RuntimeError('Evaluation record lock unavailable; inspect any interrupted recorder process')
            time.sleep(0.05)
    try:
        yield
    finally:
        lock.unlink()


def save_record(path, record):
    temporary = path.with_name(path.name + '.' + uuid.uuid4().hex + '.tmp')
    temporary.write_text(json.dumps(record, indent=2) + '\n')
    os.replace(temporary, path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    start = commands.add_parser('start')
    start.add_argument('--project', type=Path, required=True)
    start.add_argument('--dm', type=Path, required=True)
    start.add_argument('--client', required=True)
    start.add_argument('--model', required=True, help='Exact exposed identifier, or explicitly unavailable')
    start.add_argument('--transport', choices=('cli', 'mcp'), default='cli')
    run = commands.add_parser('run', help='Run a CLI command and retain its result and timing')
    run.add_argument('--project', type=Path, required=True)
    run.add_argument('args', nargs=argparse.REMAINDER)
    finish = commands.add_parser('finish')
    finish.add_argument('--project', type=Path, required=True)
    finish.add_argument('--outcome', choices=('pass', 'fail', 'partial'), required=True)
    finish.add_argument('--manual-corrections', type=int, required=True)
    finish.add_argument('--evidence', required=True, help='Reviewed report path relative to project')
    args = parser.parse_args()
    project = args.project.resolve(strict=True)
    path = project / 'evaluation.json'
    if args.command == 'start':
        dm = args.dm.resolve(strict=True)
        skill = project / '.agents/skills/denmother/SKILL.md'
        record = {'schema_version': 1, 'started_at': now(), 'client': args.client,
                  'model': args.model, 'transport': args.transport, 'binary': str(dm),
                  'binary_sha256': hashlib.sha256(dm.read_bytes()).hexdigest(),
                  'skill_sha256': hashlib.sha256(skill.read_bytes()).hexdigest(), 'commands': []}
        with path.open('x') as output:
            json.dump(record, output, indent=2)
        return
    record = json.loads(path.read_text())
    if 'finished_at' in record:
        raise ValueError('Record is already finished')
    if args.command == 'run':
        argv = args.args[1:] if args.args[:1] == ['--'] else args.args
        if not argv:
            raise ValueError('CLI arguments are required')
        with record_lock(project):
            record = json.loads(path.read_text())
            if 'finished_at' in record:
                raise ValueError('Record is already finished')
            commands_dir = project / 'evaluation-commands'
            commands_dir.mkdir(exist_ok=True)
            pending = commands_dir / (uuid.uuid4().hex + '.running')
            pending.write_text(json.dumps({'argv': argv, 'started_at': now()}))
        started = time.monotonic()
        result = subprocess.run([record['binary'], *argv], cwd=project, capture_output=True, text=True)
        entry = {'argv': argv, 'exit_code': result.returncode,
                 'elapsed_seconds': round(time.monotonic() - started, 3),
                 'stdout': result.stdout, 'stderr': result.stderr}
        entry['started_at'] = json.loads(pending.read_text())['started_at']
        with record_lock(project):
            save_record(pending.with_suffix('.json'), entry)
            pending.unlink()
        print(result.stdout, end='')
        if result.stderr:
            import sys
            print(result.stderr, end='', file=sys.stderr)
        raise SystemExit(result.returncode)
    if args.manual_corrections < 0:
        raise ValueError('Manual correction count must be nonnegative')
    report = (project / args.evidence).resolve(strict=True)
    if not report.is_relative_to(project) or not report.is_file():
        raise ValueError('Report must be a regular file inside the project')
    with record_lock(project):
        record = json.loads(path.read_text())
        if 'finished_at' in record:
            raise ValueError('Record is already finished')
        commands_dir = project / 'evaluation-commands'
        if list(commands_dir.glob('*.running')):
            raise ValueError('Commands are still running or were interrupted; review before finishing')
        recorded = [json.loads(p.read_text()) for p in commands_dir.glob('*.json')]
        record['commands'].extend(sorted(recorded, key=lambda entry: entry['started_at']))
        record.update(finished_at=now(), completed_at=datetime.fromtimestamp(report.stat().st_mtime, timezone.utc).isoformat(), outcome=args.outcome, manual_corrections=args.manual_corrections,
                      evidence=args.evidence, evidence_sha256=hashlib.sha256(report.read_bytes()).hexdigest(),
                      command_count=len(record['commands']))
        record['elapsed_seconds'] = round((datetime.fromisoformat(record['completed_at']) - datetime.fromisoformat(record['started_at'])).total_seconds(), 3)
        save_record(path, record)


if __name__ == '__main__':
    main()

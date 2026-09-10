#!/usr/bin/env python3
"""Exercise a native packaged CLI outside its checkout; no HA or Docker required."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import time
import tarfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--dm', type=Path)
    source.add_argument('--archive-dir', type=Path, help='Release archives and SHA256SUMS')
    parser.add_argument('--directory', required=True, type=Path, help='New acceptance directory')
    args = parser.parse_args()
    directory = args.directory.resolve()
    directory.mkdir(parents=True, exist_ok=False)
    archive_name = None
    if args.archive_dir:
        system = {'Linux': 'linux', 'Darwin': 'darwin', 'Windows': 'windows'}[platform.system()]
        arch = {'x86_64': 'amd64', 'AMD64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}[platform.machine()]
        matches = list(args.archive_dir.glob(f'denmother-*-{system}-{arch}.tar.gz'))
        if len(matches) != 1:
            raise ValueError('Expected exactly one archive for this host')
        archive = matches[0]
        checksums = dict(line.split(maxsplit=1)[::-1] for line in (args.archive_dir / 'SHA256SUMS').read_text().splitlines())
        if checksums.get(archive.name) != hashlib.sha256(archive.read_bytes()).hexdigest():
            raise ValueError('Archive checksum mismatch')
        with tarfile.open(archive) as bundle:
            bundle.extractall(directory / 'package', filter='data')
        dm = directory / 'package' / ('dm.exe' if system == 'windows' else 'dm')
        archive_name = archive.name
    else:
        dm = args.dm.resolve(strict=True)
    report = {'schema_version': 1, 'archive': archive_name, 'platform': platform.platform(), 'machine': platform.machine(),
              'binary_sha256': hashlib.sha256(dm.read_bytes()).hexdigest(), 'checks': [],
              'scope': 'native CLI and offline examples; HA runtime and hardware unverified'}
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(('HASS', 'HA_', 'DM_DEV_'))}

    def run(expected, *argv):
        started = time.monotonic()
        result = subprocess.run([str(dm), *argv], cwd=directory, env=env,
                                capture_output=True, text=True, timeout=90)
        evidence = {'argv': list(argv), 'exit_code': result.returncode,
                    'elapsed_seconds': round(time.monotonic() - started, 3),
                    'expected_exit_code': expected}
        report['checks'].append(evidence)
        try:
            output = json.loads(result.stdout)
        except ValueError as exc:
            evidence['error'] = 'output is not JSON'
            raise RuntimeError(evidence) from exc
        evidence['result'] = output
        if result.returncode != expected or result.stderr or output.get('exit_code') != expected:
            raise RuntimeError(f'CLI contract failed: {argv}: {result.returncode}: {result.stderr}')
        return output

    try:
        report['version'] = run(0, 'version', '--json')
        run(0, 'capabilities', '--json')
        for starter in ('quickstart', 'automations', 'dashboard'):
            project = directory / (starter + ' project with spaces')
            run(0, 'init', '--directory', str(project), '--example', starter, '--json')
            config = project / 'ha-config'
            run(0, '--config', str(config), '--no-schema', '--json')
            run(0, 'init', '--directory', str(project), '--example', starter, '--json')
        broken = directory / 'quickstart project with spaces/ha-config/automations/presence/motion_lamp.yaml'
        broken.write_text(broken.read_text().replace('input_boolean.study_lamp', 'input_boolean.missing_lamp'))
        run(1, '--config', str(broken.parents[2]), '--no-schema', '--json')
        report['status'] = 'success'
    except Exception as exc:
        report['status'] = 'failure'
        report['error'] = str(exc)
        raise
    finally:
        (directory / 'acceptance.json').write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    main()

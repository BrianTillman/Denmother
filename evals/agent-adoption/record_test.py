"""Recorder must retain simultaneous CLI calls and refuse active finalization."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest


class RecorderTest(unittest.TestCase):
    def test_parallel_commands_are_retained(self):
        script = Path(__file__).with_name('record.py')
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            skill = project / '.agents/skills/denmother/SKILL.md'
            skill.parent.mkdir(parents=True)
            skill.write_text('test skill')
            base = [sys.executable, str(script)]
            subprocess.run(base + ['start', '--project', directory, '--dm', sys.executable,
                                   '--client', 'test', '--model', 'none'], check=True)
            processes = [subprocess.Popen(base + ['run', '--project', directory, '--', '-c',
                          'import time; time.sleep(0.5); print("result")'], stdout=subprocess.PIPE)
                         for _ in range(2)]
            (project / 'REPORT.md').write_text('reviewed')
            deadline = time.monotonic() + 5
            while len(list((project / 'evaluation-commands').glob('*.running'))) < 2 and time.monotonic() < deadline:
                time.sleep(0.01)
            finish = base + ['finish', '--project', directory, '--outcome', 'pass',
                             '--manual-corrections', '0', '--evidence', 'REPORT.md']
            self.assertNotEqual(subprocess.run(finish, capture_output=True).returncode, 0)
            for process in processes:
                output, _ = process.communicate(timeout=5)
                self.assertEqual(process.returncode, 0)
                self.assertEqual(output, b'result\n')
            subprocess.run(finish, check=True)
            result = json.loads((project / 'evaluation.json').read_text())
            self.assertEqual(result['command_count'], 2)
            self.assertEqual(len(result['commands']), 2)
            self.assertNotEqual(subprocess.run(finish, capture_output=True).returncode, 0)


if __name__ == '__main__':
    unittest.main()

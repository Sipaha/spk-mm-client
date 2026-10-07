#!/usr/bin/env python3
"""Run a validation command, preserving its exit code and public CI failure tail."""
from collections import deque
import os
from pathlib import Path
import subprocess
import sys
from diagnostics import report_failure


def run(command):
    root = Path(os.environ['MM_RELEASE_SCRATCH']) / 'validation-logs'
    root.mkdir(parents=True, exist_ok=True)
    log = root / f'{os.getpid()}-{Path(command[0]).name}.log'
    tail = deque(maxlen=120)
    with log.open('w', encoding='utf-8') as output:
        child = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                 text=True, encoding='utf-8', errors='replace')
        for line in child.stdout:
            print(line, end='', flush=True)
            output.write(line)
            tail.append(line)
        code = child.wait()
    if code:
        report_failure(subprocess.CalledProcessError(code, command, stderr=''.join(tail)))
    return code


if __name__ == '__main__':
    raise SystemExit(run(sys.argv[1:]))

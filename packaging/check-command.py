#!/usr/bin/env python3
"""Run a validation command, preserving its exit code and public CI failure tail."""
from collections import deque
import os
from pathlib import Path
import subprocess
import sys
from diagnostics import report_failure


def failure_blocks(log):
    """Keep test names and adjacent details even if later tests flood the tail."""
    blocks = []
    previous = deque(maxlen=5)
    remaining = 0
    with log.open(encoding='utf-8') as source:
        for line in source:
            if line.lstrip().startswith(('--- FAIL:', 'WARNING: DATA RACE', 'panic:')) and len(blocks) < 8:
                blocks.append(''.join(previous) + line)
                remaining = 30
            elif remaining:
                blocks[-1] += line
                remaining -= 1
            previous.append(line)
    return [block[:4000] for block in blocks]


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
        for block in failure_blocks(log):
            report_failure(subprocess.CalledProcessError(code, command, stderr=block))
        report_failure(subprocess.CalledProcessError(code, command, stderr=''.join(tail)))
    return code


if __name__ == '__main__':
    raise SystemExit(run(sys.argv[1:]))

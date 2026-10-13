#!/usr/bin/env python3
"""Verify current audit fixes and benchmark projection using isolated fixtures."""
import argparse
import json
import os
from pathlib import Path
import subprocess

here = Path(__file__).resolve().parent
root = here.parents[2]
scratch = root.parent / '.tmp' / 'mm-audit-repro'
scratch.mkdir(parents=True, exist_ok=True)
env = dict(os.environ, TMPDIR=str(scratch))
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('area', choices=['api', 'performance', 'ui', 'all'], default='all', nargs='?')
args = parser.parse_args()


def run(cmd, cwd, name):
    with (scratch / name).open('w') as log:
        result = subprocess.run(cmd, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT)
    print((scratch / name).read_text())
    if result.returncode:
        raise SystemExit(result.returncode)


replacements = {}
for pkg, src in [('state', 'state-benchmarks.go.txt')]:
    target = scratch / (pkg + '_probe_test.go')
    target.write_bytes((here / src).read_bytes())
    replacements[str(root / 'internal' / pkg / 'audit_probe_test.go')] = str(target)
overlay = scratch / 'overlay.json'
overlay.write_text(json.dumps({'Replace': replacements}))

if args.area in ['api', 'all']:
    run(['go', 'test', '-race', './internal/api', '-run', '^TestSessionChangeCancelsPendingAuthentication$|^TestClosedServiceRejectsNewAuthentication$', '-count=1', '-v'], root, 'sessions-regressions.log')
    run(['go', 'test', '-race', './internal/api', '-run', '^TestMediaAccountIsolation$|^TestDownloadOwnedByServiceAndSession$', '-count=1', '-v'], root, 'api-regressions.log')
    run(['go', 'test', '-race', './internal/media', '-run', '^TestAccountSwitchCancelsInflightMedia$', '-count=1', '-v'], root, 'media-inflight-regression.log')
if args.area in ['performance', 'all']:
    run(['go', 'test', '-overlay', str(overlay), './internal/state', '-run', '^TestAudit', '-bench', '^BenchmarkAudit', '-benchmem', '-benchtime=500ms', '-count=1', '-v'], root, 'benchmarks.log')
    run(['go', 'test', './internal/state', '-run', '^TestChannelDeltaMatchesFullProjection$|^TestLongHistoryDeltaWireSize$', '-bench', '^BenchmarkChannelDeltaAndJSON$', '-benchmem', '-benchtime=500ms', '-count=1', '-v'], root, 'delta-benchmarks.log')
if args.area in ['ui', 'all']:
    run(['pnpm', 'exec', 'vitest', 'run', 'src/chat.test.ts', 'src/media.test.ts', 'src/channelPatch.test.ts'], root / 'frontend', 'ui-regressions.log')

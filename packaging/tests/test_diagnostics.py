import contextlib
import io
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import patch
sys.path.insert(0, str(Path(__file__).parents[1]))
from diagnostics import report_failure


class PublicFailureDetails(unittest.TestCase):
    def test_compiler_details_are_preserved_without_workflow_command_injection(self):
        error = subprocess.CalledProcessError(1, ['compiler'], stderr='bad%\n::warning::injected\r')
        output = io.StringIO()
        with patch.dict(os.environ, {'GITHUB_ACTIONS': 'true'}), contextlib.redirect_stdout(output):
            report_failure(error)
        value = output.getvalue()
        self.assertEqual(len(value.splitlines()), 1)
        self.assertIn('bad%25%0A::warning::injected%0D', value)
        self.assertTrue(value.startswith('::error title=Native release verification::'))

    def test_local_errors_do_not_emit_ci_commands(self):
        output = io.StringIO()
        with patch.dict(os.environ, {'GITHUB_ACTIONS': 'false'}), contextlib.redirect_stdout(output):
            report_failure(ValueError('local failure'))
        self.assertEqual(output.getvalue(), '')

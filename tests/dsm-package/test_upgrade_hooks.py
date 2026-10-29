"""Exercise new-binary upgrade ordering without DSM or any real database mutation."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]

class UpgradeHookTests(unittest.TestCase):
    """The post hook must back up first and stop immediately on either failed stage."""
    def test_postupgrade_orders_prepare_apply_and_propagates_failures(self):
        for fail in ['', 'prepare', 'apply']:
            with self.subTest(fail=fail), tempfile.TemporaryDirectory() as temp:
                base=Path(temp)
                (base/'bin').mkdir()
                # Existing stores require offline migration before service startup.
                (base/'state').mkdir()
                (base/'state/control.db').touch()
                daemon=base/'bin/diskmapd'
                daemon.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$RECORD"\n[ "$3" != "--upgrade=$FAIL_STAGE" ]\n')
                daemon.chmod(0o755)
                env=dict(os.environ,SYNOPKG_PKGDEST=temp,SYNOPKG_PKGVAR=str(base/'state'),RECORD=str(base/'record'),FAIL_STAGE=fail)
                result=subprocess.run(['sh',str(ROOT/'packaging/scripts/postupgrade')],env=env,capture_output=True)
                lines=(base/'record').read_text().splitlines()
                self.assertEqual(len(lines),1 if fail=='prepare' else 2)
                self.assertTrue(lines[0].endswith('--upgrade=prepare'))
                if len(lines)==2:self.assertTrue(lines[1].endswith('--upgrade=apply'))
                self.assertEqual(result.returncode==0,fail=='')

    def test_cleared_store_does_not_run_a_migration(self):
        # An explicitly cleared installation has no database to back up or migrate.
        with tempfile.TemporaryDirectory() as temp:
            result=subprocess.run(['sh',str(ROOT/'packaging/scripts/postupgrade')],env=dict(os.environ,SYNOPKG_PKGDEST=temp,SYNOPKG_PKGVAR=temp),capture_output=True)
            self.assertEqual(result.returncode,0)

    def test_preupgrade_never_calls_an_old_binary_with_new_flags(self):
        result=subprocess.run(['sh',str(ROOT/'packaging/scripts/preupgrade')],env=dict(os.environ,SYNOPKG_PKGVAR='/unused/own-state'),capture_output=True)
        self.assertEqual(result.returncode,0)
        self.assertEqual(result.stdout,b'')

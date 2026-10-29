"""Exercise the real lifecycle shell with synthetic proc records and recorded signals."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class LifecycleIdentityTests(unittest.TestCase):
    """No process is signalled; shell functions expose deterministic identity fixtures."""

    def invoke(self, operation, socket='/canonical/state/run/control.sock', uid=167438,
               expected_uid=167438, malformed=False, termination="immediate", health=0):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            (base/'bin').mkdir()
            # Simulate readiness only; repeated start must not launch or signal a daemon.
            cli = base/'bin/diskmapctl'
            cli.write_text('#!/bin/sh\nexit "$HEALTH_EXIT"\n')
            cli.chmod(0o700)
            (base/'diskmapd.pid').write_text('4242\n')
            (base/'status').write_text('Name:\tdiskmapd\nUid:\t167438\t%d\t167438\t167438\n' % uid)
            argv = ['/canonical/target/bin/diskmapd', '--socket', socket]
            if malformed:
                argv.append('--unexpected')
            (base/'cmdline').write_bytes(b'\0'.join(x.encode() for x in argv)+b'\0')
            # The production script is sourced unchanged; only external identity sources and kill are isolated.
            harness = r'''
id() { printf '%s\n' "$TEST_UID"; }
cat() {
 case "$1" in
 /proc/4242/status) command cat "$FIXTURE/status";;
 /proc/4242/cmdline) command cat "$FIXTURE/cmdline";;
 *) command cat "$@";;
 esac
}
readlink() {
 case "$*" in
 /proc/4242/exe) [ ! -f "$FIXTURE/dead" ] || return 1; printf '%s\n' /canonical/target/bin/diskmapd;;
 "-f $SYNOPKG_PKGDEST/bin/diskmapd") printf '%s\n' /canonical/target/bin/diskmapd;;
 "-f $SYNOPKG_PKGVAR/run/control.sock") printf '%s\n' /canonical/state/run/control.sock;;
 "-f /canonical/state/run/control.sock") printf '%s\n' /canonical/state/run/control.sock;;
 "-f /other/state/run/control.sock") printf '%s\n' /other/state/run/control.sock;;
 *) return 1;;
 esac
}
kill() {
 printf '%s\n' "$*" >> "$FIXTURE/signals"
 if [ "$1" = -0 ] && [ -f "$FIXTURE/dead" ]; then return 1; fi
 if [ "$1" = -TERM ]; then
  case "$TERMINATION" in
   immediate) : > "$FIXTURE/dead";;
   replacement) printf "9999\n" > "$FIXTURE/diskmapd.pid";;
   identity-loss) printf "wrong\n" > "$FIXTURE/cmdline";;
  esac
 fi
 return 0
}
# Advance a deterministic polling clock without sleeping or signalling a process.
sleep() {
 printf '%s\n' "$1" >> "$FIXTURE/sleeps"
 if [ "$TERMINATION" = delayed ] && [ "$(wc -l < "$FIXTURE/sleeps")" -ge 3 ]; then : > "$FIXTURE/dead"; fi
}
set -- "$OPERATION"
. "$LIFECYCLE"
'''
            env = dict(os.environ, FIXTURE=str(base), TEST_UID=str(expected_uid), TERMINATION=termination,
                       SYNOPKG_PKGVAR=str(base), SYNOPKG_PKGDEST=str(base), HEALTH_EXIT=str(health),
                       OPERATION=operation, LIFECYCLE=str(ROOT/'packaging/scripts/start-stop-status'))
            # Host scheduling must not replace the asserted logical polling bound; sleep is stubbed above.
            result = subprocess.run(['sh', '-c', harness], env=env, capture_output=True, timeout=30)
            self.last_sleeps = (base/"sleeps").read_text().splitlines() if (base/"sleeps").exists() else []
            signals = (base/'signals').read_text().splitlines() if (base/'signals').exists() else []
            return result.returncode, signals, (base/'diskmapd.pid').exists()

    def test_repeated_start_checks_readiness_without_restarting(self):
        # A living but unready daemon cannot make a repeated start report success.
        for health in [0, 4]:
            code, signals, remains = self.invoke('start', health=health)
            self.assertEqual(code, health)
            self.assertEqual(signals, ['-0 4242'])
            self.assertTrue(remains)

    def test_matching_package_identity_reports_running_and_stops(self):
        code, signals, _ = self.invoke('status')
        self.assertEqual(code, 0)
        self.assertEqual(signals, ['-0 4242'])
        code, signals, pid_remains = self.invoke('stop')
        self.assertEqual(code, 0)
        self.assertEqual(signals, ['-0 4242', '-TERM 4242', '-0 4242'])
        self.assertFalse(pid_remains)

    def test_blocked_process_exhausts_bounded_polling_without_kill_or_cleanup(self):
        # A blocked daemon is left for DSM/operator handling; no forced termination is invented.
        code, signals, remains = self.invoke('stop', termination='blocked')
        self.assertEqual(code, 1)
        self.assertEqual(self.last_sleeps, ['1'] * 15)
        self.assertEqual([s for s in signals if not s.startswith('-0 ')], ['-TERM 4242'])
        self.assertTrue(remains)

    def test_delayed_exit_is_waited_for_and_removes_only_original_pid(self):
        code, signals, remains = self.invoke('stop', termination='delayed')
        self.assertEqual(code, 0)
        self.assertEqual(self.last_sleeps, ['1'] * 3)
        self.assertFalse(remains)
        self.assertNotIn('-KILL 4242', signals)

    def test_identity_loss_or_replaced_pid_does_not_claim_stop_success(self):
        for termination in ['replacement', 'identity-loss']:
            code, signals, remains = self.invoke('stop', termination=termination)
            self.assertEqual(code, 1)
            self.assertTrue(remains)
            self.assertEqual([s for s in signals if not s.startswith('-0 ')], ['-TERM 4242'])

    def test_stale_pid_same_binary_foreign_socket_never_signals(self):
        for operation, expected in [('status', 3), ('stop', 0)]:
            code, signals, pid_remains = self.invoke(operation, socket='/other/state/run/control.sock')
            self.assertEqual(code, expected)
            self.assertEqual(signals, [])
            self.assertTrue(pid_remains)

    def test_wrong_effective_uid_or_root_hook_never_signals(self):
        for parameters in ({'uid': 12345}, {'expected_uid': 0}):
            for operation, expected in [('status', 3), ('stop', 0)]:
                code, signals, _ = self.invoke(operation, **parameters)
                self.assertEqual(code, expected)
                self.assertEqual(signals, [])

    def test_unexpected_arguments_and_relative_socket_never_signal(self):
        for parameters in ({'malformed': True}, {'socket': 'run/control.sock'}):
            code, signals, _ = self.invoke('stop', **parameters)
            self.assertEqual(code, 0)
            self.assertEqual(signals, [])


if __name__ == '__main__':
    unittest.main()

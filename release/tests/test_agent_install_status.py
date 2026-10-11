"""Installer status fixtures; never touch an actual service or remote node."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SUCCESS = '2026/10/11 12:00:00 Basic info uploaded successfully\n'
FAKE = r'''
systemctl() {
  case " $* " in
    *' show '*)
      if [ "${RESTART:-0}" = 1 ]; then
        if [ -f "$STATE" ]; then printf '%s\n' bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb;
        else touch "$STATE"; printf '%s\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; fi
      else printf '%s\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; fi ;;
    *' is-active '*) return "$ACTIVE" ;;
  esac
}
journalctl() {
  printf '%s\n' "$*" > "$QUERY"
  if [ "${DENIED:-0}" = 1 ]; then return 1; fi
  printf '%s' "$JOURNAL"
}
sleep() { :; }
version_to_install=v1.0.5
komari_agent_path=/opt/komari/agent
service_user=ubuntu
service_name=komari-agent
init_system=systemd
user_service=${USER_SERVICE:-false}
install_check_timeout=0
. "$1"
komari_install_status
'''


class AgentInstallStatusTests(unittest.TestCase):
    def run_check(self, journal='', active=0, **extra):
        with tempfile.TemporaryDirectory() as work:
            env = dict(os.environ, JOURNAL=journal, ACTIVE=str(active),
                       STATE=work+'/state', QUERY=work+'/query', **extra)
            result = subprocess.run(['sh', '-c', FAKE, 'fixture',
                                     str(ROOT/'release/agent-install-status.sh')],
                                    env=env, capture_output=True, text=True)
            query = Path(env['QUERY'])
            return result, query.read_text() if query.exists() else ''

    def test_actual_startup_report_and_connection_are_distinct(self):
        result, query = self.run_check(SUCCESS)
        self.assertEqual(result.returncode, 0)
        self.assertIn('面板接入已确认', result.stdout)
        self.assertIn('监控长连接：尚未确认', result.stdout)
        self.assertIn('_SYSTEMD_INVOCATION_ID=aaaaaaaa', query)
        self.assertNotIn('--since', query)

    def test_websocket_handshake_alone_is_not_successful_report(self):
        result, _ = self.run_check('2026/10/11 12:00:00 WebSocket connected using v2 protocol\n')
        self.assertIn('[待确认]', result.stdout)
        self.assertIn('监控长连接：已建立', result.stdout)
        self.assertNotIn('面板接入已确认', result.stdout)

    def test_restart_and_dead_service_cannot_reuse_success(self):
        result, _ = self.run_check(SUCCESS, RESTART='1')
        self.assertNotIn('面板接入已确认', result.stdout)
        result, _ = self.run_check(SUCCESS, active=1)
        self.assertEqual(result.returncode, 1)
        self.assertIn('[失败]', result.stdout)
        self.assertIn('查看日志', result.stdout)

    def test_error_reason_is_safe_and_service_keeps_running(self):
        for text, expected in [('status code: 401', '认证'),
                               ('x509: bad certificate', '证书'),
                               ('lookup host: no such host', 'DNS'),
                               ('Non-compliance ICP Filing', '备案')]:
            with self.subTest(error=text):
                result, _ = self.run_check('2026/10/11 12:00:00 Error uploading basic info: '+text+' token=fixture-private-value\n')
                self.assertEqual(result.returncode, 0)
                self.assertIn('[连接异常]', result.stdout)
                self.assertIn(expected, result.stdout)
                self.assertNotIn('fixture-private-value', result.stdout+result.stderr)

    def test_missing_journal_is_unverified_and_user_scope_is_preserved(self):
        result, query = self.run_check(SUCCESS, DENIED='1', USER_SERVICE='true')
        self.assertIn('无法读取本次服务日志', result.stdout)
        self.assertNotIn('面板接入已确认', result.stdout)
        self.assertIn('--user', query)
        self.assertIn('journalctl --user', result.stdout)

    def test_latest_disconnect_is_not_reported_as_live_connection(self):
        result, _ = self.run_check(SUCCESS+'2026/10/11 12:00:01 WebSocket connected using v2 protocol\n2026/10/11 12:00:02 WebSocket disconnected\n')
        self.assertIn('监控长连接：已断开', result.stdout)

    def test_embedded_helper_matches_source(self):
        embedded = (ROOT/'install.sh').read_text().split('# BEGIN KOMARI AGENT INSTALL STATUS\n')[1].split('# END KOMARI AGENT INSTALL STATUS\n')[0]
        self.assertEqual(embedded, (ROOT/'release/agent-install-status.sh').read_text())


if __name__ == '__main__':
    unittest.main()

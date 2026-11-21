"""Installer source, migration, lock and rollback fixtures; never touch host services."""

import hashlib
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
IS_ROOT = hasattr(os, "geteuid") and os.geteuid() == 0


@unittest.skipUnless(sys.platform.startswith("linux"), "requires Linux /proc and POSIX launchers")
class AgentInstallerRecoveryTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.work = Path(temp.name)
        self.bin = self.work / "bin"
        self.bin.mkdir()
        self.dest = self.work / "agent install"
        self.xdg = self.work / "xdg"
        self.unit = self.xdg / "systemd" / "user" / "komari-agent.service"
        self.etc_root = self.work / "etc"
        self.system_unit = self.etc_root / "systemd" / "system" / "komari-agent.service"
        self.state = self.work / "systemd-state"
        self.state.write_text("active\nenabled\n")
        self.env = {
            **os.environ,
            "PATH": f"{self.bin}{os.pathsep}{os.environ['PATH']}",
            "XDG_CONFIG_HOME": str(self.xdg),
            "KOMARI_TEST_ASSETS": str(self.work),
            "KOMARI_TEST_BASE": "https://mirror.example/releases",
            "KOMARI_TEST_VERSION": "v1.2.3",
            "KOMARI_TEST_SYSTEMD_STATE": str(self.state),
            "KOMARI_TEST_SYSTEMD_SYSTEM_UNIT": str(self.system_unit),
            "KOMARI_TEST_RUNLEVEL_ROOT": str(self.etc_root / "runlevels"),
            "KOMARI_TEST_INIT_D": str(self.etc_root / "init.d"),
            "KOMARI_TEST_PROCD_LINK": str(self.etc_root / "rc.d" / "S99komari-agent"),
            "AGENT_DISABLE_AUTO_UPDATE": "false",
        }
        self.env.pop("SUDO_USER", None)
        self.env.pop("PREFIX", None)
        self.write_executable(self.bin / "ps", "#!/bin/sh\necho systemd\n")
        self.write_executable(self.bin / "uname", "#!/bin/sh\ncase $1 in -s) echo Linux;; -m) echo x86_64;; esac\n")
        self.write_executable(self.bin / "systemctl", self.systemctl_stub())
        self.write_executable(self.bin / "curl", self.curl_stub())
        self.agent = self.work / "fixture-agent"
        self.write_executable(self.agent, "#!/bin/sh\n[ \"${KOMARI_TEST_AGENT_FAIL:-0}\" = 1 ] && exit 7\nexec sleep 120\n")
        self.manifest = self.work / "fixture-sha256sums.txt"
        self.manifest.write_text(f"{hashlib.sha256(self.agent.read_bytes()).hexdigest()}  komari-agent-linux-amd64\n")
        self.addCleanup(self.stop_manual_agent)

    def write_executable(self, path, body):
        path.write_text(body)
        path.chmod(0o700)

    def curl_stub(self):
        return r'''#!/bin/sh
set -eu
url=''; out=''; curl_protocols=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        https://*|http://*) url=$1 ;;
        -o) out=$2; shift ;;
        --proto|--proto-redir) curl_protocols="$curl_protocols $1 $2"; shift ;;
    esac
    shift
done
printf '%s\n' "$url" >> "$KOMARI_TEST_ASSETS/requests"
printf '%s\n' "$curl_protocols" >> "$KOMARI_TEST_ASSETS/curl-options"
[ "${KOMARI_TEST_FAIL_DOWNLOAD:-0}" = 1 ] && exit 22
case "$url" in
    "$KOMARI_TEST_BASE/$KOMARI_TEST_VERSION/komari-agent-linux-amd64") source_file="$KOMARI_TEST_ASSETS/fixture-agent" ;;
    "$KOMARI_TEST_BASE/$KOMARI_TEST_VERSION/sha256sums.txt") source_file="$KOMARI_TEST_ASSETS/fixture-sha256sums.txt" ;;
    *) exit 22 ;;
esac
cp "$source_file" "$out"
'''

    def systemctl_stub(self):
        return r'''#!/bin/sh
set -eu
scope='system'
if [ "${1:-}" = --user ]; then scope=user; shift; fi
op=${1:-}; shift || :
unit_file="$XDG_CONFIG_HOME/systemd/user/komari-agent.service"
system_unit_file="$KOMARI_TEST_SYSTEMD_SYSTEM_UNIT"
state_file="$KOMARI_TEST_SYSTEMD_STATE"
log_file="$KOMARI_TEST_ASSETS/systemctl.log"
if [ "$scope" != user ]; then
    case "$op" in
        list-unit-files)
            [ "${KOMARI_TEST_SYSTEM_LIST_FAIL:-0}" = 1 ] && exit 1
            if [ -f "$system_unit_file" ]; then printf 'komari-agent.service enabled\n'; fi
            exit 0 ;;
        list-units) exit 0 ;;
        show-environment) exit 1 ;;
    esac
fi
case "$op" in
    show-environment) exit 0 ;;
    list-unit-files)
        if [ -f "$unit_file" ]; then printf 'komari-agent.service enabled\n'; fi
        exit 0 ;;
    is-active)
        grep -qx active "$state_file" && exit 0 || exit 3 ;;
    is-enabled)
        grep -qx enabled "$state_file" && exit 0 || exit 1 ;;
    stop)
        printf 'stop\n' >> "$log_file"
        if [ "$scope" = system ] && [ "${KOMARI_TEST_FAIL_SYSTEM_STOP:-0}" = 1 ]; then exit 1; fi
        sed -i '/^active$/d' "$state_file"; exit 0 ;;
    disable)
        printf 'disable\n' >> "$log_file"; sed -i '/^enabled$/d' "$state_file"; exit 0 ;;
    enable)
        printf 'enable\n' >> "$log_file"; grep -qx enabled "$state_file" || echo enabled >> "$state_file"; exit 0 ;;
    start)
        printf 'start\n' >> "$log_file"
        starts=$(grep -c '^start$' "$log_file" || :)
        if [ "${KOMARI_TEST_FAIL_FIRST_START:-0}" = 1 ] && [ "$starts" -eq 1 ]; then exit 1; fi
        grep -qx active "$state_file" || echo active >> "$state_file"
        exit 0 ;;
    daemon-reload) printf 'daemon-reload\n' >> "$log_file"; exit 0 ;;
    *) printf '%s\n' "$op" >> "$log_file"; exit 0 ;;
esac
'''

    def install(self, *args, fail_first_start=False, script=None, env=None):
        env = dict(self.env if env is None else env)
        if fail_first_start:
            env["KOMARI_TEST_FAIL_FIRST_START"] = "1"
        return subprocess.run(
            ["bash", str(script or ROOT / "install.sh"), "--install-dir", str(self.dest),
             "--install-no-mirror", "--install-version", "v1.2.3", *args],
            env=env, capture_output=True, text=True, timeout=30,
        )

    def remapped_installer(self):
        source = (ROOT / "install.sh").read_text()
        replacements = (
            ("/etc/systemd/system", (self.etc_root / "systemd" / "system").as_posix()),
            ("/etc/init.d", (self.etc_root / "init.d").as_posix()),
            ("/etc/runlevels", (self.etc_root / "runlevels").as_posix()),
            ("/etc/rc.d", (self.etc_root / "rc.d").as_posix()),
            ("/etc/init/", (self.etc_root / "init").as_posix() + "/"),
        )
        for source_path, fixture_path in replacements:
            source = source.replace(source_path, fixture_path)
        target = self.work / "install-fixture.sh"
        target.write_text(source)
        target.chmod(0o700)
        return target

    def stop_manual_agent(self):
        launcher = self.dest / "stop-komari-agent.sh"
        if launcher.exists():
            subprocess.run([str(launcher)], capture_output=True, timeout=15)

    def request_log(self):
        path = self.work / "requests"
        return path.read_text().splitlines() if path.exists() else []

    def test_base_url_requires_https_exact_version_and_no_source_conflict(self):
        for args in (
            ("--install-base-url", "http://mirror.example/releases"),
            ("--install-base-url", "https://user@mirror.example/releases"),
            ("--install-base-url", "https://mirror.example/releases?x=1"),
            ("--install-base-url", "https://mirror.example:65536/releases", "--install-version", "v1.2.3"),
            ("--install-base-url", "https://mirror.example/releases", "--install-version", "latest"),
            ("--install-base-url", "https://mirror.example/releases", "--install-ghproxy", "https://proxy.example"),
        ):
            with self.subTest(args=args):
                result = subprocess.run(
                    ["bash", str(ROOT / "install.sh"), "--install-dir", str(self.dest), *args],
                    env=self.env, capture_output=True, text=True, timeout=10,
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.request_log(), [])

    def test_base_url_uses_exact_version_directory_and_same_directory_manifest(self):
        result = self.install("--install-no-systemd", "--install-base-url", self.env["KOMARI_TEST_BASE"])
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.request_log(), [
            "https://mirror.example/releases/v1.2.3/komari-agent-linux-amd64",
            "https://mirror.example/releases/v1.2.3/sha256sums.txt",
        ])
        options = (self.work / "curl-options").read_text()
        self.assertIn("--proto =https", options)
        self.assertIn("--proto-redir =https", options)
        self.assertEqual((self.dest / "agent").read_bytes(), self.agent.read_bytes())

    def test_base_failure_does_not_fall_back_and_preserves_existing_binary(self):
        self.dest.mkdir()
        old = self.dest / "agent"
        old.write_bytes(b"old unmarked fixture")
        env = dict(self.env, KOMARI_TEST_FAIL_DOWNLOAD="1")
        result = subprocess.run(
            ["bash", str(ROOT / "install.sh"), "--install-dir", str(self.dest),
             "--install-no-mirror", "--install-version", "v1.2.3", "--install-no-systemd",
             "--install-base-url", self.env["KOMARI_TEST_BASE"]],
            env=env, capture_output=True, text=True, timeout=30,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(old.read_bytes(), b"old unmarked fixture")
        self.assertEqual(self.request_log(), ["https://mirror.example/releases/v1.2.3/komari-agent-linux-amd64"])
        self.assertFalse(any("github.com" in url or "ghfast.top" in url or "ghproxy" in url for url in self.request_log()))

    def test_base_checksum_mismatch_preserves_existing_binary(self):
        self.dest.mkdir()
        old = self.dest / "agent"
        old.write_bytes(b"old unmarked fixture")
        self.manifest.write_text("0" * 64 + "  komari-agent-linux-amd64\n")
        result = self.install("--install-no-systemd", "--install-base-url", self.env["KOMARI_TEST_BASE"])
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(old.read_bytes(), b"old unmarked fixture")
        self.assertEqual(self.request_log(), [
            "https://mirror.example/releases/v1.2.3/komari-agent-linux-amd64",
            "https://mirror.example/releases/v1.2.3/sha256sums.txt",
        ])

    def test_manual_migration_disables_old_systemd_user_service_and_reclaims_stale_lock(self):
        self.dest.mkdir()
        (self.dest / "agent").write_bytes(b"old unmarked fixture")
        self.unit.parent.mkdir(parents=True)
        self.unit.write_text("[Service]\nExecStart=/old/agent\n")
        result = self.install("--install-no-systemd", "--install-base-url", self.env["KOMARI_TEST_BASE"])
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("stop", (self.work / "systemctl.log").read_text())
        self.assertIn("disable", (self.work / "systemctl.log").read_text())
        self.assertNotIn("enabled", self.state.read_text())

        lock = self.dest / ".komari-agent.lock"
        lock.mkdir()
        (lock / "owner").write_text("2147483646 12345\n")
        start = subprocess.run([str(self.dest / "start-komari-agent.sh")], capture_output=True, text=True, timeout=15)
        self.assertEqual(start.returncode, 0, start.stdout + start.stderr)
        self.assertIn("Komari Agent already running. PID ", start.stdout)
        self.assertFalse(lock.exists())

        # A matching live PID and /proc start tick must be retained.
        stat = Path(f"/proc/{os.getpid()}/stat").read_text()
        start_tick = stat[stat.rfind(")") + 2:].split()[19]
        lock.mkdir()
        (lock / "owner").write_text(f"{os.getpid()} {start_tick}\n")
        blocked = subprocess.run([str(self.dest / "start-komari-agent.sh")], capture_output=True, text=True, timeout=15)
        self.assertNotEqual(blocked.returncode, 0)
        self.assertIn(f"PID {os.getpid()}", blocked.stderr)
        self.assertTrue((lock / "owner").exists())
        (lock / "owner").unlink()
        lock.rmdir()

    def test_system_unit_file_detected_when_systemctl_listing_fails(self):
        self.dest.mkdir()
        old_agent = b"old system agent"
        old_unit = "[Service]\nExecStart=/old/system-agent\n"
        (self.dest / "agent").write_bytes(old_agent)
        self.system_unit.parent.mkdir(parents=True)
        self.system_unit.write_text(old_unit)
        env = dict(self.env, KOMARI_TEST_SYSTEM_LIST_FAIL="1", KOMARI_TEST_FAIL_SYSTEM_STOP="1")
        result = self.install(
            "--install-no-systemd", "--install-base-url", self.env["KOMARI_TEST_BASE"],
            script=self.remapped_installer(), env=env,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("stop", (self.work / "systemctl.log").read_text())
        self.assertEqual((self.dest / "agent").read_bytes(), old_agent)
        self.assertEqual(self.system_unit.read_text(), old_unit)
        self.assertIn("active", self.state.read_text())
        self.assertIn("enabled", self.state.read_text())
        self.assertFalse((self.dest / "start-komari-agent.sh").exists())

    def test_manual_start_failure_restores_openrc_active_and_enabled_state(self):
        self.dest.mkdir()
        (self.dest / "agent").write_bytes(b"old unmarked fixture")
        init_script = self.etc_root / "init.d" / "komari-agent"
        init_script.parent.mkdir(parents=True)
        init_script.write_text("fixture OpenRC service\n")
        runlevel_dir = self.etc_root / "runlevels" / "default"
        runlevel_dir.mkdir(parents=True)
        (runlevel_dir / "komari-agent").symlink_to(init_script)
        state = self.work / "openrc-state"
        state.write_text("active\n")
        self.write_executable(self.bin / "rc-service", r'''#!/bin/sh
set -eu
state="$KOMARI_TEST_LEGACY_STATE"
case "${2:-}" in
    status) if grep -qx active "$state"; then echo 'status: started'; else echo 'status: stopped'; fi ;;
    stop) sed -i '/^active$/d' "$state" ;;
    start) grep -qx active "$state" || echo active >> "$state" ;;
    *) exit 2 ;;
esac
''')
        self.write_executable(self.bin / "rc-update", r'''#!/bin/sh
set -eu
action=$1; name=$2; runlevel=$3
link="$KOMARI_TEST_RUNLEVEL_ROOT/$runlevel/$name"
case "$action" in
    del) rm -f "$link" ;;
    add) mkdir -p "${link%/*}"; ln -sf "$KOMARI_TEST_INIT_D/$name" "$link" ;;
    *) exit 2 ;;
esac
''')
        env = dict(self.env, KOMARI_TEST_LEGACY_STATE=str(state), KOMARI_TEST_AGENT_FAIL="1")
        result = self.install(
            "--install-no-systemd", "--install-base-url", self.env["KOMARI_TEST_BASE"],
            script=self.remapped_installer(), env=env,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.dest / "agent").read_bytes(), b"old unmarked fixture")
        self.assertIn("active", state.read_text())
        self.assertTrue((runlevel_dir / "komari-agent").is_symlink())

    def test_manual_start_failure_restores_procd_active_and_enabled_state(self):
        self.dest.mkdir()
        (self.dest / "agent").write_bytes(b"old unmarked fixture")
        init_script = self.etc_root / "init.d" / "komari-agent"
        init_script.parent.mkdir(parents=True)
        state = self.work / "procd-state"
        state.write_text("active\n")
        enabled_link = self.etc_root / "rc.d" / "S99komari-agent"
        enabled_link.parent.mkdir(parents=True)
        enabled_link.symlink_to(init_script)
        self.write_executable(self.bin / "uci", "#!/bin/sh\nexit 0\n")
        self.write_executable(init_script, r'''#!/bin/sh
set -eu
state="$KOMARI_TEST_LEGACY_STATE"
case "${1:-}" in
    status) if grep -qx active "$state"; then echo running; else echo 'not running'; fi ;;
    stop) sed -i '/^active$/d' "$state" ;;
    start) grep -qx active "$state" || echo active >> "$state" ;;
    disable) rm -f "$KOMARI_TEST_PROCD_LINK" ;;
    enable) mkdir -p "${KOMARI_TEST_PROCD_LINK%/*}"; ln -sf "$KOMARI_TEST_INIT_D/komari-agent" "$KOMARI_TEST_PROCD_LINK" ;;
    *) exit 2 ;;
esac
''')
        env = dict(self.env, KOMARI_TEST_LEGACY_STATE=str(state), KOMARI_TEST_AGENT_FAIL="1")
        result = self.install(
            "--install-no-systemd", "--install-base-url", self.env["KOMARI_TEST_BASE"],
            script=self.remapped_installer(), env=env,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.dest / "agent").read_bytes(), b"old unmarked fixture")
        self.assertIn("active", state.read_text())
        self.assertTrue(enabled_link.is_symlink())

    @unittest.skipIf(IS_ROOT, "requires unprivileged systemd-user path to avoid host system unit paths")
    def test_systemd_user_start_failure_restores_binary_unit_and_active_state(self):
        self.dest.mkdir()
        old_agent = b"old unmarked fixture"
        old_unit = "[Service]\nExecStart=/old/komari-agent\n"
        (self.dest / "agent").write_bytes(old_agent)
        self.unit.parent.mkdir(parents=True)
        self.unit.write_text(old_unit)
        result = self.install(fail_first_start=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.dest / "agent").read_bytes(), old_agent)
        self.assertEqual(self.unit.read_text(), old_unit)
        self.assertIn("active", self.state.read_text())
        self.assertIn("enabled", self.state.read_text())
        self.assertFalse(list(self.dest.glob(".install-rollback.*")))
        self.assertFalse(list(self.unit.parent.glob(".komari-agent.service.*")))


if __name__ == "__main__":
    unittest.main()

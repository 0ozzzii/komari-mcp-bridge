"""Exercise the delivered shell installer against local fixture assets."""

import hashlib
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


@unittest.skipUnless(sys.platform.startswith("linux"), "requires Linux /proc")
class ManualAgentInstallTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.work = Path(temp.name)
        self.dest = self.work / "user agent"
        self.bin = self.work / "bin"
        self.bin.mkdir()
        self.env = {
            **os.environ,
            "PATH": f"{self.bin}:{os.environ['PATH']}",
            "KOMARI_TEST_ASSETS": str(self.work),
            "AGENT_DISABLE_AUTO_UPDATE": "false",
        }
        self.env.pop("SUDO_USER", None)
        self.env.pop("PREFIX", None)
        self.write_executable(self.bin / "ps", "#!/bin/sh\necho tini\n")
        self.write_executable(self.bin / "systemctl", "#!/bin/sh\necho unexpected systemctl >&2\nexit 98\n")
        self.write_executable(self.bin / "uname", "#!/bin/sh\ncase $1 in -s) echo Linux;; -m) echo x86_64;; esac\n")
        self.write_executable(self.bin / "curl", """#!/bin/sh
set -eu
printf 'curl called\\n' >> "$KOMARI_TEST_ASSETS/downloads"
while [ "$#" -gt 0 ]; do
    case "$1" in
        https://*) name=${1##*/} ;;
        -o) out=$2; shift ;;
    esac
    shift
done
cp "$KOMARI_TEST_ASSETS/$name" "$out"
""")
        self.asset = self.work / "komari-agent-linux-amd64"
        self.write_executable(self.asset, """#!/bin/sh
printf '%s\\n' "$@"
exec sleep 120
""")
        self.manifest = self.work / "sha256sums.txt"
        self.manifest.write_text(f"{hashlib.sha256(self.asset.read_bytes()).hexdigest()}  {self.asset.name}\n")
        self.addCleanup(self.stop_agent)

    def write_executable(self, path, body):
        path.write_text(body)
        path.chmod(0o700)

    def install(self, *args):
        return subprocess.run(
            ["bash", str(ROOT / "install.sh"), "--install-dir", str(self.dest),
             "--install-no-mirror", "--install-version", "v1.0.5", *args],
            env=self.env, capture_output=True, text=True, timeout=25,
        )

    def stop_agent(self):
        launcher = self.dest / "stop-komari-agent.sh"
        if launcher.exists():
            subprocess.run([str(launcher)], capture_output=True, check=True, timeout=15)

    def test_tini_install_starts_with_literal_arguments_and_stops(self):
        literal = "fixture with ' quote; $ literal"
        result = self.install("-t", literal, "--disable-auto-update", "--month-rotate", "1")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Detected init system: manual", result.stdout)
        self.assertIn("Komari Agent running. PID ", result.stdout)
        self.assertNotIn(literal, result.stdout)
        self.assertNotIn("unexpected systemctl", result.stderr)
        self.assertEqual((self.dest / "start-komari-agent.sh").stat().st_mode & 0o777, 0o700)
        log_file = self.dest / "komari-agent.log"
        self.assertIn(literal, log_file.read_text().splitlines())
        self.assertEqual(log_file.stat().st_mode & 0o777, 0o600)
        self.stop_agent()
        self.assertFalse((self.dest / "komari-agent.pid").exists())

    def test_existing_custom_or_pinned_agent_is_kept_before_download(self):
        self.dest.mkdir()
        agent = self.dest / "agent"
        for data, args in (
            (b"custom fixture\x00komari-agent:custom-runtime", ()),
            (b"unmarked legacy fixture", ("--disable-auto-update",)),
        ):
            with self.subTest(args=args):
                agent.write_bytes(data)
                result = self.install("--install-no-systemd", *args)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Existing custom or pinned agent preserved", result.stdout)
                result = self.install("--install-no-systemd", "--keep-custom", *args)
                self.assertEqual(result.returncode, 0)
                self.assertEqual(agent.read_bytes(), data)
                self.assertFalse((self.work / "downloads").exists())

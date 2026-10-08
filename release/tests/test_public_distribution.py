import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("public_tree", ROOT / "release/check-public-tree.py")
scanner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scanner)


class PublicDistributionTests(unittest.TestCase):
    def test_repository_override_and_invalid_values_fail_before_download(self):
        for value, ok in [("publisher/release-artifacts", True), ("https://example.com/repo", False), ("owner/repo?token=example", False), ("owner/repo/extra", False)]:
            env = dict(os.environ, KOMARI_RELEASE_REPOSITORY=value)
            result = subprocess.run(["sh", "-c", '. "$1"; printf "%s" "$komari_release_repository"', "test", str(ROOT / "release/download.sh")], env=env, text=True, capture_output=True)
            self.assertEqual(result.returncode == 0, ok)
            if ok:
                self.assertEqual(result.stdout, value)

    def test_source_and_distribution_identities_are_distinct_and_recorded(self):
        config = json.loads((ROOT / "release/config.json").read_text())
        self.assertNotEqual(config["repository"], config["distribution_repository"])
        self.assertIn(config["distribution_repository"], (ROOT / "release/download.sh").read_text())
        self.assertIn(config["distribution_repository"], (ROOT / "install.ps1").read_text())

    def test_nixos_printed_template_does_not_disclose_runtime_arguments(self):
        source = (ROOT / "install.sh").read_text()
        block = source.split('if [ "$init_system" = "nixos" ]; then', 1)[1].split('elif ', 1)[0]
        # Run only the printed declaration, never dependency/service handling.
        block = block.split('elif ', 1)[0].rstrip()
        setup = 'log_warning() { :; }; log_info() { echo "$1"; }; CYAN=; NC=; service_name=test; komari_agent_path=/test/agent; target_dir=/test; service_user=test; komari_args="-t private-sentinel";\n'
        result = subprocess.run(["bash", "-c", setup + block], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("private-sentinel", result.stdout)
        self.assertIn("<PRIVATE_AGENT_ARGUMENTS>", result.stdout)

    def test_scan_flags_credentials_private_files_and_optional_hosts_without_values(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / ".env").write_text("KEY=" + "sk-" + "x" * 32)
            (root / "guide.md").write_text("https://deployment.personal.invalid/mcp\n")
            (root / "safe.md").write_text("https://komari.example.com/mcp/<YOUR_KEY>\n")
            hits = scanner.findings(root, [".env", "guide.md", "safe.md"], ["deployment.personal.invalid"])
            self.assertEqual({h[2] for h in hits}, {"private-runtime-file", "model-api-key", "private-host"})
            self.assertNotIn("safe.md", {h[0] for h in hits})
            self.assertNotIn("sk-", str(hits))

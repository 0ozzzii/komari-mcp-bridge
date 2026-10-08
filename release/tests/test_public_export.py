import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, ROOT / "release" / file)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


distribution = load("distribution", "public-distribution.py")
registry = load("registry", "registry.py")
manifest = load("manifest", "manifest.py")


class PublicExportTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.base = Path(self.temp.name)
        self.source = self.base / "source"
        self.source.mkdir()
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.config = json.loads((ROOT / "release/config.json").read_text())
        (self.source / "release").mkdir()
        (self.source / "release/config.json").write_text(json.dumps(self.config))
        (self.source / "README.md").write_text("Public source")
        (self.source / ".github/workflows").mkdir(parents=True)
        (self.source / ".github/workflows/private.yml").write_text("private workflow")
        self.commit()
        self.sha = self.git("rev-parse", "HEAD")

    def tearDown(self):
        self.temp.cleanup()

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.source), *args], text=True, stderr=subprocess.DEVNULL).strip()

    def commit(self):
        self.git("add", ".")
        self.git("commit", "-m", "Fixture")

    def test_export_contains_only_immutable_main_without_private_history_or_handoff_branch(self):
        self.git("checkout", "-b", "private/handoff")
        (self.source / "handoff.txt").write_text("private handoff")
        self.commit()
        self.git("checkout", "main")
        (self.source / "README.md").write_text("local private modification")
        target = self.base / "export"
        metadata = distribution.export_source(self.source, self.sha, target)
        self.assertEqual((target / "README.md").read_text(), "Public source")
        self.assertFalse((target / ".git").exists())
        self.assertFalse((target / "handoff.txt").exists())
        self.assertFalse((target / ".github/workflows").exists())
        self.assertEqual(metadata["source_commit"], self.sha)
        self.assertEqual(metadata["source_repository"], self.config["repository"])
        self.assertEqual((self.source / "README.md").read_text(), "local private modification")
        with self.assertRaisesRegex(ValueError, "already exists"):
            distribution.export_source(self.source, self.sha, target)

    def test_credential_or_private_host_rejected_before_output_directory_created(self):
        secret = "ghp_" + "x" * 36
        (self.source / "secret.txt").write_text(secret)
        self.commit()
        with self.assertRaises(ValueError) as error:
            distribution.export_source(self.source, "HEAD", self.base / "rejected")
        self.assertNotIn(secret, str(error.exception))
        self.assertFalse((self.base / "rejected").exists())
        (self.source / "secret.txt").unlink()
        (self.source / "host.md").write_text("https://deployment.personal.invalid/")
        self.commit()
        with self.assertRaises(ValueError):
            distribution.export_source(self.source, "HEAD", self.base / "host-rejected", ["deployment.personal.invalid"])
        self.assertFalse((self.base / "host-rejected").exists())

    def bundle(self):
        path = self.base / "assets"
        path.mkdir()
        for name in distribution.binary_names(self.config) | (distribution.SUPPORT_FILES - {"release.json", "sha256sums.txt"}):
            (path / name).write_text("fixture:" + name)
        with patch.object(manifest, "ROOT", self.source):
            # Fixture does not carry the project's patched upstream sources.
            config = dict(self.config, upstream={})
            (self.source / "release/config.json").write_text(json.dumps(config))
            self.commit()
            manifest.generate(path, "v1.0.3")
        return path, self.git("rev-parse", "HEAD")

    def test_verify_requires_matching_complete_clean_bundle_and_rejects_corruption_or_extra_files(self):
        path, sha = self.bundle()
        result = distribution.verify_assets(path, self.config, "v1.0.3", sha)
        self.assertEqual(result["verified_assets"], len(distribution.binary_names(self.config) | distribution.SUPPORT_FILES))
        with self.assertRaisesRegex(ValueError, "provenance"):
            distribution.verify_assets(path, self.config, "v1.0.3", "0" * 40)
        (path / ".env").write_text("PRIVATE=value")
        with self.assertRaisesRegex(ValueError, "unexpected"):
            distribution.verify_assets(path, self.config, "v1.0.3", sha)
        (path / ".env").unlink()
        (path / "komari-linux-amd64").write_text("corrupted")
        with self.assertRaisesRegex(ValueError, "checksum"):
            distribution.verify_assets(path, self.config, "v1.0.3", sha)

    def test_duplicate_or_path_traversal_checksum_is_rejected(self):
        path, sha = self.bundle()
        sums = path / "sha256sums.txt"
        original = sums.read_text()
        for malformed in (original + original.splitlines()[0] + "\n", "0" * 64 + "  ../private-config\n"):
            sums.write_text(malformed)
            with self.assertRaisesRegex(ValueError, "checksum"):
                distribution.verify_assets(path, self.config, "v1.0.3", sha)

    def test_registry_identity_stays_independent_of_development_repository_rename(self):
        config = dict(self.config, repository="publisher/different-private-dev")
        for suffix in ("", "-agent", "-mcp"):
            self.assertEqual(registry.image_repository(config, suffix), self.config["container_repository"] + suffix)
        with self.assertRaises(ValueError):
            registry.image_repository(config, "-unknown")
        with self.assertRaises(ValueError):
            registry.image_repository(dict(config, container_repository="https://ghcr.io/publisher/image"))

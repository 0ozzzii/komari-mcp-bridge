#!/usr/bin/env python3
"""Export a sanitized immutable source tree or verify a public release bundle.

This helper never copies private Git history, pushes, uploads, or reads secrets.
Publication uses the normal authenticated Git/GitHub clients separately.
"""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("public_tree", ROOT / "release/check-public-tree.py")
scanner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scanner)
SUPPORT_FILES = {
    "ATTRIBUTION.md", "INSTALL.md", "LICENSE", "LICENSE.komari-agent", "NOTICE",
    "compose.yml", "install-komari.sh", "install.ps1", "install.sh",
    "komari-mcp.env.example", "komari-mcp.service", "release.json", "sha256sums.txt",
    "AI_REVIEW_SETUP.zh-CN.md", "upgrade-prompt.zh-CN.md",
    "windows-deployment-guide.md", "PUBLIC_DISTRIBUTION.md",
}


def digest(path):
    result = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def binary_names(config):
    return {prefix + "-" + target.replace("/", "-") + (".exe" if target.startswith("windows/") else "")
            for component, prefix in (("panel", "komari"), ("agent", "komari-agent"), ("bridge", "komari-mcp"))
            for target in config[component + "_targets"]}


def export_source(source, commit, destination, forbidden_hosts=()):
    source, destination = Path(source), Path(destination)
    sha = subprocess.check_output(["git", "-C", str(source), "rev-parse", "--verify", commit + "^{commit}"], text=True).strip()
    if destination.exists():
        raise ValueError("Export directory already exists; existing files preserved")
    # Validate before exposing any files at the requested destination.
    with tempfile.TemporaryDirectory() as temporary:
        tree = Path(temporary) / "tree"
        tree.mkdir()
        with tempfile.TemporaryFile() as archive:
            subprocess.run(["git", "-C", str(source), "archive", sha], stdout=archive, check=True)
            archive.seek(0)
            with tarfile.open(fileobj=archive) as tar:
                tar.extractall(tree, filter="data")
        excluded = []
        for path in sorted((tree / ".github/workflows").rglob("*")):
            if path.is_file() or path.is_symlink():
                excluded.append(str(path.relative_to(tree)))
                path.unlink()
        if (tree / ".github/workflows").exists():
            import shutil
            shutil.rmtree(tree / ".github/workflows")
        names = sorted(str(p.relative_to(tree)) for p in tree.rglob("*") if p.is_file() or p.is_symlink())
        findings = scanner.findings(tree, names, forbidden_hosts)
        if findings:
            raise ValueError("Public text scan rejected paths: " + ", ".join(f"{name}:{line}:{kind}" for name, line, kind in findings))
        config = json.loads((tree / "release/config.json").read_text())
        if config["repository"] == config["distribution_repository"]:
            raise ValueError("Source and distribution repositories must be distinct")
        records = {name: digest(tree / name) for name in names}
        metadata = {"source_repository": config["repository"], "source_commit": sha,
                    "distribution_repository": config["distribution_repository"],
                    "export_mode": "sanitized immutable source snapshot; private history excluded",
                    "excluded_paths": excluded, "source_files_sha256": records,
                    "note": "Private development workflows and private handoff branches are not mirrored. Binary provenance remains in the unchanged release.json."}
        (tree / "release/distribution.json").write_text(json.dumps(metadata, indent=2) + "\n")
        import shutil
        shutil.copytree(tree, destination)
    return metadata


def verify_assets(directory, config, version, commit):
    directory = Path(directory)
    if not re.fullmatch(r"v\d+\.\d+\.\d+|Snapshot-[A-Za-z0-9._-]+", version) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("Invalid version or full source commit")
    expected = binary_names(config) | SUPPORT_FILES
    actual = {p.name for p in directory.iterdir()}
    if actual != expected or any(p.is_symlink() or not p.is_file() for p in directory.iterdir()):
        raise ValueError("Incomplete or unexpected release assets; refusing public upload")
    checks = {}
    for line in (directory / "sha256sums.txt").read_text().splitlines():
        checksum, name = line.split(None, 1)
        if name not in expected or name == "sha256sums.txt" or name in checks or not re.fullmatch(r"[0-9a-f]{64}", checksum):
            raise ValueError("Unsafe or duplicate checksum entry")
        checks[name] = checksum
    if set(checks) != expected - {"sha256sums.txt"}:
        raise ValueError("Incomplete checksum inventory")
    for name, checksum in checks.items():
        if digest(directory / name) != checksum:
            raise ValueError("Release checksum mismatch: " + name)
    metadata = json.loads((directory / "release.json").read_text())
    if metadata["version"] != version or metadata["commit"] != commit or metadata["working_tree_dirty"]:
        raise ValueError("Release source provenance mismatch")
    for field in ("repository", "distribution_repository", "container_repository"):
        if metadata[field] != config[field]:
            raise ValueError("Release repository mismatch: " + field)
    records = metadata["assets"]
    if len(records) != len(expected) - 2 or {r["name"] for r in records} != expected - {"release.json", "sha256sums.txt"}:
        raise ValueError("Incomplete manifest inventory")
    for record in records:
        if record["sha256"] != checks[record["name"]] or record["size"] != (directory / record["name"]).stat().st_size:
            raise ValueError("Manifest asset mismatch: " + record["name"])
    # Support text is eligible for publication only after the same credential scan.
    findings = scanner.findings(directory, sorted(SUPPORT_FILES), ())
    if findings:
        raise ValueError("Public support-file text scan rejected paths: " + str(findings))
    return {"version": version, "commit": commit, "verified_assets": len(expected)}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    export = sub.add_parser("export")
    export.add_argument("--commit", required=True)
    export.add_argument("--directory", required=True)
    export.add_argument("--forbidden-host", action="append", default=[])
    verify = sub.add_parser("verify")
    verify.add_argument("--directory", required=True)
    verify.add_argument("--version", required=True)
    verify.add_argument("--commit", required=True)
    verify.add_argument("--config", default=str(ROOT / "release/config.json"))
    args = parser.parse_args()
    if args.action == "export":
        result = export_source(ROOT, args.commit, args.directory, args.forbidden_host)
        result = {key: result[key] for key in ("source_repository", "source_commit", "distribution_repository")}
    else:
        result = verify_assets(args.directory, json.loads(Path(args.config).read_text()), args.version, args.commit)
    print(json.dumps(result))

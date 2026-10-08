#!/usr/bin/env python3
"""Require the complete official-compatible asset matrix before publication."""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def binary_names(config):
    result = []
    for component, prefix in (("panel", "komari"), ("agent", "komari-agent"), ("bridge", "komari-mcp")):
        for target in config[component + "_targets"]:
            os_name, arch = target.split("/")
            result.append(f"{prefix}-{os_name}-{arch}" + (".exe" if os_name == "windows" else ""))
    return sorted(result)


def generate(directory, version, complete=True):
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+|Snapshot-[A-Za-z0-9._-]+", version):
        raise ValueError("Invalid version")
    directory = Path(directory)
    config = json.loads((ROOT / "release/config.json").read_text())
    if complete:
        for name in binary_names(config):
            if not (directory / name).is_file() or not (directory / name).stat().st_size:
                raise ValueError("Missing or empty release asset: " + name)
    files = sorted(p for p in directory.iterdir() if p.is_file() and p.name not in ("sha256sums.txt", "release.json"))
    records = [{"name": p.name, "size": p.stat().st_size, "sha256": hashlib.sha256(p.read_bytes()).hexdigest()} for p in files]
    meta = {"version": version, "repository": config["repository"],
            "distribution_repository": config["distribution_repository"],
            "container_repository": config["container_repository"],
            "commit": subprocess.check_output(["git", "-C", str(ROOT), "rev-parse", "HEAD"], text=True).strip(),
            "working_tree_dirty": bool(subprocess.check_output(["git", "-C", str(ROOT), "status", "--porcelain"])),
            "patch_sha256": {p: hashlib.sha256((ROOT / p).read_bytes()).hexdigest()
                             for u in config["upstream"].values() for p in u["patches"]},
            "upstream": config["upstream"], "assets": records,
            "validation": "CI build and regression tests; not production deployment verification"}
    (directory / "release.json").write_text(json.dumps(meta, indent=2) + "\n")
    lines = [f"{item['sha256']}  {item['name']}\n" for item in records]
    lines.append(hashlib.sha256((directory / "release.json").read_bytes()).hexdigest() + "  release.json\n")
    (directory / "sha256sums.txt").write_text("".join(lines))


if __name__ == "__main__":
    generate(sys.argv[1], sys.argv[2], "--partial" not in sys.argv[3:])

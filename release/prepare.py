#!/usr/bin/env python3
"""Apply pinned upstream patches without overwriting unrelated local changes."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def git(target, *args, check=True):
    return subprocess.run(["git", "-C", str(target), *args], check=check,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def prepare(component, target):
    config = json.loads((ROOT / "release/config.json").read_text())["upstream"][component]
    target = Path(target).resolve()
    if not target.exists():
        subprocess.run(["git", "clone", "--config", "core.autocrlf=false", "--config", "core.eol=lf", "--no-checkout", "https://github.com/" +
                        config["repository"] + ".git", str(target)], check=True)
        git(target, "checkout", "--detach", config["commit"])
    if git(target, "rev-parse", "HEAD").stdout.decode().strip() != config["commit"]:
        raise RuntimeError("Upstream commit differs from the pinned baseline; refusing to overwrite")
    patches = [ROOT / p for p in config["patches"]]
    overlay = ROOT / config["overlay"] if config["overlay"] else None
    # A private index reconstructs the entire expected result, including changes
    # outside patch hunks. Reverse-apply alone cannot detect those user edits.
    with tempfile.TemporaryDirectory(prefix="komari-patch-index-") as work:
        env = dict(os.environ, GIT_INDEX_FILE=str(Path(work) / "index"))
        def index_git(*args, check=True):
            result = subprocess.run(["git", "-C", str(target), *args], env=env,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            if check and result.returncode:
                raise RuntimeError("Pinned patch index failed: " + result.stderr.decode(errors="replace")[-2000:])
            return result
        index_git("read-tree", "HEAD")
        for patch in patches:
            index_git("apply", "--cached", str(patch))
        names = index_git("diff", "--cached", "--name-only", "-z").stdout.split(b"\0")
        expected = {}
        for raw in names:
            if raw:
                name = os.fsdecode(raw)
                value = index_git("show", ":" + name, check=False)
                expected[name] = value.stdout if value.returncode == 0 else None
        if overlay:
            for source in sorted(overlay.rglob("*")):
                if source.is_file():
                    expected[str(source.relative_to(overlay))] = source.read_bytes()
    dirty = git(target, "status", "--porcelain", "-z", "--untracked-files=all").stdout
    for entry in dirty.split(b"\0"):
        if not entry:
            continue
        status, name = entry[:2], os.fsdecode(entry[3:])
        if b"R" in status or b"C" in status or name not in expected:
            raise RuntimeError("Unrelated local changes present; refusing to overwrite: " + name)
    for name, value in expected.items():
        dest = target / name
        if dest.is_symlink() or (dest.exists() and not dest.is_file()):
            raise RuntimeError("Non-file patch target present; refusing to overwrite: " + name)
        current = dest.read_bytes() if dest.exists() else None
        base = git(target, "show", "HEAD:" + name, check=False)
        original = base.stdout if base.returncode == 0 else None
        if current != value and current != original:
            raise RuntimeError("Modified patch target present; refusing to overwrite: " + name)
    for name, value in expected.items():
        dest = target / name
        if value is None:
            dest.unlink(missing_ok=True)
        else:
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_bytes(value)
    print(component + ": exact pinned patches prepared; no service changes")


if __name__ == "__main__":
    prepare(sys.argv[1], sys.argv[2])

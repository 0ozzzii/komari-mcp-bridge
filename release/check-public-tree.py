#!/usr/bin/env python3
"""Check tracked current-tree files, reporting locations rather than secrets.

This is a bounded first line of defense, not a historical or binary secret scan.
Private hosts may be supplied by a private CI setting without embedding them in
the public source. Image pixels and release assets still need separate review.
"""
import argparse
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
SECRET_PATTERNS = {
    "private-key": re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----"),
    "github-token": re.compile(r"\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b"),
    "model-api-key": re.compile(r"\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{24,}\b"),
    "aws-access-key": re.compile(r"\bAKIA[A-Z0-9]{16}\b"),
    "credential-url": re.compile(r"https?://[^\s/]+:[^\s/@]+@", re.I),
}


def findings(root, files, forbidden_hosts=()):
    issues = []
    for name in files:
        p = root / name
        if not p.exists():  # A tracked deletion is absent from the future tree.
            continue
        if p.is_symlink():
            issues.append((name, 1, "symlink-needs-review"))
            continue
        if p.name == ".env" or any(part in (".private", "local") for part in p.relative_to(root).parts):
            issues.append((name, 1, "private-runtime-file"))
        raw = p.read_bytes()
        if b"\0" in raw:
            continue
        try:
            text = raw.decode("utf-8")
        except UnicodeDecodeError:
            continue
        for line_number, line in enumerate(text.splitlines(), 1):
            for kind, pattern in SECRET_PATTERNS.items():
                if pattern.search(line):
                    issues.append((name, line_number, kind))
            if any(host.lower() in line.lower() for host in forbidden_hosts):
                issues.append((name, line_number, "private-host"))
    return issues


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--forbidden-host", action="append", default=[])
    args = parser.parse_args()
    hosts = args.forbidden_host + [s.strip() for s in os.getenv("PUBLIC_FORBIDDEN_HOSTS", "").split(",") if s.strip()]
    names = subprocess.check_output(["git", "-C", str(ROOT), "ls-files", "-z"]).decode().split("\0")
    result = findings(ROOT, [n for n in names if n], hosts)
    for name, line, kind in result:
        print(f"{name}:{line}: {kind}")
    if result:
        raise SystemExit(1)
    print("Current tracked text scan passed; history, binary contents and runtime secrets require separate review.")


if __name__ == "__main__":
    main()

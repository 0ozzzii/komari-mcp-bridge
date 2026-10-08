#!/usr/bin/env python3
"""Use the configured package identity independently of the source repository."""
import argparse
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]


def image_repository(config, suffix=""):
    base = config["container_repository"]
    if not re.fullmatch(r"ghcr\.io/[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*", base):
        raise ValueError("Invalid configured GHCR repository")
    if suffix not in ("", "-agent", "-mcp"):
        raise ValueError("Invalid component suffix")
    return base + suffix


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--suffix", default="")
    args = parser.parse_args()
    print(image_repository(json.loads((ROOT / "release/config.json").read_text()), args.suffix))

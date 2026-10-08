#!/usr/bin/env bash
set -euo pipefail
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
exec python3 "$task_root/release/prepare.py" agent "${1:?Usage: prepare.sh /path/to/checkout}"

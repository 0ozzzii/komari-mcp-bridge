#!/usr/bin/env python3
"""Run the isolated Windows test; expose bounded failures as check annotations."""
import subprocess
import sys
import os
from pathlib import Path

root = Path(__file__).resolve().parents[1]
if sys.platform != "win32":
    raise SystemExit("Requires native Windows/ConPTY; Linux is not Windows runtime validation")
commands = [(["go", "test", "./internal/terminal", "-count=1", "-timeout=90s", "-v", "-args", "-require-pwsh"], root / "bridge")]
title = "Windows ConPTY validation failed"
if "--agent" in sys.argv:
    agent = Path(os.environ["RUNNER_TEMP"]) / "komari-agent-windows-validation"
    commands = [([sys.executable, str(root / "release/prepare.py"), "agent", str(agent)], root),
                (["go", "test", "./terminal", "./executionguard", "-count=1", "-timeout=90s", "-v"], agent)]
    title = "Windows agent validation failed"
for command, cwd in commands:
    result = subprocess.run(command, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                            encoding="utf-8", errors="replace")
    print(result.stdout)
    if result.returncode:
        # Only synthetic test data; no credentials or user terminal recordings.
        tail = result.stdout[-12000:]
        escaped = tail.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
        print("::error title=" + title + "::" + escaped)
        sys.exit(result.returncode)

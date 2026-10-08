#!/usr/bin/env python3
"""Keep the single-file official-style installers self-contained."""
from pathlib import Path

root = Path(__file__).resolve().parents[1]
body = (root / "release/download.sh").read_text()
for name in ("install.sh", "install-komari.sh"):
    path = root / name
    source = path.read_text()
    begin = "# BEGIN KOMARI RELEASE DOWNLOAD\n"
    end = "# END KOMARI RELEASE DOWNLOAD\n"
    if begin not in source:
        source = source.replace("\n", "\n" + begin + end, 1)
    before, rest = source.split(begin, 1)
    _, after = rest.split(end, 1)
    path.write_text(before + begin + body + end + after)

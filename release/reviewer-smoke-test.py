#!/usr/bin/env python3
"""Test Actions model bindings serially; never review or merge a real PR."""
import importlib.util
import json
import os
from pathlib import Path
import re
import time

spec = importlib.util.spec_from_file_location("reviewer", Path(__file__).with_name("ai-review.py"))
reviewer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reviewer)


def probe(providers, sha, rpm=6, call=reviewer.call_provider,
          clock=time.monotonic, sleep=time.sleep):
    if not re.fullmatch(r"[a-f0-9]{40}", sha) or not 1 <= rpm <= 10:
        raise ValueError("Invalid test SHA or RPM")
    payload = {"test_only": True, "reviewed_sha": sha,
               "scope": "Synthetic documentation change; no real PR or deployment.",
               "pr_diff": "--- a/example.md\n+++ b/example.md\n@@ -1 +1 @@\n-Hello\n+Hello world\n",
               "official_deltas": {}}
    results = []
    previous_start = None
    for name, url, key, model in providers:
        # Report only binding presence, never URLs, model responses or keys.
        record = {"provider": name, "configured": all((url, key, model)), "passed": False}
        if not record["configured"]:
            record["error"] = "Missing base URL, model ID or API key"
        else:
            if previous_start is not None:
                wait = max(0, previous_start + 60 / rpm - clock())
                if wait:
                    sleep(wait)
            previous_start = clock()
            try:
                # One actual request per provider. Real review has its own
                # bounded retry/failover policy, covered by regression tests.
                answer = call(url, key, model, payload, sha, 90)
                reviewer.parse_review(json.dumps(answer), sha)
                record["passed"] = True
            except reviewer.ProviderFailure as error:
                record["error"] = str(error)
            record["elapsed_seconds"] = round(clock() - previous_start, 2)
        results.append(record)
    return {"test_only": True, "source_commit": sha, "providers": results,
            "passed": bool(results) and all(r["passed"] for r in results),
            "note": "Connectivity/schema test only; no real candidate approval, merge, release or email-delivery proof."}


def main():
    providers = [(slot.lower(), os.environ.get("REVIEW_" + slot + "_BASE_URL", ""),
                  os.environ.get("REVIEW_" + slot + "_API_KEY", ""),
                  os.environ.get("REVIEW_" + slot + "_MODEL", "")) for slot in ("PRIMARY", "BACKUP")]
    report = probe(providers, os.environ["GITHUB_SHA"], int(os.environ.get("REVIEW_RPM", "6")))
    Path(os.environ["REPORT_PATH"]).write_text(json.dumps(report, indent=2) + "\n")
    lines = ["### Model connectivity test (no merge)", "", "| Provider | Result |", "| --- | --- |"]
    for record in report["providers"]:
        status = "PASS" if record["passed"] else record["error"]
        lines.append("| " + record["provider"] + " | " + status + " |")
        print(record["provider"] + ": " + status)
        print("::notice title=Review model connectivity::" + record["provider"] + ": " + status)
    lines += ["", report["note"]]
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as output:
            output.write("\n".join(lines) + "\n")
    raise SystemExit(0 if report["passed"] else 1)


if __name__ == "__main__":
    main()

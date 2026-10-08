#!/usr/bin/env python3
"""Consume a review from this trusted workflow's immediately preceding job."""
import argparse
import importlib.util
import json
import os
from pathlib import Path

spec = importlib.util.spec_from_file_location("gate", Path(__file__).with_name("review-gate.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


def outcome(state):
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as output:
            output.write("state=" + state + "\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--review", required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--expected-sha", required=True)
    args = parser.parse_args()
    review = json.loads(Path(args.review).read_text())
    if (review.get("decision") != "approve" or review.get("reviewed_sha") != args.expected_sha
            or review.get("repository") != args.repository or not isinstance(review.get("pr_number"), int)):
        raise ValueError("Invalid or stale review result")
    number = review["pr_number"]
    pr = gate.api(args.repository, "pulls/" + str(number))
    if pr["state"] != "open":
        print("PR already closed; no operation")
        outcome("already_closed")
        return
    if pr["head"]["sha"] != args.expected_sha or pr["base"]["sha"] != review["base_sha"]:
        print("PR or base changed since review; requires new review")
        outcome("held")
        return
    default = gate.api(args.repository, "")["default_branch"]
    if pr["base"]["ref"] != default:
        raise ValueError("Unexpected merge target")
    compare = gate.api(args.repository, "compare/" + pr["base"]["sha"] + "..." + pr["head"]["sha"])
    if compare["status"] not in ("ahead", "identical"):
        print("Candidate does not include current base; update branch, rebuild and review again")
        outcome("held")
        return
    workflow = gate.api(args.repository, "actions/workflows/build.yml")
    runs = gate.api(args.repository, "actions/workflows/build.yml/runs?head_sha=" + args.expected_sha + "&per_page=100")["workflow_runs"]
    # Reuse all deterministic merge protections, substituting the trusted
    # workflow result for a GitHub review (the creating bot cannot self-approve).
    synthetic = [{"id": 1, "user": {"login": "internal-ai-review"}, "state": "APPROVED", "commit_id": args.expected_sha}]
    allowed, reason = gate.eligible(pr, synthetic, runs, {"internal-ai-review"}, workflow["id"])
    print(reason)
    if not allowed:
        outcome("held")
        return
    response = gate.api(args.repository, f"pulls/{number}/merge", "PUT", {"sha": args.expected_sha, "merge_method": "squash"})
    if not response.get("merged"):
        raise RuntimeError("GitHub rejected merge; branch protection was not bypassed")
    print("Reviewed upstream candidate merged; no release or deployment performed")
    outcome("merged")


if __name__ == "__main__":
    main()

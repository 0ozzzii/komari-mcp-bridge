#!/usr/bin/env python3
"""Merge an upstream PR only after an allowlisted review and successful CI.

This file must run from the trusted default branch, never from the PR checkout.
GitHub remains the authentication, audit, protection and atomic-merge authority.
"""
import argparse
import json
import os
import subprocess


def api(repository, path, method="GET", body=None):
    args = ["gh", "api", "repos/" + repository + ("/" + path if path else ""), "--method", method]
    if body is not None:
        args += ["--input", "-"]
    result = subprocess.run(args, input=json.dumps(body).encode() if body is not None else None,
                            check=True, stdout=subprocess.PIPE)
    return json.loads(result.stdout)


def eligible(pr, reviews, runs, reviewers, workflow_id):
    if not reviewers:
        return False, "No reviewer identities configured; automatic merging disabled"
    if pr["state"] != "open" or pr["draft"] or pr.get("mergeable") is not True:
        return False, "PR is draft, closed, conflicting or mergeability not yet known"
    if pr["user"]["login"] != "github-actions[bot]" or not pr["head"]["ref"].startswith("upstream/official-sync-"):
        return False, "Not a sync task PR"
    if pr["head"]["repo"]["full_name"] != pr["base"]["repo"]["full_name"]:
        return False, "Cross-repository PR refused"
    sha = pr["head"]["sha"]
    # Latest review by each authorized identity wins. Old approvals do not
    # approve new commits, and a later changes-requested review blocks merging.
    latest = {}
    for review in sorted(reviews, key=lambda item: item["id"]):
        login = review["user"]["login"]
        if login in reviewers and review["state"] in ("APPROVED", "CHANGES_REQUESTED", "DISMISSED"):
            latest[login] = review
    if any(r["state"] == "CHANGES_REQUESTED" for r in latest.values()):
        return False, "An authorized reviewer requested changes"
    if not any(r["state"] == "APPROVED" and r["commit_id"] == sha for r in latest.values()):
        return False, "No authorized approval for the exact current commit"
    matching = [r for r in runs if r["workflow_id"] == workflow_id and r["head_sha"] == sha]
    if not matching:
        return False, "No build workflow run for current commit"
    # A newer failed or still-running build cannot be hidden by an older pass.
    latest_run = max(matching, key=lambda item: (item["run_number"], item.get("run_attempt", 1)))
    if latest_run["status"] != "completed" or latest_run["conclusion"] != "success":
        return False, "Current build workflow has not completed successfully"
    return True, "Authorized current-commit approval and complete build passed"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True)
    parser.add_argument("--number", type=int)
    parser.add_argument("--sha")
    args = parser.parse_args()
    reviewers = {x.strip() for x in os.environ.get("UPSTREAM_REVIEWER_LOGINS", "").split(",") if x.strip()}
    if not reviewers:
        print("Reviewer not connected; all PRs remain awaiting review")
        return
    repository = args.repository
    default = api(repository, "")["default_branch"]
    workflow = api(repository, "actions/workflows/build.yml")
    numbers = [args.number] if args.number else [p["number"] for p in api(repository, "commits/" + args.sha + "/pulls")]
    for number in numbers:
        pr = api(repository, "pulls/" + str(number))
        if pr["base"]["ref"] != default:
            continue
        if args.sha and pr["head"]["sha"] != args.sha:
            continue
        compare = api(repository, "compare/" + pr["base"]["sha"] + "..." + pr["head"]["sha"])
        if compare["status"] not in ("ahead", "identical"):
            print(f"PR #{number}: current base must be included; update, rebuild and review again")
            continue
        # GitHub can briefly return mergeable=null. This attempt leaves the PR
        # unchanged; manual workflow_dispatch can rerun the gate when ready.
        reviews = []
        page = 1
        while True:
            batch = api(repository, f"pulls/{number}/reviews?per_page=100&page={page}")
            reviews.extend(batch)
            if len(batch) < 100:
                break
            page += 1
        runs = api(repository, "actions/workflows/build.yml/runs?head_sha=" + pr["head"]["sha"] + "&per_page=100")["workflow_runs"]
        allowed, reason = eligible(pr, reviews, runs, reviewers, workflow["id"])
        print(f"PR #{number}: {reason}")
        if allowed:
            # The expected head SHA closes the review-to-merge race. Branch
            # protections still apply; rejected merges are never bypassed.
            response = api(repository, f"pulls/{number}/merge", "PUT",
                           {"sha": pr["head"]["sha"], "merge_method": "squash"})
            if not response.get("merged"):
                raise RuntimeError("GitHub rejected merge: " + response.get("message", "unknown reason"))
            print(f"PR #{number} merged; no release or deployment was performed")


if __name__ == "__main__":
    main()

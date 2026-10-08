#!/usr/bin/env python3
"""Optional webhook notification. Carries metadata, never a merge credential."""
import json
import os
import urllib.parse
import urllib.request


def main():
    url = os.environ.get("UPSTREAM_REVIEW_WEBHOOK_URL", "")
    if not url:
        print("Review webhook not configured; PR remains available through GitHub")
        return
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError("Webhook must be an HTTPS URL without embedded credentials")
    payload = {"event": "komari.upstream_review.requested", "repository": os.environ["REPOSITORY"],
               "pull_request_url": os.environ["PR_URL"], "head_sha": os.environ["PR_HEAD_SHA"],
               "candidate_state": os.environ["CANDIDATE_STATE"], "policy": "review exact SHA; CI required; merge only; never release or deploy"}
    headers = {"Content-Type": "application/json"}
    token = os.environ.get("UPSTREAM_REVIEW_WEBHOOK_TOKEN", "")
    if token:
        headers["Authorization"] = "Bearer " + token
    # Do not forward the authentication header through redirects to another host.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, request, fp, code, message, response_headers, new_url):
            return None
    opener = urllib.request.build_opener(NoRedirect)
    request = urllib.request.Request(url, data=json.dumps(payload).encode(), headers=headers, method="POST")
    with opener.open(request, timeout=15) as response:
        if not 200 <= response.status < 300:
            raise RuntimeError("Webhook returned non-success status")
    print("Review notification delivered; receiver still needs independent GitHub identity")


if __name__ == "__main__":
    main()

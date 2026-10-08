#!/usr/bin/env python3
"""Deduplicated failure Issues; GitHub delivers according to account settings."""
import argparse
import hashlib
import json
import os
import subprocess


def api(repository, path, method="GET", body=None):
    command = ["gh", "api", "repos/" + repository + "/" + path, "--method", method]
    if body is not None:
        command += ["--input", "-"]
    result = subprocess.run(command, input=json.dumps(body).encode() if body is not None else None,
                            check=True, stdout=subprocess.PIPE)
    return json.loads(result.stdout)


def notify(repository, kind, reason, run_url, resolved=False, request=api):
    titles = {"model": "[Upstream automation] 模型审查接口需要处理",
              "review": "[Upstream automation] 模型审核拒绝合并",
              "merge": "[Upstream automation] 审核通过但未完成合并",
              "build": "[Upstream automation] 同步候选构建需要处理",
              "sync": "[Upstream automation] 官方同步冲突或任务故障"}
    title = titles[kind]
    existing = None
    for page in range(1, 11):
        issues = request(repository, f"issues?state=open&per_page=100&page={page}")
        existing = next((x for x in issues if x["title"] == title and "pull_request" not in x), None)
        if existing or len(issues) < 100:
            break
    else:
        raise RuntimeError("Cannot establish notification deduplication within 1000 open Issues")
    if resolved:
        if existing:
            request(repository, f"issues/{existing['number']}", "PATCH", {"state": "closed"})
            print("Failure Issue closed after successful recovery")
        return
    repo = request(repository, "")
    owner = repo["owner"]
    mention = "@" + owner["login"] + "\n\n" if owner["type"] == "User" else ""
    marker = "<!-- upstream-notice:" + hashlib.sha256(run_url.encode()).hexdigest() + " -->"
    body = marker + "\n" + mention + "自动同步没有因此绕过审查或更新设备。\n\n" + reason + "\n\n" + run_url + "\n\n" + \
           "修复方法：见仓库 release/README.md 的故障处理表。密钥只在 Actions Secrets 修改，不要粘贴到 Issue。\n" + \
           "通知是否发邮件由 GitHub 的 Watching/Notifications/邮箱设置决定，工作流没有读取私有邮箱。"
    if existing:
        # One comment for each new failed workflow run generates a GitHub
        # notification. The same run is deduplicated, including a retry after
        # comment creation succeeded but updating the Issue body failed.
        if marker not in (existing.get("body") or ""):
            for page in range(1, 11):
                comments = request(repository, f"issues/{existing['number']}/comments?per_page=100&page={page}")
                if any(marker in (comment.get("body") or "") for comment in comments):
                    break
                if len(comments) < 100:
                    request(repository, f"issues/{existing['number']}/comments", "POST", {"body": body})
                    break
            else:
                raise RuntimeError("Cannot establish run notification deduplication within 1000 comments")
        request(repository, f"issues/{existing['number']}", "PATCH", {"body": body})
        print("Existing failure Issue updated; no duplicate created")
    else:
        data = {"title": title, "body": body}
        if owner["type"] == "User":
            data["assignees"] = [owner["login"]]
        request(repository, "issues", "POST", data)
        print("Failure Issue created; GitHub controls email delivery")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True)
    parser.add_argument("--kind", choices=("model", "review", "merge", "build", "sync"), required=True)
    parser.add_argument("--reason", default="自动化任务未完成，请查看 Actions 报告。")
    parser.add_argument("--run-url", required=True)
    parser.add_argument("--resolved", action="store_true")
    args = parser.parse_args()
    if os.environ.get("UPSTREAM_NOTIFICATION_MODE") == "off":
        print("Failure notifications disabled")
        return
    notify(args.repository, args.kind, args.reason, args.run_url, args.resolved)


if __name__ == "__main__":
    main()

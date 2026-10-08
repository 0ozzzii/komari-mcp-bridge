#!/usr/bin/env python3
"""Deduplicated failure and outcome Issues; GitHub delivers according to account settings."""
import argparse
import hashlib
import json
import os
import subprocess


def api(repository, path, method="GET", body=None):
    command = ["gh", "api", "repos/" + repository + ("/" + path if path else ""), "--method", method]
    if body is not None:
        command += ["--input", "-"]
    result = subprocess.run(command, input=json.dumps(body).encode() if body is not None else None,
                            check=True, stdout=subprocess.PIPE)
    return json.loads(result.stdout)


def notify(repository, kind, reason, run_url, resolved=False, pr_number=None, head_sha=None, request=api):
    titles = {
        "model": "[Upstream automation] 模型审查接口需要处理",
        "review": "[Upstream automation] 模型审核拒绝合并",
        "merge": "[Upstream automation] 审核通过但未完成合并",
        "build": "[Upstream automation] 同步候选构建需要处理",
        "sync": "[Upstream automation] 官方同步冲突或任务故障",
        "approved": "[Upstream automation] 官方更新审核通过",
        "merged": "[Upstream automation] 官方更新已成功合并",
    }
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
            print(f"Issue [{title}] closed after successful resolution")
        return

    repo = request(repository, "")
    owner = repo["owner"]
    mention = "@" + owner["login"] + "\n\n" if owner["type"] == "User" else ""

    # 使用 kind、run_url 与 head_sha 构造精确的去重指纹
    marker_key = f"{kind}:{run_url}:{head_sha or ''}"
    marker = "<!-- upstream-notice:" + hashlib.sha256(marker_key.encode()).hexdigest() + " -->"

    # 构建富文本通知正文，包含 PR、提交 SHA 和 Actions 链接，不泄露任何密钥
    pr_info = f"- **候选 PR**：#{pr_number} (https://github.com/{repository}/pull/{pr_number})\n" if pr_number else ""
    sha_info = f"- **提交 SHA**：`{head_sha}`\n" if head_sha else ""

    body = (
        f"{marker}\n"
        f"{mention}### {title}\n\n"
        f"{pr_info}"
        f"{sha_info}"
        f"- **Actions 运行链接**：{run_url}\n\n"
        f"**详情说明**：\n{reason}\n\n"
        f"---\n"
        f"*说明：通知是否发邮件由 GitHub 的 Watching / Notifications / 邮箱设置决定，工作流没有读取私有邮箱。*"
    )

    if existing:
        # 同一运行和相同状态的通知不重复追加评论
        if marker not in (existing.get("body") or ""):
            for page in range(1, 11):
                comments = request(repository, f"issues/{existing['number']}/comments?per_page=100&page={page}")
                if any(marker in (comment.get("body") or "") for comment in comments):
                    print("Notification already exists in comments; deduplicated.")
                    break
                if len(comments) < 100:
                    request(repository, f"issues/{existing['number']}/comments", "POST", {"body": body})
                    print("New comment appended to existing notification Issue.")
                    break
            else:
                raise RuntimeError("Cannot establish run notification deduplication within 1000 comments")
        request(repository, f"issues/{existing['number']}", "PATCH", {"body": body})
        print(f"Existing Issue [{title}] updated; no duplicate created.")
    else:
        data = {"title": title, "body": body}
        if owner["type"] == "User":
            data["assignees"] = [owner["login"]]
        request(repository, "issues", "POST", data)
        print(f"Notification Issue [{title}] created; GitHub controls email delivery.")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True)
    parser.add_argument("--kind", choices=("model", "review", "merge", "build", "sync", "approved", "merged"), required=True)
    parser.add_argument("--reason", default="自动化任务完成或状态更新，请查看 Actions 报告。")
    parser.add_argument("--run-url", required=True)
    parser.add_argument("--pr-number", type=int, default=None)
    parser.add_argument("--sha", default=None)
    parser.add_argument("--resolved", action="store_true")
    args = parser.parse_args()
    if os.environ.get("UPSTREAM_NOTIFICATION_MODE") == "off":
        print("Failure notifications disabled")
        return
    notify(args.repository, args.kind, args.reason, args.run_url, args.resolved, pr_number=args.pr_number, head_sha=args.sha)


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Prepare a reviewed upstream update. Never merge, release, or deploy it."""
import argparse
import importlib.util
import json
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def run(repo, *args, check=True):
    return subprocess.run(["git", "-C", str(repo), *args], check=check,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def checked(repo, *args):
    result = run(repo, *args, check=False)
    if result.returncode:
        conflicts = run(repo, "diff", "--name-only", "--diff-filter=U", check=False).stdout.decode()
        raise RuntimeError("Git operation failed: " + " ".join(args[:3]) + "\n" +
                           conflicts + result.stderr.decode(errors="replace")[-3000:])
    return result.stdout


def sync(repo, targets, sources=None):
    repo = Path(repo).resolve()
    if run(repo, "status", "--porcelain").stdout:
        raise RuntimeError("Working checkout must be clean; no existing edits will be overwritten")
    config = json.loads((repo / "release/config.json").read_text())
    if set(targets) != set(config["upstream"]):
        raise ValueError("Specify exact panel, agent and web targets together")
    for target in targets.values():
        if not re.fullmatch("[a-f0-9]{40}", target):
            raise ValueError("Targets must be complete commit SHAs")
    if all(targets[k] == v["commit"] for k, v in config["upstream"].items()):
        return "unchanged"
    report = ["# 官方更新同步候选", "", "此任务不合并主分支、不发布版本、不连接或更新运行中的设备。", "",
              "| 组件 | 原基线 | 候选提交 |", "| --- | --- | --- |"]
    for name, item in config["upstream"].items():
        report.append(f"| {name} | `{item['commit']}` | `{targets[name]}` |")
    try:
        with tempfile.TemporaryDirectory(prefix="komari-upstream-sync-") as workspace:
            work = Path(workspace)
            candidate = work / "candidate"
            subprocess.run(["git", "clone", "--quiet", "--no-hardlinks", str(repo), str(candidate)], check=True)
            run(candidate, "config", "user.name", "Komari upstream sync")
            run(candidate, "config", "user.email", "upstream-sync@users.noreply.github.com")
            for name, item in config["upstream"].items():
                if item["commit"] == targets[name]:
                    continue
                source = (sources or {}).get(name, "https://github.com/" + item["repository"] + ".git")
                upstream = work / name
                subprocess.run(["git", "clone", "--quiet", "--no-checkout", source, str(upstream)], check=True)
                checked(upstream, "checkout", "--detach", item["commit"])
                if run(upstream, "merge-base", "--is-ancestor", item["commit"], targets[name], check=False).returncode:
                    raise RuntimeError(name + ": candidate is not a descendant of the pinned baseline; possible downgrade or history rewrite")
                if name == "panel":
                    # The original repository imported a source snapshot without
                    # upstream Git ancestry. Blob-based 3-way application keeps
                    # our changes and detects conflicts without fake merge ancestry.
                    checked(candidate, "fetch", "--no-tags", str(upstream), item["commit"], targets[name])
                    patch = checked(upstream, "diff", "--binary", item["commit"], targets[name], "--", ".",
                                    ":(exclude).github/workflows/**")
                    if patch:
                        delta = work / "panel.diff"
                        delta.write_bytes(patch)
                        checked(candidate, "apply", "--3way", str(delta))
                    report.extend(["", "官方工作流不自动导入，避免覆盖我们的发布来源、同步策略或引入官方自动合并任务。"])
                else:
                    # Run only our trusted patch-preparation code. New upstream
                    # build code is exercised separately with read-only CI credentials.
                    subprocess.run(["python3", str(candidate / "release/prepare.py"), name, str(upstream)], check=True)
                    if name == "agent":
                        for script in ("install.sh", "install.ps1"):
                            shutil.copyfile(candidate / script, upstream / script)
                    run(upstream, "config", "user.name", "Komari upstream sync")
                    run(upstream, "config", "user.email", "upstream-sync@users.noreply.github.com")
                    checked(upstream, "add", "-A")
                    checked(upstream, "commit", "-m", "Preserve our patches and distribution identity")
                    checked(upstream, "rebase", "--onto", targets[name], item["commit"])
                    patch = checked(upstream, "diff", "--binary", targets[name], "HEAD")
                    for old in item["patches"]:
                        (candidate / old).unlink()
                    if item["overlay"]:
                        shutil.rmtree(candidate / item["overlay"])
                    patch_path = f"{'agent' if name == 'agent' else 'web'}-patches/upstream-compatible.patch"
                    (candidate / patch_path).write_bytes(patch)
                    item["patches"] = [patch_path]
                    item["overlay"] = None
                    if name == "agent":
                        for script in ("install.sh", "install.ps1"):
                            shutil.copyfile(upstream / script, candidate / script)
                        # Carry the exact candidate's upstream license into our
                        # distribution notices; changes remain visible in the PR.
                        notice = candidate / "LICENSE.komari-agent"
                        if notice.exists():
                            license_file = upstream / "LICENSE"
                            if not license_file.is_file():
                                raise RuntimeError("agent: upstream LICENSE disappeared; manual review required")
                            shutil.copyfile(license_file, notice)
                item["commit"] = targets[name]
            (candidate / "release/config.json").write_text(json.dumps(config, indent=2) + "\n")
            report.extend(["", "同步候选已生成。构建、协议回归、容器统计及 Windows 兼容性仍需 CI 和隔离环境验证。",
                           "禁止仅因补丁应用成功或构建成功就自动发布、重启服务或替换探针。"])
            (candidate / "release/upstream-report.md").write_text("\n".join(report) + "\n")
            checked(candidate, "add", "-A")
            delta = checked(candidate, "diff", "--cached", "--binary", "HEAD")
            output = work / "candidate.diff"
            output.write_bytes(delta)
            checked(repo, "apply", "--check", str(output))
            checked(repo, "apply", str(output))
            return "ready"
    except (RuntimeError, subprocess.CalledProcessError) as error:
        # A failed component discards the entire temporary candidate. Only the
        # report changes in the caller's checkout, never partially updated pins.
        report.extend(["", "## 需要开发 Agent 处理，不能合并", "", "```text", str(error), "```"])
        (repo / "release/upstream-report.md").write_text("\n".join(report) + "\n")
        return "conflict"


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--targets", required=True, help="JSON file with panel, agent and web commit SHAs")
    args = parser.parse_args()
    result = sync(ROOT, json.loads(Path(args.targets).read_text()))
    print(result)

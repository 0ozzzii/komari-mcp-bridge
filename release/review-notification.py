#!/usr/bin/env python3
"""Route review outcomes without treating a rejection as merge recovery."""
import importlib.util
import os
from pathlib import Path

spec = importlib.util.spec_from_file_location("notices", Path(__file__).with_name("notify-failure.py"))
notices = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notices)


def actions(review_result, decision, merge_result, merge_state):
    result = []
    if review_result in ("failure", "cancelled") or decision == "uncertain":
        result.append(("model", "审查任务失败、被取消或结论不确定；查看 upstream-ai-review artifact 和 Actions。未获批准不会自动合并。", False))
    elif review_result == "success" and decision in ("approve", "request_changes"):
        result.append(("model", "", True))
        if decision == "request_changes":
            result.append(("review", "模型审核拒绝合并；查看 upstream-ai-review artifact 中的 findings，交给开发 Agent 修复后重新测试、审核。", False))
        else:
            result.append(("review", "", True))
    if merge_result in ("failure", "cancelled"):
        result.append(("merge", "审核通过，但合并任务失败或被取消；检查分支保护、权限及 Actions，未绕过合并规则。", False))
    elif merge_result == "success":
        if merge_state in ("merged", "already_closed"):
            result.append(("merge", "", True))
        else:
            result.append(("merge", "审核通过但未完成合并；候选提交、主线或合并条件可能已变化，检查 Actions 后更新分支、重新构建和审核。", False))
    return result


def main():
    if os.environ.get("UPSTREAM_NOTIFICATION_MODE") == "off":
        return
    for kind, reason, resolved in actions(os.environ.get("REVIEW_RESULT", ""),
                                          os.environ.get("DECISION", ""),
                                          os.environ.get("MERGE_RESULT", ""),
                                          os.environ.get("MERGE_STATE", "")):
        notices.notify(os.environ["REPOSITORY"], kind, reason, os.environ["RUN_URL"], resolved)


if __name__ == "__main__":
    main()

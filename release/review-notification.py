#!/usr/bin/env python3
"""Route review outcomes, handling approvals, rejections, merge success, and reasons when unmerged."""
import importlib.util
import json
import os
from pathlib import Path

spec = importlib.util.spec_from_file_location("notices", Path(__file__).with_name("notify-failure.py"))
notices = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notices)


def actions(review_result, decision, merge_result, merge_state, automerge_enabled=""):
    result = []
    # 1. 模型调用异常或审核结论不确定
    if review_result in ("failure", "cancelled") or decision == "uncertain":
        result.append(("model", "审查任务失败、被取消或结论不确定；查看 upstream-ai-review artifact 和 Actions 报告。未获批准不会自动合并。", False))
    elif review_result == "success" and decision in ("approve", "request_changes"):
        result.append(("model", "", True))  # 审查任务正常完成，关闭之前的模型故障 Issue
        if decision == "request_changes":
            result.append(("review", "模型审核拒绝合并；查看 upstream-ai-review artifact 中的 findings，交给开发 Agent 修复后重新测试、审核。", False))
        else:
            result.append(("review", "", True))  # 审查通过，关闭之前的拒绝合并 Issue

    # 2. 审查通过 (approve) 场景：明确区分“实际合并成功”与“批准但未合并（附原因）”
    if review_result == "success" and decision == "approve":
        if merge_result == "success" and merge_state == "merged":
            result.append(("merged", "官方同步候选 PR 已通过 AI 审查并成功自动合并入私有 main 分支。合并已遵守全部安全准入规则，未执行发布或部署。", False))
            result.append(("merge", "", True))  # 合并成功，关闭之前的合并失败 Issue
            result.append(("approved", "", True))  # 关闭之前批准但未合并的通知
        else:
            # 批准但未合并：根据具体上下文详细说明原因
            if merge_result in ("failure", "cancelled"):
                reason = (
                    "官方更新候选 PR 已通过 AI 审查（approve）。\n"
                    "未执行自动合并原因：自动合并任务执行失败或被取消。请检查 GitHub 分支保护限制、权限或 Actions 运行日志。"
                )
            elif merge_state == "held":
                reason = (
                    "官方更新候选 PR 已通过 AI 审查（approve）。\n"
                    "未执行自动合并原因：合并关卡保持等待（未放行）。候选分支不包含最新 main 基线、CI 构建未就绪或受分支保护限制，"
                    "需更新分支、重新跑 CI 并重新审核。"
                )
            elif merge_state == "already_closed":
                reason = "官方更新候选 PR 已通过 AI 审查（approve）。未执行合并原因：该 PR 已经处于关闭状态，未重复执行合并。"
            elif merge_result == "skipped" or automerge_enabled != "true":
                reason = (
                    "官方更新候选 PR 已通过 AI 审查（approve）。\n"
                    f"未执行自动合并原因：自动合并开关 UPSTREAM_AI_AUTOMERGE 当前未开启或未执行（当前配置值: '{automerge_enabled}'），"
                    "未触发自动合并，保持 PR 开启等待人工确认或手动合并。"
                )
            else:
                reason = f"官方更新候选 PR 已通过 AI 审查（approve），但尚未完成合并（合并任务状态: '{merge_result}', 阶段: '{merge_state}'）。"

            result.append(("approved", reason, False))

            # 若合并存在明确失败或 held，同步维护 merge 故障 Issue
            if merge_result in ("failure", "cancelled"):
                result.append(("merge", "审核通过，但合并任务失败或被取消；检查分支保护、权限及 Actions，未绕过合并规则。", False))
            elif merge_state == "held":
                result.append(("merge", "审核通过但未完成合并；候选提交、主线或合并条件可能已变化，检查 Actions 后更新分支、重新构建和审核。", False))
            elif merge_result == "success" and merge_state != "already_closed":
                result.append(("merge", "合并任务返回成功，但没有确认 merged 状态；不能据此声明已合并。请检查 Actions 的合并结果。", False))

    return result


def main():
    if os.environ.get("UPSTREAM_NOTIFICATION_MODE") == "off":
        print("Notifications globally disabled via UPSTREAM_NOTIFICATION_MODE=off")
        return

    repository = os.environ["REPOSITORY"]
    run_url = os.environ["RUN_URL"]
    head_sha = os.environ.get("HEAD_SHA")
    review_result = os.environ.get("REVIEW_RESULT", "")
    decision = os.environ.get("DECISION", "")
    merge_result = os.environ.get("MERGE_RESULT", "")
    merge_state = os.environ.get("MERGE_STATE", "")
    automerge_enabled = os.environ.get("AUTOMERGE_ENABLED", "")
    review_json_path = os.environ.get("REVIEW_JSON_PATH")

    pr_number = None
    if review_json_path and Path(review_json_path).exists():
        try:
            rdata = json.loads(Path(review_json_path).read_text())
            pr_number = rdata.get("pr_number")
            if not head_sha:
                head_sha = rdata.get("reviewed_sha")
        except Exception as e:
            print("Failed to read review JSON metadata:", e)

    items = actions(review_result, decision, merge_result, merge_state, automerge_enabled)
    for kind, reason, resolved in items:
        notices.notify(
            repository=repository,
            kind=kind,
            reason=reason,
            run_url=run_url,
            resolved=resolved,
            pr_number=pr_number,
            head_sha=head_sha
        )


if __name__ == "__main__":
    main()

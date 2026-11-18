#!/usr/bin/env python3
"""T10 验收场景 C：失败与 HumanGate 都必须有明确说明。

从 `test_task_router_coordinate_ab.py` 再拆一层（500 行硬线）。夹具与机械层在
`test_task_router_coordinate_fixtures.py` / `test_task_router_coordinate_common.py`。
"""

from __future__ import annotations

import json
import time
import traceback

from test_task_router_coordinate_common import (
    ApiError,
    activity_by_id,
    activity_text,
    clip,
    evidence_activities,
    evidence_decisions,
    evidence_events,
    evidence_nodes,
    has_internal_id,
    is_router_effect,
    node_by_id,
    result,
    wait_decision,
    wait_effect_activity,
    wait_event_decision,
    wait_event_terminal,
    wait_for,
)

EXEC_TIMEOUT = 300
GATE_TIMEOUT = 240
SETTLE_TIMEOUT = 300


# ── 场景 C：失败与 gate → 明确说明 ─────────────────────────────────────────


def scenario_c_failure(client, db, fixture_versions):
    """子项 C1：确定性失败必须有明确说明，且事件终态 done。"""
    started = time.time()
    task_id = client.create_task("T10-C1 失败说明")
    # 必须点名「必然失败」的那条流程：两条流程描述都含「执行」，只靠动作词分不开。
    body = "请执行那个一定会失败的流程（T10 必然失败），跑完告诉我结果。"
    user, _ = client.post_activity(task_id, body)
    decision = wait_decision(client, task_id, user["id"])
    evidence = {
        "task_id": task_id,
        "request": {"body": body, "workflow_choice": "empty"},
        "decision": {"id": decision.get("id"), "status": decision.get("status"),
                     "action_kinds": decision.get("action_kinds"), "latency_ms": decision.get("latency_ms")},
        "fixture_versions": fixture_versions,
    }
    try:
        wait_for(
            lambda: client.node_runs(task_id),
            lambda runs: runs and all(r.get("status") in ("success", "fail", "cancelled", "waiting_user")
                                      for r in runs),
            EXEC_TIMEOUT,
            what="failing node terminal",
        )
    except TimeoutError:
        pass
    nodes = client.node_runs(task_id)
    failed_run = next((n.get("run_id") for n in nodes if n.get("run_id")), "")
    event = wait_event_terminal(db, task_id, "run_failed", SETTLE_TIMEOUT, run_id=failed_run or None)
    failure_decision = wait_event_decision(client, task_id, "run_failed")
    progress = wait_effect_activity(client, task_id, user["id"], failure_decision.get("id"), SETTLE_TIMEOUT)
    text = clip(activity_text(progress))
    evidence.update(
        {
            "node_runs": evidence_nodes(nodes),
            "router_events": evidence_events(db, task_id),
            "failure_event": {"id": event.get("id"), "kind": event.get("kind"), "status": event.get("status"),
                              "last_error": clip(event.get("last_error") or "")},
            "failure_decision": {
                "id": failure_decision.get("id"),
                "trigger_kind": failure_decision.get("trigger_kind"),
                "status": failure_decision.get("status"),
                "action_kinds": failure_decision.get("action_kinds"),
            },
            "progress_activity": {
                "id": progress.get("id"), "purpose": progress.get("purpose"), "status": progress.get("status"),
                "body_excerpt": text, "mentions_internal_id": has_internal_id(text),
            },
            "decisions": evidence_decisions(client, task_id),
            "task_status": client.task(task_id).get("status"),
            "activities": evidence_activities(client, task_id),
        }
    )
    chosen = next((a.get("workflow_id") for a in (client.decision_detail(task_id, decision["id"]).get("actions") or [])
                   if a.get("kind") == "run_workflow"), "")
    evidence["chosen_workflow"] = chosen
    failing = [n for n in nodes if n.get("status") == "fail"]
    error_text = json.dumps([n.get("output") or {} for n in failing], ensure_ascii=False)
    task_status = client.task(task_id).get("status")
    checks = {
        "chosen_fail_fixture": chosen == "t10-fail",
        "node_failed_with_missing_agent": bool(failing) and "t10-missing-agent" in error_text,
        "failure_event_done": event.get("status") == "done",
        "failure_decision_applied": failure_decision.get("status") == "applied",
        "failure_decision_is_event": (failure_decision.get("trigger_kind") or "").startswith("run_"),
        # 对话模型下成功结算的任务也不会变成 success（下一轮要等用户），
        # 所以这里只断言「没有被标成成功」，实际值记进 evidence["task_status"]。
        "task_not_claimed_success": task_status != "success",
        "report_is_progress": progress.get("purpose") == "progress",
        "report_states_failure": any(m in text for m in ("失败", "没有跑通", "出错", "错误")),
        "report_has_no_internal_id": not has_internal_id(text),
    }
    evidence["checks"] = checks
    passed = all(checks.values())
    notes = "" if passed else "未通过的判据：" + "、".join(k for k, v in checks.items() if not v)
    return result("C1", passed, evidence, int((time.time() - started) * 1000), notes)


def scenario_c_gate(client, db, fixture_versions):
    """子项 C2：gate 等待 → 普通留言不代批 → 专用端点批准 → 下游继续 → 重复 409。"""
    started = time.time()
    task_id = client.create_task("T10-C2 gate 审批")
    body = "请执行需要人工审批的那个流程（T10 需要人工审批），跑到审批点停住。"
    user, _ = client.post_activity(task_id, body)
    decision = wait_decision(client, task_id, user["id"])
    evidence = {
        "task_id": task_id,
        "request": {"body": body, "workflow_choice": "empty"},
        "decision": {"id": decision.get("id"), "status": decision.get("status"),
                     "action_kinds": decision.get("action_kinds")},
        "fixture_versions": fixture_versions,
    }
    try:
        wait_for(
            lambda: client.node_runs(task_id),
            lambda runs: any(r.get("node_id") == "gate" and r.get("status") == "waiting_user" for r in runs),
            GATE_TIMEOUT,
            what="gate node waiting_user",
        )
    except TimeoutError as err:
        evidence["error"] = clip(str(err), 400)
        evidence["node_runs"] = evidence_nodes(client.node_runs(task_id))
        return result("C2", False, evidence, int((time.time() - started) * 1000),
                      "gate 节点没有进入 waiting_user；无法验证审批语义。", status="blocked")
    gate_run = next((r.get("run_id") for r in client.node_runs(task_id) if r.get("run_id")), "")
    gate_event = wait_event_terminal(db, task_id, "gate_waiting", GATE_TIMEOUT, run_id=gate_run or None)
    gate_decision = wait_event_decision(client, task_id, "gate_waiting")
    gate_report = wait_effect_activity(client, task_id, user["id"], gate_decision.get("id"), SETTLE_TIMEOUT)
    gate_text = clip(activity_text(gate_report))
    # 负例：普通留言永远不能代批 gate。
    neg_user, neg_placeholder = client.post_activity(task_id, "批准继续。")
    neg_decision = wait_decision(client, task_id, neg_user["id"])
    nodes_after_neg = client.node_runs(task_id)
    gate_node = node_by_id(nodes_after_neg, "gate") or {}
    neg_items = client.activities(task_id)
    neg_placeholder_after = activity_by_id(neg_items, neg_placeholder.get("id")) or {}
    neg_reply = next((a for a in neg_items if a.get("role") == "agent" and a.get("reply_to_id") == neg_user["id"]), None)
    # 正例：专用端点批准。
    approve_status, approve_body = None, None
    try:
        approve_status, approve_body = client.human_gate(task_id, "gate", "approve")
    except ApiError as err:
        approve_status, approve_body = err.status, clip(err.body, 300)
    # 产品契约（D4）：同向重复请求幂等 200，反向决定 409。
    # 依据 `task_store_workflow_human_gate.go:65-73`（current == status → 返回既有行、
    # changed=false）与 :74-76（current != waiting_user → AlreadyDecided）。
    repeat_status = None
    try:
        _, _ = client.human_gate(task_id, "gate", "approve")
        repeat_status = 200
    except ApiError as err:
        repeat_status = err.status
    opposite_status = None
    try:
        _, _ = client.human_gate(task_id, "gate", "reject")
        opposite_status = 200
    except ApiError as err:
        opposite_status = err.status
    try:
        wait_for(
            lambda: client.node_runs(task_id),
            lambda runs: runs and all(r.get("status") in ("success", "fail", "cancelled", "waiting_user")
                                      for r in runs),
            EXEC_TIMEOUT,
            what="downstream nodes after approval",
        )
    except TimeoutError:
        pass
    final_nodes = client.node_runs(task_id)
    settle_event = wait_event_terminal(db, task_id, "run_settled", SETTLE_TIMEOUT, run_id=gate_run or None)
    settle_decision = wait_event_decision(client, task_id, "run_settled")
    effect_rows = [a for a in client.activities(task_id) if is_router_effect(a)]
    # 这个任务有两条事件（gate_waiting、run_settled），各自一条 effect 行才正常；
    # 「不重复汇报」按「同一决策只落一条 effect 行」判，不是按总行数。
    settle_progress = [a for a in effect_rows if a.get("router_decision_id") == settle_decision.get("id")]
    evidence.update(
        {
            "gate_event": {"id": gate_event.get("id"), "kind": gate_event.get("kind"),
                           "status": gate_event.get("status"), "last_error": clip(gate_event.get("last_error") or "")},
            "gate_decision": {"id": gate_decision.get("id"), "trigger_kind": gate_decision.get("trigger_kind"),
                              "status": gate_decision.get("status"),
                              "action_kinds": gate_decision.get("action_kinds")},
            "settle_decision": {"id": settle_decision.get("id"),
                                "trigger_kind": settle_decision.get("trigger_kind"),
                                "status": settle_decision.get("status"),
                                "action_kinds": settle_decision.get("action_kinds")},
            "gate_report": {
                "id": gate_report.get("id"), "purpose": gate_report.get("purpose"),
                "body_excerpt": gate_text, "mentions_internal_id": has_internal_id(gate_text),
                "mentions_approval": any(m in gate_text for m in ("审批", "批准", "驳回")),
            },
            "negative_comment": {
                "request": "批准继续。",
                "decision": {
                    "id": neg_decision.get("id"), "status": neg_decision.get("status"),
                    "action_kinds": neg_decision.get("action_kinds"), "latency_ms": neg_decision.get("latency_ms"),
                    "error": clip(neg_decision.get("error") or ""),
                },
                "gate_status_after": gate_node.get("status"),
                "placeholder_after": {
                    "id": neg_placeholder_after.get("id"),
                    "hidden": bool(neg_placeholder_after.get("hidden")),
                    "status": neg_placeholder_after.get("status"),
                    "body_excerpt": clip(neg_placeholder_after.get("body") or ""),
                    "summary_excerpt": clip(neg_placeholder_after.get("summary") or ""),
                },
                "visible_reply": None if neg_reply is None else {
                    "id": neg_reply.get("id"), "purpose": neg_reply.get("purpose"),
                    "status": neg_reply.get("status"), "body_excerpt": clip(neg_reply.get("body") or ""),
                },
            },
            "approve": {"http_status": approve_status,
                        "node_run_status": ((approve_body or {}).get("node_run") or {}).get("status")},
            "repeat_approve_http_status": repeat_status,
            "opposite_reject_http_status": opposite_status,
            "effect_activity_ids": [a.get("id") for a in effect_rows],
            "settle_effect_activity_ids": [a.get("id") for a in settle_progress],
            "node_runs_final": evidence_nodes(final_nodes),
            "settle_event": {"id": settle_event.get("id"), "kind": settle_event.get("kind"),
                             "status": settle_event.get("status")},
            "progress_activity_count": len(settle_progress),
            "router_events": evidence_events(db, task_id),
            "decisions": evidence_decisions(client, task_id),
            "task_status": client.task(task_id).get("status"),
            "activities": evidence_activities(client, task_id),
        }
    )
    finish_node = node_by_id(final_nodes, "finish") or {}
    placeholder_ok = bool(neg_placeholder_after.get("hidden")) or bool(
        clip(neg_placeholder_after.get("body") or neg_placeholder_after.get("summary") or "")
    )
    checks = {
        "gate_event_done": gate_event.get("status") == "done",
        "gate_report_explains_approval": evidence["gate_report"]["mentions_approval"],
        "gate_report_has_no_internal_id": not has_internal_id(gate_text),
        "normal_comment_did_not_approve": gate_node.get("status") == "waiting_user",
        "normal_comment_not_blank_placeholder": placeholder_ok,
        "gate_decision_applied": gate_decision.get("status") == "applied",
        "settle_decision_applied": settle_decision.get("status") == "applied",
        "approve_200": approve_status == 200,
        "repeat_same_decision_idempotent": repeat_status == 200,
        "opposite_decision_409": opposite_status == 409,
        "downstream_node_ran": finish_node.get("status") == "success",
        "settle_event_done": settle_event.get("status") == "done",
        "settle_reported_once": len(settle_progress) == 1,
        "no_duplicate_effect_rows": len([a.get("id") for a in effect_rows]) == len({a.get("id") for a in effect_rows}),
    }
    evidence["checks"] = checks
    passed = all(checks.values())
    notes = "" if passed else "未通过的判据：" + "、".join(k for k, v in checks.items() if not v)
    if not checks["normal_comment_not_blank_placeholder"]:
        notes += "（普通留言把占位留成空壳可见行）"
    return result("C2", passed, evidence, int((time.time() - started) * 1000), notes)


def scenario_c(client, db, fixture_versions):
    started = time.time()

    def run(name, fn):
        """子项异常不能吞掉另一个子项的证据：异常就地变成带 traceback 的 fail。"""
        try:
            return fn(client, db, fixture_versions)
        except Exception as err:  # noqa: BLE001  验收脚本要把任何异常变成显式失败
            return result(name, False,
                          {"error": clip(repr(err), 600), "traceback": clip(traceback.format_exc(), 1200)},
                          int((time.time() - started) * 1000), "子项脚本异常，非产品判定")

    c1 = run("C1", scenario_c_failure)
    c2 = run("C2", scenario_c_gate)
    c2["scenario"] = "C2"
    passed = c1["pass"] and c2["pass"]
    blocked = c1.get("status") == "blocked" or c2.get("status") == "blocked"
    merged = result(
        "C", passed,
        {"failure": c1["evidence"], "gate": c2["evidence"], "sub_results": [c1, c2]},
        int((time.time() - started) * 1000),
        " / ".join(n for n in (c1.get("notes"), c2.get("notes")) if n),
        status=None if passed else ("blocked" if blocked else "fail"),
    )
    merged["sub_results"] = [c1, c2]
    return merged

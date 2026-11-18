#!/usr/bin/env python3
"""T10 验收场景 A/B：需求→执行→主动汇报、中途留言有归宿。

从 `test_task_router_coordinate.py` 拆出（500 行硬线）。这里只放场景编排，
机械部分（HTTP/只读库/取证）在 `test_task_router_coordinate_common.py`，
夹具定义在 `test_task_router_coordinate_fixtures.py`。

判据一律「有库行/有决策/有活动」才算通过；任何跑不起来的子项写 blocked。
"""

from __future__ import annotations

import time

from test_task_router_coordinate_common import (
    activity_by_id,
    activity_text,
    clip,
    evidence_activities,
    evidence_continue_requests,
    evidence_decisions,
    evidence_events,
    evidence_nodes,
    has_internal_id,
    node_by_id,
    is_router_effect,
    result,
    wait_decision,
    wait_effect_activity,
    wait_event_decision,
    wait_event_terminal,
    wait_for,
)

EXEC_TIMEOUT = 300
SETTLE_TIMEOUT = 300


def settle_and_report(client, db, task_id, user_activity_id, run_id=None):
    """等 Run 结算事件 + 该事件回合写的汇报行；返回 (事件行, 决策, 汇报活动)。

    一条留言会先得到消息回合的 `say`（也是 purpose=progress），再得到事件回合的
    确定性 effect 行。要先等事件决策落库，才能按 decision/effect 精确收窄。
    """
    event = wait_event_terminal(db, task_id, "run_settled", SETTLE_TIMEOUT, run_id=run_id)
    decision = wait_event_decision(client, task_id, "run_settled")
    activity = wait_effect_activity(client, task_id, user_activity_id, decision.get("id"), SETTLE_TIMEOUT)
    return event, decision, activity

# ── 场景 A：一次需求 → 执行 → 主动汇报 ──────────────────────────────────────


def scenario_a(client, db, fixture_versions):
    started = time.time()
    task_id = client.create_task("T10-A 需求→执行→汇报")
    body = "请执行快速验证流程：这是 t10-quick 的一步式确认，跑完回我一句结果就行。"
    user, _ = client.post_activity(task_id, body)
    decision = wait_decision(client, task_id, user["id"])
    detail = client.decision_detail(task_id, decision["id"])
    kinds = set(decision.get("action_kinds") or [])
    evidence = {
        "task_id": task_id,
        "request": {"body": body, "workflow_choice": "empty"},
        "decision": {
            "id": decision.get("id"),
            "trigger_kind": decision.get("trigger_kind"),
            "status": decision.get("status"),
            "action_kinds": decision.get("action_kinds"),
            "latency_ms": decision.get("latency_ms"),
            "error": clip(decision.get("error") or ""),
        },
        "actions": [
            {"kind": a.get("kind"), "workflow_id": a.get("workflow_id"), "body": clip(a.get("body") or ""),
             "purpose": a.get("purpose")}
            for a in (detail.get("actions") or [])
        ],
        "router_status": client.router_status(),
    }
    if "run_workflow" not in kinds:
        evidence["node_runs"] = evidence_nodes(client.node_runs(task_id))
        return result(
            "A", False, evidence, int((time.time() - started) * 1000),
            f"Router 没有选执行动作（action_kinds={sorted(kinds)}）；场景 A 需要一次真实执行。",
        )
    chosen = next(a for a in (detail.get("actions") or []) if a.get("kind") == "run_workflow")
    nodes = wait_for(
        lambda: client.node_runs(task_id),
        lambda runs: runs and all(r.get("status") in ("success", "fail", "cancelled", "waiting_user") for r in runs),
        EXEC_TIMEOUT,
        what="workflow node terminal",
    )
    nodes_after = client.node_runs(task_id)
    run_ids = {n.get("run_id") for n in nodes_after}
    run_id = next((i for i in sorted(run_ids) if i), "")
    event, settle_decision, progress = settle_and_report(client, db, task_id, user["id"], run_id=run_id)
    items_after = client.activities(task_id)
    effect_rows = [a for a in items_after if is_router_effect(a)]
    node_ok = nodes_after and all(n.get("status") == "success" for n in nodes_after)
    evidence.update(
        {
            "chosen_workflow": chosen.get("workflow_id"),
            "fixture_versions": fixture_versions,
            "run_ids": sorted(i for i in run_ids if i),
            "node_runs": evidence_nodes(nodes_after),
            "router_events": evidence_events(db, task_id),
            "settle_event": {
                "id": event.get("id"), "kind": event.get("kind"), "status": event.get("status"),
                "run_id": event.get("run_id"), "completed_at": event.get("completed_at"),
                "last_error": clip(event.get("last_error") or ""),
            },
            "settle_decision": {
                "id": settle_decision.get("id"),
                "trigger_kind": settle_decision.get("trigger_kind"),
                "trigger_ref": settle_decision.get("trigger_ref"),
                "status": settle_decision.get("status"),
                "action_kinds": settle_decision.get("action_kinds"),
                "latency_ms": settle_decision.get("latency_ms"),
                "error": clip(settle_decision.get("error") or ""),
            },
            "progress_activity": {
                "id": progress.get("id"),
                "purpose": progress.get("purpose"),
                "run_id": progress.get("run_id") or "",
                "reply_to_id": progress.get("reply_to_id"),
                "status": progress.get("status"),
                "body_excerpt": clip(activity_text(progress)),
                "mentions_internal_id": has_internal_id(activity_text(progress)),
            },
            "router_effect_activity_ids": [a.get("id") for a in effect_rows],
            "progress_activity_count": len(effect_rows),
            "decisions": evidence_decisions(client, task_id),
            "activities": evidence_activities(client, task_id),
            "task_status": client.task(task_id).get("status"),
        }
    )
    checks = {
        "execution_succeeded": bool(node_ok),
        "settle_event_done": event.get("status") == "done",
        "settle_decision_applied": settle_decision.get("status") == "applied",
        "settle_decision_is_event": (settle_decision.get("trigger_kind") or "").startswith("run_"),
        "progress_reported_once": len(effect_rows) == 1,
        "progress_run_id_empty": not (progress.get("run_id") or ""),
        "progress_replies_to_user": progress.get("reply_to_id") == user["id"],
        "progress_has_no_internal_id": not has_internal_id(activity_text(progress)),
    }
    evidence["checks"] = checks
    passed = all(checks.values())
    notes = ""
    if not passed:
        notes = "未通过的判据：" + "、".join(k for k, v in checks.items() if not v)
    return result("A", passed, evidence, int((time.time() - started) * 1000), notes)


# ── 场景 B：中途留言 → 有归宿 ──────────────────────────────────────────────


def scenario_b(client, db, fixture_versions):
    started = time.time()
    task_id = client.create_task("T10-B 中途留言归宿")
    body1 = "请执行快速验证流程，跑完回我一句结果。"
    user1, _ = client.post_activity(task_id, body1)
    decision1 = wait_decision(client, task_id, user1["id"])
    evidence = {
        "task_id": task_id,
        "first_request": {"body": body1, "workflow_choice": "empty"},
        "first_decision": {"id": decision1.get("id"), "status": decision1.get("status"),
                           "action_kinds": decision1.get("action_kinds")},
        "fixture_versions": fixture_versions,
    }
    try:
        running = wait_for(
            lambda: client.node_runs(task_id),
            lambda runs: any(r.get("status") in ("running", "queued") for r in runs),
            90,
            what="a node run enters running",
        )
    except TimeoutError as err:
        evidence["error"] = clip(str(err), 400)
        node = node_by_id(client.node_runs(task_id), "run") or {}
        return result("B", False, evidence, int((time.time() - started) * 1000),
                      "未能在节点运行中插入第二条留言（执行太快/没起来）；无法验证中途归宿。",
                      status="blocked")
    active = next(r for r in running if r.get("status") in ("running", "queued"))
    body2 = "补充：完成后请顺带说明耗时。"
    user2, placeholder2 = client.post_activity(task_id, body2)
    decision2 = wait_decision(client, task_id, user2["id"])
    detail2 = client.decision_detail(task_id, decision2["id"])
    kinds2 = set(decision2.get("action_kinds") or [])
    # 等整轮收尾后再读归宿，不能只看运行中快照。
    try:
        wait_for(
            lambda: client.node_runs(task_id),
            lambda runs: runs and all(r.get("status") in ("success", "fail", "cancelled", "waiting_user")
                                      for r in runs),
            EXEC_TIMEOUT,
            what="node runs terminal after the second message",
        )
    except TimeoutError:
        pass
    items_after = client.activities(task_id)
    user2_after = activity_by_id(items_after, user2["id"]) or {}
    placeholder_after = activity_by_id(items_after, placeholder2.get("id")) or {}
    crs = [r for r in db.continue_requests(task_id) if r.get("comment_id") == user2["id"]]
    visible_reply = next(
        (a for a in items_after if a.get("role") == "agent" and a.get("reply_to_id") == user2["id"]), None
    )
    delivery_failure = next(
        (a for a in items_after if str(a.get("id", "")).endswith(":delivery_failed") and user2["id"] in str(a.get("id", ""))),
        None,
    )
    terminal_cr = {"consumed", "delivery_failed", "blocked", "unknown"}
    cr_status = crs[0]["status"] if crs else ""
    evidence.update(
        {
            "active_node_before_second_message": {
                "node_run_id": active.get("id"), "node_id": active.get("node_id"),
                "status": active.get("status"), "session_key": active.get("session_key"),
            },
            "second_request": {"body": body2, "workflow_choice": "empty"},
            "second_decision": {
                "id": decision2.get("id"),
                "trigger_kind": decision2.get("trigger_kind"),
                "status": decision2.get("status"),
                "action_kinds": decision2.get("action_kinds"),
                "latency_ms": decision2.get("latency_ms"),
                "error": clip(decision2.get("error") or ""),
            },
            "second_actions": [
                {"kind": a.get("kind"), "body": clip(a.get("body") or ""), "reason": clip(a.get("reason") or "")}
                for a in (detail2.get("actions") or [])
            ],
            "continue_requests": evidence_continue_requests(db, task_id),
            "user_activity_after": {
                "id": user2_after.get("id"),
                "run_id": user2_after.get("run_id") or "",
                "node_run_id": user2_after.get("node_run_id") or "",
            },
            "placeholder_after": {
                "id": placeholder_after.get("id"),
                "hidden": bool(placeholder_after.get("hidden")),
                "status": placeholder_after.get("status"),
                "body_excerpt": clip(placeholder_after.get("body") or ""),
            },
            "visible_reply": None if visible_reply is None else {
                "id": visible_reply.get("id"),
                "purpose": visible_reply.get("purpose"),
                "status": visible_reply.get("status"),
                "body_excerpt": clip(visible_reply.get("body") or ""),
            },
            "delivery_failure_activity": None if delivery_failure is None else {
                "id": delivery_failure.get("id"),
                "body_excerpt": clip(delivery_failure.get("body") or ""),
            },
            "router_events": evidence_events(db, task_id),
            "activities": evidence_activities(client, task_id),
        }
    )
    if "deliver_to_active_run" in kinds2:
        has_home = cr_status in terminal_cr
        d1_regression = cr_status == "delivery_failed" and "agent required" in clip(
            (delivery_failure or {}).get("body") or ""
        )
        checks = {
            "delivered_decision_applied": decision2.get("status") == "applied",
            "continue_request_terminal": has_home,
            "no_agent_required_regression": not d1_regression,
            "no_silent_disappearance": has_home or visible_reply is not None,
        }
    else:
        checks = {
            "decision_applied": decision2.get("status") == "applied",
            "no_silent_disappearance": visible_reply is not None or bool(user2_after.get("node_run_id")),
            "no_agent_required_regression": "agent required" not in clip(
                (delivery_failure or {}).get("body") or ""
            ),
        }
    evidence["checks"] = checks
    passed = all(checks.values())
    notes = ""
    if not passed:
        notes = "未通过的判据：" + "、".join(k for k, v in checks.items() if not v)
    if checks.get("no_agent_required_regression") is False:
        notes += "（D-1 回归：deliver 出现 agent required → 直接 BLOCKED）"
        return result("B", False, evidence, int((time.time() - started) * 1000), notes, status="blocked")
    return result("B", passed, evidence, int((time.time() - started) * 1000), notes)

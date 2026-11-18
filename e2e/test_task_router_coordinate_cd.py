#!/usr/bin/env python3
"""T10 验收场景 D：隔离进程重启对账。

从 `test_task_router_coordinate_scenarios.py` 再拆一层（500 行硬线）。场景 D 只动
`--iso-root` 自己的 pid/端口，并且每次停/起都核对线上 7331 的 health。
"""

from __future__ import annotations

import os
import subprocess
import time

from test_task_router_coordinate_common import (
    ApiError,
    clip,
    evidence_events,
    evidence_nodes,
    is_router_effect,
    result,
    wait_decision,
    wait_for,
)

EXEC_TIMEOUT = 300
GATE_TIMEOUT = 240
SETTLE_TIMEOUT = 300
RESTART_CONVERGE_TIMEOUT = 180
TERMINAL_EVENT = {"done", "failed", "skipped"}


# ── 场景 D：隔离进程重启对账 ───────────────────────────────────────────────


def iso_env(root, port):
    env = dict(os.environ)
    env.pop("ANTHROPIC_BASE_URL", None)
    env["MINDFS_ISO_ROOT"] = root
    env["MINDFS_ISO_PORT"] = str(port)
    env["MINDFS_ISO_KEEP"] = "1"
    return env


def iso_ctl(repo, root, port, action, timeout=900):
    """只动隔离根自己的 pid/端口。stdout/stderr 一并返回，写进证据。"""
    proc = subprocess.run(
        ["bash", os.path.join(repo, "scripts", "mindfs-iso.sh"), action],
        cwd=repo,
        env=iso_env(root, port),
        capture_output=True,
        text=True,
        timeout=timeout,
    )
    return {
        "action": action,
        "returncode": proc.returncode,
        "stdout_tail": clip("\n".join((proc.stdout or "").strip().splitlines()[-14:]), 1200),
        "stderr_tail": clip("\n".join((proc.stderr or "").strip().splitlines()[-10:]), 800),
    }


def snapshot_task(db, task_id):
    return {
        "run_ids": db.run_ids(task_id),
        "activity_ids": db.activity_ids(task_id),
        "decision_ids": db.decision_ids(task_id),
        "events": db.events(task_id),
    }


def scenario_d(client, db, fixture_versions, iso_root, iso_port, repo):
    """重启前抓 pending/claimed 窗口，重启后断言全部收敛且无重复汇报。"""
    started = time.time()
    task_id = client.create_task("T10-D 重启对账")
    body = "请执行快速验证流程，跑完回我一句结果。"
    user, _ = client.post_activity(task_id, body)
    decision = wait_decision(client, task_id, user["id"])
    evidence = {
        "task_id": task_id,
        "request": {"body": body, "workflow_choice": "empty"},
        "first_decision": {"id": decision.get("id"), "status": decision.get("status"),
                           "action_kinds": decision.get("action_kinds")},
        "fixture_versions": fixture_versions,
        "iso_root": iso_root,
        "iso_port": iso_port,
        "online_health_before": client.health(),
    }
    if not iso_root:
        evidence["node_runs"] = evidence_nodes(client.node_runs(task_id))
        return result("D", False, evidence, int((time.time() - started) * 1000),
                      "没有 --iso-root：脚本不会自行重启实例，重启子项 blocked（其余子项由报告说明）。",
                      status="blocked")
    run_id = ""
    window = []
    try:
        run_id = wait_for(
            lambda: [n.get("run_id") for n in client.node_runs(task_id) if n.get("run_id")],
            lambda ids: bool(ids), EXEC_TIMEOUT, what="workflow run created", interval=1.0,
        )[0]
        # 盯住结算瞬间：Run 投影一进终态，事件行一出现就立刻抓窗口。
        # 0.2s 采样：事件行由结算钩子写入，而消费只在 30s 一拍的 tick 里发生，
        # 所以 pending/claimed 窗口最长约 30s；采到就立刻停进程。
        wait_for(
            lambda: [e for e in db.events(task_id) if e.get("run_id") == run_id
                     and e.get("kind") in ("run_settled", "run_failed")],
            lambda rows: bool(rows), EXEC_TIMEOUT, what="settle event row appears", interval=0.2,
        )
        window = db.events(task_id)
    except TimeoutError as err:
        evidence["error"] = clip(str(err), 400)
        evidence["node_runs"] = evidence_nodes(client.node_runs(task_id))
        evidence["events"] = evidence_events(db, task_id)
        return result("D", False, evidence, int((time.time() - started) * 1000),
                      "没能等到结算事件行出现，重启窗口未取得。", status="blocked")
    pending = [e for e in window if e.get("status") in ("pending", "claimed")]
    before = snapshot_task(db, task_id)
    evidence["snapshot_before_restart"] = {
        "run_count": len(before["run_ids"]),
        "activity_count": len(before["activity_ids"]),
        "decision_count": len(before["decision_ids"]),
        "events": evidence_events(db, task_id),
        "pending_or_claimed": [
            {"id": e.get("id"), "kind": e.get("kind"), "status": e.get("status")} for e in pending
        ],
        "run_id": run_id,
    }
    stop = iso_ctl(repo, iso_root, iso_port, "stop")
    start = iso_ctl(repo, iso_root, iso_port, "start")
    evidence["restart"] = {"stop": stop, "start": start}
    # 重启后等事件全部收敛（有界）。
    converged = None
    deadline = time.time() + RESTART_CONVERGE_TIMEOUT
    while time.time() < deadline:
        try:
            rows = db.events(task_id)
        except Exception:  # noqa: BLE001  库在重启瞬间可能短暂不可读
            rows = []
        if rows and all(e.get("status") in TERMINAL_EVENT for e in rows):
            converged = rows
            break
        time.sleep(2.0)
    after = snapshot_task(db, task_id)
    try:
        effect_rows = [
            a for a in client.activities(task_id)
            if is_router_effect(a) and a.get("reply_to_id") == user["id"]
        ]
    except ApiError as err:
        effect_rows = []
        evidence["post_restart_activity_error"] = clip(str(err), 300)
    event_ids = [e.get("id") for e in after["events"]]
    effect_ids = [a.get("id") for a in effect_rows]
    effect_decisions = [a.get("router_decision_id") for a in effect_rows if a.get("router_decision_id")]
    # 重启后允许新增的只有「确定性 effect 行」：任何别的活动都说明重启改写了既有结果。
    added_ids = sorted(set(after["activity_ids"]) - set(before["activity_ids"]))
    added_non_effect = [i for i in added_ids if not i.startswith("act-rtev-")]
    evidence["snapshot_after_restart"] = {
        "run_count": len(after["run_ids"]),
        "activity_count": len(after["activity_ids"]),
        "decision_count": len(after["decision_ids"]),
        "events": evidence_events(db, task_id),
        "effect_activity_count": len(effect_rows),
        "effect_activities": [
            {"id": a.get("id"), "purpose": a.get("purpose"), "status": a.get("status"),
             "router_decision_id": a.get("router_decision_id"),
             "body_excerpt": clip(a.get("body") or "")}
            for a in effect_rows
        ],
        "added_activity_ids": added_ids,
        "added_non_effect_ids": added_non_effect,
        "online_health_after": client.health(),
        "router_status": client.router_status(),
    }
    checks = {
        "caught_pending_window": bool(pending),
        "events_all_terminal_after_restart": bool(converged) and all(
            e.get("status") in TERMINAL_EVENT for e in after["events"]
        ),
        "no_duplicate_event_rows": len(event_ids) == len(set(event_ids)),
        "no_new_run": len(after["run_ids"]) == len(before["run_ids"]),
        "no_duplicate_effect_rows": len(effect_ids) == len(set(effect_ids)) and len(effect_rows) == 1,
        "same_progress_decision_once": len(effect_decisions) == len(set(effect_decisions)),
        "activity_set_only_grew_by_effect": not added_non_effect,
        "online_health_200_after": client.health() == 200,
    }
    evidence["checks"] = checks
    passed = all(checks.values())
    notes = "" if passed else "未通过的判据：" + "、".join(k for k, v in checks.items() if not v)
    if not pending:
        notes += "（未能在消费前停下：事件已被 30s 循环消费，重启恢复部分如实记 blocked）"
        return result("D", False, evidence, int((time.time() - started) * 1000), notes, status="blocked")
    return result("D", passed, evidence, int((time.time() - started) * 1000), notes)

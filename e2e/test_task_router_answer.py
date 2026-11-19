#!/usr/bin/env python3
"""G-MT 隔离验收：用户答复真正落进停住的 Agent 节点（`answer_waiting_node`）。

跑法（隔离实例必须先起好，端口绝不能是线上 7331）：

    python3 e2e/test_task_router_answer.py \
        --base http://127.0.0.1:7434/mindfs \
        --user u_xxxxxxxxxxxx \
        --root /tmp/mindfs-answer-iso/proj \
        --db /tmp/mindfs-answer-iso/config/mindfs/users/u_xxx/meta/proj/tasks/task-kanban.db

`--db` 可选：给了就额外核对账本（`workflow_run_events`），没给只按 HTTP 证据判。

断言的是一条完整链路，任一不成立即失败：

1. 夹具节点先停进 `waiting_user`（Run 投影与行状态一致）；
2. 用户答复那一轮的 Router 决策就是 `answer_waiting_node`，且已 `applied`；
3. 动作里钉住的目标 `node_run_id` 就是那条停住的行；
4. 出现 attempt+1 的新 NodeRun：`prev_attempt_id` 指回原行、复用同一 SessionKey、
   `input.user_answer` 带答复正文；
5. 该尝试带着答复重跑并成功，节点输出里出现用户选择的方案（答复真的进了 prompt）；
6. 用户看得到事实回执：「已把你的答复交给「…」节点，正在带着答复重跑。」；
7. 账本（给了 `--db` 时）：`workflow_node_retried` 的 payload 对得上；
8. 任务不再停在 `waiting_user`。

边界：纯标准库；`DB` 只以 `mode=ro` 打开 SQLite；不碰生产实例。
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.parse

from test_task_router_coordinate_common import (
    Client,
    DB,
    clip,
    wait_decision,
    wait_for,
)

WORKFLOW_ID = "gmt-answer"
NODE_ID = "run"
ANSWER = "我希望是A"
CHOICE_MARK = "已按用户答复采用方案A"
RECEIPT = "已把你的答复交给"

NODE_PROMPT = (
    "这是 G-MT 隔离验收夹具。不要读取任何文件、不要调用任何工具、不要修改任何东西。\n"
    "你的任务只有一件：确认最终采用方案 A 还是方案 B。\n"
    "- 如果这段提示后面带有「## 用户答复」一节：直接采用用户答复里说的方案，"
    "用一句话中文回答「已按用户答复采用方案A。」或「已按用户答复采用方案B。」。\n"
    "- 如果没有「## 用户答复」：不要自己决定，直接回答「无法完成：需要用户确认选 A 还是 B。」。"
)


def workflow_run(client, task_id):
    path = f"/api/tasks/{task_id}/workflow-run?root_id={urllib.parse.quote(client.root)}"
    _, resp = client.call("GET", path)
    return resp.get("run") or {}


def fixture_nodes():
    return [
        {
            "id": NODE_ID,
            "name": "方案确认",
            "kind": "agent",
            "params": {
                "agent": "claude",
                "model": "sonnet",
                "effort": "low",
                "prompt": NODE_PROMPT,
            },
        }
    ]


def check(checks, name, ok, detail):
    checks.append({"check": name, "ok": bool(ok), "detail": clip(str(detail), 500)})
    return bool(ok)


def find_node(nodes, node_run_id):
    for node in nodes:
        if node.get("id") == node_run_id:
            return node
    return None


def output_text(node_run):
    return json.dumps(node_run.get("output") or {}, ensure_ascii=False)


def run_scenario(client, db, log):
    checks = []
    client_router = client.router_status()
    log(f"[router] mode={client_router.get('mode')} agent={client_router.get('agent')} "
        f"model={client_router.get('model')}")

    version = client.ensure_fixture(
        WORKFLOW_ID,
        "G-MT 答复回填",
        "验收流程：节点停在 waiting_user，用户答复后带着答复重跑",
        fixture_nodes(),
        [],
    )
    log(f"[fixture] {WORKFLOW_ID} version={version}")

    task_id = client.create_task("gmt-answer")
    log(f"[task] {task_id}")

    # ① 用 specified 直接把流程跑起来（绕开 Router，保证首个回合是确定性的）。
    client.post_activity(task_id, "请确认采用方案 A 还是方案 B。", choice="specified", version_id=version)

    def waiting_nodes():
        return client.node_runs(task_id)

    nodes = wait_for(
        waiting_nodes,
        lambda ns: any(n.get("status") == "waiting_user" for n in ns),
        300,
        what="node run reaches waiting_user",
    )
    first = next(n for n in nodes if n.get("status") == "waiting_user")
    run = workflow_run(client, task_id)
    check(checks, "1. 节点停在 waiting_user（行状态 + Run 投影）",
          run.get("node_states", {}).get(first["node_id"]) == "waiting_user",
          f"node_run={first['id']} attempt={first['attempt']} state={run.get('node_states')}")
    check(checks, "1b. 停住原因可读（节点自报 blocked 并说明为什么需要用户）",
          (first.get("output") or {}).get("outcome") == "blocked"
          and bool(str((first.get("output") or {}).get("reason") or "").strip()),
          f"output={clip(output_text(first), 300)}")
    log(f"[waiting] node_run={first['id']} attempt={first['attempt']} "
        f"session={first.get('session_key')}")

    # ② 用户回复 → Router 决策。空 workflow_choice 才会进 Router。
    user_act, _ = client.post_activity(task_id, ANSWER, choice="empty")
    log(f"[answer] activity={user_act['id']} body={ANSWER}")
    decision = wait_decision(client, task_id, user_act["id"])
    kinds = decision.get("action_kinds") or []
    check(checks, "2. 决策动作是 answer_waiting_node 且 applied",
          "answer_waiting_node" in kinds and decision.get("status") == "applied",
          f"status={decision.get('status')} kinds={kinds} error={clip(decision.get('error') or '', 200)}")

    detail = client.decision_detail(task_id, decision["id"])
    actions = detail.get("actions") or []
    target = next((a for a in actions if a.get("kind") == "answer_waiting_node"), {})
    check(checks, "3. 动作钉住的目标就是那条停住的行",
          target.get("node_run_id") == first["id"],
          f"node_run_id={target.get('node_run_id')} want={first['id']} body={clip(target.get('body') or '', 80)}")

    # ③ 等节点带着答复重跑完。
    def answered_attempt():
        for node in client.node_runs(task_id):
            if node.get("prev_attempt_id") == first["id"]:
                return node
        return None

    second = wait_for(
        answered_attempt,
        lambda n: n is not None and n.get("status") in ("success", "fail", "waiting_user", "unknown", "cancelled"),
        300,
        what="answered attempt terminal",
    )
    answer_input = (second.get("input") or {}).get("user_answer")
    check(checks, "4. attempt+1 首次尝试链回原行并复用会话",
          second.get("attempt") == first.get("attempt", 1) + 1
          and second.get("session_key") == first.get("session_key"),
          f"attempt={second.get('attempt')} session={second.get('session_key')} "
          f"first_session={first.get('session_key')}")
    # 注入口径就是决策里记录的 body：Router 可能把用户原话整理成更明确的指令
    # （已实测出现「用户选择方案 A（原话：「我希望是A」）。…」），两者必须逐字一致，
    # 且必须含用户原话 —— 这才是「用户答复真的进了节点」。
    check(checks, "4b. 答复原样写进新尝试的 input（与动作 body 逐字一致）",
          answer_input == (target.get("body") or "") and ANSWER in (answer_input or ""),
          f"user_answer={answer_input!r} action_body={clip(target.get('body') or '', 120)!r} "
          f"activity_id={(second.get('input') or {}).get('user_answer_activity_id')}")

    # ④ 答复真的进了 prompt：节点的输出里出现用户选的方案。
    final_run = workflow_run(client, task_id)
    check(checks, "5. 带答复重跑成功",
          second.get("status") == "success",
          f"status={second.get('status')} output={clip(output_text(second), 300)}")
    check(checks, "5b. 节点输出里出现了用户选的方案（答复真进了 prompt）",
          CHOICE_MARK in output_text(second) or "方案A" in output_text(second),
          f"output={clip(output_text(second), 300)}")
    check(checks, "5c. Run 投影推进到 success",
          final_run.get("node_states", {}).get(first["node_id"]) == "success",
          f"node_states={final_run.get('node_states')}")

    # ⑤ 可见回执。
    receipt = wait_for(
        lambda: [
            a for a in client.activities(task_id)
            if RECEIPT in str(a.get("body") or "") and a.get("status") == "completed"
        ],
        lambda rows: bool(rows),
        60,
        what="visible answer receipt",
    )[0]
    check(checks, "6. 用户看得到事实回执",
          receipt.get("reply_to_id") == user_act["id"],
          f"activity={receipt.get('id')} purpose={receipt.get('purpose')} body={clip(receipt.get('body') or '', 120)}")

    # ⑥ 账本（可选）。
    if db is not None:
        rows = db.query(
            "SELECT type, node_run_id, attempt, payload_json FROM workflow_run_events"
            " WHERE run_id = ? AND node_run_id = ? ORDER BY seq ASC",
            (final_run.get("id"), second["id"]),
        )
        retried = [r for r in rows if r.get("type") == "node_retried"]
        payload = {}
        if retried:
            try:
                payload = json.loads(retried[0].get("payload_json") or "{}")
            except json.JSONDecodeError:
                payload = {}
        check(checks, "7. 账本记了带答复重试",
              bool(retried) and payload.get("reason") == "user_answer"
              and payload.get("prev_attempt_id") == first["id"]
              and ANSWER in str(payload.get("answer_excerpt") or ""),
              f"events={[r.get('type') for r in rows]} payload={clip(json.dumps(payload, ensure_ascii=False), 240)}")

    # ⑦ 任务落定。注意 `waiting_user` 在这里是「待审核」（G-MS：Run 结算成功后任务停在
    # 待审核等用户看），不是「节点在等答复」—— 后者由 5c 的 Run 投影排除。
    task = client.task(task_id)
    check(checks, "8. 任务落定在待审核/已完成（不再等节点答复）",
          task.get("status") in ("waiting_user", "success"),
          f"task_status={task.get('status')}")

    return {
        "task_id": task_id,
        "workflow_version": version,
        "first_node_run": {
            "id": first.get("id"),
            "attempt": first.get("attempt"),
            "status": first.get("status"),
            "session_key": first.get("session_key"),
            "output": clip(output_text(first), 240),
        },
        "answered_node_run": {
            "id": second.get("id"),
            "attempt": second.get("attempt"),
            "status": second.get("status"),
            "prev_attempt_id": second.get("prev_attempt_id"),
            "session_key": second.get("session_key"),
            "input_user_answer": answer_input,
            "output": clip(output_text(second), 240),
        },
        "decision": {
            "id": decision.get("id"),
            "status": decision.get("status"),
            "action_kinds": kinds,
            "latency_ms": decision.get("latency_ms"),
            "target_node_run_id": target.get("node_run_id"),
        },
        "receipt": {"id": receipt.get("id"), "body": clip(receipt.get("body") or "", 200)},
        "checks": checks,
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description="G-MT answer_waiting_node isolated acceptance")
    parser.add_argument("--base", required=True, help="隔离实例地址，例如 http://127.0.0.1:7434/mindfs")
    parser.add_argument("--user", required=True)
    parser.add_argument("--root", required=True)
    parser.add_argument("--db", default="", help="可选：task-kanban.db（只读核对账本）")
    parser.add_argument("--json", default="", help="把证据 JSON 写到这个路径")
    parser.add_argument("--quiet", action="store_true")
    args = parser.parse_args(argv)

    def log(line):
        if not args.quiet:
            print(line, flush=True)

    if ":7331" in args.base:
        print("拒绝执行：隔离验收不能打线上 7331", file=sys.stderr)
        return 2
    client = Client(args.base, args.user, args.root)
    if client.health() != 200:
        print(f"隔离实例不可用：{args.base}/health", file=sys.stderr)
        return 2
    db = DB(args.db) if args.db else None

    started = time.time()
    evidence = run_scenario(client, db, log)
    elapsed = round(time.time() - started, 1)
    passed = all(c["ok"] for c in evidence["checks"])
    report = {"pass": passed, "elapsed_s": elapsed, **evidence}
    if args.json:
        with open(args.json, "w", encoding="utf-8") as fh:
            json.dump(report, fh, ensure_ascii=False, indent=2)
    for item in evidence["checks"]:
        print(("PASS  " if item["ok"] else "FAIL  ") + item["check"] + "  | " + item["detail"])
    print(f"\n{'PASS' if passed else 'FAIL'}  elapsed={elapsed}s task={evidence['task_id']}")
    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""G-MY 隔离验收：审批卡说清在批什么、审批附言驱动下一轮、末端 gate 不再卡人。

用法（只在隔离实例上跑，绝不指向线上 7331）：

    python3 e2e/test_task_router_gate_note.py \
        --base http://127.0.0.1:7435/mindfs \
        --user <隔离账户 id> --root <隔离 root> \
        --db /tmp/mindfs-iso-gmy/config/mindfs/users/<uid>/meta/<root>/tasks/task-kanban.db \
        --out /tmp/mindfs-iso-gmy/gate-note-result.json

覆盖用户 2026-11-19 的四条原话里可自动判定的三条：

  A. 审批卡必须自述：节点名 + 审批提示 + 上游节点产出的可读摘要（「我都不知道要我审批什么」）。
  B. 审批框内附言：批准带 note → 作为一条排队用户留言落库，本轮成功收尾后才派发，
     并真实驱动下一轮（route 模式下由 Router 决策）。普通输入框仍保持禁用。
  C. 末端 human_gate 不能再卡人：定义期拒绝（HTTP 400）、已发布旧版本的末端 gate
     运行期自动通过（auto_approved），不再物化等待卡。

边界：纯标准库；`--db` **只读**打开（`mode=ro`）；只碰 `--base` 指向的实例；
不采集凭据与转录，正文一律截断。

退出码：全部 pass = 0；有 fail = 1；模型/凭据不可用 = 2。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import traceback
import urllib.error
import urllib.request

from test_task_router_coordinate_common import (  # noqa: E402  （同目录导入，脚本直跑）
    ApiError,
    Client,
    DB,
    activity_text,
    clip,
    is_model_unavailable,
    wait_decision,
    wait_for,
)

ONLINE_PORT = 7331
EXEC_TIMEOUT = 300
SETTLE_TIMEOUT = 300

SCENARIO_NAMES = {
    "A": "审批卡自述（节点名 + 提示 + 上游产出）",
    "B": "审批附言驱动下一轮",
    "C": "末端 gate 定义期拒绝 + 旧版本自动通过",
}

# 同 T10 夹具：agent 节点必须显式给 agent/model，否则适配层以 "agent required" 失败。
AGENT_PROFILE = {"agent": "claude", "model": "sonnet", "effort": "low"}
QUIET_PROMPT = (
    "这是 G-MY 验收夹具。不要读取任何文件、不要调用任何工具、不要修改任何东西。"
    "只用一句话中文回答：{line}"
)


def online_health(port=ONLINE_PORT):
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}/mindfs/health", timeout=5) as resp:
            return resp.status
    except urllib.error.HTTPError as err:
        return err.code
    except (urllib.error.URLError, OSError):
        return 0


def result(key, ok, evidence, latency_ms, error=""):
    return {"pass": bool(ok), "status": "pass" if ok else "fail",
            "evidence": evidence, "error": clip(error, 500), "latency_ms": latency_ms}


def gate_cards(client, task_id):
    return [a for a in client.activities(task_id) if a.get("type") == "human_gate"]


def waiting_gate_card(client, task_id, timeout=EXEC_TIMEOUT):
    """等一张停在 waiting_user 的审批卡（运行到 gate 需要真实 agent 回合）。"""

    def fetch():
        cards = [c for c in gate_cards(client, task_id) if c.get("status") == "waiting_user"]
        return cards[-1] if cards else None

    return wait_for(fetch, lambda c: c is not None, timeout, what="a waiting human_gate card")


def node_statuses(client, task_id):
    return {r.get("node_id"): r.get("status") for r in client.node_runs(task_id)}


def scenario_a(client, fixtures):
    """审批卡必须自述：只在拿到真实卡片后才判定。"""
    started = time.time()
    task_id = client.create_task("G-MY-A 审批卡自述")
    client.create_specified_activity(task_id, "跑一遍需要审批的流程，跑到审批那一步停下。", fixtures["gmy-gate"])
    card = waiting_gate_card(client, task_id)
    payload = json.loads(card.get("payload_json") or "{}")
    upstream = payload.get("upstream") or []
    evidence = {
        "task_id": task_id,
        "card": {"id": card.get("id"), "body": clip(card.get("body") or ""),
                 "status": card.get("status"), "payload": payload},
        "node_statuses": node_statuses(client, task_id),
    }
    problems = []
    if not str(payload.get("node_name") or "").strip():
        problems.append("审批卡没有 node_name：用户看不到在批哪个节点")
    if not str(payload.get("prompt") or "").strip():
        problems.append("审批卡没有 prompt：用户看不到在批什么")
    if not upstream:
        problems.append("审批卡没有 upstream：上游节点的产出没有呈现")
    else:
        first = upstream[0]
        if not str(first.get("name") or "").strip() or not str(first.get("summary") or "").strip():
            problems.append(f"upstream 条目不完整：{first}")
    return result("A", not problems, evidence, int((time.time() - started) * 1000), "；".join(problems))


def scenario_b(client, db, fixtures):
    """批准带附言 → 排队用户留言，本轮成功收尾后派发并驱动下一轮。"""
    started = time.time()
    task_id = client.create_task("G-MY-B 审批附言")
    client.create_specified_activity(task_id, "跑一遍需要审批的流程，等我批准。", fixtures["gmy-gate"])
    card = waiting_gate_card(client, task_id)
    payload = json.loads(card.get("payload_json") or "{}")
    node_id = payload.get("node_id")
    note = "批准。另外请顺手确认收尾节点确实执行了。"
    before = {a.get("id") for a in client.activities(task_id)}
    client.human_gate(task_id, node_id, "approve", note=note)

    # 附言必须作为可见用户留言落库（用户在时间线里立刻看到）。
    def fetch_note():
        fresh = [a for a in client.activities(task_id)
                 if a.get("id") not in before and a.get("type") == "user_message"
                 and a.get("role") == "user"]
        for act in fresh:
            if note.strip() in (act.get("body") or "").strip():
                return act
        return None

    note_act = wait_for(fetch_note, lambda a: a is not None, 60, what="approval note as a user message")
    evidence = {
        "task_id": task_id,
        "node_id": node_id,
        "note_activity": {"id": note_act.get("id"), "body": clip(note_act.get("body") or ""),
                          "status": note_act.get("status")},
    }
    problems = []
    if note_act.get("status") in ("queued", "pending"):
        # 派发前是 queued 属正常；这里只是记录当时值，最终以是否被领走为准。
        evidence["note_status_at_first_seen"] = note_act.get("status")

    # 下一轮：route 模式下，附言被派发后应有针对它的 Router 决策。
    decision_ok = False
    try:
        decision = wait_decision(client, task_id, note_act["id"], timeout=SETTLE_TIMEOUT)
        decision_ok = True
        evidence["decision"] = {
            "id": decision.get("id"),
            "status": decision.get("status"),
            "action_kinds": decision.get("action_kinds"),
            "error": clip(decision.get("error") or ""),
        }
        if decision.get("status") in ("failed", "error"):
            problems.append(f"附言那一轮 Router 决策失败：{clip(decision.get('error') or '', 200)}")
    except TimeoutError:
        # 附言可能被领走但作为普通后续留言走了直连路径；此时以「留言被领走」为准。
        fresh = next((a for a in client.activities(task_id) if a.get("id") == note_act.get("id")), {})
        evidence["decision"] = None
        if (fresh.get("status") or "") in ("", "queued"):
            problems.append("附言既没有被 Router 决策，也没有被派发（仍停在队列里）")

    # 时间线里必须能看到后续轮的回复行（证明附言真的驱动了下一轮）。
    def fetch_next_reply():
        replies = [a for a in client.activities(task_id)
                   if a.get("type") == "agent_reply" and a.get("reply_to_id") == note_act.get("id")]
        return replies[-1] if replies else None

    try:
        reply = wait_for(fetch_next_reply, lambda a: a is not None, SETTLE_TIMEOUT,
                         what="a reply bound to the approval note")
        evidence["reply"] = {"id": reply.get("id"), "status": reply.get("status"),
                             "purpose": reply.get("purpose"), "reply_to_id": reply.get("reply_to_id"),
                             "text": clip(activity_text(reply))}
        if reply.get("status") == "failed":
            problems.append(f"附言那一轮的回复失败：{clip(activity_text(reply), 200)}")
    except TimeoutError:
        evidence["reply"] = None
        problems.append("附言之后没有任何回复行：它没有真正进入下一轮")
    evidence["decision_round"] = decision_ok

    evidence["node_statuses"] = node_statuses(client, task_id)
    return result("B", not problems, evidence, int((time.time() - started) * 1000), "；".join(problems))


def scenario_c(client, workflows_path, fixtures):
    """末端 gate：定义期拒绝（HTTP 400）+ 旧版本运行期自动通过。"""
    started = time.time()
    evidence = {}
    problems = []

    # C1 定义期：建一个末端 gate 的 workflow 必须被拒。
    wid = "gmy-terminal"
    client.try_call("DELETE", f"/api/workflows/{wid}")
    try:
        client.call("POST", "/api/workflows", {
            "workflow_id": wid, "root_id": client.root, "author": "gmy",
            "graph": {"nodes": [{"id": "verify", "name": "验收", "kind": "human_gate",
                                 "params": {"prompt": "验收"}}], "edges": []},
        })
        problems.append("末端 human_gate 的定义被接受了：应当 400 拒绝")
        evidence["authoring"] = "accepted"
    except ApiError as err:
        evidence["authoring"] = {"status": err.status, "body": clip(err.body or "", 300)}
        if err.status != 400:
            problems.append(f"末端 gate 拒绝状态码 = {err.status}，期望 400")

    # C2 旧版本：直接往隔离实例的 workflows.json 塞一个末端 gate 的已发布版本
    # （模拟 workflow-new-feature:v1），再用 workflow_choice=specified 建 Run。
    version_id, inject_note = inject_legacy_terminal_version(workflows_path, "gmy-legacy-terminal")
    evidence["legacy_version"] = {"id": version_id, **inject_note}
    if version_id:
        task_id = client.create_task("G-MY-C 旧版本末端 gate")
        client.create_specified_activity(task_id, "跑一遍旧版本的末端审批流程。", version_id)
        try:
            statuses = wait_for(
                lambda: node_statuses(client, task_id),
                lambda s: s.get("verify") in ("success", "fail", "cancelled"),
                EXEC_TIMEOUT,
                what="legacy terminal gate to settle",
            )
            evidence["node_statuses"] = statuses
            if statuses.get("verify") != "success":
                problems.append(f"旧版本末端 gate 状态 = {statuses.get('verify')}，期望 success（自动通过）")
        except TimeoutError:
            evidence["node_statuses"] = node_statuses(client, task_id)
            problems.append("旧版本末端 gate 一直没结算：它仍然卡住了用户")
        waiting = [c for c in gate_cards(client, task_id) if c.get("status") == "waiting_user"]
        evidence["waiting_cards"] = len(waiting)
        if waiting:
            problems.append("旧版本末端 gate 仍然物化了等待卡：用户会被要求批一个没有下游的东西")
        gate_runs = [r for r in client.node_runs(task_id) if r.get("node_id") == "verify"]
        evidence["gate_node_runs"] = [{"status": r.get("status"), "output": r.get("output")} for r in gate_runs]

    return result("C", not problems, evidence, int((time.time() - started) * 1000), "；".join(problems))


def inject_legacy_terminal_version(path, workflow_id):
    """把末端 gate 的已发布版本写进隔离实例的 workflows.json（只动隔离根）。

    不能走 HTTP：定义期校验正是要拒绝这种图。旧版本能建 Run 是运行期兼容，
    所以这里直接构造与 PublishDraft 相同形状的版本记录。
    """
    if not path:
        return "", {"skipped": "no workflows.json path"}
    try:
        with open(path, "r", encoding="utf-8") as fh:
            data = json.load(fh)
    except (OSError, ValueError) as err:
        return "", {"error": f"read {path}: {err}"}
    versions = data.setdefault("versions", {})
    seqs = data.setdefault("next_seq", {})
    seq = int(seqs.get(workflow_id, 1) or 1)
    version_id = f"{workflow_id}:v{seq}"
    versions[version_id] = {
        "version": version_id,
        "workflow_id": workflow_id,
        "revision": "r1",
        "graph": {
            "nodes": [
                {"id": "prepare", "name": "准备", "kind": "agent",
                 "params": {**AGENT_PROFILE, "prompt": QUIET_PROMPT.format(line="准备完成。")}},
                {"id": "verify", "name": "验收", "kind": "human_gate", "params": {"prompt": "验收"}},
            ],
            "edges": [{"from": "prepare", "to": "verify"}],
        },
        "author": "gmy",
        "published_by": "gmy",
        "published_at": "2026-11-19T00:00:00Z",
    }
    seqs[workflow_id] = seq + 1
    meta = data.setdefault("meta", {})
    meta[workflow_id] = {"id": workflow_id, "name": "G-MY 旧版本末端审批", "description": "旧版本的末端 gate",
                         "use_cases": [], "tags": [], "auto_select_enabled": False}
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=2)
    os.replace(tmp, path)
    return version_id, {"path": path}


def ensure_fixtures(client):
    """gate 有下游：定义期合法（末端 gate 现在会被拒，所以身后必须还有节点）。"""
    nodes = [
        {"id": "prepare", "name": "准备", "kind": "agent",
         "params": {**AGENT_PROFILE, "prompt": QUIET_PROMPT.format(line="准备完成：已整理好待审批的内容。")}},
        {"id": "gate", "name": "确认继续", "kind": "human_gate", "params": {"prompt": "上面的准备结果可以继续吗？"}},
        {"id": "finish", "name": "收尾", "kind": "agent",
         "params": {**AGENT_PROFILE, "prompt": QUIET_PROMPT.format(line="收尾完成。")}},
    ]
    edges = [{"from": "prepare", "to": "gate"}, {"from": "gate", "to": "finish"}]
    return {"gmy-gate": client.ensure_fixture("gmy-gate", "G-MY 审批夹具", "准备 → 审批 → 收尾", nodes, edges)}


def main(argv=None):
    parser = argparse.ArgumentParser(description="G-MY 隔离验收：审批卡 / 附言 / 末端 gate")
    parser.add_argument("--base", required=True)
    parser.add_argument("--user", required=True)
    parser.add_argument("--root", required=True)
    parser.add_argument("--db", required=True)
    parser.add_argument("--iso-root", default="",
                        help="隔离根（--workflows 必须落在它里面才会写入）")
    parser.add_argument("--workflows", default="",
                        help="隔离实例的 workflows.json（场景 C 注入旧版本用；必须落在隔离根内）")
    parser.add_argument("--out", default="")
    parser.add_argument("--scenarios", default="A,B,C")
    args = parser.parse_args(argv)
    scenarios = [s.strip().upper() for s in args.scenarios.split(",") if s.strip()]
    unknown = [s for s in scenarios if s not in SCENARIO_NAMES]
    if unknown:
        raise SystemExit(f"未知场景 {unknown}（可选 A,B,C）")
    if f":{ONLINE_PORT}" in args.base:
        print(f"拒绝：不得指向线上 {ONLINE_PORT}", file=sys.stderr)
        return 2

    client = Client(args.base, args.user, args.root)
    try:
        db = DB(args.db)
    except ValueError as err:
        print(f"拒绝：{err}", file=sys.stderr)
        return 2
    if args.workflows and not os.path.abspath(args.workflows).startswith(os.path.abspath(args.iso_root) + os.sep):
        print(f"拒绝：--workflows 必须落在 --iso-root 内：{args.workflows}", file=sys.stderr)
        return 2

    health_before = online_health()
    try:
        fixtures = ensure_fixtures(client)
    except ApiError as err:
        print(f"夹具创建失败：{err}", file=sys.stderr)
        return 2
    print(f"fixtures={fixtures}", flush=True)

    runners = {
        "A": lambda: scenario_a(client, fixtures),
        "B": lambda: scenario_b(client, db, fixtures),
        "C": lambda: scenario_c(client, args.workflows, fixtures),
    }
    results = []
    for key in scenarios:
        started = time.time()
        print(f"→ [{key}] {SCENARIO_NAMES[key]} 开始", flush=True)
        try:
            entry = runners[key]()
        except (ApiError, TimeoutError, KeyboardInterrupt) as err:
            entry = result(key, False, {"error": clip(str(err), 800),
                                        "traceback": clip(traceback.format_exc(), 1200)},
                           int((time.time() - started) * 1000), "场景执行异常")
        except Exception as err:  # noqa: BLE001  验收脚本把任何异常变成显式失败
            entry = result(key, False, {"error": clip(repr(err), 800),
                                        "traceback": clip(traceback.format_exc(), 1200)},
                           int((time.time() - started) * 1000), "脚本异常，非产品判定")
        entry.update({"scenario": key, "name": SCENARIO_NAMES[key]})
        results.append(entry)
        print(f"← [{key}] {entry['status']} ({entry['latency_ms']}ms)"
              + (f"  {entry['error']}" if entry.get("error") else ""), flush=True)

    health_after = online_health()
    payload = {
        "base": args.base, "root": args.root,
        "router_status": client.router_status(),
        "online_health": {"before": health_before, "after": health_after},
        "scenarios": results,
        "summary": {"total": len(results), "passed": sum(1 for r in results if r["pass"]),
                    "failed": sum(1 for r in results if not r["pass"])},
    }
    if args.out:
        with open(args.out, "w", encoding="utf-8") as fh:
            json.dump(payload, fh, ensure_ascii=False, indent=2)
        print(f"结果已写入 {args.out}", flush=True)
    else:
        print(json.dumps(payload, ensure_ascii=False, indent=2))

    if health_before != 200 or health_after != 200:
        print(f"警告：线上 health before={health_before} after={health_after}", file=sys.stderr)
    errors = " ".join(str(r.get("error") or "") for r in results)
    if results and all(not r["pass"] for r in results) and is_model_unavailable(errors):
        return 2
    return 0 if all(r["pass"] for r in results) else 1


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""W5-C：长回合中途断电（kill -9 整棵进程树）+ 重启恢复。

从 `test_router_turn_settle_follow.py` 拆出来只为守住 500 行硬线：共用取证工具
（事件/决策读取、摘要判据、wait_for）仍在那个文件里，这里只放 C 场景自己的机械部分。

期望（`docs/superpowers/plans/2026-11-22-router-turn-settlement-supervision-and-session-follow.md` W5-1 C）：
  1. 重启后 5 min 内出现 `turn_interrupted`；
  2. 那一轮回复被结算成 cancelled，并带一条用户可见说明（含「进程重启」）；
  3. `router.supervision.mode=on` 时 Router 自动 `retry_direct` 一次并跑成功。
"""

from __future__ import annotations

import os
import subprocess
import time

from test_router_turn_settle_follow import (
    clip,
    decision_kinds,
    decisions,
    event_payload,
    events,
    parse_ts,
    result,
    wait_for,
)

C_PROMPT = (
    "这是一次崩溃恢复压测，请严格按顺序执行，不要提问、不要省略、不要合并：\n"
    "连续执行 12 次 Bash 工具调用。第 n 次（n=1..12）的命令是 `sleep 30 && echo TICK <n>`"
    "（把 <n> 换成序号），timeout 参数设为 60000。必须等上一次返回后再发起下一次。\n"
    "12 次全部跑完后，只回复一行：DONE 12"
)


def thread_children(pid):
    """一个进程所有线程的 children 并集：Go 运行时从任意线程 fork，只看主线程会漏掉 CLI 子进程。"""
    children = []
    try:
        tids = os.listdir(f"/proc/{pid}/task")
    except OSError:
        return children
    for tid in tids:
        try:
            with open(f"/proc/{pid}/task/{tid}/children", encoding="ascii") as fh:
                children.extend(fh.read().split())
        except OSError:
            continue
    return children


def process_tree(pid):
    """整棵进程树（含 claude CLI 子进程）。模拟断电时只杀父进程不够：子进程会继续跑。"""
    seen, stack = [], [pid]
    while stack:
        current = stack.pop()
        for child in thread_children(current):
            try:
                child_pid = int(child)
            except ValueError:
                continue
            if child_pid not in seen:
                seen.append(child_pid)
                stack.append(child_pid)
    return seen


def iso_control(repo, iso_root, iso_port, iso_src, action, timeout=900):
    # 白名单环境，不继承调用者：本会话的 ANTHROPIC_* / CLAUDE_* 是临时值，带进实例会
    # 改变模型路由（实测：继承后 agent 走的是调用者的中转，不是实例自己的登录态）。
    env = {
        key: os.environ[key]
        for key in ("HOME", "USER", "LOGNAME", "PATH", "LANG", "SHELL", "TMPDIR")
        if key in os.environ
    }
    env.update({
        "MINDFS_ISO_ROOT": iso_root,
        "MINDFS_ISO_PORT": str(iso_port),
        "MINDFS_ISO_SRC": iso_src,
        "MINDFS_ISO_KEEP": "1",
    })
    return subprocess.run(["bash", os.path.join(repo, "scripts", "mindfs-iso.sh"), action],
                          env=env, capture_output=True, text=True, timeout=timeout)


def scenario_c(client, db, opts):
    if ":7331" in opts.base:
        raise SystemExit("拒绝在线上实例上 kill -9：--base 指向 7331，本脚本只动隔离实例")
    if not (opts.iso_root and opts.iso_port and opts.iso_src):
        raise SystemExit("C 场景需要 --iso-root / --iso-port / --iso-src：它会 kill -9 并重启实例")
    mode = (client.router_status().get("supervision_mode") or "").strip()
    if mode != "on":
        raise SystemExit(f"C 场景要求 router.supervision.mode=on（当前 {mode!r}）：先改实例 config.json 再重启")
    started = time.time()
    task_id = client.create_task("W5-C 重启恢复")
    client.post_activity(task_id, C_PROMPT, choice="disabled")
    evidence = {"task_id": task_id}
    wait_for(lambda: client.task(task_id), lambda t: (t or {}).get("status") == "running",
             180, "task running (C)")

    # 等回复行真的进入 running（回合已开始、CLI 已起）再断电。
    def running_reply():
        return [a for a in client.activities(task_id) if a.get("type") == "agent_reply"]

    replies = wait_for(running_reply, lambda rs: any(r.get("status") == "running" for r in rs),
                       240, "reply running (C)")
    evidence["running_reply"] = next(r.get("id") for r in replies if r.get("status") == "running")
    time.sleep(60)  # 让 CLI 真的在跑（至少完成一次工具调用）

    pid_path = os.path.join(opts.iso_root, "server.pid")
    with open(pid_path, encoding="ascii") as fh:
        server_pid = int(fh.read().strip())
    tree = process_tree(server_pid)
    for pid in [server_pid] + tree:
        try:
            os.kill(pid, 9)
        except OSError:
            pass
    evidence["killed"] = {"server_pid": server_pid, "descendants": len(tree)}
    killed_at = time.time()

    start = iso_control(opts.repo, opts.iso_root, opts.iso_port, opts.iso_src, "start")
    evidence["restart_ms"] = int((time.time() - killed_at) * 1000)
    evidence["restart_exit"] = start.returncode
    if start.returncode != 0:
        evidence["restart_tail"] = clip((start.stderr or start.stdout or "").strip(), 800)
        return result("C", False, evidence, int((time.time() - started) * 1000),
                      notes="实例没能重新起来")

    wait_for(lambda: client.health(), lambda code: code == 200, 180, "isolated health (C)")

    interrupted = wait_for(lambda: events(db, task_id, "turn_interrupted"),
                           lambda rows: bool(rows), 300, "turn_interrupted within 5 min (C)")
    payload = event_payload(interrupted[0])
    evidence["turn_interrupted"] = {
        "latency_s": round(time.time() - killed_at, 1),
        "activity_id": payload.get("activity_id"),
        "payload_keys": sorted(payload.keys()),
    }

    def visible_note():
        return [a for a in client.activities(task_id)
                if a.get("id") == payload.get("activity_id")]

    note = wait_for(visible_note, lambda rows: bool(rows) and bool(rows[0].get("summary")),
                    180, "visible interrupted note (C)") if payload.get("activity_id") else []
    evidence["interrupted_reply"] = {
        "status": note[0].get("status") if note else None,
        "summary": clip((note[0].get("summary") or "") if note else "", 300),
    }
    c2 = bool(note) and note[0].get("status") == "cancelled" and \
        "进程重启" in (note[0].get("summary") or "")

    retry = wait_for(lambda: [d for d in decisions(db, task_id)
                              if d["trigger_kind"] == "turn_interrupted"
                              and "retry_direct" in decision_kinds(d)],
                     lambda rows: any(r["status"] == "applied" for r in rows), 300,
                     "retry_direct applied (C)")
    applied = next(d for d in retry if d["status"] == "applied")
    evidence["retry_decision"] = {"id": applied["id"], "status": applied["status"],
                                  "kinds": decision_kinds(applied)}

    def retried_reply():
        return [a for a in client.activities(task_id)
                if str(a.get("id") or "").startswith("direct-retry:")]

    try:
        retry_rows = wait_for(retried_reply,
                              lambda rows: any(r.get("status") in ("completed", "failed") for r in rows),
                              900, "retried direct turn finished (C)")
        interrupted_at = parse_ts(interrupted[0]["created_at"])
        settled_after = [e for e in events(db, task_id, "turn_settled")
                         if parse_ts(e["created_at"]) >= interrupted_at]
    except TimeoutError:
        retry_rows, settled_after = retried_reply(), []
    evidence["retried_reply"] = [
        {"id": r.get("id"), "status": r.get("status"),
         "summary": clip(r.get("summary") or "", 200)} for r in retry_rows
    ]
    evidence["turn_settled_after"] = len(settled_after)
    c4 = bool(retry_rows) and any(r.get("status") == "completed" for r in retry_rows)
    return result(
        "C", c2 and bool(applied) and c4,
        evidence, int((time.time() - started) * 1000),
        notes=("；".join(filter(None, [
            "" if c2 else "没有 cancelled + 「进程重启」可见说明",
            "retry_direct 已应用" if applied else "没有 retry_direct 决策",
            "" if c4 else "重试回合没有跑成功",
            f"turn_interrupted 延迟 {evidence['turn_interrupted']['latency_s']}s",
        ])) or "三条断言全过"),
    )

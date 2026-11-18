#!/usr/bin/env python3
"""Task Router R0 隔离验收：真实 agent（claude/sonnet）下的七个场景。

用法（只在隔离实例上跑，绝不指向线上 7331）：

    python3 e2e/test_task_router_r0.py \
        --base http://127.0.0.1:7432/mindfs \
        --user <隔离账户 id> --root <隔离 root> \
        --out /tmp/mindfs-r0-t4/result.json

    # 只跑部分场景（场景 6 需要把隔离配置的 router.turn_timeout 调小并重启实例）
    python3 e2e/test_task_router_r0.py ... --scenarios 6

边界：

- 纯标准库（urllib/json），不依赖 pytest / playwright。
- 只碰 base 指向的实例；脚本不启动/停止服务，也不改任何配置。场景 6 的
  「超时诱发」由外层编排改隔离配置并重启隔离实例完成，脚本只记录诱发方式。
- 不采集凭据、不采集完整转录；正文一律截断到 300 字符。
- 每个场景独立建任务（POST /api/tasks，start_immediately=false，
  create_worktree=false），用 POST /api/tasks/{id}/activities 发
  workflow_choice=empty 的留言触发 Router。
- 任何「未运行」必须写 blocked/not_reproducible 原因，绝不写 pass。

退出码：全部运行且通过 = 0；有失败 = 1；模型不可用（BLOCKED）= 2。
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import traceback
import urllib.error
import urllib.request

BODY_LIMIT = 300
DECISION_TIMEOUT = 240
EXEC_TIMEOUT = 300
CANCEL_SETTLE_TIMEOUT = 60
POLL_INTERVAL = 2.0

SCENARIO_NAMES = {
    1: "简单实质问题",
    2: "debug/新功能/单步任务路径匹配",
    3: "关键信息缺失 → ask_user",
    4: "specified/disabled 绕过 Router",
    5: "活动 Run 中补充留言",
    6: "Router 错误/超时/非法动作",
    7: "同任务消息排队",
}


def clip(text, limit=BODY_LIMIT):
    text = (text or "").strip()
    if len(text) <= limit:
        return text
    return text[:limit] + "…"


class ApiError(RuntimeError):
    def __init__(self, status, path, body):
        super().__init__(f"{status} {path}: {body}")
        self.status = status
        self.path = path
        self.body = body


class Client:
    """账户作用域 HTTP 客户端；所有请求都带 user=。"""

    def __init__(self, base, user, root, request_timeout=60):
        self.base = base.rstrip("/")
        self.user = user
        self.root = root
        self.request_timeout = request_timeout

    def url(self, path):
        sep = "&" if "?" in path else "?"
        return f"{self.base}{path}{sep}user={self.user}"

    def call(self, method, path, payload=None):
        data = None
        if payload is not None:
            data = json.dumps(payload).encode()
        req = urllib.request.Request(
            self.url(path),
            data=data,
            method=method,
            headers={"Content-Type": "application/json"},
        )
        try:
            with urllib.request.urlopen(req, timeout=self.request_timeout) as resp:
                raw = resp.read().decode() or "{}"
                return resp.status, json.loads(raw)
        except urllib.error.HTTPError as err:
            raw = err.read().decode(errors="replace")
            raise ApiError(err.code, path, clip(raw, 600)) from None

    def try_call(self, method, path, payload=None):
        """404 表示「没有」，返回 None；其余错误照常抛。"""
        try:
            return self.call(method, path, payload)
        except ApiError as err:
            if err.status == 404:
                return None, None
            raise

    # ── 数据面 ──────────────────────────────────────────────────────────────

    def create_task(self, name):
        _, detail = self.call(
            "POST",
            "/api/tasks",
            {
                "root_id": self.root,
                "input": "",
                "name": name,
                "create_worktree": False,
                "start_immediately": False,
            },
        )
        task = detail.get("task") or {}
        if not task.get("id"):
            raise ApiError(0, "/api/tasks", f"missing task id: {detail}")
        return task["id"]

    def post_activity(self, task_id, body, choice="empty", version_id=None):
        payload = {"root_id": self.root, "body": body, "workflow_choice": choice}
        if version_id:
            payload["workflow_version_id"] = version_id
        _, resp = self.call("POST", f"/api/tasks/{task_id}/activities", payload)
        return resp.get("user_activity") or {}, resp.get("agent_activity") or {}

    def activities(self, task_id, include_hidden=True):
        path = f"/api/tasks/{task_id}/activities?root_id={self.root}"
        if include_hidden:
            path += "&include_hidden=1"
        _, resp = self.call("GET", path)
        return resp.get("activities") or []

    def decisions(self, task_id):
        _, resp = self.call(
            "GET", f"/api/tasks/{task_id}/router-decisions?root_id={self.root}&limit=50"
        )
        return resp.get("decisions") or []

    def decision_detail(self, task_id, decision_id):
        _, resp = self.call(
            "GET",
            f"/api/tasks/{task_id}/router-decisions/{decision_id}?root_id={self.root}",
        )
        return resp

    def node_runs(self, task_id):
        _, resp = self.try_call(
            "GET", f"/api/tasks/{task_id}/workflow-run/nodes?root_id={self.root}"
        )
        if not resp:
            return []
        return resp.get("node_runs") or []

    def engine(self, task_id):
        _, resp = self.try_call("GET", f"/api/tasks/{task_id}/engine?root_id={self.root}")
        return resp or {}

    def cancel_task(self, task_id):
        try:
            self.call("POST", f"/api/tasks/{task_id}/cancel", {"root_id": self.root})
            return True
        except ApiError as err:
            return f"cancel failed: {clip(str(err), 160)}"

    # ── workflow 目录 ───────────────────────────────────────────────────────

    def workflow_catalog(self):
        _, resp = self.call("GET", "/api/workflows/available")
        return resp.get("workflows") or []

    def published_versions(self):
        _, resp = self.call("GET", "/api/workflows/published")
        return resp.get("items") or []


def pick_workflow(catalog, keywords):
    for wf in catalog:
        haystack = " ".join(
            [
                str(wf.get("id") or ""),
                str(wf.get("name") or ""),
                str(wf.get("description") or ""),
            ]
        ).lower()
        if any(k.lower() in haystack for k in keywords):
            return wf
    return None


def latest_version(items, workflow_id):
    best = None
    best_seq = -1
    for item in items:
        if (item.get("workflow_id") or "") != workflow_id:
            continue
        version = str(item.get("version") or "")
        seq = 0
        if ":v" in version:
            try:
                seq = int(version.rsplit(":v", 1)[1])
            except ValueError:
                seq = 0
        if seq > best_seq:
            best_seq, best = seq, version
    return best


def wait_for(fetch, predicate, timeout, interval=POLL_INTERVAL, what=""):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = fetch()
        if predicate(last):
            return last
        time.sleep(interval)
    raise TimeoutError(f"timeout waiting for {what or 'condition'}; last={clip(json.dumps(last, ensure_ascii=False, default=str), 600)}")


def find_decision(client, task_id, user_activity_id, timeout):
    def fetch():
        for dec in client.decisions(task_id):
            if dec.get("user_activity_id") == user_activity_id:
                return dec
        return None

    return wait_for(fetch, lambda d: d is not None and d.get("status") not in ("applying", "shadow"), timeout, what=f"decision for {user_activity_id}")


def activity_by_id(items, activity_id):
    for item in items:
        if item.get("id") == activity_id:
            return item
    return None


def agent_activities(items):
    return [a for a in items if a.get("role") == "agent"]


def is_model_unavailable(decisions):
    text = " ".join(str(d.get("error") or "") for d in decisions).lower()
    markers = ("api key", "authentication", "unauthorized", "401", "model not found", "invalid api key")
    return any(m in text for m in markers)


def result(scenario, passed, evidence, latency_ms, notes="", status=None):
    entry = {
        "scenario": scenario,
        "pass": bool(passed),
        "evidence": evidence,
        "latency_ms": latency_ms,
        "notes": notes,
    }
    if status:
        entry["status"] = status
    return entry


# ── 场景 1：简单实质问题 ────────────────────────────────────────────────────


def scenario_simple_question(client):
    started = time.time()
    task_id = client.create_task("T4-S1 简单实质问题")
    body = "请用一两句话解释：Go 的 goroutine 和操作系统线程有什么区别？"
    user_act, placeholder = client.post_activity(task_id, body)
    decision = find_decision(client, task_id, user_act["id"], DECISION_TIMEOUT)

    def fetch_activities():
        return client.activities(task_id)

    wants_execution = bool(set(decision.get("action_kinds") or []) & {"message_agent", "run_workflow"})
    reply = None
    nodes = []
    if wants_execution:
        try:
            items = wait_for(
                fetch_activities,
                lambda acts: any(
                    a.get("status") in ("completed", "failed") and a.get("reply_to_id") == user_act["id"]
                    for a in agent_activities(acts)
                ),
                EXEC_TIMEOUT,
                what="agent execution result",
            )
            reply = activity_by_id(items, placeholder.get("id"))
            if not reply:
                reply = next(
                    (a for a in agent_activities(items) if a.get("reply_to_id") == user_act["id"]),
                    None,
                )
        except TimeoutError:
            reply = None
        nodes = client.node_runs(task_id)

    detail = client.decision_detail(task_id, decision["id"])
    evidence = {
        "task_id": task_id,
        "request": {"body": body, "workflow_choice": "empty"},
        "decision": {
            "id": decision.get("id"),
            "status": decision.get("status"),
            "action_kinds": decision.get("action_kinds"),
            "latency_ms": decision.get("latency_ms"),
            "actions": [
                {"kind": a.get("kind"), "workflow_id": a.get("workflow_id"), "body": clip(a.get("body") or a.get("question") or "")}
                for a in (detail.get("actions") or [])
            ],
        },
        "reply": {
            "activity_id": (reply or {}).get("id"),
            "status": (reply or {}).get("status"),
            "body_excerpt": clip((reply or {}).get("body") or ""),
        },
        "node_runs": [{"node_id": n.get("node_id"), "status": n.get("status")} for n in nodes],
    }
    substantive = bool((reply or {}).get("body") and len(str(reply.get("body")).strip()) >= 10)
    passed = wants_execution and substantive and (reply or {}).get("status") == "completed"
    notes = "" if passed else "期望：Router 转交执行或给出实质答案；实际见 evidence。"
    return result(1, passed, evidence, int((time.time() - started) * 1000), notes)


# ── 场景 2：debug / 新功能 / 单步任务路径匹配 ──────────────────────────────


def scenario_path_matching(client, catalog, versions):
    started = time.time()
    debug = pick_workflow(catalog, ("debug", "排查", "问题"))
    feature = pick_workflow(catalog, ("feature", "开发", "功能"))
    if not debug or not feature:
        return result(
            2,
            False,
            {"catalog": [w.get("id") for w in catalog]},
            0,
            "隔离 root 缺少可区分的 debug/feature workflow，场景无法运行",
            status="blocked",
        )

    cases = [
        ("debug", debug, "请用「问题排查」流程处理：线上登录接口偶发 500 报错，重试两次仍然失败。"),
        ("feature", feature, "请用「功能开发」流程处理：给设置页新增一个「导出配置」按钮。"),
    ]
    evidence = []
    all_pass = True
    for label, wf, body in cases:
        task_id = client.create_task(f"T4-S2 {label} 路径匹配")
        user_act, _ = client.post_activity(task_id, body)
        decision = find_decision(client, task_id, user_act["id"], DECISION_TIMEOUT)
        detail = client.decision_detail(task_id, decision["id"])
        actions = detail.get("actions") or []
        chosen = next((a for a in actions if a.get("kind") == "run_workflow"), None)
        matched = bool(chosen and chosen.get("workflow_id") == wf.get("id"))
        nodes = []
        if matched:
            try:
                wait_for(
                    lambda: client.node_runs(task_id),
                    lambda runs: any(r.get("status") in ("success", "fail", "waiting_user") for r in runs),
                    EXEC_TIMEOUT,
                    what=f"{label} node run terminal",
                )
            except TimeoutError:
                pass
            nodes = client.node_runs(task_id)
        all_pass = all_pass and matched
        evidence.append(
            {
                "case": label,
                "task_id": task_id,
                "request": body,
                "expected_workflow": wf.get("id"),
                "decision": {
                    "id": decision.get("id"),
                    "status": decision.get("status"),
                    "action_kinds": decision.get("action_kinds"),
                    "latency_ms": decision.get("latency_ms"),
                },
                "actions": [
                    {"kind": a.get("kind"), "workflow_id": a.get("workflow_id"), "version_id": a.get("version_id"), "reason": clip(a.get("reason") or "")}
                    for a in actions
                ],
                "node_runs": [{"node_id": n.get("node_id"), "status": n.get("status"), "output": clip(json.dumps(n.get("output") or {}, ensure_ascii=False), 200)} for n in nodes],
                "matched": matched,
            }
        )
    available = [{"id": w.get("id"), "name": w.get("name"), "description": clip(w.get("description") or "", 120)} for w in catalog]
    notes = "" if all_pass else "路径选择与描述不匹配；见 evidence.actions。"
    return result(2, all_pass, {"workflows": available, "cases": evidence}, int((time.time() - started) * 1000), notes)


# ── 场景 3：关键信息缺失 → ask_user ────────────────────────────────────────


def scenario_ask_user(client):
    started = time.time()
    task_id = client.create_task("T4-S3 信息缺失")
    body = "帮我把它按之前说的改一下，然后跟上次一样处理。"
    user_act, placeholder = client.post_activity(task_id, body)
    decision = find_decision(client, task_id, user_act["id"], DECISION_TIMEOUT)
    detail = client.decision_detail(task_id, decision["id"])
    kinds = set(decision.get("action_kinds") or [])
    items = client.activities(task_id)
    question_act = activity_by_id(items, placeholder.get("id"))
    asking = question_act and question_act.get("status") == "waiting_user" and clip(question_act.get("body") or "")
    passed = "ask_user" in kinds and bool(asking)
    evidence = {
        "task_id": task_id,
        "request": body,
        "decision": {
            "id": decision.get("id"),
            "status": decision.get("status"),
            "action_kinds": decision.get("action_kinds"),
            "latency_ms": decision.get("latency_ms"),
        },
        "actions": [
            {"kind": a.get("kind"), "question": clip(a.get("question") or ""), "options": a.get("options")}
            for a in (detail.get("actions") or [])
        ],
        "question_activity": {
            "id": (question_act or {}).get("id"),
            "status": (question_act or {}).get("status"),
            "body_excerpt": clip((question_act or {}).get("body") or ""),
        },
    }
    notes = "" if passed else "期望出现 ask_user 且问题指向缺失信息；实际见 evidence。"
    return result(3, passed, evidence, int((time.time() - started) * 1000), notes)


# ── 场景 4：specified / disabled 严格绕过 Router ───────────────────────────


def scenario_bypass(client, versions):
    started = time.time()
    debug = pick_workflow(client.workflow_catalog(), ("debug", "排查", "问题"))
    version_id = latest_version(versions, (debug or {}).get("id")) if debug else None
    if not version_id:
        return result(4, False, {"catalog": [w.get("id") for w in client.workflow_catalog()]}, 0, "缺少可指定的 workflow 版本", status="blocked")

    disabled_task = client.create_task("T4-S4 disabled 绕过")
    d_user, d_placeholder = client.post_activity(
        disabled_task, "你好，请简单确认你已收到这条消息。", choice="disabled"
    )
    try:
        wait_for(
            lambda: client.activities(disabled_task),
            lambda acts: (activity_by_id(acts, d_placeholder.get("id")) or {}).get("status") in ("completed", "failed"),
            EXEC_TIMEOUT,
            what="disabled direct turn terminal",
        )
    except TimeoutError:
        pass
    d_items = client.activities(disabled_task)
    d_reply = activity_by_id(d_items, d_placeholder.get("id")) or {}

    specified_task = client.create_task("T4-S4 specified 绕过")
    s_user, s_placeholder = client.post_activity(
        specified_task,
        "执行一次问题排查流程：检查登录失败的常见原因。",
        choice="specified",
        version_id=version_id,
    )
    try:
        wait_for(
            lambda: client.node_runs(specified_task),
            lambda runs: bool(runs) and all(r.get("status") in ("success", "fail", "waiting_user", "cancelled") for r in runs),
            EXEC_TIMEOUT,
            what="specified workflow run terminal",
        )
    except TimeoutError:
        pass
    s_nodes = client.node_runs(specified_task)
    d_decisions = client.decisions(disabled_task)
    s_decisions = client.decisions(specified_task)
    passed = (
        not d_decisions
        and not s_decisions
        and d_reply.get("status") == "completed"
        and bool(clip(d_reply.get("body") or ""))
        and bool(s_nodes)
    )
    evidence = {
        "disabled": {
            "task_id": disabled_task,
            "request": {"body": "你好，请简单确认你已收到这条消息。", "workflow_choice": "disabled"},
            "router_decisions": len(d_decisions),
            "reply": {
                "activity_id": d_reply.get("id"),
                "status": d_reply.get("status"),
                "body_excerpt": clip(d_reply.get("body") or ""),
            },
        },
        "specified": {
            "task_id": specified_task,
            "request": {"body": "执行一次问题排查流程：检查登录失败的常见原因。", "workflow_choice": "specified", "workflow_version_id": version_id},
            "router_decisions": len(s_decisions),
            "node_runs": [
                {
                    "node_id": n.get("node_id"),
                    "status": n.get("status"),
                    "session_key": n.get("session_key"),
                    "output": clip(json.dumps(n.get("output") or {}, ensure_ascii=False), 200),
                }
                for n in s_nodes
            ],
        },
    }
    notes = "" if passed else "期望两条留言都不产生 Router 决策且旧路径照常执行；实际见 evidence。"
    return result(4, passed, evidence, int((time.time() - started) * 1000), notes)


# ── 场景 5：活动 Run 中补充留言 ────────────────────────────────────────────


def scenario_active_run(client, versions):
    started = time.time()
    slow = pick_workflow(client.workflow_catalog(), ("slow", "长任务"))
    gate = pick_workflow(client.workflow_catalog(), ("gate", "审批", "人工"))
    slow_version = latest_version(versions, (slow or {}).get("id")) if slow else None
    gate_version = latest_version(versions, (gate or {}).get("id")) if gate else None
    if not slow_version:
        return result(5, False, {"catalog": [w.get("id") for w in client.workflow_catalog()]}, 0, "缺少长任务 workflow 版本，无法构造活动 Run", status="blocked")

    # 5A：唯一活动 Agent 节点 → deliver_to_active_run
    task_id = client.create_task("T4-S5 活动 Run 补充")
    user1, _ = client.post_activity(
        task_id, "执行长任务：等待 40 秒后报告完成。", choice="specified", version_id=slow_version
    )
    try:
        runs = wait_for(
            lambda: client.node_runs(task_id),
            lambda runs: any(r.get("status") == "running" and r.get("session_key") for r in runs),
            150,
            what="single active agent node",
        )
    except TimeoutError as err:
        return result(5, False, {"task_id": task_id, "request": {"body": "执行长任务：等待 40 秒后报告完成。", "workflow_version_id": slow_version}, "error": clip(str(err), 400)}, int((time.time() - started) * 1000), "未能构造唯一活动 Agent 节点，deliver 分支未运行", status="blocked")

    active = next(r for r in runs if r.get("status") == "running" and r.get("session_key"))
    user2, placeholder2 = client.post_activity(task_id, "补充：请在完成后顺便说明耗时。")
    decision = find_decision(client, task_id, user2["id"], DECISION_TIMEOUT)
    detail = client.decision_detail(task_id, decision["id"])
    kinds = set(decision.get("action_kinds") or [])
    items = client.activities(task_id)
    user2_final = activity_by_id(items, user2["id"]) or {}
    placeholder_final = activity_by_id(items, placeholder2.get("id")) or {}
    delivered = "deliver_to_active_run" in kinds and bool(user2_final.get("node_run_id") or user2_final.get("run_id"))
    delivered = delivered and user2_final.get("node_run_id") == active.get("id")
    hidden = bool(placeholder_final.get("hidden"))
    try:
        wait_for(
            lambda: client.node_runs(task_id),
            lambda runs: all(r.get("status") in ("success", "fail", "cancelled", "waiting_user") for r in runs),
            EXEC_TIMEOUT,
            what="slow node terminal after delivery",
        )
    except TimeoutError:
        pass
    final_nodes = client.node_runs(task_id)
    # 绑定成功不等于投递成功：outbox 在节点收尾后还要真的发一轮。旧路径的
    # 投递失败会以 system attention 行出现（workflow_comments.go deliveryFailure），
    # 这是用户可见证据，必须纳入判据。
    final_items = client.activities(task_id)
    delivery_failure = next(
        (a for a in final_items if str(a.get("id", "")).endswith(":delivery_failed") and user2.get("id") in str(a.get("id", ""))),
        None,
    )
    if delivery_failure is not None:
        delivered = False

    # 5B：0 个活动 Agent 节点（HumanGate 等待）→ 可见解释
    gate_task = client.create_task("T4-S5 gate 可见解释")
    gate_evidence = {"skipped": True}
    gate_passed = False
    if gate_version:
        g_user1, _ = client.post_activity(
            gate_task, "发起一次需要人工审批的流程。", choice="specified", version_id=gate_version
        )
        try:
            wait_for(
                lambda: client.node_runs(gate_task),
                lambda runs: any(r.get("status") == "waiting_user" for r in runs),
                120,
                what="human gate waiting",
            )
            g_user2, g_placeholder = client.post_activity(gate_task, "现在进展怎么样？")
            gate_decision = find_decision(client, gate_task, g_user2["id"], DECISION_TIMEOUT)
            gate_detail = client.decision_detail(gate_task, gate_decision["id"])
            g_kinds = set(gate_decision.get("action_kinds") or [])
            g_items = client.activities(gate_task)
            g_reply = activity_by_id(g_items, g_placeholder.get("id")) or {}
            # 可见解释可能落在 body（say/ask_user）或 summary（回退旧路径把守卫
            # 错误写成可见失败回复）。判据是「用户能看到一句解释」，不是必须
            # 由 Router 亲自 say。
            g_text = clip(g_reply.get("body") or "") or clip(g_reply.get("summary") or "")
            visible = bool(g_text) and g_reply.get("status") in ("completed", "waiting_user", "failed")
            router_explained = bool(g_kinds & {"ask_user", "say"})
            gate_passed = visible
            gate_evidence = {
                "skipped": False,
                "task_id": gate_task,
                "request": "现在进展怎么样？",
                "decision": {
                    "id": gate_decision.get("id"),
                    "status": gate_decision.get("status"),
                    "action_kinds": gate_decision.get("action_kinds"),
                    "latency_ms": gate_decision.get("latency_ms"),
                    "error": clip(gate_decision.get("error") or ""),
                },
                "actions": [
                    {"kind": a.get("kind"), "body": clip(a.get("body") or ""), "question": clip(a.get("question") or "")}
                    for a in (gate_detail.get("actions") or [])
                ],
                "visible_reply": {
                    "activity_id": g_reply.get("id"),
                    "status": g_reply.get("status"),
                    "body_excerpt": clip(g_reply.get("body") or ""),
                    "summary_excerpt": clip(g_reply.get("summary") or ""),
                },
                "router_explained_directly": router_explained,
                "visible": visible,
            }
        except TimeoutError as err:
            gate_evidence = {"skipped": False, "task_id": gate_task, "error": clip(str(err), 400)}

    passed = delivered and hidden and gate_passed
    evidence = {
        "deliver": {
            "task_id": task_id,
            "slow_version": slow_version,
            "active_node_run_before": {
                "node_id": active.get("node_id"),
                "status": active.get("status"),
                "session_key": active.get("session_key"),
            },
            "request": "补充：请在完成后顺便说明耗时。",
            "decision": {
                "id": decision.get("id"),
                "status": decision.get("status"),
                "action_kinds": decision.get("action_kinds"),
                "latency_ms": decision.get("latency_ms"),
            },
            "actions": [
                {"kind": a.get("kind"), "body": clip(a.get("body") or ""), "run_id": a.get("run_id"), "reason": clip(a.get("reason") or "")}
                for a in (detail.get("actions") or [])
            ],
            "user_activity_binding": {
                "activity_id": user2.get("id"),
                "node_run_id": user2_final.get("node_run_id"),
                "run_id": user2_final.get("run_id"),
            },
            "placeholder_hidden": hidden,
            "delivery_failed_activity": None
            if delivery_failure is None
            else {
                "id": delivery_failure.get("id"),
                "type": delivery_failure.get("type"),
                "body_excerpt": clip(delivery_failure.get("body") or ""),
            },
            "node_runs_after": [
                {"node_id": n.get("node_id"), "status": n.get("status"), "output": clip(json.dumps(n.get("output") or {}, ensure_ascii=False), 200)}
                for n in final_nodes
            ],
            "delivered": delivered,
        },
        "zero_active_node_explanation": gate_evidence,
    }
    notes = ""
    if not passed and delivery_failure is not None:
        notes = (
            "deliver_to_active_run 决策已应用且绑定到唯一活动节点，但实际投递失败："
            f"{clip(delivery_failure.get('body') or '')}（continue_requests 终态 delivery_failed，"
            "用户侧只看到 system 告警行）。"
        )
    elif not passed:
        notes = "期望：唯一活动节点 deliver 且占位隐藏；0 活动节点给可见解释。实际见 evidence。"
    elif gate_evidence.get("router_explained_directly") is False:
        notes = (
            "5A（唯一活动节点 deliver）通过；5B 的可见解释来自回退旧路径：Router 回合"
            f"耗时 {gate_evidence.get('decision', {}).get('latency_ms')}ms 超过 90s 被 "
            f"`{gate_evidence.get('decision', {}).get('error')}` 打断，回退后旧路径守卫把"
            "「等待审批」写成可见失败回复（status=failed + summary），用户仍能看到解释。"
        )
    return result(5, passed, evidence, int((time.time() - started) * 1000), notes)


# ── 场景 7：同任务消息排队 ─────────────────────────────────────────────────


def scenario_queue(client):
    started = time.time()
    task_id = client.create_task("T4-S7 消息排队")
    body1 = "请用「问题排查」流程处理：登录按钮点击后没有任何反应。"
    body2 = "补充：这个问题只在移动端出现，桌面端正常。"
    user1, _ = client.post_activity(task_id, body1)
    user2, _ = client.post_activity(task_id, body2)

    decisions = {}
    for user_act in (user1, user2):
        decisions[user_act["id"]] = find_decision(client, task_id, user_act["id"], DECISION_TIMEOUT)
    d1 = decisions[user1["id"]]
    d2 = decisions[user2["id"]]
    detail2 = client.decision_detail(task_id, d2["id"])
    brief2 = detail2.get("brief") or {}
    recent2 = [a.get("id") for a in (brief2.get("recent_activities") or [])]
    items = client.activities(task_id)
    users = [a for a in items if a.get("role") == "user"]
    placeholders = [a for a in items if a.get("role") == "agent"]
    lost = int(user2.get("id") not in [u.get("id") for u in users]) + int(user1.get("id") not in [u.get("id") for u in users])
    saw_state = (
        user1.get("id") in recent2
        or bool(brief2.get("active_execution"))
        or bool(brief2.get("latest_run"))
        or any(a.get("reply_to_id") == user1.get("id") for a in (brief2.get("recent_activities") or []))
    )
    applied = d1.get("status") in ("applied", "rejected", "failed") and d2.get("status") in ("applied", "rejected", "failed")
    ordered = str(d1.get("created_at")) <= str(d2.get("created_at"))
    silent_turns = [d for d in (d1, d2) if set(d.get("action_kinds") or []) == {"silent"}]
    passed = lost == 0 and saw_state and applied and ordered and d1.get("status") == "applied" and d2.get("status") == "applied"
    evidence = {
        "task_id": task_id,
        "requests": [{"body": body1}, {"body": body2}],
        "user_activities": [{"id": u.get("id"), "body_excerpt": clip(u.get("body") or "")} for u in users],
        "agent_activities": [
            {"id": a.get("id"), "status": a.get("status"), "hidden": bool(a.get("hidden")), "body_excerpt": clip(a.get("body") or "")}
            for a in placeholders
        ],
        "decisions": [
            {"id": d1.get("id"), "user_activity_id": d1.get("user_activity_id"), "status": d1.get("status"), "action_kinds": d1.get("action_kinds"), "error": clip(d1.get("error") or ""), "created_at": d1.get("created_at"), "latency_ms": d1.get("latency_ms")},
            {"id": d2.get("id"), "user_activity_id": d2.get("user_activity_id"), "status": d2.get("status"), "action_kinds": d2.get("action_kinds"), "error": clip(d2.get("error") or ""), "created_at": d2.get("created_at"), "latency_ms": d2.get("latency_ms")},
        ],
        "second_brief_state": {
            "recent_activity_ids": recent2,
            "has_latest_run": bool(brief2.get("latest_run")),
            "has_active_execution": bool(brief2.get("active_execution")),
            "user_message": clip((brief2.get("trigger") or {}).get("user_message", {}).get("body") or ""),
        },
        "second_actions": [
            {"kind": a.get("kind"), "workflow_id": a.get("workflow_id"), "body": clip(a.get("body") or ""), "reason": clip(a.get("reason") or "")}
            for a in (detail2.get("actions") or [])
        ],
        "losts": lost,
        "saw_current_state": saw_state,
        "ordered": ordered,
        "silent_turns": len(silent_turns),
    }
    notes = ""
    if silent_turns:
        notes = (
            f"{len(silent_turns)}/2 条决策为 silent（模型回合 FinalText 为空，"
            "synthesizeActions 回退成 silent，用户没有任何可见回复）；排队/状态可见性"
            "本身已由 evidence 证实，该现象按缺陷单独记录。"
        )
    elif not passed:
        notes = "期望：两条留言都已落库、第二条决策能看到第一条后的状态、顺序不乱。实际见 evidence。"
    return result(7, passed, evidence, int((time.time() - started) * 1000), notes)


# ── 场景 6：Router 错误/超时/非法动作 ──────────────────────────────────────


def scenario_fallback_timeout(client, induction):
    started = time.time()
    task_id = client.create_task("T4-S6 超时回退")
    body = "请用「问题排查」流程看一下：保存配置时偶尔出现超时告警。"
    user_act, placeholder = client.post_activity(task_id, body)
    try:
        decision = find_decision(client, task_id, user_act["id"], 150)
    except TimeoutError as err:
        return result(6, False, {"task_id": task_id, "request": body, "induction": induction, "error": clip(str(err), 400)}, int((time.time() - started) * 1000), "未观察到回退决策", status="blocked")

    fallback = decision.get("status") == "fallback"
    # 回退后的旧路径执行：占位复用（同一 activity id 拿到 run_id 或完成态）。
    try:
        wait_for(
            lambda: client.activities(task_id),
            lambda acts: (activity_by_id(acts, placeholder.get("id")) or {}).get("run_id")
            or (activity_by_id(acts, placeholder.get("id")) or {}).get("status") in ("completed", "failed", "running"),
            120,
            what="legacy fallback execution",
        )
    except TimeoutError:
        pass
    items = client.activities(task_id)
    placeholder_final = activity_by_id(items, placeholder.get("id")) or {}
    engine = client.engine(task_id)
    nodes = client.node_runs(task_id)
    decisions = client.decisions(task_id)
    agent_rows = agent_activities(items)
    users = [a for a in items if a.get("role") == "user"]
    reused = bool(placeholder_final.get("run_id")) or placeholder_final.get("status") in ("completed", "running")
    single_execution = len(agent_rows) == 1 and len(nodes) <= 1 and len(users) == 1
    cancel_note = client.cancel_task(task_id)
    # D-3 守卫：取消/失败收尾后，可见的 agent 回复不能停在 running/pending。
    # 首轮 S6 的取消路径用过期 Run 结算，复用的回复活动曾永久停在 running；
    # 把「收尾后无残留」变成判据，而不是只记录取消请求本身。
    try:
        post_cancel = wait_for(
            lambda: client.activities(task_id),
            lambda acts: not any(
                a.get("type") == "agent_reply"
                and not a.get("hidden")
                and (a.get("status") or "") in ("running", "pending")
                for a in acts
            ),
            CANCEL_SETTLE_TIMEOUT,
            what="visible agent replies settle after cancel",
        )
    except TimeoutError:
        post_cancel = client.activities(task_id)
    stuck = [
        {"id": a.get("id"), "type": a.get("type"), "status": a.get("status"), "hidden": bool(a.get("hidden"))}
        for a in post_cancel
        if a.get("type") == "agent_reply"
        and not a.get("hidden")
        and (a.get("status") or "") in ("running", "pending")
    ]
    passed = fallback and reused and single_execution and len(decisions) == 1 and not stuck
    evidence = {
        "task_id": task_id,
        "request": body,
        "induction": induction,
        "decision": {
            "id": decision.get("id"),
            "status": decision.get("status"),
            "error": clip(decision.get("error") or ""),
            "action_kinds": decision.get("action_kinds"),
            "latency_ms": decision.get("latency_ms"),
        },
        "placeholder": {
            "activity_id": placeholder.get("id"),
            "reused": reused,
            "status": placeholder_final.get("status"),
            "run_id": placeholder_final.get("run_id"),
        },
        "counts": {
            "decisions": len(decisions),
            "agent_activities": len(agent_rows),
            "user_activities": len(users),
            "node_runs": len(nodes),
        },
        "engine": {"engine": engine.get("engine"), "workflow_run_id": engine.get("workflow_run_id")},
        "node_runs": [{"node_id": n.get("node_id"), "status": n.get("status")} for n in nodes],
        "cancel": cancel_note,
        "cancel_settle": {
            "wait_seconds": CANCEL_SETTLE_TIMEOUT,
            "activities": [
                {"id": a.get("id"), "type": a.get("type"), "status": a.get("status"), "hidden": bool(a.get("hidden"))}
                for a in post_cancel
            ],
            "stuck_running": stuck,
        },
    }
    notes = "" if passed else "期望：单次回退、复用同步占位、不重复执行；实际见 evidence。"
    if stuck:
        notes = "取消收尾后仍有可见 agent 回复停在 running/pending：" + json.dumps(stuck, ensure_ascii=False)
    return result(6, passed, evidence, int((time.time() - started) * 1000), notes)


# ── 编排 ────────────────────────────────────────────────────────────────────


def run_scenarios(client, only):
    catalog = client.workflow_catalog()
    versions = client.published_versions()
    results = []
    runners = {
        1: lambda: scenario_simple_question(client),
        2: lambda: scenario_path_matching(client, catalog, versions),
        3: lambda: scenario_ask_user(client),
        4: lambda: scenario_bypass(client, versions),
        5: lambda: scenario_active_run(client, versions),
        6: lambda: scenario_fallback_timeout(
            client,
            "隔离配置 router.turn_timeout=1s（其余同验收配置），重启隔离实例后发 workflow_choice=empty 留言",
        ),
        7: lambda: scenario_queue(client),
    }
    for idx in only:
        label = f"[{idx}] {SCENARIO_NAMES[idx]}"
        print(f"→ {label} 开始", flush=True)
        started = time.time()
        try:
            entry = runners[idx]()
        except (ApiError, TimeoutError, KeyboardInterrupt) as err:
            entry = result(idx, False, {"error": clip(str(err), 800), "traceback": clip(traceback.format_exc(), 1200)}, int((time.time() - started) * 1000), "场景执行异常")
        except Exception as err:  # noqa: BLE001 - 验收脚本要把任何异常变成显式失败
            entry = result(idx, False, {"error": clip(repr(err), 800), "traceback": clip(traceback.format_exc(), 1200)}, int((time.time() - started) * 1000), "脚本异常，非产品判定")
        entry["name"] = SCENARIO_NAMES[idx]
        results.append(entry)
        verdict = "pass" if entry["pass"] else entry.get("status", "fail")
        print(f"← {label} = {verdict} ({entry['latency_ms']}ms)", flush=True)
    return results


def latencies(results):
    samples = sorted(r["latency_ms"] for r in results if r.get("pass") or r.get("status") not in ("blocked",))
    if not samples:
        return {"samples": 0, "p50_ms": None, "p95_ms": None, "note": "样本不足"}
    p50 = samples[len(samples) // 2]
    p95 = samples[min(len(samples) - 1, int(len(samples) * 0.95))]
    out = {"samples": len(samples), "p50_ms": p50}
    if len(samples) < 3:
        out["p95_ms"] = None
        out["note"] = "样本不足（<3），不报 p95"
    else:
        out["p95_ms"] = p95
    return out


def main(argv=None):
    parser = argparse.ArgumentParser(description="Task Router R0 隔离验收")
    parser.add_argument("--base", required=True, help="隔离实例 base，如 http://127.0.0.1:7432/mindfs")
    parser.add_argument("--user", required=True, help="隔离账户 id")
    parser.add_argument("--root", required=True, help="隔离 root id")
    parser.add_argument("--out", default="", help="结果 JSON 输出路径")
    parser.add_argument("--scenarios", default="1,2,3,4,5,6,7", help="要跑的场景，逗号分隔")
    args = parser.parse_args(argv)

    if "7331" in args.base:
        print("拒绝：不得指向线上 7331", file=sys.stderr)
        return 2
    only = []
    for part in args.scenarios.split(","):
        part = part.strip()
        if not part:
            continue
        idx = int(part)
        if idx not in SCENARIO_NAMES:
            print(f"未知场景 {idx}", file=sys.stderr)
            return 2
        only.append(idx)

    client = Client(args.base, args.user, args.root)
    print(f"base={args.base} user={args.user} root={args.root} scenarios={only}", flush=True)
    results = run_scenarios(client, only)

    blocked = [r for r in results if r.get("status") == "blocked"]
    failed = [r for r in results if not r["pass"] and r.get("status") != "blocked"]
    payload = {
        "base": args.base,
        "root": args.root,
        "scenarios": results,
        "latency": latencies(results),
        "summary": {
            "total": len(results),
            "passed": sum(1 for r in results if r["pass"]),
            "failed": len(failed),
            "blocked": len(blocked),
        },
    }
    if args.out:
        with open(args.out, "w", encoding="utf-8") as fh:
            json.dump(payload, fh, ensure_ascii=False, indent=2)
        print(f"结果已写入 {args.out}", flush=True)
    else:
        print(json.dumps(payload, ensure_ascii=False, indent=2))

    all_decisions = []
    for item in results:
        dec = (item.get("evidence") or {}).get("decision") or {}
        all_decisions.append(dec)
    if blocked and all(r.get("status") == "blocked" for r in results) and is_model_unavailable(all_decisions):
        return 2
    if failed:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
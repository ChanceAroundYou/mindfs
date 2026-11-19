#!/usr/bin/env python3
"""T10 验收脚本的共享层：HTTP 客户端、只读事件库、等待/取证工具。

`test_task_router_coordinate.py` 负责场景编排；本模块只放与场景无关的机械部分，
这样两个文件都能稳定留在 500 行硬线内（`docs/agent/rules/common/file-size.md`）。

边界：纯标准库；`DB` 只以 `mode=ro` 打开 SQLite；不做任何写操作。
"""

from __future__ import annotations

import json
import os
import sqlite3
import time
import urllib.error
import urllib.parse
import urllib.request

BODY_LIMIT = 300
DECISION_TIMEOUT = 240
SETTLE_TIMEOUT = 300
POLL_INTERVAL = 3.0

# 内部 ID 前缀：汇报正文里出现即视为把内部标识泄露给用户。
INTERNAL_ID_PREFIXES = ("run-", "rdec_", "act_", "act-rtev-", "revt_", "task_", "revt-turn:")


def clip(text, limit=BODY_LIMIT):
    text = (text or "").strip()
    if len(text) <= limit:
        return text
    return text[:limit] + "…"


def has_internal_id(text):
    text = text or ""
    return any(prefix in text for prefix in INTERNAL_ID_PREFIXES)


def summary_obj(activity):
    """活动摘要可能是字符串也可能是对象；统一成 dict 再取字段。"""
    return activity if isinstance(activity, dict) else {}


def activity_text(activity):
    """可见正文优先 `body`，没有就退到 `summary`（字符串或是对象）。"""
    activity = activity or {}
    body = (activity.get("body") or "").strip()
    if body:
        return body
    summary = activity.get("summary")
    if isinstance(summary, str):
        return summary.strip()
    if isinstance(summary, dict):
        return str(summary.get("text") or summary.get("content") or "").strip()
    return ""


def is_router_effect(activity):
    """运行期事件回合的汇报行：确定性 effect ID（`act-rtev-…`）。"""
    return str((activity or {}).get("id") or "").startswith("act-rtev-")


class ApiError(RuntimeError):
    def __init__(self, status, path, body):
        super().__init__(f"{status} {path}: {body}")
        self.status = status
        self.path = path
        self.body = body


class Client:
    """账户作用域 HTTP 客户端；所有请求都带 user=。"""

    def __init__(self, base, user, root, request_timeout=120):
        self.base = base.rstrip("/")
        self.user = user
        self.root = root
        self.request_timeout = request_timeout

    def url(self, path):
        sep = "&" if "?" in path else "?"
        return f"{self.base}{path}{sep}user={urllib.parse.quote(self.user)}"

    def call(self, method, path, payload=None):
        data = json.dumps(payload).encode() if payload is not None else None
        req = urllib.request.Request(
            self.url(path), data=data, method=method, headers={"Content-Type": "application/json"}
        )
        try:
            with urllib.request.urlopen(req, timeout=self.request_timeout) as resp:
                return resp.status, json.loads(resp.read().decode() or "{}")
        except urllib.error.HTTPError as err:
            raw = err.read().decode(errors="replace")
            raise ApiError(err.code, path, clip(raw, 600)) from None

    def try_call(self, method, path, payload=None):
        try:
            return self.call(method, path, payload)
        except ApiError as err:
            if err.status == 404:
                return None, None
            raise

    def health(self):
        try:
            with urllib.request.urlopen(f"{self.base}/health", timeout=10) as resp:
                return resp.status
        except urllib.error.HTTPError as err:
            return err.code
        except (urllib.error.URLError, OSError):
            return 0

    def router_status(self):
        _, resp = self.call("GET", "/api/router/status?root_id=" + urllib.parse.quote(self.root))
        return resp.get("router") or {}

    # ── 夹具 ────────────────────────────────────────────────────────────────

    def ensure_fixture(self, workflow_id, name, description, nodes, edges):
        """建并发布一个夹具。已存在就先删，保证图与本脚本一致。"""
        self.try_call("DELETE", f"/api/workflows/{workflow_id}")
        _, resp = self.call(
            "POST",
            "/api/workflows",
            {
                "workflow_id": workflow_id,
                "root_id": self.root,
                "author": "t10",
                "graph": {"nodes": nodes, "edges": edges},
            },
        )
        revision = (resp.get("draft") or {}).get("revision") or "r1"
        self.call("PUT", f"/api/workflows/{workflow_id}/meta", {"name": name, "description": description})
        _, pub = self.call(
            "POST", f"/api/workflows/{workflow_id}/publish", {"confirmed_revision": revision, "published_by": "t10"}
        )
        version = (pub.get("version") or {}).get("version")
        if not version:
            raise ApiError(0, f"/api/workflows/{workflow_id}/publish", clip(json.dumps(pub, ensure_ascii=False)))
        return version

    # ── 数据面 ──────────────────────────────────────────────────────────────

    def create_task(self, name):
        _, detail = self.call(
            "POST",
            "/api/tasks",
            {"root_id": self.root, "input": "", "name": name, "create_worktree": False, "start_immediately": False},
        )
        task = detail.get("task") or {}
        if not task.get("id"):
            raise ApiError(0, "/api/tasks", f"missing task id: {clip(str(detail), 400)}")
        return task["id"]

    def task(self, task_id):
        _, resp = self.call("GET", f"/api/tasks?root={urllib.parse.quote(self.root)}&limit=200")
        for item in resp.get("items") or []:
            task = item.get("task") or item
            if task.get("id") == task_id:
                return task
        return {}

    def post_activity(self, task_id, body, choice="empty", version_id=None):
        payload = {"root_id": self.root, "body": body, "workflow_choice": choice}
        if version_id:
            payload["workflow_version_id"] = version_id
        _, resp = self.call("POST", f"/api/tasks/{task_id}/activities", payload)
        return resp.get("user_activity") or {}, resp.get("agent_activity") or {}

    def activities(self, task_id):
        _, resp = self.call(
            "GET", f"/api/tasks/{task_id}/activities?root_id={urllib.parse.quote(self.root)}&include_hidden=1"
        )
        return resp.get("activities") or []

    def decisions(self, task_id):
        _, resp = self.call(
            "GET", f"/api/tasks/{task_id}/router-decisions?root_id={urllib.parse.quote(self.root)}&limit=50"
        )
        return resp.get("decisions") or []

    def decision_detail(self, task_id, decision_id):
        _, resp = self.call(
            "GET", f"/api/tasks/{task_id}/router-decisions/{decision_id}?root_id={urllib.parse.quote(self.root)}"
        )
        return resp

    def node_runs(self, task_id):
        _, resp = self.try_call(
            "GET", f"/api/tasks/{task_id}/workflow-run/nodes?root_id={urllib.parse.quote(self.root)}"
        )
        return (resp or {}).get("node_runs") or []

    def human_gate(self, task_id, node_id, decision, note=""):
        payload = {"root_id": self.root, "node_id": node_id, "decision": decision}
        if note:
            payload["note"] = note
        return self.call("POST", f"/api/tasks/{task_id}/human-gate", payload)

    def create_specified_activity(self, task_id, body, version_id):
        """显式指定版本建的一轮（main 会话需求）：审批前不需要 Router 决策。"""
        return self.post_activity(task_id, body, choice="specified", version_id=version_id)


class DB:
    """router_events / continue_requests 的只读视图（`mode=ro`，绝不写库）。"""

    def __init__(self, path):
        if not path:
            raise ValueError("--db required：router_events 没有 HTTP 端点，只能只读库")
        if not os.path.exists(path):
            raise ValueError(f"DB not found: {path}")
        self.path = path
        self.uri = f"file:{urllib.parse.quote(path)}?mode=ro"

    def query(self, sql, params=()):
        conn = sqlite3.connect(self.uri, uri=True, timeout=5)
        try:
            conn.row_factory = sqlite3.Row
            return [dict(row) for row in conn.execute(sql, params)]
        finally:
            conn.close()

    def events(self, task_id=None):
        sql = (
            "SELECT id, task_id, run_id, kind, status, chain_depth, user_activity_id, turn_id, attempt,"
            " claimed_by, created_at, completed_at, last_error FROM router_events"
        )
        if task_id:
            return self.query(sql + " WHERE task_id = ? ORDER BY created_at ASC, id ASC", (task_id,))
        return self.query(sql + " ORDER BY created_at ASC, id ASC")

    def continue_requests(self, task_id):
        return self.query(
            "SELECT request_id, task_id, comment_id, status, node_run_id, session_key, updated_at"
            " FROM continue_requests WHERE task_id = ? ORDER BY created_at ASC",
            (task_id,),
        )

    def run_ids(self, task_id):
        return [
            row["id"]
            for row in self.query("SELECT id FROM workflow_runs WHERE task_id = ? ORDER BY created_at ASC, id ASC",
                                  (task_id,))
        ]

    def activity_ids(self, task_id):
        return [row["id"] for row in self.query("SELECT id FROM task_activities WHERE task_id = ?", (task_id,))]

    def decision_ids(self, task_id):
        return [row["id"] for row in self.query("SELECT id FROM router_decisions WHERE task_id = ?", (task_id,))]


def wait_for(fetch, predicate, timeout, what="", interval=POLL_INTERVAL):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = fetch()
        if predicate(last):
            return last
        time.sleep(interval)
    raise TimeoutError(
        f"timeout {timeout}s waiting for {what or 'condition'}; last="
        + clip(json.dumps(last, ensure_ascii=False, default=str), 600)
    )


def wait_decision(client, task_id, user_activity_id, timeout=DECISION_TIMEOUT):
    """等某条留言的决策落到非过渡态（applying/shadow 不算完成）。"""

    def fetch():
        for dec in client.decisions(task_id):
            if dec.get("user_activity_id") == user_activity_id:
                return dec
        return None

    return wait_for(
        fetch,
        lambda d: d is not None and d.get("status") not in ("applying", "shadow"),
        timeout,
        what=f"decision for activity {user_activity_id}",
    )


def wait_event_terminal(db, task_id, kind, timeout=SETTLE_TIMEOUT, run_id=None):
    def fetch():
        rows = [
            e for e in db.events(task_id)
            if e.get("kind") == kind and (run_id is None or e.get("run_id") == run_id)
        ]
        return rows[-1] if rows else None

    return wait_for(
        fetch,
        lambda e: e is not None and e.get("status") not in ("pending", "claimed"),
        timeout,
        what=f"router_events kind={kind} terminal",
    )


def wait_event_decision(client, task_id, trigger_kind, timeout=DECISION_TIMEOUT, trigger_ref=None):
    """等一条由运行期事件驱动的决策（`trigger_kind` 不是 user_message）。"""

    def fetch():
        for dec in client.decisions(task_id):
            if dec.get("trigger_kind") != trigger_kind:
                continue
            if trigger_ref and dec.get("trigger_ref") != trigger_ref:
                continue
            return dec
        return None

    return wait_for(
        fetch,
        lambda d: d is not None and d.get("status") not in ("applying", "shadow"),
        timeout,
        what=f"decision trigger_kind={trigger_kind}",
    )


def wait_progress_activity(client, task_id, user_activity_id, timeout=SETTLE_TIMEOUT, decision_id=None):
    """等该留言的汇报行。给了 decision_id 就只认那条决策写的行。

    一条留言可能先有消息回合写的 `say`（也是 purpose=progress），事件回合的汇报
    在 Run 结算之后才出现；按 decision 收窄才能区分「应答」与「事件汇报」。
    """

    def fetch():
        items = [
            a
            for a in client.activities(task_id)
            if a.get("purpose") == "progress" and a.get("reply_to_id") == user_activity_id
            and (decision_id is None or a.get("router_decision_id") == decision_id)
        ]
        return items[-1] if items else None

    return wait_for(
        fetch, lambda a: a is not None and a.get("status") in ("completed", "failed"), timeout,
        what="progress activity bound to the originating user activity",
    )


def wait_effect_activity(client, task_id, user_activity_id, decision_id=None, timeout=SETTLE_TIMEOUT):
    """等确定性 effect 行：事件回合的汇报只可能是 `act-rtev-…`。"""

    def fetch():
        items = [
            a
            for a in client.activities(task_id)
            if is_router_effect(a) and a.get("reply_to_id") == user_activity_id
            and (decision_id is None or a.get("router_decision_id") == decision_id)
        ]
        return items[-1] if items else None

    return wait_for(
        fetch, lambda a: a is not None and a.get("status") in ("completed", "failed"), timeout,
        what="router event effect activity",
    )


def activity_by_id(items, activity_id):
    for item in items:
        if item.get("id") == activity_id:
            return item
    return None


def node_by_id(nodes, node_id):
    for node in nodes:
        if node.get("node_id") == node_id:
            return node
    return None


def is_model_unavailable(text):
    text = (text or "").lower()
    markers = ("api key", "authentication", "unauthorized", "401", "model not found", "invalid api key",
               "credit balance")
    return any(m in text for m in markers)


def evidence_decisions(client, task_id):
    return [
        {
            "id": d.get("id"),
            "trigger_kind": d.get("trigger_kind"),
            "trigger_ref": d.get("trigger_ref"),
            "user_activity_id": d.get("user_activity_id"),
            "status": d.get("status"),
            "action_kinds": d.get("action_kinds"),
            "latency_ms": d.get("latency_ms"),
            "error": clip(d.get("error") or ""),
        }
        for d in client.decisions(task_id)
    ]


def evidence_events(db, task_id):
    return [
        {
            "id": ev.get("id"),
            "run_id": ev.get("run_id"),
            "kind": ev.get("kind"),
            "status": ev.get("status"),
            "chain_depth": ev.get("chain_depth"),
            "user_activity_id": ev.get("user_activity_id"),
            "created_at": ev.get("created_at"),
            "completed_at": ev.get("completed_at"),
            "last_error": clip(ev.get("last_error") or ""),
        }
        for ev in db.events(task_id)
    ]


def evidence_activities(client, task_id):
    return [
        {
            "id": a.get("id"),
            "type": a.get("type"),
            "role": a.get("role"),
            "status": a.get("status"),
            "purpose": a.get("purpose"),
            "reply_to_id": a.get("reply_to_id"),
            "run_id": a.get("run_id"),
            "node_run_id": a.get("node_run_id"),
            "hidden": bool(a.get("hidden")),
            "router_decision_id": a.get("router_decision_id"),
            "body_excerpt": clip(activity_text(a)),
            "summary_excerpt": clip(a.get("summary") if isinstance(a.get("summary"), str) else ""),
        }
        for a in client.activities(task_id)
    ]


def evidence_nodes(nodes):
    return [
        {
            "node_run_id": n.get("id"),
            "node_id": n.get("node_id"),
            "attempt": n.get("attempt"),
            "status": n.get("status"),
            "agent": n.get("agent"),
            "session_key": n.get("session_key"),
            "output_excerpt": clip(json.dumps(n.get("output") or {}, ensure_ascii=False), 240),
        }
        for n in nodes
    ]


def evidence_continue_requests(db, task_id):
    return [
        {
            "request_id": r.get("request_id"),
            "comment_id": r.get("comment_id"),
            "status": r.get("status"),
            "node_run_id": r.get("node_run_id"),
            "session_key": r.get("session_key"),
            "updated_at": r.get("updated_at"),
        }
        for r in db.continue_requests(task_id)
    ]


def result(scenario, passed, evidence, latency_ms, notes="", status=None):
    return {
        "scenario": scenario,
        "pass": bool(passed),
        "status": status or ("pass" if passed else "fail"),
        "evidence": evidence,
        "latency_ms": latency_ms,
        "notes": notes,
    }

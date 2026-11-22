#!/usr/bin/env python3
"""W2 节点级管理验收（隔离实例，真实模型）。

A. 运行期图编辑冻结：节点一开跑，它的参数就锁住（409 frozen）；没跑过的节点照常可改，
   并且改后的 prompt 真的被下游使用。
B. Router 节点管理：运行中给用户留言，Router 用 `router_skip_node` 跳过还没开始的节点，
   整轮 Run 仍然收尾（被跳过的节点记 skipped，不进执行）。

用法（只在隔离实例上跑，绝不指向线上 7331）：

    python3 e2e/test_task_router_w2_node_admin.py \
        --base http://127.0.0.1:7465/mindfs \
        --user iso-e2e-... --root proj
"""

import argparse
import copy
import json
import sys
import time

from test_task_router_coordinate_common import ApiError, Client

ONLINE_PORT = 7331
POLL_INTERVAL = 3.0
RUNNING_TIMEOUT = 240
NODE_TIMEOUT = 420
DECISION_TIMEOUT = 300

# n1 睡这么久，给「运行中编辑 / Router 留言 / 跳过节点」留出窗口；醒来只回 DONE。
LONG_PROMPT = "请用 Bash 执行命令 `sleep 240`（只这一条，不要做别的），命令结束后只回复 DONE。"


def fail(msg):
    print(f"FAIL {msg}", flush=True)
    sys.exit(1)


def short(value, limit=600):
    text = json.dumps(value, ensure_ascii=False) if not isinstance(value, str) else value
    return text if len(text) <= limit else text[:limit] + "…"


def run_of(client, task_id):
    path = f"/api/tasks/{task_id}/workflow-run?root_id={client.root}"
    try:
        _, resp = client.call("GET", path)
    except ApiError as err:
        if err.status == 404:
            return None
        raise
    return (resp or {}).get("run")


def wait_run(client, task_id, predicate, timeout=RUNNING_TIMEOUT, what="run"):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = run_of(client, task_id)
        if last and predicate(last):
            return last
        time.sleep(POLL_INTERVAL)
    fail(f"等不到{what}；最后状态：{short(last)}")


def node_run_status(client, task_id, node_id, attempt=1):
    path = f"/api/tasks/{task_id}/workflow-run/nodes?root_id={client.root}"
    _, resp = client.call("GET", path)
    for nr in resp.get("node_runs") or []:
        if nr.get("node_id") == node_id and nr.get("attempt") == attempt:
            return nr.get("status")
    return None


def wait_node_run_status(client, task_id, node_id, states, timeout=NODE_TIMEOUT):
    """node_states 投影只在节点收尾时落库；运行中的真相要看 NodeRun 行。"""
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = node_run_status(client, task_id, node_id)
        if last in states:
            return last
        time.sleep(POLL_INTERVAL)
    fail(f"等不到 {node_id} NodeRun 进入 {sorted(states)}；最后状态：{last}")


def edit_graph(client, task_id, graph, revision):
    return client.call(
        "PUT",
        f"/api/tasks/{task_id}/workflow-run?root_id={client.root}",
        {"revision": revision, "graph": graph},
    )


def with_prompt(graph, node_id, prompt):
    out = copy.deepcopy(graph)
    for node in out.get("nodes") or []:
        if node.get("id") == node_id:
            node.setdefault("params", {})["prompt"] = prompt
            return out
    fail(f"图里没有节点 {node_id}")


def wait_decision(client, task_id, predicate, timeout=DECISION_TIMEOUT, what="decision"):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        for item in client.decisions(task_id):
            last = item
            if predicate(item):
                return item
        time.sleep(POLL_INTERVAL)
    fail(f"等不到{what}；最后一条决策：{short(last)}")


def main(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", required=True)
    parser.add_argument("--user", required=True)
    parser.add_argument("--root", required=True)
    parser.add_argument("--tag", default="")
    args = parser.parse_args(argv)
    if f":{ONLINE_PORT}" in args.base:
        fail("拒绝指向线上 7331")

    tag = args.tag or str(int(time.time()))
    client = Client(args.base, args.user, args.root)
    wf = f"wf-iso-w2-{tag}"
    nodes = [
        {
            "id": "n1",
            "name": "长任务",
            "kind": "agent",
            "params": {"agent": "claude", "prompt": LONG_PROMPT, "model": "sonnet"},
        },
        {"id": "n2", "name": "收尾", "kind": "agent", "params": {"agent": "claude", "prompt": "只回复 OK"}},
        {"id": "n3", "name": "可选", "kind": "agent", "params": {"agent": "claude", "prompt": "只回复 OK"}},
    ]
    edges = [
        {"from": "n1", "to": "n2"},
        {"from": "n2", "to": "n3"},
    ]
    version = client.ensure_fixture(wf, "ISO W2 节点管理验收", "运行中编辑冻结与跳过", nodes, edges)

    task_id = client.create_task("ISO W2 节点管理验收")
    client.post_activity(task_id, "请用这个 workflow 跑一轮。", choice="specified", version_id=version)

    wait_node_run_status(client, task_id, "n1", {"running"})
    _, current = client.call("GET", f"/api/tasks/{task_id}/workflow-run?root_id={client.root}")
    run = (current or {}).get("run") or {}
    revision = run.get("revision")
    if not revision:
        fail(f"拿不到 Run 的 revision：{short(run)}")

    # A. 运行中的节点不许改：图里改 n1 的 prompt 必须被 409 拒绝。
    try:
        edit_graph(client, task_id, with_prompt(run["graph"], "n1", "改成一个立刻结束的节点"), revision)
    except ApiError as err:
        if err.status != 409 or "frozen" not in err.body:
            fail(f"运行中改 n1 的拒绝不对：{err}")
    else:
        fail("运行中改 n1 竟然成功了")

    # 没跑过的 n2 照常可改，改完 revision 前进；NodeRun 的 input 会证明它真的生效。
    _, resp = edit_graph(
        client,
        task_id,
        with_prompt(run["graph"], "n2", "请用 Bash 执行命令 `echo EDITED-OK`，命令结束后只回复 EDITED-OK。"),
        revision,
    )
    if not resp.get("ok"):
        fail(f"改未触发的 n2 没有生效：{short(resp)}")
    _, current = client.call("GET", f"/api/tasks/{task_id}/workflow-run?root_id={client.root}")
    new_revision = ((current or {}).get("run") or {}).get("revision")
    if not new_revision or new_revision <= revision:
        fail(f"编辑后 revision 没有前进：{revision} → {new_revision}")

    # B. 运行中给 Router 留言：跳过还没开始的 n3。
    client.post_activity(task_id, "n3 这次不需要跑，请跳过它；n1 正在运行就不用动它。")
    decision = wait_decision(
        client,
        task_id,
        lambda d: "skip_node" in (d.get("action_kinds") or [])
        and d.get("status") not in (None, "pending", "applying"),
        what="router_skip_node 决策",
    )
    if decision.get("status") != "applied":
        fail(f"skip_node 决策未应用：{short(decision)}")
    detail = client.decision_detail(task_id, decision.get("id"))
    skip_targets = [a.get("node_id") for a in detail.get("actions") or [] if a.get("kind") == "skip_node"]
    if "n3" not in skip_targets:
        fail(f"跳过的不是 n3：{skip_targets}")
    if node_run_status(client, task_id, "n1") != "running":
        fail("跳过落定时 n1 已经不在运行，验收窗口没对上")

    run = wait_run(
        client,
        task_id,
        lambda r: (r.get("node_states") or {}).get("n2") == "success"
        and (r.get("node_states") or {}).get("n3") == "skipped",
        timeout=NODE_TIMEOUT,
        what="n2 成功且 n3 记 skipped",
    )
    states = run.get("node_states") or {}
    if states.get("n1") != "success":
        fail(f"n1 没有成功：{short(states)}")
    n2_runs = [nr for nr in client.node_runs(task_id) if nr.get("node_id") == "n2"]
    if not n2_runs:
        fail("Task 上没有 n2 的 NodeRun")
    n2_prompt = ((n2_runs[-1].get("input") or {}).get("prompt") or "")
    if "EDITED-OK" not in n2_prompt:
        fail(f"n2 执行时用的还是旧 prompt：{short(n2_prompt)}")

    print("PASS W2：运行中改节点被冻结、未触发节点可改、Router 跳过节点，Run 正常收尾", flush=True)
    print(
        json.dumps(
            {
                "workflow": wf,
                "version": version,
                "task": task_id,
                "run": run.get("id"),
                "revision": new_revision,
                "decision": decision.get("id"),
                "node_states": states,
            },
            ensure_ascii=False,
            indent=2,
        )
    )
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ApiError as err:
        fail(str(err))
#!/usr/bin/env python3
"""W3 固化验收（隔离实例，真实模型）：Router 把一张图固化进目录的两条路径。

A. 全局条目：Router `router_save_workflow(scope=global)` 只落一张 `workflow_publish`
   待批准卡，控制面零写入；用户在任务详情里批准后发布为一个新版本，`defaults.model`
   随图带过去。
B. 项目级新条目：同一工具直接发布，不落卡、不需要批准。

用法（只在隔离实例上跑，绝不指向线上 7331）：

    python3 e2e/test_task_router_w3_publish.py \
        --base http://127.0.0.1:7465/mindfs \
        --user iso-e2e-... --root /tmp/mindfs-w123/proj
"""

import argparse
import json
import sys
import time
import urllib.parse

from test_task_router_coordinate_common import ApiError, Client

ONLINE_PORT = 7331
DECISION_TIMEOUT = 300
RUN_TIMEOUT = 420
POLL_INTERVAL = 3.0


def fail(msg):
    print(f"FAIL {msg}", flush=True)
    sys.exit(1)


def short(value, limit=500):
    text = json.dumps(value, ensure_ascii=False) if not isinstance(value, str) else value
    return text if len(text) <= limit else text[:limit] + "…"


def wait_decision(client, task_id, predicate, timeout=DECISION_TIMEOUT, what="decision"):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        items = client.decisions(task_id)
        for item in items:
            last = item
            if predicate(item):
                return item
        time.sleep(POLL_INTERVAL)
    fail(f"等不到{what}；最后一条决策：{short(last)}")


def wait_activity(client, task_id, predicate, timeout=120, what="activity"):
    deadline = time.time() + timeout
    last = []
    while time.time() < deadline:
        items = client.activities(task_id)
        last = items
        for item in items:
            if predicate(item):
                return item
        time.sleep(POLL_INTERVAL)
    fail(f"等不到{what}；活动：{short(last)}")


def decision_done(item, kind):
    return (
        kind in (item.get("action_kinds") or [])
        and item.get("status") not in (None, "pending", "applying")
    )


def workflow_ids(client):
    _, resp = client.call("GET", "/api/workflows")
    out = set()
    for item in resp.get("items") or []:
        draft = item.get("draft") or item
        wid = draft.get("workflow_id") or item.get("workflow_id")
        if wid:
            out.add(wid)
    return out


def run_of(client, task_id):
    path = f"/api/tasks/{task_id}/workflow-run?root_id={urllib.parse.quote(client.root)}"
    try:
        _, resp = client.call("GET", path)
    except ApiError as err:
        if err.status == 404:
            return None
        raise
    return (resp or {}).get("run")


def wait_run(client, task_id, predicate, timeout=RUN_TIMEOUT, what="run"):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = run_of(client, task_id)
        if last and predicate(last):
            return last
        time.sleep(POLL_INTERVAL)
    fail(f"等不到{what}；最后状态：{short(last)}")


def versions_for(client, workflow_id):
    _, resp = client.call("GET", "/api/workflows/published")
    items = resp.get("items") or []
    return [v for v in items if v.get("workflow_id") == workflow_id]


def save_prompt(workflow_id, name):
    return (
        "请把这份固定流程固化进目录，直接调用 router_save_workflow，不要执行它：\n"
        f"- workflow_id: {workflow_id}\n"
        f"- 名字: {name}\n"
        "- 图里只有一个 agent 节点：id=n1，name=输出，prompt=只需回复 OK\n"
        "- 图级默认模型档 defaults.model 设为 sonnet\n"
    )


def scenario_global(client, tag):
    workflow_id = f"wf-iso-w3g-{tag}"
    task_id = client.create_task("ISO W3 全局固化验收")
    client.post_activity(
        task_id,
        save_prompt(workflow_id, "ISO W3 全局流程")
        + "这是全局条目：按规矩只落一张待批准卡，不要自行批准，也不要谎称已经发布。",
    )
    decision = wait_decision(
        client,
        task_id,
        lambda d: decision_done(d, "save_workflow"),
        what="router_save_workflow 决策",
    )
    if decision.get("status") != "applied":
        fail(f"save_workflow 决策未应用：{short(decision)}")

    card = wait_activity(
        client,
        task_id,
        lambda a: a.get("type") == "workflow_publish" and a.get("status") == "waiting_user",
        what="workflow_publish 待批准卡",
    )
    if workflow_id in workflow_ids(client):
        fail("批准之前控制面就出现了草稿")
    if versions_for(client, workflow_id):
        fail("批准之前就发布了版本")

    status, resp = client.call(
        "POST",
        f"/api/tasks/{task_id}/workflow-publish",
        {"root_id": client.root, "activity_id": card.get("id"), "decision": "approve"},
    )
    if status != 200:
        fail(f"批准失败：{status} {short(resp)}")

    if workflow_id not in workflow_ids(client):
        fail("批准后控制面仍没有草稿")
    versions = versions_for(client, workflow_id)
    if not versions:
        fail("批准后没有发布版本")
    graph = (versions[-1] or {}).get("graph") or {}
    model = ((graph.get("defaults") or {}).get("model") or "").strip()
    if model != "sonnet":
        fail(f"发布版本没有带上 defaults.model=sonnet：{short(graph)}")
    nodes = graph.get("nodes") or []
    if len(nodes) != 1 or nodes[0].get("id") != "n1":
        fail(f"发布版本节点与草稿不一致：{short(nodes)}")
    agent = ((nodes[0].get("params") or {}).get("agent") or "").strip()
    if agent != "claude":
        fail(f"发布版本节点没补上默认 harness：{short(nodes)}")
    return {
        "workflow_id": workflow_id,
        "version": versions[-1].get("version"),
        "card": card.get("id"),
        "decision": decision.get("id"),
        "defaults_model": model,
        "node_agent": agent,
    }


def scenario_project(client, tag):
    workflow_id = f"wf-iso-w3p-{tag}"
    task_id = client.create_task("ISO W3 项目固化验收")
    client.post_activity(
        task_id,
        save_prompt(workflow_id, "ISO W3 项目流程")
        + "这是项目级新条目：按规矩直接发布、不需要批准，发布后告诉我。",
    )
    decision = wait_decision(
        client,
        task_id,
        lambda d: decision_done(d, "save_workflow"),
        what="router_save_workflow 决策",
    )
    if decision.get("status") != "applied":
        fail(f"项目级 save_workflow 决策未应用：{short(decision)}")
    versions = versions_for(client, workflow_id)
    if not versions:
        fail("项目级条目没有直接发布版本")
    cards = [
        a
        for a in client.activities(task_id)
        if a.get("type") == "workflow_publish"
    ]
    if cards:
        fail(f"项目级条目不该落待批准卡：{short(cards)}")
    graph = versions[-1].get("graph") or {}
    model = ((graph.get("defaults") or {}).get("model") or "").strip()
    if model != "sonnet":
        fail(f"项目级版本没有带上 defaults.model=sonnet：{short(graph)}")
    nodes = graph.get("nodes") or []
    agent = ((nodes[0].get("params") or {}).get("agent") or "").strip() if nodes else ""
    if agent != "claude":
        fail(f"项目级版本节点没补上默认 harness：{short(nodes)}")
    return {
        "workflow_id": workflow_id,
        "version": versions[-1].get("version"),
        "defaults_model": model,
        "node_agent": agent,
    }


def scenario_adhoc(client, tag):
    """Router 当场起草的图（节点不写 params.agent）必须真的能跑。

    `agent required` 曾经只在执行期才炸：图能起草、能校验、能发布，节点一开跑
    整个 Run 就失败。这里要求 Router 用 `router_run_workflow(graph)` 起一轮，
    两个节点都必须 success，且输出各自是 A-OK / B-OK。
    """
    catalog_before = workflow_ids(client)
    task_id = client.create_task(f"ISO W3 临时执行验收 {tag}")
    client.post_activity(
        task_id,
        "请现场起草一张临时工作流，用 router_run_workflow 的 graph 参数直接执行这一轮；"
        "不要保存到目录，不要传 workflow_id，不要问我确认。"
        "图：两个 agent 节点 n1 → n2。n1 的 prompt 是「请用 Bash 执行命令 `echo A-OK`，"
        "命令结束后只回复 A-OK。」；n2 的 prompt 是「请用 Bash 执行命令 `echo B-OK`，"
        "命令结束后只回复 B-OK。」。节点不要写 params.agent。",
    )
    decision = wait_decision(
        client,
        task_id,
        lambda d: decision_done(d, "run_workflow"),
        what="router_run_workflow 决策",
    )
    if decision.get("status") != "applied":
        fail(f"run_workflow 决策未应用：{short(decision)}")
    run = wait_run(
        client,
        task_id,
        lambda r: (r.get("status") or "") in ("success", "fail", "failed", "cancelled", "waiting_user")
        or all(
            (r.get("node_states") or {}).get(n) in ("success", "failed", "cancelled", "waiting_user")
            for n in ("n1", "n2")
        ),
        what="临时 Run 收尾",
    )
    states = run.get("node_states") or {}
    if states.get("n1") != "success" or states.get("n2") != "success":
        fail(f"临时 Run 的节点没跑成功（缺 harness 会在执行期报 agent required）：{short(run)}")
    outputs = run.get("node_outputs") or {}
    blob = json.dumps(outputs, ensure_ascii=False)
    if "A-OK" not in blob or "B-OK" not in blob:
        fail(f"节点输出与预期不符：{short(blob, 400)}")
    leaked = {wid for wid in workflow_ids(client) if wid not in catalog_before}
    if leaked:
        fail(f"临时图不该进控制面目录：{sorted(leaked)}")
    return {
        "task_id": task_id,
        "run_id": run.get("id") or run.get("run_id"),
        "decision": decision.get("id"),
        "states": states,
    }


def main(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", required=True)
    parser.add_argument("--user", required=True)
    parser.add_argument("--root", required=True)
    parser.add_argument("--scenario", default="ABC", help="A 全局 / B 项目 / C 临时执行 / 组合")
    parser.add_argument("--tag", default="", help="workflow id 后缀，默认时间戳")
    args = parser.parse_args(argv)

    if f":{ONLINE_PORT}" in args.base:
        fail("拒绝指向线上 7331")
    tag = args.tag or str(int(time.time()))
    client = Client(args.base, args.user, args.root)

    results = {}
    if "A" in args.scenario:
        results["A"] = scenario_global(client, tag)
        print("PASS A：全局条目落卡 → 批准 → 发布，defaults.model / 节点 harness 都带过去了", flush=True)
    if "B" in args.scenario:
        results["B"] = scenario_project(client, tag)
        print("PASS B：项目级条目直接发布、没有待批准卡，节点 harness 已补", flush=True)
    if "C" in args.scenario:
        results["C"] = scenario_adhoc(client, tag)
        print("PASS C：Router 临时起草的图（节点没写 agent）真的跑完了两个节点", flush=True)
    print(json.dumps(results, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ApiError as err:
        fail(str(err))
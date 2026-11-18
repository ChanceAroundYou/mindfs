#!/usr/bin/env python3
"""T10 验收夹具：三个 workflow 图的定义与建/发布入口。

从 `test_task_router_coordinate.py` 拆出（500 行硬线）。夹具的区分靠 name/
description —— Router 只看 brief 里的元数据选流程，看不到图。

关键事实（T10 探针实测）：agent 节点**必须**显式给 `params.agent`，否则适配层
以 `agent required` 直接失败，看起来像产品缺陷其实是夹具缺字段。
"""

from __future__ import annotations

# agent 参数必须显式给：缺省会以 "agent required" 直接失败。
AGENT_PROFILE = {"agent": "claude", "model": "sonnet", "effort": "low"}
QUIET_PROMPT = (
    "这是 T10 验收夹具。不要读取任何文件、不要调用任何工具、不要修改任何东西。"
    "只用一句话中文回答：{line}"
)

FIXTURES = {
    "t10-quick": {
        "name": "T10 快速确认",
        "description": "快速执行，一句话确认完成",
        "nodes": [
            {
                "id": "run",
                "name": "快速执行",
                "kind": "agent",
                "params": {**AGENT_PROFILE, "prompt": QUIET_PROMPT.format(line="快速验证已完成。")},
            }
        ],
        "edges": [],
    },
    "t10-fail": {
        "name": "T10 必然失败",
        "description": "一定会失败的执行（用于失败汇报）",
        "nodes": [
            {
                "id": "run",
                "name": "失败执行",
                "kind": "agent",
                "params": {"agent": "t10-missing-agent", "prompt": "任意非空提示"},
            }
        ],
        "edges": [],
    },
    "t10-gate": {
        "name": "T10 需要人工审批",
        "description": "需要人工审批后再继续",
        "nodes": [
            {
                "id": "prepare",
                "name": "准备",
                "kind": "agent",
                "params": {**AGENT_PROFILE, "prompt": QUIET_PROMPT.format(line="准备完成。")},
            },
            {"id": "gate", "name": "人工审批", "kind": "human_gate", "params": {"prompt": "请审批后继续"}},
            {
                "id": "finish",
                "name": "收尾",
                "kind": "agent",
                "params": {**AGENT_PROFILE, "prompt": QUIET_PROMPT.format(line="收尾完成。")},
            },
        ],
        "edges": [{"from": "prepare", "to": "gate"}, {"from": "gate", "to": "finish"}],
    },
}


def ensure_fixtures(client):
    """建并发布三个夹具；返回 {workflow_id: published_version_id}。"""
    versions = {}
    for wid, spec in FIXTURES.items():
        versions[wid] = client.ensure_fixture(wid, spec["name"], spec["description"], spec["nodes"], spec["edges"])
    return versions

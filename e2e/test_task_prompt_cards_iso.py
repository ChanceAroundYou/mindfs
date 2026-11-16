"""任务对话里的提问卡 / HumanGate 卡：隔离实例浏览器验收。

与 `e2e/test_conversation_task.py` 的区别：那个文件由 conftest 自己起一个随机端口
的后端（没有 `-agent-config`，跑不了真实 Agent）。这个文件打的是
`scripts/mindfs-iso.sh start` 起的实例（默认 `http://127.0.0.1:7431/mindfs`），
它指向工作区源码与 `web/dist`，并带 `agents.json`，所以能跑真实 Claude 回合。

跑法：

    bash scripts/mindfs-iso.sh start
    .venv/bin/python -m pytest e2e/test_task_prompt_cards_iso.py -v -p no:cacheprovider

真实 Agent 用例会继承隔离实例的 Claude 默认模型。若那个模型在当前代理上
限流（表现为 `agent_reply` 直接 failed、`body` 带 429），用例会立即失败并打出
该错误正文；换一个可用模型后再跑（隔离实例的 preferences `agents.claude.model`）。

覆盖：

- HumanGate：卡片出现 → 普通输入被挡 → 「停止」暂停任务 → 「恢复」→ 批准后卡片收口；
- HumanGate 驳回：卡片收口为拒绝、任务 cancelled；
- 真实 Agent 提问：Claude 调 AskUserQuestion → 任务对话出现提问卡 → 输入被挡 →
  在任务卡里作答 → 卡片 completed、Agent 继续并把答案写进最终回复。

数据准备全部走隔离实例的 HTTP API（`?user=<隔离账户 id>`），不碰线上 7331。
"""

from __future__ import annotations

import json
import os
import time
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Callable

import pytest
from playwright.sync_api import Page, sync_playwright

ISO_URL = os.environ.get("MINDFS_ISO_URL", "http://127.0.0.1:7431/mindfs").rstrip("/")
ISO_ACCOUNT = Path(os.environ.get("MINDFS_ISO_ACCOUNT", "/tmp/mindfs-iso/account"))
ISO_PROJECT = Path(os.environ.get("MINDFS_ISO_PROJECT", "/tmp/mindfs-iso/proj"))
# 真实 Agent 的回合包含冷启动与模型思考；给足时间，但不要无限等。
AGENT_TIMEOUT = float(os.environ.get("MINDFS_ISO_AGENT_TIMEOUT", "240"))

VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
WORKSPACE = '[data-onboarding="workspace"]'
PANEL_DIALOG = ".mindfs-panel-dialog"
PANEL_OVERLAY = ".mindfs-panel-overlay"
TASK_REFRESH = '[data-onboarding="task-refresh"]'

CONVERSATION = '[data-testid="conversation-view"]'
ACTIVITY_INPUT = '[data-testid="activity-input"]'
PROMPT_PENDING = '[data-testid="conversation-prompt-pending"]'
QUESTION_CARD = '[data-testid="task-question-card"]'
GATE_CARD = '[data-testid="task-human-gate"]'
GATE_APPROVE = '[data-testid="task-human-gate-approve"]'
GATE_REJECT = '[data-testid="task-human-gate-reject"]'
GATE_STOP = '[data-testid="task-human-gate-stop"]'
GATE_RESUME = '[data-testid="task-human-gate-resume"]'


# ── HTTP ────────────────────────────────────────────────────────────────────


def _request(method: str, path: str, body: dict[str, Any] | None = None) -> Any:
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(
        f"{ISO_URL}{path}",
        data=data,
        headers={"Content-Type": "application/json"},
        method=method,
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        raw = resp.read().decode()
    return json.loads(raw) if raw else {}


def _api(path: str, user_query: str) -> str:
    """把 `user=<隔离账户>` 拼到路径上（路径已有 query 时用 `&`）。"""
    return path + ("&" if "?" in path else "?") + user_query


def _poll(fn: Callable[[], Any], timeout: float, what: str, interval: float = 0.5) -> Any:
    deadline = time.time() + timeout
    last: Any = None
    while time.time() < deadline:
        last = fn()
        if last:
            return last
        time.sleep(interval)
    raise AssertionError(f"等待 {what} 超时（{timeout:.0f}s），最后一次结果：{last!r}")


# ── fixtures ────────────────────────────────────────────────────────────────


@pytest.fixture(scope="session")
def iso() -> dict[str, str]:
    """隔离实例的地址、账户与项目；实例没起就直接失败，而不是静默跳过。"""
    try:
        _request("GET", "/health")
    except Exception as exc:  # noqa: BLE001 - 没起实例是一种明确的前置失败
        pytest.fail(
            f"隔离实例不可达：{ISO_URL}（{exc}）。先跑 `bash scripts/mindfs-iso.sh start`。"
        )
    if not ISO_ACCOUNT.is_file():
        pytest.fail(f"找不到隔离账户文件：{ISO_ACCOUNT}（先跑 mindfs-iso.sh start）")
    lines = ISO_ACCOUNT.read_text(encoding="utf-8").splitlines()
    if len(lines) < 3:
        pytest.fail(f"隔离账户文件格式不完整：{ISO_ACCOUNT}")
    user, password, uid = (line.strip() for line in lines[:3])
    user_query = f"user={urllib.parse.quote(uid)}"
    dirs = _request("GET", _api("/api/dirs", user_query))
    project = next(
        (
            d
            for d in dirs
            if str(d.get("root_path") or d.get("path") or "").rstrip("/") == str(ISO_PROJECT)
        ),
        None,
    )
    if not project or not project.get("id"):
        pytest.fail(f"隔离项目未注册：{ISO_PROJECT}（dirs={dirs}）")
    return {
        "user": user,
        "password": password,
        "uid": uid,
        "user_query": user_query,
        "root": str(project["id"]),
    }


@pytest.fixture(scope="session")
def page(iso: dict[str, str]):
    """登录隔离实例的浏览器页；登录走真实表单，与用户看到的一致。"""
    with sync_playwright() as p:
        browser = p.chromium.launch()
        context = browser.new_context(viewport={"width": 1440, "height": 900})
        page = context.new_page()
        _login(page, iso)
        try:
            yield page
        finally:
            browser.close()


# ── 浏览器导航 ──────────────────────────────────────────────────────────────


def _login(page: Page, iso: dict[str, str]) -> None:
    page.goto(f"{ISO_URL}/", wait_until="domcontentloaded")
    page.wait_for_selector('input[autocomplete="username"]', timeout=60_000)
    page.fill('input[autocomplete="username"]', iso["user"])
    page.fill('input[autocomplete="current-password"]', iso["password"])
    page.press('input[autocomplete="current-password"]', "Enter")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    try:
        page.click('button:has-text("Skip tour")', timeout=4_000)
        page.wait_for_selector('button:has-text("Skip tour")', state="detached", timeout=5_000)
    except Exception:  # noqa: BLE001 - 引导浮层只在首次登录出现
        pass


def _close_panel(page: Page) -> None:
    overlay = page.locator(PANEL_OVERLAY)
    if overlay.count() == 0:
        return
    page.mouse.click(4, 4)
    overlay.first.wait_for(state="detached", timeout=5_000)


def _open_task(page: Page, name: str) -> None:
    _close_panel(page)
    page.locator(VIEW_SWITCHER).first.get_by_role("tab", name="Board", exact=True).click()
    page.wait_for_selector(WORKSPACE, timeout=20_000)
    page.click(TASK_REFRESH)
    card = page.get_by_text(name, exact=True).first
    card.wait_for(state="visible", timeout=20_000)
    card.click()
    page.wait_for_selector(PANEL_DIALOG, timeout=10_000)
    page.wait_for_selector(CONVERSATION, timeout=20_000)


# ── 数据准备 ────────────────────────────────────────────────────────────────


def _publish_gate_workflow(iso: dict[str, str], suffix: str) -> str:
    workflow_id = f"e2e-gate-{suffix}"
    _request(
        "POST",
        _api("/api/workflows", iso["user_query"]),
        {
            "workflow_id": workflow_id,
            "author": "e2e",
            "graph": {
                "nodes": [
                    {
                        "id": "gate",
                        "name": "gate",
                        "kind": "human_gate",
                        "params": {"prompt": f"确认可以继续吗？({suffix})"},
                    }
                ]
            },
        },
    )
    published = _request(
        "POST",
        _api(f"/api/workflows/{workflow_id}/publish", iso["user_query"]),
        {"confirmed_revision": "r1", "published_by": "e2e"},
    )
    version = ((published.get("version") or {}).get("version") or "").strip()
    assert version, f"发布 gate 工作流失败：{published}"
    return version


def _create_task(iso: dict[str, str], name: str) -> dict[str, Any]:
    detail = _request(
        "POST",
        _api("/api/tasks", iso["user_query"]),
        {"root_id": iso["root"], "input": "", "name": name},
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    return task


def _start_gate_run(iso: dict[str, str], task_id: str, version: str) -> None:
    _request(
        "POST",
        _api(f"/api/tasks/{task_id}/activities", iso["user_query"]),
        {
            "root_id": iso["root"],
            "body": "开始人工确认",
            "workflow_choice": "specified",
            "workflow_version_id": version,
        },
    )


def _activities(iso: dict[str, str], task_id: str) -> list[dict[str, Any]]:
    payload = _request(
        "GET",
        _api(f"/api/tasks/{task_id}/activities?root_id={iso['root']}", iso["user_query"]),
    )
    return payload.get("activities") or []


def _find_activity(
    iso: dict[str, str], task_id: str, **match: Any
) -> dict[str, Any] | None:
    for activity in _activities(iso, task_id):
        if all(activity.get(key) == value for key, value in match.items()):
            return activity
    return None


def _wait_gate(iso: dict[str, str], task_id: str, status: str) -> dict[str, Any]:
    found = _poll(
        lambda: _find_activity(iso, task_id, type="human_gate", status=status),
        30,
        f"human_gate status={status}",
    )
    return found


def _answered_question(
    iso: dict[str, str], task_id: str, activity_id: str
) -> dict[str, Any] | None:
    item = _find_activity(iso, task_id, id=activity_id, status="completed")
    if not item:
        return None
    answers = json.loads(item.get("payload_json") or "{}").get("answers")
    return item if answers else None


def _wait_question(iso: dict[str, str], task_id: str, timeout: float) -> dict[str, Any]:
    """等真实 Agent 的提问卡；回合失败（限流/鉴权/崩溃）时立刻带着错误正文失败。

    等满 timeout 只会报「没有提问卡」，把真正原因（例如 429）藏起来。
    """
    deadline = time.time() + timeout
    while time.time() < deadline:
        failure = _find_activity(iso, task_id, type="agent_reply", status="failed")
        if failure:
            raise AssertionError(
                f"真实 Agent 回合失败，不会再有提问卡：{failure.get('body')}"
            )
        question = _find_activity(iso, task_id, type="question", status="waiting_user")
        if question:
            return question
        time.sleep(1.0)
    raise AssertionError(f"等待真实 Agent 发出 AskUserQuestion 超时（{timeout:.0f}s）")


def _task_status(iso: dict[str, str], task_id: str) -> str:
    payload = _request(
        "GET", _api(f"/api/tasks?root={iso['root']}", iso["user_query"])
    )
    for item in payload.get("items") or []:
        task = item.get("task") or {}
        if task.get("id") == task_id:
            return str(task.get("status") or "")
    return ""


# ── 用例 ────────────────────────────────────────────────────────────────────


def test_human_gate_blocks_composer_pause_resume_and_approve(
    page: Page, iso: dict[str, str]
) -> None:
    """Gate 卡挡住普通输入；停止=暂停、恢复后仍可审批；批准把卡收口。"""
    suffix = str(int(time.time()))
    name = f"HumanGate 通过 E2E {suffix}"
    version = _publish_gate_workflow(iso, suffix)
    task = _create_task(iso, name)
    _start_gate_run(iso, task["id"], version)
    _wait_gate(iso, task["id"], "waiting_user")

    _open_task(page, name)

    gate = page.locator(GATE_CARD).first
    gate.wait_for(state="visible", timeout=20_000)
    assert f"({suffix})" in gate.inner_text(), "Gate 卡没有渲染出节点 prompt"
    # 普通留言不能代替审批（D4）：输入框整体换成提示。
    page.wait_for_selector(PROMPT_PENDING, timeout=10_000)
    assert page.locator(ACTIVITY_INPUT).count() == 0

    # 停止 = 暂停当前任务；恢复前不再给审批按钮。
    page.click(GATE_STOP)
    page.wait_for_selector(GATE_RESUME, timeout=20_000)
    assert page.locator(GATE_APPROVE).count() == 0
    assert _poll(lambda: _task_status(iso, task["id"]) == "paused", 15, "task paused")

    page.click(GATE_RESUME)
    page.wait_for_selector(GATE_APPROVE, timeout=20_000)
    assert page.locator(GATE_RESUME).count() == 0

    page.click(GATE_APPROVE)
    approved = _poll(
        lambda: (
            _find_activity(iso, task["id"], type="human_gate", status="completed")
        ),
        30,
        "human_gate completed",
    )
    payload = json.loads(approved.get("payload_json") or "{}")
    assert payload.get("decision") == "approve", f"审批结果没有写回 payload：{approved}"
    # 任务头也必须从「待开始/等待你」自动变成完成；回复状态与任务头是两条
    # 独立的推送，任一丢失都会出现「已批准但头部还停在等待」。
    status = page.locator('[data-testid="task-detail-status"]')
    assert _poll(
        lambda: status.inner_text().strip() in {"已完成", "Completed"},
        20,
        "task header completed",
    )
    # 卡收口后不再挡输入框。
    page.wait_for_selector(PROMPT_PENDING, state="detached", timeout=20_000)

    _close_panel(page)


def test_human_gate_reject_closes_card_and_task(page: Page, iso: dict[str, str]) -> None:
    """驳回走专用端点：卡片收口为 cancelled，任务终态 cancelled。"""
    suffix = str(int(time.time())) + "r"
    name = f"HumanGate 驳回 E2E {suffix}"
    version = _publish_gate_workflow(iso, suffix)
    task = _create_task(iso, name)
    _start_gate_run(iso, task["id"], version)
    _wait_gate(iso, task["id"], "waiting_user")

    _open_task(page, name)
    page.wait_for_selector(GATE_CARD, timeout=20_000)
    page.click(GATE_REJECT)

    rejected = _poll(
        lambda: _find_activity(iso, task["id"], type="human_gate", status="cancelled"),
        30,
        "human_gate cancelled",
    )
    payload = json.loads(rejected.get("payload_json") or "{}")
    assert payload.get("decision") == "reject", f"驳回结果没有写回 payload：{rejected}"
    assert _poll(lambda: _task_status(iso, task["id"]) == "cancelled", 20, "task cancelled")
    # 已决定就没有审批按钮了。
    page.wait_for_selector(GATE_APPROVE, state="detached", timeout=20_000)
    page.wait_for_selector(PROMPT_PENDING, state="detached", timeout=20_000)

    _close_panel(page)


def test_real_agent_question_card_answer_round_trip(
    page: Page, iso: dict[str, str]
) -> None:
    """真实 Claude 提问 → 任务对话出卡 → 在卡里作答 → 答案真的回到 Agent 回复。

    这一条同时钉住三件事：AskUserQuestion 的会话事件能落成任务活动；等待期间
    普通输入被挡；任务卡提交走的是会话答题链路（不是伪造一个本地 completed）。
    """
    suffix = str(int(time.time())) + "q"
    name = f"提问卡 E2E {suffix}"
    task = _create_task(iso, name)

    _open_task(page, name)
    # 直连回合：不建 WorkflowRun，由真实 Claude 回答。三态选择器默认折叠，
    # 先展开高级选项再选「纯对话」。
    page.locator(f'{ACTIVITY_INPUT} [data-testid="workflow-show-advanced"]').click()
    page.locator(f'{ACTIVITY_INPUT} [data-testid="workflow-choice-none"]').click()
    page.fill(f"{ACTIVITY_INPUT} [contenteditable='true']", (
        "Use the AskUserQuestion tool exactly once to ask me which color I prefer, "
        "with two options: 'crimson' and 'azure'. After I answer, reply with exactly the "
        "chosen label and nothing else. Do not use any other tool."
    ))
    page.click('[data-testid="activity-send"]')

    # 卡片由会话流的 tool_call 事件创建；先按 API 等它落库，再重开面板强制走 REST，
    # 这样即使广播丢失也能稳定渲染（也顺带验证 root_id 让重载后仍归位本任务）。
    question = _wait_question(iso, task["id"], AGENT_TIMEOUT)
    _close_panel(page)
    _open_task(page, name)

    card = page.locator(QUESTION_CARD).first
    card.wait_for(state="visible", timeout=20_000)
    assert page.locator(PROMPT_PENDING).count() == 1
    assert page.locator(ACTIVITY_INPUT).count() == 0, "等待回答时普通输入必须被挡住"

    # 选第一个选项（按 live 用例的 prompt 是 crimson），再点卡片里的提交按钮。
    card.locator('input[type="radio"]').first.check()
    card.locator("button").last.click()

    answered = _poll(
        lambda: _answered_question(iso, task["id"], question["id"]),
        60,
        "提问卡收到答案",
    )
    answers = json.loads(answered.get("payload_json") or "{}").get("answers") or {}
    assert "crimson" in json.dumps(answers).lower(), f"答案没有写回任务卡：{answered}"

    # 提交成功后卡片退出等待态，输入框恢复。
    page.wait_for_selector(PROMPT_PENDING, state="detached", timeout=20_000)
    page.wait_for_selector(ACTIVITY_INPUT, timeout=20_000)

    # 答案真的到了 Agent：最终回复只包含所选标签。
    reply = _poll(
        lambda: next(
            (
                item
                for item in _activities(iso, task["id"])
                if item.get("type") == "agent_reply"
                and item.get("status") == "completed"
                and "crimson" in str(item.get("body", "")).lower()
            ),
            None,
        ),
        AGENT_TIMEOUT,
        "Agent 把所选答案写进最终回复",
        interval=1.0,
    )
    assert "crimson" in str(reply.get("body", "")).lower()

    _close_panel(page)
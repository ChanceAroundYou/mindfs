"""对话式任务系统端到端测试（Playwright + pytest）。

跑法（与其它 e2e 用例一致，刻意不进常规门禁）：

    .venv/bin/python -m pytest e2e/test_conversation_task.py -v -p no:cacheprovider

覆盖范围：

- ``test_conversation_task_full_flow``：打开对话式任务 → 选择「禁用 workflow」→
  发送留言 → 用户留言与 Agent 回复都出现在对话流里 → 状态徽标合法。
- ``test_legacy_task_shows_summary``：历史任务显示 Agent 总结卡；
  阶段列表默认收起，展开后每段都能看到。
- ``test_empty_message_blocked``：文本框为空时发送键禁用，有内容后启用。

数据准备说明
------------
复用 ``conftest.py`` 的隔离后端（临时 ``XDG_CONFIG_HOME``、随机端口、``e2e_test``
账户），不碰本机 7331 的真实服务。

任务通过 REST API 创建。Agent 回复统一用 ``workflow_choice=disabled``：
不创建 WorkflowRun、不依赖外部 workflow 或真实 Agent，回复停在 ``pending``。
**查看执行详情**按钮只在存在 ``run_id`` 时出现，所以这里做条件断言。

对话式新任务在数据模型里的判据是 ``task_stages_json`` 为空（见
``web/src/components/task/TaskDetailPanel.tsx`` 的 ``hasLegacyStages``）。当前创建
API 仍要求随包带 ``stages``，因此本文件在创建后把隔离库里该任务的行清成 ``[]``
来模拟新模型任务 —— 这是**测试夹具**，不修改任何产品代码。
"""

from __future__ import annotations

import json
import sqlite3
import time
import urllib.parse
import urllib.request
from pathlib import Path

import pytest
from playwright.sync_api import Page

BOARD_TAB = "Board"
PANEL_DIALOG = ".mindfs-panel-dialog"
PANEL_OVERLAY = ".mindfs-panel-overlay"

DEFAULT_STAGES = [
    {
        "name": "准备",
        "role": "user",
        "auto_advance": True,
        "prompt_template": "",
    },
    {
        "name": "执行",
        "role": "agent",
        "auto_advance": False,
        "agent": "claude",
        "model": "sonnet",
        "prompt_template": "{previous_input}",
    },
]


# ── HTTP / DB 夹具辅助 ──────────────────────────────────────────────────────


def _post_json(url: str, payload: dict) -> dict:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read().decode() or "{}")


def _create_task(api_url: str, user_query: str, root_id: str, name: str, input_text: str) -> dict:
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": input_text,
            "name": name,
            "create_worktree": False,
            "stages": DEFAULT_STAGES,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    return task


def _task_db_path(config_dir: Path, root_id: str) -> Path:
    """定位隔离实例里该项目的任务库（账户私有 meta 或项目 .mindfs 两种落点都兼容）。"""
    deadline = time.time() + 10
    while time.time() < deadline:
        candidates = [
            path
            for path in config_dir.rglob("task-kanban.db")
            if path.parent.name == "tasks" and path.parent.parent.name == root_id
        ]
        if candidates:
            return candidates[0]
        time.sleep(0.1)
    raise AssertionError(f"找不到 root={root_id} 的任务库（config_dir={config_dir}）")


def _mark_task_as_conversation(config_dir: Path, root_id: str, task_id: str) -> None:
    """把任务流水快照清空，模拟「对话式新任务」（task_stages_json 为空）。"""
    db_path = _task_db_path(config_dir, root_id)
    con = sqlite3.connect(str(db_path), timeout=15)
    try:
        con.execute("UPDATE tasks SET task_stages_json = '[]' WHERE id = ?", (task_id,))
        con.commit()
        updated = con.execute(
            "SELECT task_stages_json FROM tasks WHERE id = ?", (task_id,)
        ).fetchone()
    finally:
        con.close()
    assert updated and updated[0] == "[]", f"清空任务流水失败：{updated}"


# ── 浏览器导航辅助 ─────────────────────────────────────────────────────────


def _show_board(page: Page) -> None:
    tab = (
        page.locator('[data-onboarding="main-view-switcher"]')
        .first.get_by_role("tab", name=BOARD_TAB, exact=True)
    )
    tab.click()
    page.wait_for_selector('[data-onboarding="workspace"]', timeout=20_000)


def _open_task(page: Page, task_name: str) -> None:
    """切换到看板、刷新数据，然后点开同名任务卡。"""
    _show_board(page)
    page.click('[data-onboarding="task-refresh"]')
    card = page.get_by_text(task_name, exact=True).first
    card.wait_for(state="visible", timeout=20_000)
    card.click()
    page.wait_for_selector(PANEL_DIALOG, timeout=10_000)


def _close_task_panel(page: Page) -> None:
    """点面板外的遮罩关闭（PanelShell 只支持遮罩/关闭确认，没有 × 键）。"""
    overlay = page.locator(PANEL_OVERLAY)
    if overlay.count() == 0:
        return
    page.mouse.click(4, 4)
    overlay.first.wait_for(state="detached", timeout=5_000)


# ── 测试夹具 ───────────────────────────────────────────────────────────────


@pytest.fixture(scope="module")
def conversation_task(api_url: str, user_query: str, project: dict, config_dir: Path) -> dict:
    root_id = str(project["id"])
    task = _create_task(
        api_url,
        user_query,
        root_id,
        name="对话式任务 E2E",
        input_text="请实现用户登录功能",
    )
    _mark_task_as_conversation(config_dir, root_id, task["id"])
    return {"id": task["id"], "root_id": root_id, "name": "对话式任务 E2E"}


@pytest.fixture(scope="module")
def legacy_task(api_url: str, user_query: str, project: dict) -> dict:
    root_id = str(project["id"])
    task = _create_task(
        api_url,
        user_query,
        root_id,
        name="历史任务 E2E",
        input_text="旧版阶段式任务的输入",
    )
    return {"id": task["id"], "root_id": root_id, "name": "历史任务 E2E"}


# ── 用例 ───────────────────────────────────────────────────────────────────


def test_conversation_task_full_flow(authed_page: Page, conversation_task: dict) -> None:
    """完整对话流程：发留言 → 用户消息 → Agent 回复 → 状态徽标。"""
    page = authed_page
    _open_task(page, conversation_task["name"])

    # 1. 对话式视图打开（不是历史阶段视图）。
    page.wait_for_selector('[data-testid="conversation-view"]', timeout=10_000)
    assert page.locator('[data-testid="activity-input"]').count() == 1
    assert page.locator('[data-testid="legacy-summary-reply"]').count() == 0

    # 2. 选择「禁用 workflow」：Agent 回复停在 pending，不依赖外部 workflow。
    page.locator('[data-testid="activity-input"] [data-choice="disabled"]').click()

    # 3. 发送留言。
    message = "先实现基本的表单"
    page.fill('[data-testid="activity-input"] textarea', message)
    page.click('[data-testid="activity-send"]')

    # 4. 用户留言出现。
    user_msg = page.wait_for_selector(
        f'[data-testid="activity-message"][data-role="user"]:has-text("{message}")',
        timeout=10_000,
    )
    assert message in user_msg.inner_text()

    # 5. Agent 回复出现，状态徽标只允许合法值。
    agent_msg = page.wait_for_selector(
        '[data-testid="activity-message"][data-role="agent"]', timeout=10_000
    )
    status = agent_msg.get_attribute("data-status")
    assert status in {"pending", "running", "completed"}, f"非法状态徽标：{status}"
    assert page.locator('[data-testid="activity-status"]').count() >= 1

    # 6. 只有存在 workflow_run（run_id）时才出现「查看执行详情」。
    view_run = page.locator('[data-testid="activity-view-run"]')
    if view_run.count() > 0:
        assert view_run.first.is_visible()

    _close_task_panel(page)


def test_legacy_task_shows_summary(authed_page: Page, legacy_task: dict) -> None:
    """历史任务：总结回复卡 + 默认收起的可折叠阶段列表。"""
    page = authed_page
    _open_task(page, legacy_task["name"])

    # 1. 历史任务投影成一条 Agent 总结回复。
    page.wait_for_selector('[data-testid="legacy-summary-reply"]', timeout=10_000)
    agent_messages = page.locator('[data-testid="activity-message"][data-role="agent"]')
    assert agent_messages.count() == 1

    # 2. 阶段折叠区存在，默认收起。
    collapsible = page.locator('[data-testid="legacy-stages-collapsible"]').first
    assert collapsible.count() == 1
    assert collapsible.get_attribute("data-open") is None
    assert page.locator('[data-testid="legacy-stage-row"]').count() == 0

    # 3. 展开后每个阶段都渲染出来。
    collapsible.locator("button").first.click()
    page.wait_for_selector('[data-testid="legacy-stage-row"]', timeout=5_000)
    assert page.locator('[data-testid="legacy-stage-row"]').count() == len(DEFAULT_STAGES)
    assert collapsible.get_attribute("data-open") == "true"

    _close_task_panel(page)


def test_empty_message_blocked(authed_page: Page, conversation_task: dict) -> None:
    """空留言不能发送：发送键随文本框内容启用/禁用。"""
    page = authed_page
    _open_task(page, conversation_task["name"])
    page.wait_for_selector('[data-testid="conversation-view"]', timeout=10_000)

    send = page.locator('[data-testid="activity-send"]')
    textarea = page.locator('[data-testid="activity-input"] textarea')

    # 空文本框 → 禁用
    assert send.is_disabled()
    assert textarea.input_value() == ""

    # 有内容 → 启用
    page.fill('[data-testid="activity-input"] textarea', "test")
    assert not send.is_disabled()

    # 清空 → 再次禁用
    page.fill('[data-testid="activity-input"] textarea', "")
    assert send.is_disabled()

    _close_task_panel(page)
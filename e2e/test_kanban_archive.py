"""看板归档（任务 #86）的浏览器端到端验收（Playwright + pytest）。

跑法（与其它 e2e 用例一致，刻意不进常规门禁）：

    .venv/bin/python -m pytest e2e/test_kanban_archive.py -v -p no:cacheprovider

验的是**界面真的能做这几件事**，而不是后端单测已经覆盖过的存储语义：

1. 终态任务卡片上有「归档」键，就在删除键旁边（同一个操作行）；
2. 点归档后卡片落到「已结束 → 归档」子组，操作键变成「取消归档 + 删除」；
3. 归档后工作台上看不到这张卡（后端 Overview 过滤 + 前端重拉结构）；
4. 点「取消归档」卡片回到「完成」子组，归档组计数归零。

数据准备：全部走隔离后端（conftest 的临时 ``XDG_CONFIG_HOME`` + 随机端口 +
``e2e_test`` 账户），任务用 REST API 建、用 ``/complete`` 收尾 —— 不写库、
不依赖真实 Agent，也不碰本机 7331。

**登录后先 reload 一次**：隔离实例的首屏有个与本功能无关的启动竞态 ——
``protectedAPIReady()`` 在登录后的首轮渲染里还是 false，看板/工作台的加载
effect 被静默挡掉且不再重试（实测首屏看板四列全 0、工作台「0 projects」，
``/api/tasks?root=`` 一次都没发；刷新页面后一切正常）。这里显式 reload 绕开它，
让用例只对归档行为负责；竞态本身另行上报，不在本用例里「顺手修」。
"""

from __future__ import annotations

import json
import re
import time
import urllib.request

import pytest
from playwright.sync_api import Page, TimeoutError as PWTimeout

BOARD_TAB = "Board"
WORKSPACE_TAB = "Workspace"
ARCHIVE_BUTTON = 'button[aria-label^="Archive task"]'
UNARCHIVE_BUTTON = 'button[aria-label^="Unarchive task"]'
DELETE_BUTTON = 'button[aria-label^="Delete task"]'
BOARD_REFRESH = '[data-onboarding="task-refresh"]'
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
WORKSPACE = '[data-onboarding="workspace"]'

TASK_NAME = "归档验收任务 E2E"


# ── HTTP 夹具辅助 ──────────────────────────────────────────────────────────


def _post_json(url: str, payload: dict) -> dict:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read().decode() or "{}")


def _create_finished_task(api_url: str, user_query: str, root_id: str) -> dict:
    """建一张任务卡并立刻收尾 —— 归档只对终态任务开放。"""
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": "这条任务是归档验收夹具，不需要真的执行。",
            "name": TASK_NAME,
            "create_worktree": False,
            "start_immediately": False,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    _post_json(f"{api_url}/api/tasks/{task['id']}/complete?{user_query}", {"root_id": root_id})
    return task


# ── 浏览器导航辅助 ─────────────────────────────────────────────────────────


def _reload_for_loaded_board(page: Page) -> None:
    """首屏竞态兜底：reload 一次，让看板/工作台的加载真的发出去（见模块 docstring）。"""
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    try:
        page.click('button:has-text("Skip tour")', timeout=3_000)
    except PWTimeout:
        pass


def _switch_view(page: Page, tab_name: str) -> None:
    (
        page.locator(VIEW_SWITCHER)
        .first.get_by_role("tab", name=tab_name, exact=True)
        .click()
    )
    page.wait_for_selector(WORKSPACE, timeout=20_000)


def _show_board(page: Page) -> None:
    _switch_view(page, BOARD_TAB)
    page.click(BOARD_REFRESH)


def _task_card(page: Page):
    """按名字定位卡片（看板与工作台都用 article 渲染任务卡）。"""
    return page.locator("article", has=page.get_by_text(TASK_NAME, exact=True)).first


def _group_count(page: Page, group_name: str) -> int:
    """读「已结束」列里某个子组的计数（子组头是 `<名字>\\n<数量>` 的按钮）。"""
    header = page.get_by_role("button", name=re.compile(rf"^{group_name} \d+$")).first
    header.wait_for(state="visible", timeout=20_000)
    text = header.inner_text().strip()
    match = re.search(r"(\d+)\s*$", text)
    assert match, f"子组头文本不含数字：{text!r}"
    return int(match.group(1))


# ── 夹具 ───────────────────────────────────────────────────────────────────


@pytest.fixture(scope="module")
def finished_task(api_url: str, user_query: str, project: dict) -> dict:
    root_id = str(project["id"])
    task = _create_finished_task(api_url, user_query, root_id)
    return {"id": task["id"], "root_id": root_id}


# ── 用例 ───────────────────────────────────────────────────────────────────


def test_archive_button_moves_task_and_hides_it_from_workspace(
    authed_page: Page, finished_task: dict
) -> None:
    page = authed_page
    _reload_for_loaded_board(page)

    # 1. 归档前：工作台看得到这张已结束的卡（否则「归档后看不到」没有意义）。
    _switch_view(page, WORKSPACE_TAB)
    page.get_by_text(TASK_NAME, exact=True).first.wait_for(state="visible", timeout=20_000)

    # 2. 看板上卡片有归档键，且与删除键同一操作行。
    _show_board(page)
    card = _task_card(page)
    card.wait_for(state="visible", timeout=20_000)
    archive_button = card.locator(ARCHIVE_BUTTON)
    archive_button.wait_for(state="visible", timeout=10_000)
    assert card.locator(DELETE_BUTTON).count() == 1, "归档键应与删除键并排在同一行"

    # 3. 点归档：卡片进入「已结束 → 归档」子组，操作键变成「取消归档 + 删除」。
    archive_button.click()
    archived_card = _task_card(page)
    archived_card.locator(UNARCHIVE_BUTTON).wait_for(state="visible", timeout=20_000)
    assert archived_card.locator(ARCHIVE_BUTTON).count() == 0, "已归档的卡片不该再出现归档键"
    assert _group_count(page, "Archived") == 1, "归档子组计数应为 1"
    assert _group_count(page, "Done") == 0, "归档后应离开「完成」子组"

    # 4. 工作台不再显示 —— 服务端 Overview 过滤，卡片不靠前端藏。
    _switch_view(page, WORKSPACE_TAB)
    time.sleep(1.5)  # 等工作台重拉一轮（refreshWorkspaceBoard 是异步的）
    assert page.get_by_text(TASK_NAME, exact=True).count() == 0, "归档任务仍出现在工作台"

    # 5. 取消归档：回到「完成」子组，归档组清零。
    _show_board(page)
    card = _task_card(page)
    card.locator(UNARCHIVE_BUTTON).click()
    restored = _task_card(page)
    restored.locator(ARCHIVE_BUTTON).wait_for(state="visible", timeout=20_000)
    assert _group_count(page, "Archived") == 0, "取消归档后归档子组应为空"
    assert _group_count(page, "Done") == 1, "取消归档后应回到「完成」子组"

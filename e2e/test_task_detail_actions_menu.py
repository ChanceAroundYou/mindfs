"""任务详情 header「操作控件」菜单的浏览器端到端验收（Playwright + pytest）。

跑法（与其它 e2e 用例一致，刻意不进常规门禁）：

    .venv/bin/python -m pytest e2e/test_task_detail_actions_menu.py -v -p no:cacheprovider

验的是用户报的那件事：**卡片上有几个动作，菜单里就有几个**，且菜单项只有图标、
竖排一列。2026-11-22 之前详情菜单手写 finish/complete/reopen 三段互斥条件，
任何时刻都只剩一个键；现在两边都读 `taskCardActions()`。

隔离：XDG_CONFIG_HOME 指向 pytest 临时目录，任务库在临时项目目录的
`.mindfs/...` 下；running / paused / worktree 三档直接改隔离库的行（没有真跑 agent）。
"""

from __future__ import annotations

import json
import re
import sqlite3
import urllib.request
from pathlib import Path

import pytest
from playwright.sync_api import Page

BOARD_TAB = "Board"
BOARD_REFRESH = '[data-onboarding="task-refresh"]'
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
WORKSPACE = '[data-onboarding="workspace"]'
PILLS = ".task-detail-execution"
MENU_TRIGGER = '.mindfs-panel-dialog button[aria-haspopup="menu"]'
MENU_ITEM = '[role="menu"] [role="menuitem"]'

# 与 zh-CN 无关：浏览器默认 en-US，菜单项文案取 en-US-tasks.ts。
LABELS = {
    "finish-worktree": "Finish worktree",
    "pause": "Pause task",
    "resume": "Resume task",
    "complete": "Complete task",
    "cancel": "Cancel task",
    "reopen": "Return to review and keep talking",
    "archive-task": "Archive task (moves it to Ended → Archived)",
    "unarchive-task": "Unarchive task (back to Ended)",
}


def _post_json(url: str, payload: dict) -> dict:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read().decode() or "{}")


def _create_task(api_url: str, user_query: str, root_id: str, name: str, **extra) -> dict:
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": f"{name}：菜单验收夹具，不需要真的执行。",
            "name": name,
            "create_worktree": False,
            "start_immediately": False,
            **extra,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    return task


def _task_db(config_dir: Path) -> Path:
    """任务库落在账户分区：<config>/mindfs/users/<uid>/meta/<root>/tasks/task-kanban.db。"""
    for candidate in config_dir.glob("**/tasks/task-kanban.db"):
        return candidate
    raise AssertionError(f"隔离任务库不存在：{config_dir}")


def _patch_row(db: Path, task_id: str, **columns) -> None:
    assignments = ", ".join(f"{key} = ?" for key in columns)
    con = sqlite3.connect(db)
    try:
        con.execute(f"UPDATE tasks SET {assignments} WHERE id = ?", (*columns.values(), task_id))
        con.commit()
    finally:
        con.close()


def _close_detail(page: Page) -> None:
    """点遮罩左侧关闭详情（PanelShell 的关闭键按 escape 不生效）。"""
    if page.locator(PILLS).count() == 0:
        return
    page.locator(".mindfs-panel-overlay").first.click(position={"x": 5, "y": 5})
    page.wait_for_selector(PILLS, state="detached", timeout=10_000)
    page.wait_for_timeout(200)


def _open_task(page: Page, name: str) -> None:
    _close_detail(page)
    page.click(BOARD_REFRESH)
    page.wait_for_timeout(600)
    card = page.locator("article", has=page.get_by_text(name, exact=True)).first
    card.wait_for(state="visible", timeout=20_000)
    card.get_by_text(name, exact=True).first.click()
    page.wait_for_selector(PILLS, timeout=20_000)
    page.wait_for_timeout(800)


def _menu_items(page: Page) -> list[dict]:
    """点开「操作控件」，返回每个菜单项的 {title, text, height, width}。"""
    trigger = page.locator(MENU_TRIGGER).first
    trigger.wait_for(state="visible", timeout=20_000)
    assert trigger.get_attribute("aria-expanded") != "true", "打开详情时菜单应默认收起"
    trigger.click()
    page.wait_for_selector(MENU_ITEM, timeout=10_000)
    page.wait_for_timeout(200)
    items = page.eval_on_selector_all(
        MENU_ITEM,
        """(nodes) => nodes.map((node) => {
          const rect = node.getBoundingClientRect();
          return {
            title: node.getAttribute('title') || '',
            aria: node.getAttribute('aria-label') || '',
            text: (node.innerText || '').trim(),
            height: rect.height,
            width: rect.width,
          };
        })""",
    )
    # 关掉菜单，避免盖住下一张卡。
    trigger.click()
    page.wait_for_timeout(150)
    return items


def _assert_menu(page: Page, expected: list[str], state: str) -> None:
    items = _menu_items(page)
    titles = [item["title"] for item in items]
    assert titles == expected, f"{state}: 菜单动作 {titles!r} != {expected!r}"
    for item in items:
        assert item["text"] == "", f"{state}: 菜单项只放图标，不该有文字：{item['text']!r}"
        assert item["aria"] == item["title"], f"{state}: 读屏文案要跟 title 一致"
        assert item["width"] <= 40 and item["height"] <= 40, f"{state}: 图标键尺寸异常 {item}"


# ── 夹具 ───────────────────────────────────────────────────────────────────


@pytest.fixture(scope="module")
def menu_tasks(api_url: str, user_query: str, project: dict, config_dir: Path) -> dict:
    root_id = str(project["id"])
    names = {
        "pending": "菜单验收 · 待执行",
        "running": "菜单验收 · 运行中",
        "paused": "菜单验收 · 已暂停",
        "success": "菜单验收 · 已完成",
        "cancelled": "菜单验收 · 已取消",
        "archived": "菜单验收 · 已归档",
        "worktree": "菜单验收 · 带 worktree",
    }
    tasks = {
        key: _create_task(api_url, user_query, root_id, name)
        for key, name in names.items()
    }
    _post_json(f"{api_url}/api/tasks/{tasks['success']['id']}/complete?{user_query}", {"root_id": root_id})
    _post_json(f"{api_url}/api/tasks/{tasks['cancelled']['id']}/cancel?{user_query}", {"root_id": root_id})
    _post_json(f"{api_url}/api/tasks/{tasks['archived']['id']}/complete?{user_query}", {"root_id": root_id})
    _post_json(
        f"{api_url}/api/tasks/{tasks['archived']['id']}/archive?{user_query}",
        {"root_id": root_id, "archived": True},
    )
    db = _task_db(config_dir)
    # 收尾键要求目录真的还在（worktree_missing 由服务端按文件系统派生）。
    (config_dir / ".worktree" / "menu-e2e").mkdir(parents=True, exist_ok=True)
    _patch_row(db, tasks["running"]["id"], status="running")
    _patch_row(db, tasks["paused"]["id"], status="paused")
    _patch_row(
        db,
        tasks["worktree"]["id"],
        status="waiting_user",
        create_worktree=1,
        worktree_path=str(config_dir / ".worktree" / "menu-e2e"),
    )
    return {"root_id": root_id, "names": names, "ids": {k: v["id"] for k, v in tasks.items()}}


# ── 用例 ───────────────────────────────────────────────────────────────────


def test_detail_menu_matches_card_actions(authed_page: Page, menu_tasks: dict) -> None:
    page = authed_page
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    try:
        page.click('button:has-text("Skip tour")', timeout=3_000)
    except Exception:  # noqa: BLE001 - 引导只出现一次
        pass
    page.locator(VIEW_SWITCHER).first.get_by_role("tab", name=BOARD_TAB, exact=True).click()
    page.wait_for_selector(BOARD_REFRESH, timeout=30_000)
    # 抽屉宽 75vw，最右边这条竖带落在遮罩上，点它收起抽屉。
    page.mouse.click(page.viewport_size["width"] - 12, 400)
    page.wait_for_timeout(400)

    names = menu_tasks["names"]
    expectations = {
        "pending": [LABELS["complete"], LABELS["cancel"]],
        "running": [LABELS["pause"], LABELS["cancel"]],
        "paused": [LABELS["resume"], LABELS["complete"], LABELS["cancel"]],
        "success": [LABELS["reopen"], LABELS["archive-task"]],
        "cancelled": [LABELS["reopen"], LABELS["archive-task"]],
        "worktree": [LABELS["finish-worktree"], LABELS["complete"], LABELS["cancel"]],
    }
    for state, expected in expectations.items():
        _open_task(page, names[state])
        _assert_menu(page, expected, state)

    # 归档卡默认收在「已结束 → 归档」子组里，展开后只剩「取消归档」。
    _close_detail(page)
    page.get_by_role("button", name=re.compile(r"^Archived \d+$")).first.click()
    page.wait_for_timeout(600)
    _open_task(page, names["archived"])
    _assert_menu(page, [LABELS["unarchive-task"]], "archived")


def test_menu_is_a_vertical_column(authed_page: Page, menu_tasks: dict) -> None:
    """竖排的几何证明：任意两项不能并排（同一行），且整体是单列。"""
    page = authed_page
    _open_task(page, menu_tasks["names"]["paused"])
    trigger = page.locator(MENU_TRIGGER).first
    trigger.click()
    page.wait_for_selector(MENU_ITEM, timeout=10_000)
    boxes = page.eval_on_selector_all(
        MENU_ITEM,
        "(nodes) => nodes.map((n) => { const r = n.getBoundingClientRect(); return {x: r.x, y: r.y, w: r.width, h: r.height}; })",
    )
    assert len(boxes) == 3, boxes
    for previous, current in zip(boxes, boxes[1:]):
        assert abs(current["x"] - previous["x"]) < 1, "菜单项必须贴同一列"
        assert current["y"] >= previous["y"] + previous["h"] - 1, "菜单项必须竖向堆叠"


"""任务详情 header 平铺动作的浏览器端到端验收（Playwright + pytest）。

跑法（与其它 e2e 用例一致，刻意不进常规门禁）：

    .venv/bin/python -m pytest e2e/test_task_detail_actions.py -v -p no:cacheprovider

验的是用户 2026-11-22 的口径：**菜单整个去掉，按状态把可用按钮平铺出来，删除也在
同一排里**。动作集合来自 `taskDetailActions()`（与看板卡片的 `taskCardActions()`
有意不同，差异见该模块文件头）：

    完成 / 取消  → 返回、归档、删除
    已归档      → 返回、取消归档、删除
    待审阅      → 完成、取消
    执行中      → 中断
    待开始      → 开始
    已暂停      → 恢复（用户清单没列，否则中断后无处可去）

隔离：XDG_CONFIG_HOME 指向 pytest 临时目录；running / paused / waiting_user /
fail 四档直接改隔离库的行（没有真跑 agent）。
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
ACTION_ROW = '[data-testid="task-detail-actions"]'

# 与 zh-CN 无关：浏览器默认 en-US，按钮文案取 en-US-tasks.ts。
LABELS = {
    "start": "Start task",
    "interrupt": "Interrupt task",
    "resume": "Resume task",
    "complete": "Complete task",
    "cancel": "Cancel task",
    "reopen": "Return to review and keep talking",
    "archive-task": "Archive task (moves it to Ended → Archived)",
    "unarchive-task": "Unarchive task (back to Ended)",
    "delete-task": "Delete task card",
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
            "input": f"{name}：按钮验收夹具，不需要真的执行。",
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


def _actions(page: Page) -> list[dict]:
    """平铺动作行里每个按钮的 {title, aria, text, 几何}。"""
    row = page.locator(ACTION_ROW).first
    row.wait_for(state="visible", timeout=20_000)
    return page.eval_on_selector_all(
        f"{ACTION_ROW} button",
        """(nodes) => nodes.map((node) => {
          const rect = node.getBoundingClientRect();
          return {
            title: node.getAttribute('title') || '',
            aria: node.getAttribute('aria-label') || '',
            text: (node.innerText || '').trim(),
            x: rect.x,
            y: rect.y,
            height: rect.height,
            width: rect.width,
          };
        })""",
    )


def _assert_actions(page: Page, expected: list[str], state: str) -> None:
    items = _actions(page)
    titles = [item["title"] for item in items]
    assert titles == expected, f"{state}: 按钮 {titles!r} != {expected!r}"
    for item in items:
        assert item["text"] == "", f"{state}: 按钮只放图标，不该有文字：{item['text']!r}"
        assert item["aria"] == item["title"], f"{state}: 读屏文案要跟 title 一致"
        assert item["width"] <= 40 and item["height"] <= 40, f"{state}: 图标键尺寸异常 {item}"
    # header 里必须彻底没有菜单触发键（面板正文的其它下拉不算）。
    assert page.locator('.mindfs-panel-dialog header button[aria-haspopup="menu"]').count() == 0, f"{state}: header 不该再有菜单触发键"


# ── 夹具 ───────────────────────────────────────────────────────────────────


@pytest.fixture(scope="module")
def action_tasks(api_url: str, user_query: str, project: dict, config_dir: Path) -> dict:
    root_id = str(project["id"])
    names = {
        "pending": "按钮验收 · 待开始",
        "waiting": "按钮验收 · 待审阅",
        "running": "按钮验收 · 执行中",
        "paused": "按钮验收 · 已暂停",
        "success": "按钮验收 · 已完成",
        "cancelled": "按钮验收 · 已取消",
        "failed": "按钮验收 · 失败",
        "archived": "按钮验收 · 已归档",
        "worktree": "按钮验收 · 带 worktree",
    }
    tasks = {key: _create_task(api_url, user_query, root_id, name) for key, name in names.items()}
    _post_json(f"{api_url}/api/tasks/{tasks['success']['id']}/complete?{user_query}", {"root_id": root_id})
    _post_json(f"{api_url}/api/tasks/{tasks['cancelled']['id']}/cancel?{user_query}", {"root_id": root_id})
    _post_json(f"{api_url}/api/tasks/{tasks['archived']['id']}/complete?{user_query}", {"root_id": root_id})
    _post_json(
        f"{api_url}/api/tasks/{tasks['archived']['id']}/archive?{user_query}",
        {"root_id": root_id, "archived": True},
    )
    db = _task_db(config_dir)
    # worktree 目录真的存在（worktree_missing 由服务端按文件系统派生）；详情口径
    # 不提供收尾键，这里用来证明 worktree 不会给待审阅任务多加按钮。
    (config_dir / ".worktree" / "actions-e2e").mkdir(parents=True, exist_ok=True)
    _patch_row(db, tasks["waiting"]["id"], status="waiting_user")
    _patch_row(db, tasks["running"]["id"], status="running")
    _patch_row(db, tasks["paused"]["id"], status="paused")
    _patch_row(db, tasks["failed"]["id"], status="fail")
    _patch_row(
        db,
        tasks["worktree"]["id"],
        status="waiting_user",
        create_worktree=1,
        worktree_path=str(config_dir / ".worktree" / "actions-e2e"),
    )
    return {"root_id": root_id, "names": names, "ids": {k: v["id"] for k, v in tasks.items()}}


# ── 用例 ───────────────────────────────────────────────────────────────────


def test_detail_actions_match_state(authed_page: Page, action_tasks: dict) -> None:
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

    names = action_tasks["names"]
    terminal = [LABELS["reopen"], LABELS["archive-task"], LABELS["delete-task"]]
    expectations = {
        "pending": [LABELS["start"]],
        "waiting": [LABELS["complete"], LABELS["cancel"]],
        "running": [LABELS["interrupt"]],
        "paused": [LABELS["resume"]],
        "success": terminal,
        "cancelled": terminal,
        "failed": terminal,
        "worktree": [LABELS["complete"], LABELS["cancel"]],
    }
    for state, expected in expectations.items():
        _open_task(page, names[state])
        _assert_actions(page, expected, state)

    # 归档卡默认收在「已结束 → 归档」子组里：返回、取消归档、删除。
    _close_detail(page)
    page.get_by_role("button", name=re.compile(r"^Archived \d+$")).first.click()
    page.wait_for_timeout(600)
    _open_task(page, names["archived"])
    _assert_actions(page, [LABELS["reopen"], LABELS["unarchive-task"], LABELS["delete-task"]], "archived")


def test_actions_are_a_horizontal_row(authed_page: Page, action_tasks: dict) -> None:
    """平铺的几何证明：三个键在同一行、从左到右排开（不是之前的竖排菜单）。"""
    page = authed_page
    _open_task(page, action_tasks["names"]["success"])
    boxes = _actions(page)
    assert len(boxes) == 3, boxes
    for previous, current in zip(boxes, boxes[1:]):
        assert abs(current["y"] - previous["y"]) < 1, "按钮必须同一行"
        assert current["x"] >= previous["x"] + previous["width"] - 1, "按钮必须横向排开"

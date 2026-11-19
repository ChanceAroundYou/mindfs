"""工作台项目头的折叠箭头：行首位置 + 点击仍能展开/收起（Playwright + pytest）。

跑法：.venv/bin/python -m pytest e2e/test_workspace_project_toggle.py -v -p no:cacheprovider

钉住四件事：箭头排在项目名之前（几何坐标，折叠前后都要成立）；热区仍是约 40×24；
点击切换折叠态且不触发「项目名跳看板」；折叠后任务卡真的消失、再点恢复。
"""

from __future__ import annotations

import json
import re
import urllib.request

import pytest
from playwright.sync_api import Page, TimeoutError as PWTimeout

WORKSPACE = '[data-onboarding="workspace"]'
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
WORKSPACE_TAB = "Workspace"
TASK_NAME = "折叠箭头位置验收 E2E"
TOGGLE = 'button[aria-label="Collapse"], button[aria-label="Expand"]'


def _post_json(url: str, payload: dict) -> dict:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read().decode() or "{}")


@pytest.fixture(scope="module")
def workspace_task(api_url: str, user_query: str, project: dict) -> dict:
    root_id = str(project["id"])
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": "折叠箭头位置验收夹具，不需要执行。",
            "name": TASK_NAME,
            "create_worktree": False,
            "start_immediately": False,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    _post_json(f"{api_url}/api/tasks/{task['id']}/complete?{user_query}", {"root_id": root_id})
    return {"id": task["id"], "root_id": root_id}


def test_toggle_sits_before_name_and_click_still_collapses(
    authed_page: Page, workspace_task: dict
) -> None:
    page = authed_page
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    try:
        page.click('button:has-text("Skip tour")', timeout=3_000)
    except PWTimeout:
        pass

    page.locator(VIEW_SWITCHER).first.get_by_role("tab", name=WORKSPACE_TAB, exact=True).click()
    page.wait_for_selector(WORKSPACE, timeout=20_000)
    card = page.get_by_text(TASK_NAME, exact=True).first
    card.wait_for(state="visible", timeout=20_000)

    section = page.locator("section").filter(has=page.locator(TOGGLE)).first
    assert section.get_by_text(TASK_NAME, exact=True).count() == 1, "任务卡应在该 section 内"
    toggle = section.locator(TOGGLE).first
    name_button = section.locator("button[title]:not([aria-label])").first
    assert toggle.get_attribute("aria-label") == "Collapse", "展开态标签应为 Collapse"

    toggle_box = toggle.bounding_box()
    name_box = name_button.bounding_box()
    assert toggle_box and name_box
    assert toggle_box["x"] < name_box["x"], f"箭头应在项目名左侧：{toggle_box} vs {name_box}"
    assert 22 <= toggle_box["width"] <= 26, f"热区宽度应收到约 24px：{toggle_box}"
    assert toggle_box["height"] >= 23, f"热区高度应保持约 24px：{toggle_box}"

    # 左侧三个元素紧凑贴左，右半边留空：计数紧挨名字，且不贴到行尾。
    section_box = section.bounding_box()
    badge = section.locator("span").filter(has_text=re.compile(r"^\d+$")).first
    badge_box = badge.bounding_box()
    assert section_box and badge_box
    assert badge_box["x"] - (name_box["x"] + name_box["width"]) < 20, "计数应紧挨项目名"
    assert badge_box["x"] + badge_box["width"] < section_box["x"] + section_box["width"] - 50, (
        f"右侧应留空，计数不应贴到行尾：badge={badge_box} section={section_box}"
    )
    assert name_box["width"] < section_box["width"] * 0.6, (
        f"项目名不应占满整行：name={name_box} section={section_box}"
    )

    url_before = page.url
    toggle.click()
    section.locator('button[aria-label="Expand"]').wait_for(state="visible", timeout=10_000)
    assert page.url == url_before, "点箭头不应触发「项目名跳看板」"

    # 折叠态：卡片消失，箭头仍在项目名左侧
    assert page.get_by_text(TASK_NAME, exact=True).count() == 0, "折叠后任务卡应隐藏"
    collapsed_toggle = section.locator('button[aria-label="Expand"]').first
    collapsed_toggle_box = collapsed_toggle.bounding_box()
    collapsed_name_box = name_button.bounding_box()
    assert collapsed_toggle_box and collapsed_name_box
    assert collapsed_toggle_box["x"] < collapsed_name_box["x"], "折叠态箭头仍应在项目名左侧"

    # 再点一次恢复展开
    collapsed_toggle.click()
    page.get_by_text(TASK_NAME, exact=True).first.wait_for(state="visible", timeout=10_000)
    assert section.locator('button[aria-label="Collapse"]').count() == 1

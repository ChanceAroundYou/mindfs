"""移动端任务详情胶囊行的真实几何验收（Playwright + pytest）。

跑法（与其它 e2e 一样，刻意不进常规门禁）：

    .venv/bin/python -m pytest e2e/test_task_detail_pills_flow.py -v -s -p no:cacheprovider

对应反馈（2026-11-21，用户第二次报同一处）：「移动端胶囊仍然是吸顶的」。
根因是 `web/src/index.css` 里 `@media (max-width:640px) { .task-detail-execution
{ order: -1 } }` 把胶囊行提到面板正文最前面 —— 它恒在正文顶端、贴着面板标题
下沿，读起来就是一条吸顶横条；长对话里它离面板落地处两万多像素，更够不着。

这里钉住新契约（两端同一顺序：对话流 → 胶囊行 → 收尾区）：
  1) 长对话：打开（落在真正的底部）时胶囊就在输入框下方可见；中间位置时胶囊
     的视口位移与 scrollTop 一一对应（随内容走）；滚到顶时胶囊在视口之外
     —— 任何位置都不许贴在正文顶端；
  2) 短对话：连续敲 8 行，胶囊始终在输入框下方、始终可见、位置不因输入长高而
     漂移，也不许贴到正文顶端。

源码契约见 web/tests/composer-bottom-pin.test.mjs、conversation-auto-scroll.test.mjs、
run-timeline-drill-in.test.ts。
"""

from __future__ import annotations

import json
import sqlite3
import urllib.request
from pathlib import Path

from playwright.sync_api import Page

BOARD_TAB = "Board"
BOARD_REFRESH = '[data-onboarding="task-refresh"]'
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
WORKSPACE = '[data-onboarding="workspace"]'
PILLS = ".task-detail-execution"
ACTIVITY_INPUT = '[data-testid="activity-input"]'
EDITOR = ".token-editor-input"

LONG_NAME = "胶囊流内布局验收任务（长对话）"
SHORT_NAME = "胶囊流内布局验收任务（短对话）"
LONG_BODY = "\n\n".join(
    [f"### 第 {i} 段\n\n" + ("这是一段用于撑高对话流的中文正文，需要足够长才能逼出滚动。" * 6) for i in range(1, 7)]
)

GEOM_JS = """
() => {
  const rect = (el) => {
    if (!el) return null;
    const b = el.getBoundingClientRect();
    return {top: b.top, bottom: b.bottom, height: b.height};
  };
  const body = document.querySelector('.mindfs-panel-body');
  return {
    body: rect(body),
    scroll: body ? {top: body.scrollTop, height: body.scrollHeight, client: body.clientHeight} : null,
    pills: rect(document.querySelector('.task-detail-execution')),
    composer: rect(document.querySelector('[data-testid="activity-input"]')),
  };
}
"""


def _post_json(url: str, payload: dict) -> dict:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read().decode() or "{}")


def _create_task(api_url: str, user_query: str, root_id: str, name: str) -> dict:
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": "胶囊流内布局验收夹具，不需要执行。",
            "name": name,
            "create_worktree": False,
            "start_immediately": False,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    return task


def _seed_activities(config_dir: Path, task_id: str, root_id: str, count: int = 14) -> None:
    """直接往隔离实例的任务库塞活动行，把对话撑到两万多像素。

    没有比这更省的路：真跑十几轮 agent 不值得，而本用例要验的是布局，不是执行。
    """
    db = next(config_dir.rglob("task-kanban.db"))
    con = sqlite3.connect(db)
    try:
        cols = [row[1] for row in con.execute("PRAGMA table_info(task_activities)")]
        for i in range(count):
            row = {
                "id": f"pills_flow_{i}",
                "task_id": task_id,
                "type": "user_message" if i % 2 == 0 else "agent_reply",
                "author": "user" if i % 2 == 0 else "router",
                "role": "user" if i % 2 == 0 else "assistant",
                "body": f"第 {i} 条：\n\n{LONG_BODY}",
                "root_id": root_id,
                "status": "completed",
                "summary": "",
                "session_key": "",
                "created_at": f"2026-11-21T10:{i:02d}:00Z",
            }
            row = {k: v for k, v in row.items() if k in cols}
            con.execute(
                f"INSERT OR REPLACE INTO task_activities ({','.join(row)}) VALUES ({','.join('?' for _ in row)})",
                list(row.values()),
            )
        con.commit()
    finally:
        con.close()


def _open_task(page: Page, name: str) -> None:
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    page.wait_for_timeout(1_500)
    page.locator('button[aria-label^="Expand"]').first.click()
    page.locator(VIEW_SWITCHER).first.get_by_role("tab", name=BOARD_TAB, exact=True).click()
    page.wait_for_selector(BOARD_REFRESH, timeout=30_000)
    # 抽屉宽 75vw，最右边这条竖带落在遮罩上，点它收起抽屉。
    page.mouse.click(page.viewport_size["width"] - 12, 400)
    page.wait_for_timeout(400)
    page.click(BOARD_REFRESH)
    page.wait_for_timeout(800)
    card = page.locator("article", has=page.get_by_text(name, exact=True)).first
    card.wait_for(state="visible", timeout=20_000)
    card.get_by_text(name, exact=True).first.click()
    page.wait_for_selector(PILLS, timeout=20_000)
    page.wait_for_timeout(1_500)


def _geom(page: Page) -> dict:
    data = page.evaluate(GEOM_JS)
    for key in ("body", "pills", "composer"):
        assert data.get(key), f"缺少元素：{key}"
    return data


def _set_scroll(page: Page, top: int | str) -> dict:
    page.evaluate(
        """(top) => {
          const body = document.querySelector('.mindfs-panel-body');
          body.scrollTop = top === 'max' ? body.scrollHeight : top;
        }""",
        top,
    )
    page.wait_for_timeout(350)
    return _geom(page)


def test_mobile_long_conversation_pills_flow_with_content(
    authed_page: Page, api_url: str, user_query: str, project: dict, config_dir: Path
) -> None:
    root_id = str(project["id"])
    task = _create_task(api_url, user_query, root_id, LONG_NAME)
    _seed_activities(config_dir, str(task["id"]), root_id)

    page = authed_page
    page.set_viewport_size({"width": 390, "height": 844})
    _open_task(page, LONG_NAME)

    at_open = _geom(page)
    scroll = at_open["scroll"]
    assert scroll["height"] > scroll["client"] + 500, f"对话没撑出滚动：{scroll}"

    # 1) 打开落在真正的底部：胶囊可见，而且排在输入框下方（流内顺序，不是顶端）。
    assert scroll["top"] > 0, f"打开时没有滚到底部：{scroll}"
    assert at_open["pills"]["top"] >= at_open["composer"]["bottom"] - 1, (
        f"胶囊不在输入框下方（被提到了前面）：{at_open}"
    )
    assert at_open["body"]["top"] - 1 <= at_open["pills"]["top"], f"胶囊在正文之上：{at_open}"
    assert at_open["pills"]["bottom"] <= at_open["body"]["bottom"] + 1, (
        f"落在底部时胶囊不可见：{at_open}"
    )

    # 2) 中间位置：胶囊随内容走 —— 视口位移与 scrollTop 一一对应（没有吸顶）。
    #    往上滚时内容下移，胶囊的 top 变大；位移量必须与 scrollTop 的差值相等。
    mid_target = 3_000
    at_mid = _set_scroll(page, mid_target)
    moved = at_mid["pills"]["top"] - at_open["pills"]["top"]
    expected = at_open["scroll"]["top"] - at_mid["scroll"]["top"]
    assert abs(moved - expected) <= 4, f"胶囊没跟着内容滚：moved={moved} expected={expected}"

    # 3) 滚到顶：胶囊在视口之外、离正文顶端还差一大截 —— 换回 `order:-1` 时
    #    它就贴在正文顶端（差 ≈ 0），这条会红。
    at_top = _set_scroll(page, 0)
    assert at_top["scroll"]["top"] == 0, f"没滚到顶：{at_top['scroll']}"
    assert at_top["pills"]["top"] > at_top["body"]["bottom"], (
        f"滚到顶后胶囊仍在视口内（吸顶）：{at_top}"
    )
    assert at_top["pills"]["top"] - at_top["body"]["top"] > 400, (
        f"胶囊离正文顶端太近，像一条吸顶横条：{at_top}"
    )


def test_mobile_typing_keeps_pills_below_composer(
    authed_page: Page, api_url: str, user_query: str, project: dict
) -> None:
    root_id = str(project["id"])
    _create_task(api_url, user_query, root_id, SHORT_NAME)

    page = authed_page
    page.set_viewport_size({"width": 390, "height": 844})
    _open_task(page, SHORT_NAME)

    first = _geom(page)
    snaps = [first]
    page.locator(f"{ACTIVITY_INPUT} {EDITOR}").first.click()
    for i in range(1, 9):
        page.keyboard.type(f"第{i}行")
        page.keyboard.press("Enter")
        page.wait_for_timeout(350)
        snaps.append(_geom(page))

    for step, snap in enumerate(snaps):
        assert snap["pills"]["top"] >= snap["composer"]["bottom"] - 1, (
            f"第 {step} 步胶囊不在输入框下方：{snap}"
        )
        assert snap["pills"]["top"] - snap["body"]["top"] > 60, (
            f"第 {step} 步胶囊贴到了正文顶端（吸顶）：{snap}"
        )
        assert snap["pills"]["bottom"] <= snap["body"]["bottom"] + 1, (
            f"第 {step} 步胶囊被顶出正文：{snap}"
        )

    # 输入长高时胶囊既不跟着上浮、也不掉出去（上一轮「输入增高把对话往上顶」的
    # 既有契约），整段过程的位移必须在一个小阈值内。
    drift = max(abs(snap["pills"]["top"] - first["pills"]["top"]) for snap in snaps)
    assert drift <= 8, f"输入长高时胶囊漂移了 {drift}px：{[s['pills']['top'] for s in snaps]}"
"""移动端任务详情：完成证据是胶囊，不是铺满底部的清单（Playwright + pytest）。

跑法（与其它 e2e 一样，刻意不进常规门禁）：

    .venv/bin/python -m pytest e2e/test_task_completion_capsule.py -v -s -p no:cacheprovider

对应反馈（2026-11-25）：「详情页底下这一堆是啥 太丑了 调整掉」。
当时「完成证据」常驻展开挂在胶囊行下面：5 条长验收项从输入框一直铺到屏幕底边。
现在它是同一行的第三个胶囊，默认收起，点开才显示清单。

完成记录由 `page.route` 造：清单只在「这一轮落过完成报告」时出现，让隔离实例真跑
一轮工作流才能自然产生，代价与稳定性都不划算（本套件既有的做法，见
test_workflow_authoring.py）。读口本身由后端测试与 web 单测覆盖。

注意：e2e 夹具跑英文 locale，胶囊标题是 "Completion evidence · Awaiting repair"。
"""

from __future__ import annotations

import json
import urllib.request

from playwright.sync_api import Page

BOARD_TAB = "Board"
BOARD_REFRESH = "[data-task-refresh-visual]"
VIEW_TABS = '[role="tablist"] [role="tab"]'
WORKSPACE = '[role="tablist"]'
PILLS = ".task-detail-execution"
NAME = "完成证据胶囊验收任务"

COMPLETION = {
    "present": True,
    "status": "blocked",
    "passed": False,
    "repairs_used": 0,
    "repairs_max": 2,
    "verifier_verdict": "",
    "updated_at": "2026-11-25T23:21:00Z",
    "items": [
        {"index": 0, "id": "contract", "label": "工作流未声明完成证据", "kind": "contract", "pass": False,
         "detail": "这个工作流没有声明完成契约，没有证据可以核对；请先在工作流里列出「做到什么算做完」"},
        {"index": 1, "id": "acc-1", "label": "验收项1", "kind": "acceptance", "pass": False,
         "detail": "定位子代理幽灵 pending 根因：pendingSessions 未清、CancelSessionTurn 在 active==nil 静默返回、attach 重铸身份、认领键前缀不匹配：等待独立复核结论"},
        {"index": 2, "id": "acc-2", "label": "验收项2", "kind": "acceptance", "pass": False,
         "detail": "代码修复已提交 3ced30de 并合入 main (7b4effdf)，新增 ws_session_cancel_orphan_test.go，go build ./... 与相关测试通过：等待独立复核结论"},
        {"index": 3, "id": "acc-3", "label": "验收项3", "kind": "acceptance", "pass": False,
         "detail": "pc-wsl 部署重启后实机复验：运行中子代理发送 session.cancel 后 +2s replying 消失，父会话正常收尾，/api/replying-sessions 无幽灵条目：等待独立复核结论"},
        {"index": 4, "id": "acc-4", "label": "验收项4", "kind": "acceptance", "pass": False,
         "detail": "两条测试会话与两行 session_name_aliases 别名已按授权清除，其它会话未受影响：等待独立复核结论"},
    ],
}

GEOM_JS = """
() => {
  const rect = (sel) => {
    const el = document.querySelector(sel);
    if (!el) return null;
    const b = el.getBoundingClientRect();
    return {top: b.top, bottom: b.bottom, height: b.height};
  };
  const body = document.querySelector('.mindfs-panel-body');
  return {
    body: rect('.mindfs-panel-body'),
    pills: rect('.task-detail-execution'),
    row: rect('[data-testid="task-inspect-row"]'),
    completionEntry: rect('[data-testid="task-completion-entry"]'),
    items: document.querySelectorAll('[data-testid="task-completion-items"] li').length,
    scroll: body ? {top: body.scrollTop, height: body.scrollHeight, client: body.clientHeight} : null,
    overflowX: document.documentElement.scrollWidth - window.innerWidth,
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


def _create_task(api_url: str, user_query: str, root_id: str) -> dict:
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": "完成证据胶囊验收夹具，不需要执行。",
            "name": NAME,
            "create_worktree": False,
            "start_immediately": False,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    return task


def _open_task(page: Page) -> None:
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    page.wait_for_timeout(1_500)
    page.locator('button[aria-label^="Expand"]').first.click()
    page.locator(VIEW_TABS, has_text=BOARD_TAB).first.click()
    page.wait_for_selector(BOARD_REFRESH, timeout=30_000)
    page.mouse.click(page.viewport_size["width"] - 12, 400)
    page.wait_for_timeout(400)
    page.click(BOARD_REFRESH)
    page.wait_for_timeout(800)
    card = page.locator("article", has=page.get_by_text(NAME, exact=True)).first
    card.wait_for(state="visible", timeout=20_000)
    card.get_by_text(NAME, exact=True).first.click()
    page.wait_for_selector(PILLS, timeout=20_000)
    page.wait_for_timeout(1_500)


def test_completion_evidence_is_a_capsule_not_a_wall(
    authed_page: Page, api_url: str, user_query: str, project: dict
) -> None:
    _create_task(api_url, user_query, str(project["id"]))

    page = authed_page
    page.set_viewport_size({"width": 390, "height": 844})
    page.route(
        "**/api/tasks/*/completion*",
        lambda route: route.fulfill(
            status=200,
            content_type="application/json",
            body=json.dumps({"completion": COMPLETION}),
        ),
    )
    _open_task(page)

    toggle = page.locator('[data-testid="completion-toggle"]')
    toggle.wait_for(state="visible", timeout=10_000)
    label = toggle.inner_text()
    print("capsule label:", label)
    # e2e 夹具跑英文 locale
    assert "Completion evidence" in label, label
    assert "Awaiting repair" in label, f"状态词要进胶囊标题：{label}"

    before = page.evaluate(GEOM_JS)
    print("collapsed geom:", json.dumps(before, ensure_ascii=False))
    assert before["completionEntry"] is None, "默认收起时不许再渲染清单面板"
    assert before["items"] == 0
    assert before["overflowX"] <= 0, f"出现横向滚动：{before['overflowX']}"
    # 打开就落在真正的底部，且正文的最后一块就是胶囊行（下面不再挂一墙文字）。
    assert abs(before["scroll"]["top"] - (before["scroll"]["height"] - before["scroll"]["client"])) <= 2, before["scroll"]
    assert before["row"]["bottom"] <= before["body"]["bottom"] + 1, before
    assert before["body"]["bottom"] - before["row"]["bottom"] <= 48, before
    page.screenshot(path="/tmp/completion-collapsed.png")

    toggle.click()
    page.wait_for_selector('[data-testid="task-completion-entry"]', timeout=10_000)
    page.wait_for_timeout(400)
    after = page.evaluate(GEOM_JS)
    print("expanded geom:", json.dumps(after, ensure_ascii=False))
    assert after["items"] == 5, f"展开后应列出 5 条未通过项：{after['items']}"
    assert after["overflowX"] <= 0
    page.screenshot(path="/tmp/completion-expanded.png")

    # 展开互斥：点开执行详情，完成证据面板收起。
    page.locator('[data-testid="run-view-toggle"]').click()
    page.wait_for_timeout(400)
    assert page.locator('[data-testid="task-completion-entry"]').count() == 0, "两个面板不许同时展开"
    assert page.locator('[data-testid="completion-toggle"]').get_attribute("data-open") == "false"

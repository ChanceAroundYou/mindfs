"""任务侧输入框的 @ 候选与粘贴截图（Playwright + pytest）。

跑法（与其它 e2e 用例一致，刻意不进常规门禁）：

    .venv/bin/python -m pytest e2e/test_task_composer_mentions.py -v -p no:cacheprovider

需求原文：「创建任务和任务详情页的输入框需要加上 @ 和复制图片功能，看齐会话输入框」。
这里验的是**真实浏览器里真的能用**，源码断言（web/tests/task-composer-mention-image.test.mjs）
只钉接线，钉不住 Lexical 的键盘/粘贴事件是否真的走到回调：

1. 任务详情输入框：输入 `@main` → 候选菜单出现 `@...main.ts` → Tab 选中 → 编辑器里落成 token；
   粘贴一张剪贴板图片 → 附件条出现 `pasted-image-*` 缩略图，发送键由「只有附件」放行。
2. 新建任务面板：同样两步（@ 候选 + 粘贴图片）。

数据准备走隔离后端（conftest 的临时 XDG_CONFIG_HOME + 随机端口 + e2e_test 账户），
任务用 REST API 建、`start_immediately=false` 停在待开始（非终态才有输入框），
不依赖真实 Agent，也不碰本机 7331。

登录后先 reload 一次：与 test_kanban_archive.py 同一个首屏竞态
（登录首轮 `protectedAPIReady()` 为 false，看板加载被静默挡掉）。
"""

from __future__ import annotations

import json
import re
import urllib.request

import pytest
from playwright.sync_api import Page, TimeoutError as PWTimeout

BOARD_TAB = "Board"
BOARD_REFRESH = '[data-onboarding="task-refresh"]'
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
WORKSPACE = '[data-onboarding="workspace"]'
CREATE_BUTTON = '[data-onboarding="task-create"]'

EDITOR = ".token-editor-input"
TOKEN = '[data-mindfs-token-node="true"]'
ATTACHMENTS = '[data-testid="composer-attachments"]'
PANEL = ".mindfs-panel-dialog"
ACTIVITY_INPUT = '[data-testid="activity-input"]'

TASK_NAME = "输入框能力验收任务 E2E"

# 1x1 透明 PNG：只要求 MIME 是 image/png，前端不做解码即可入附件条。
PNG_BYTES = bytes.fromhex(
    "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489"
    "0000000d49444154789c6360000002000154a24f6b0000000049454e44ae426082"
)

# 在 contenteditable 上派发带 DataTransfer 的 paste：Playwright 无法写系统剪贴板图片，
# 但 React 的 onPaste 读的就是 event.clipboardData，这条路径与真实粘贴同源。
PASTE_IMAGE_JS = """
(el) => {
  const bytes = Uint8Array.from(%s);
  const file = new File([bytes], "", { type: "image/png" });
  const dt = new DataTransfer();
  dt.items.add(file);
  const event = new ClipboardEvent("paste", {
    clipboardData: dt,
    bubbles: true,
    cancelable: true,
  });
  el.dispatchEvent(event);
  return { files: dt.files.length, items: dt.items.length };
}
""" % list(PNG_BYTES)


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
def pending_task(api_url: str, user_query: str, project: dict) -> dict:
    """建一张停在「待开始」的任务卡 —— 非终态，详情面板才有输入框。"""
    root_id = str(project["id"])
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": "这条任务是输入框能力验收夹具，不需要真的执行。",
            "name": TASK_NAME,
            "create_worktree": False,
            "start_immediately": False,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    return {"id": task["id"], "root_id": root_id}


# ── 浏览器导航辅助 ─────────────────────────────────────────────────────────


def _reload_for_loaded_board(page: Page) -> None:
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    try:
        page.click('button:has-text("Skip tour")', timeout=3_000)
    except PWTimeout:
        pass


def _show_board(page: Page) -> None:
    (
        page.locator(VIEW_SWITCHER)
        .first.get_by_role("tab", name=BOARD_TAB, exact=True)
        .click()
    )
    page.wait_for_selector(WORKSPACE, timeout=20_000)
    page.click(BOARD_REFRESH)


def _open_task(page: Page) -> None:
    card = page.locator("article", has=page.get_by_text(TASK_NAME, exact=True)).first
    card.wait_for(state="visible", timeout=20_000)
    card.get_by_text(TASK_NAME, exact=True).first.click()
    page.wait_for_selector(f"{ACTIVITY_INPUT} {EDITOR}", timeout=20_000)


def _type_mention(page: Page, editor_scope: str, query: str, expected: str) -> None:
    """输入 @<query>，等候选，按 Tab 选中，断言落成 token。"""
    editor = page.locator(f"{editor_scope} {EDITOR}").first
    editor.click()
    page.keyboard.type(f"@{query}")
    item = page.get_by_role("button", name=re.compile(rf"@[^\n]*{re.escape(expected)}")).first
    item.wait_for(state="visible", timeout=20_000)
    _assert_menu_visible(page)
    page.keyboard.press("Tab")
    token = page.locator(f"{editor_scope} {TOKEN}").first
    token.wait_for(state="visible", timeout=10_000)
    assert expected in (token.inner_text() or ""), f"token 文本不对：{token.inner_text()!r}"


# 菜单必须整体落在可视区内，且每一条候选都真的能被点到。
#
# 2026-11-20 用户报「@ 候选菜单在新建任务面板被遮挡，移动端几乎看不见」：菜单当时是
# `position:absolute; bottom:calc(100% + 6px)` 挂在编辑器外层，被 PanelShell 的
# `.mindfs-panel-body`（overflow:auto）与 `.mindfs-panel-dialog`（overflow:hidden）
# 裁掉 —— 桌面 4 条里只有最后一条可点，移动端菜单顶端是 -16px。box 断言挡位置，
# elementFromPoint 断言挡「看得见但点不到」。
MENU_GEOMETRY_JS = """
() => {
  const menu = document.querySelector('[data-testid="task-candidate-menu"]');
  if (!menu) return { error: "menu missing" };
  const vv = window.visualViewport;
  const view = vv
    ? { left: vv.offsetLeft, top: vv.offsetTop, right: vv.offsetLeft + vv.width, bottom: vv.offsetTop + vv.height }
    : { left: 0, top: 0, right: window.innerWidth, bottom: window.innerHeight };
  const r = menu.getBoundingClientRect();
  const rows = [...menu.children].map((row) => {
    const b = row.getBoundingClientRect();
    const x = b.left + Math.min(b.width / 2, 20);
    const y = (b.top + b.bottom) / 2;
    const hit = document.elementFromPoint(x, y);
    return {
      text: (row.textContent || "").trim().slice(0, 24),
      top: +b.top.toFixed(1),
      bottom: +b.bottom.toFixed(1),
      hittable: !!hit && menu.contains(hit),
      hit: hit ? hit.tagName + "." + String(hit.className || "").slice(0, 32) : null,
    };
  });
  return {
    viewport: { w: window.innerWidth, h: window.innerHeight },
    view: {
      top: +view.top.toFixed(1),
      bottom: +view.bottom.toFixed(1),
      left: +view.left.toFixed(1),
      right: +view.right.toFixed(1),
    },
    menu: {
      placement: menu.dataset.placement,
      top: +r.top.toFixed(1),
      bottom: +r.bottom.toFixed(1),
      left: +r.left.toFixed(1),
      right: +r.right.toFixed(1),
      height: +r.height.toFixed(1),
      position: getComputedStyle(menu).position,
      zIndex: getComputedStyle(menu).zIndex,
      scrollHeight: menu.scrollHeight,
      clientHeight: menu.clientHeight,
    },
    rows,
  };
}
"""


def _assert_menu_visible(page: Page) -> None:
    geo = page.evaluate(MENU_GEOMETRY_JS)
    assert "error" not in geo, f"候选菜单没渲染出来：{geo}"
    menu, view, rows = geo["menu"], geo["view"], geo["rows"]
    assert rows, f"候选菜单是空的：{geo}"
    # 两层裁剪 / 层叠上下文都逃掉了：fixed + portal 之后 position 必须是 fixed。
    assert menu["position"] == "fixed", f"候选菜单没 portal 到 body：{menu}"
    slack = 0.5
    assert menu["top"] >= view["top"] - slack, f"菜单顶端超出可视区：{menu} vs {view}"
    assert menu["bottom"] <= view["bottom"] + slack, f"菜单底端超出可视区：{menu} vs {view}"
    assert menu["left"] >= view["left"] - slack, f"菜单左边超出可视区：{menu} vs {view}"
    assert menu["right"] <= view["right"] + slack, f"菜单右边超出可视区：{menu} vs {view}"
    # 没被内部滚动压成「只剩一条」：4 条候选应一次看全。
    assert menu["scrollHeight"] <= menu["clientHeight"] + 1, f"菜单内容被压进滚动区：{menu}"
    for row in rows:
        assert row["top"] >= view["top"] - slack, f"候选行超出可视区：{row}"
        assert row["bottom"] <= view["bottom"] + slack, f"候选行超出可视区：{row}"
        assert row["hittable"], f"候选行点不到（被别的元素挡住）：{row}"



def _paste_image(page: Page, editor_scope: str) -> None:
    editor = page.locator(f"{editor_scope} {EDITOR}").first
    dispatched = editor.evaluate(PASTE_IMAGE_JS)
    assert dispatched["files"] == 1, f"DataTransfer 没装上文件：{dispatched}"
    strip = page.locator(f"{editor_scope} {ATTACHMENTS}").first
    strip.wait_for(state="visible", timeout=10_000)
    image = strip.locator('img[alt^="pasted-image-"]').first
    image.wait_for(state="visible", timeout=10_000)
    assert image.get_attribute("src"), "粘贴的图片没有预览 URL"


# ── 用例 ───────────────────────────────────────────────────────────────────


def test_task_detail_input_mentions_and_pasted_image(
    authed_page: Page, pending_task: dict
) -> None:
    page = authed_page
    _reload_for_loaded_board(page)
    _show_board(page)
    _open_task(page)

    _type_mention(page, ACTIVITY_INPUT, "main", "main.ts")
    _paste_image(page, ACTIVITY_INPUT)

    # 只有附件、没有正文时也能发送（看齐会话输入框），否则粘贴完还得随便打几个字。
    send = page.locator(f'{ACTIVITY_INPUT} [data-testid="activity-send"]').first
    send.wait_for(state="visible", timeout=10_000)
    assert send.is_enabled(), "只有附件时发送键应为可用"


def test_create_task_panel_mentions_and_pasted_image(
    authed_page: Page, pending_task: dict
) -> None:
    page = authed_page
    _reload_for_loaded_board(page)
    _show_board(page)

    page.locator(f"{CREATE_BUTTON}:visible").first.click()
    dialog = page.locator(f"{PANEL}:visible").first
    dialog.wait_for(state="visible", timeout=20_000)
    dialog.locator(EDITOR).first.wait_for(state="visible", timeout=20_000)

    _type_mention(page, f"{PANEL}:visible", "util", "util.ts")
    _paste_image(page, f"{PANEL}:visible")


def test_mobile_viewport_menu_is_fully_visible(
    authed_page: Page, pending_task: dict
) -> None:
    """窄屏（375×667）：候选菜单既不出视口，也不被面板裁成「只剩最后一条」。

    这是 2026-11-20 用户报障的原始场景：手机上面板正文顶端到输入框只有几十像素，
    向上展开的绝对定位菜单整块跑到面板 header 后面，用户只看到一条残影。
    """
    page = authed_page
    page.set_viewport_size({"width": 375, "height": 667})
    try:
        _reload_for_loaded_board(page)
        try:
            _show_board(page)
        except PWTimeout:
            # 窄屏下看板页签可能是折叠的；reload 后默认就在看板，直接用。
            pass

        # 1) 新建任务面板
        page.locator(f"{CREATE_BUTTON}:visible").first.click()
        dialog = page.locator(f"{PANEL}:visible").first
        dialog.wait_for(state="visible", timeout=20_000)
        dialog.locator(EDITOR).first.wait_for(state="visible", timeout=20_000)
        _type_mention(page, f"{PANEL}:visible", "util", "util.ts")
        _paste_image(page, f"{PANEL}:visible")

        # 2) 任务详情输入框（reload 顺便关掉面板）
        _reload_for_loaded_board(page)
        _open_task(page)
        _type_mention(page, ACTIVITY_INPUT, "main", "main.ts")
        _paste_image(page, ACTIVITY_INPUT)
    finally:
        # authed_page 是会话级夹具，别把 375 宽留给后面的用例。
        page.set_viewport_size({"width": 1440, "height": 900})

"""fork 二次确认端到端：单击不再直接复制整段历史。

跑法（会真的跑一轮模型、并真的 fork 一次；刻意不进常规门禁）：

    MINDFS_ISO_ROOT=/tmp/mindfs-iso-fork MINDFS_ISO_PORT=7441 \\
        bash scripts/mindfs-iso.sh start
    MINDFS_ISO_ROOT=/tmp/mindfs-iso-fork MINDFS_ISO_PORT=7441 \\
        MINDFS_ISO_URL=http://127.0.0.1:7441/mindfs \\
        .venv/bin/python -m pytest e2e/test_session_fork_confirm.py -v -s -p no:cacheprovider

为什么需要它：2026-11-23 pc 上「正常聊天时凭空多出一条完整副本会话」。取证结论是
误触 —— fork 按钮是回合页脚上 18×18 的小图标、与「复制」只隔 3px，单击即执行，
一次要跑二十多秒（pc 实测 24.464s），期间没有任何提示，结束时还把视图切到副本上。
修复是**先确认再 fork**（与删除会话 b46b808e 同一规矩）+ 在途不重发。

这条用例钉的就是复现路径本身：

1. 点 fork 按钮 ≠ 发出请求（旧行为：立刻 POST /api/sessions/fork）。
2. dialog 里点「取消」→ 始终没有请求，页面上也没有多出会话。
3. 再点一次并确认 → 请求真的发出、服务端真的多出一条会话。
"""

from __future__ import annotations

import json
import os
import re
import time
import urllib.request
from pathlib import Path

import pytest
from playwright.sync_api import Page, sync_playwright

ISO_ROOT = Path(os.environ.get("MINDFS_ISO_ROOT", "/tmp/mindfs-iso"))
ISO_ACCOUNT = ISO_ROOT / "account"
ISO_URL = os.environ.get("MINDFS_ISO_URL", "http://127.0.0.1:7431/mindfs")

CHROME_CANDIDATES = [
    Path.home() / ".cache/ms-playwright/chromium-1228/chrome-linux64/chrome",
    Path.home() / ".cache/ms-playwright/chromium-1223/chrome-linux/chrome",
]

TURN_TIMEOUT_MS = int(os.environ.get("MINDFS_E2E_TURN_TIMEOUT_MS", "300000"))
FORK_TIMEOUT_MS = int(os.environ.get("MINDFS_E2E_FORK_TIMEOUT_MS", "180000"))


# ── 隔离实例夹具 ────────────────────────────────────────────────────────────


def _iso_health() -> bool:
    try:
        with urllib.request.urlopen(f"{ISO_URL}/health", timeout=2) as resp:
            return resp.status == 200
    except Exception:
        return False


def _read_account() -> tuple[str, str]:
    lines = ISO_ACCOUNT.read_text(encoding="utf-8").splitlines()
    assert len(lines) >= 2, "隔离账户文件不完整，先跑 scripts/mindfs-iso.sh start"
    return lines[0], lines[1]


def _chrome_path() -> str:
    for path in CHROME_CANDIDATES:
        if path.exists():
            return str(path)
    raise RuntimeError("找不到 Playwright Chromium，先装浏览器或改 CHROME_CANDIDATES")


@pytest.fixture(scope="module")
def page() -> Page:
    if not _iso_health():
        pytest.skip("隔离实例没在跑：先执行 bash scripts/mindfs-iso.sh start")
    if not ISO_ACCOUNT.exists():
        pytest.skip("缺少隔离账户：先执行 bash scripts/mindfs-iso.sh start")
    user, password = _read_account()
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True, executable_path=_chrome_path())
        pg = browser.new_page(viewport={"width": 1440, "height": 900}, locale="zh-CN")
        pg.add_init_script(
            """() => {
                localStorage.setItem('mindfs-onboarding-state',
                    JSON.stringify({version: 2, completedAt: new Date().toISOString(), seenHints: []}));
            }"""
        )
        try:
            _login(pg, user, password)
            _dismiss_onboarding(pg)
            yield pg
        finally:
            browser.close()


def _dismiss_onboarding(page: Page) -> None:
    """新手导览是模态浮层，会拦住所有点击（隔离账户是全新的，一定会弹）。"""
    tour = page.locator('[role="dialog"][aria-label="MindFS 新手引导"]')
    if tour.count() == 0:
        return
    page.get_by_role("button", name="跳过导览").click()
    tour.wait_for(state="detached", timeout=10_000)


def _login(page: Page, user: str, password: str) -> None:
    page.goto(ISO_URL, wait_until="domcontentloaded")
    page.wait_for_selector('input[autocomplete="username"]', timeout=60_000)
    page.fill('input[autocomplete="username"]', user)
    page.fill('input[autocomplete="current-password"]', password)
    page.press('input[autocomplete="current-password"]', "Enter")
    page.wait_for_selector('[data-onboarding="workspace"]', timeout=60_000)


def _show_chat(page: Page) -> None:
    tab = page.locator('[data-onboarding="main-view-switcher"] [role="tab"]').nth(3)
    tab.click()
    page.wait_for_selector('[data-onboarding="message-input"]', timeout=30_000)


def _select_ok_model(page: Page) -> None:
    selector = page.locator('[data-onboarding="agent-selector"]')
    selector.click()
    page.wait_for_timeout(400)
    model = page.get_by_text("ok", exact=True)
    if model.count() == 0:
        row = page.locator("button").filter(has_text=re.compile(r"^\s*claude\s*$"))
        assert row.count() > 0, "agent 菜单里没有 claude"
        row.first.click()
        page.wait_for_timeout(400)
        model = page.get_by_text("ok", exact=True)
    assert model.count() > 0, "模型菜单里没有 ok（haiku 档）"
    model.first.click()
    page.wait_for_timeout(300)
    page.keyboard.press("Escape")
    page.wait_for_timeout(200)


def _composer(page: Page):
    return page.locator('[data-onboarding="message-input"] [contenteditable="true"]').first


def _send_action(page: Page):
    return page.locator('[data-onboarding="send-action"]')


def _send(page: Page, text: str) -> None:
    editor = _composer(page)
    editor.click()
    page.keyboard.press("ControlOrMeta+a")
    page.keyboard.press("Backspace")
    page.keyboard.insert_text(text)
    page.wait_for_timeout(200)
    button = _send_action(page)
    assert button.get_attribute("data-state") == "send" and button.is_enabled(), "发送键不可用"
    button.click()


def _actions_count(page: Page) -> int:
    return page.locator('[data-testid="turn-actions"]').count()


def _wait_for_new_actions(page: Page, previous: int, timeout_ms: int = TURN_TIMEOUT_MS) -> None:
    deadline = time.time() + timeout_ms / 1000
    while time.time() < deadline:
        if _actions_count(page) > previous:
            return
        page.wait_for_timeout(500)
    raise AssertionError(f"{timeout_ms}ms 内没有出现新的轮次控件行（上一轮计数 {previous}）")


def _current_root_id(page: Page) -> str:
    root = page.evaluate(
        """() => {
            for (let i = 0; i < localStorage.length; i++) {
                const k = localStorage.key(i);
                if (k && k.includes('mindfs-last-root-id')) return localStorage.getItem(k) || '';
            }
            return '';
        }"""
    )
    assert root, "浏览器里拿不到当前 root id"
    return root


def _current_user_id(page: Page) -> str:
    raw = page.evaluate("() => localStorage.getItem('mindfs.current_user') || ''")
    assert raw, "浏览器里拿不到当前账户"
    return json.loads(raw)["id"]


def _api(page: Page, path: str) -> dict:
    data = page.evaluate(
        """async (path) => {
            const base = location.pathname.replace(/\\/$/, '');
            const resp = await fetch(base + path, {credentials: 'include'});
            if (!resp.ok) throw new Error(path + ' -> ' + resp.status);
            const text = await resp.text();
            return text ? JSON.parse(text) : {};
        }""",
        path,
    )
    return data


def _session_keys(page: Page, root_id: str, user_id: str) -> list[str]:
    """列表接口的真实形状是 {items, pinned_items, ...}（http_sessions_list.go），
    不是 {sessions}；置顶的会话只出现在 pinned_items 里，两边都要算。"""
    data = _api(page, f"/api/sessions?user={user_id}&root={root_id}")
    items = (data.get("items") or []) + (data.get("pinned_items") or [])
    return [str(item.get("key") or item.get("session_key") or "") for item in items]


# ── 主测试 ─────────────────────────────────────────────────────────────────


def test_fork_requires_confirmation(page: Page) -> None:
    _show_chat(page)
    _select_ok_model(page)

    base = _actions_count(page)
    _send(page, "只回复 OK 两个字，不要调用任何工具。")
    _wait_for_new_actions(page, base)

    root_id = _current_root_id(page)
    user_id = _current_user_id(page)
    before = _session_keys(page, root_id, user_id)

    fork_requests: list[str] = []
    page.on(
        "request",
        lambda req: fork_requests.append(req.url)
        if req.method == "POST" and "/api/sessions/fork" in req.url
        else None,
    )

    # 最后一个完整轮次的 fork 图标（18×18 小图标，误触就是它）。
    fork_button = (
        page.locator('[data-testid="turn-actions"]')
        .last.get_by_role("button", name="从此消息 fork")
    )
    fork_button.click()

    dialog = page.get_by_role("dialog")
    dialog.wait_for(timeout=10_000)
    message = dialog.inner_text()
    assert "fork" in message, f"确认框没说清要做什么：{message!r}"
    assert "复制" in message, f"确认框没有提示「整段历史会被复制」：{message!r}"

    # ① 点了按钮但没确认：一个请求都不许发（旧行为在这里就已经 POST 了）。
    page.wait_for_timeout(1500)
    assert fork_requests == [], f"未确认就发出了 fork 请求：{fork_requests}"

    # ② 取消：仍然没有请求，会话表也没变。
    dialog.get_by_role("button", name="取消").click()
    dialog.wait_for(state="detached", timeout=10_000)
    page.wait_for_timeout(1000)
    assert fork_requests == [], f"取消之后仍然发了 fork 请求：{fork_requests}"
    assert _session_keys(page, root_id, user_id) == before, "取消之后会话表却多了一条"

    # ③ 再点一次并确认：这次必须真的发请求、真的多出一条会话。
    fork_button.click()
    dialog = page.get_by_role("dialog")
    dialog.wait_for(timeout=10_000)
    with page.expect_response(
        lambda resp: resp.request.method == "POST" and "/api/sessions/fork" in resp.url,
        timeout=FORK_TIMEOUT_MS,
    ) as response:
        # 确认键的文案就是 fork（与页脚图标的 aria-label 区分开：这里限定在 dialog 内）。
        dialog.get_by_role("button", name="fork", exact=True).click()
    assert response.value.status == 200, response.value.status
    assert len(fork_requests) == 1, f"确认后应当恰好一次请求：{fork_requests}"

    deadline = time.time() + 30
    after: list[str] = []
    while time.time() < deadline:
        after = _session_keys(page, root_id, user_id)
        if len(after) > len(before):
            break
        page.wait_for_timeout(1000)
    assert len(after) > len(before), f"确认 fork 之后会话列表没有新增：{before} -> {after}"

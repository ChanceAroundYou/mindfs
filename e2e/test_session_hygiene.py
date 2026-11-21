"""超长会话提示（session hygiene）端到端：真的触发一次 harness 自己的压缩。

跑法（刻意不进常规门禁，会真调模型）：

    MINDFS_ISO_ROOT=/tmp/mindfs-iso-hyg bash scripts/mindfs-iso.sh start
    .venv/bin/python -m pytest e2e/test_session_hygiene.py -v -s -p no:cacheprovider

前置（这次验收就是用它把三档压到两轮里）：

    $MINDFS_ISO_ROOT/config/mindfs/config.json

        {"session_hygiene": {"enabled": true, "active_hours": 0.0005,
                             "context_tokens": 1, "hint_turns": 1}}

覆盖范围：

- 第一轮结束后胶囊进入 `hint` 档（琥珀色、aria-label 带「自动压缩」），
  读口 `GET /api/sessions/{key}/context-saving` 的 `hygiene` 块与 UI 一致。
- 第二轮结束后服务端自己补一个 `/compact` 回合，日志出现
  `[session-hygiene] auto_compact.due` → `auto_compact.start` → `auto_compact.done`。
  只有 harness 真的发出压缩事件才会打 `done` —— 这就是「真压缩」的证据。
- 压缩后状态被清（`turns_since_hint` 归零、`auto_failed=false`）。

隔离：只连 `scripts/mindfs-iso.sh` 起的实例（默认 7431、独立 XDG 配置目录、
独立账户、隔离项目目录）。不碰 7331 线上服务，也不碰真实会话转录。
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
ISO_LOG = ISO_ROOT / "server.log"
ISO_URL = os.environ.get("MINDFS_ISO_URL", "http://127.0.0.1:7431/mindfs")

CHROME_CANDIDATES = [
    Path.home() / ".cache/ms-playwright/chromium-1228/chrome-linux64/chrome",
    Path.home() / ".cache/ms-playwright/chromium-1223/chrome-linux/chrome",
]

TURN_TIMEOUT_MS = int(os.environ.get("MINDFS_E2E_TURN_TIMEOUT_MS", "300000"))
AUTO_COMPACT_TIMEOUT_MS = int(os.environ.get("MINDFS_E2E_AUTO_COMPACT_TIMEOUT_MS", "300000"))


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


def _log_text() -> str:
    return ISO_LOG.read_text(encoding="utf-8", errors="replace") if ISO_LOG.exists() else ""


def _log_mark() -> int:
    return len(_log_text())


def _log_since(mark: int) -> str:
    return _log_text()[mark:]


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
        # 引导浮层会盖住切换器；它的「跳过」按钮文案随语言变，直接把它标成已看过。
        pg.add_init_script(
            """() => {
                localStorage.setItem('mindfs-onboarding-state',
                    JSON.stringify({version: 2, completedAt: new Date().toISOString(), seenHints: []}));
            }"""
        )
        try:
            _login(pg, user, password)
            yield pg
        finally:
            browser.close()


def _login(page: Page, user: str, password: str) -> None:
    page.goto(ISO_URL, wait_until="domcontentloaded")
    page.wait_for_selector('input[autocomplete="username"]', timeout=60_000)
    page.fill('input[autocomplete="username"]', user)
    page.fill('input[autocomplete="current-password"]', password)
    page.press('input[autocomplete="current-password"]', "Enter")
    page.wait_for_selector('[data-onboarding="workspace"]', timeout=60_000)


def _show_chat(page: Page) -> None:
    # 切换器是「工作台 / 看板 / 文件 / 聊天」四格，标签文案随语言变，按位置点第 4 格。
    tab = page.locator('[data-onboarding="main-view-switcher"] [role="tab"]').nth(3)
    tab.click()
    page.wait_for_selector('[data-onboarding="message-input"]', timeout=30_000)


def _select_ok_model(page: Page) -> None:
    """选 Claude Code + `ok`（菜单 id 是 haiku、发送时映射成 ok）。"""
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
    title = selector.locator("button").first.get_attribute("title") or ""
    assert title == "claude · haiku", f"模型选择没生效：{title!r}"
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


def _footer_count(page: Page) -> int:
    return page.locator('[data-testid="turn-meta"]').count()


def _wait_for_new_footer(page: Page, previous: int, timeout_ms: int = TURN_TIMEOUT_MS) -> None:
    deadline = time.time() + timeout_ms / 1000
    while time.time() < deadline:
        if _footer_count(page) > previous:
            return
        page.wait_for_timeout(500)
    raise AssertionError(f"{timeout_ms}ms 内没有出现新的整轮页脚（上一轮计数 {previous}）")


# ── 读口：直接用浏览器里的登录态取 API ──────────────────────────────────────


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
    """按前端同一条路由取数据：`user=` 是多账户 API 的必备参数，缺了会落到空账户。"""
    data = page.evaluate(
        """async (path) => {
            const base = location.pathname.replace(/\\/$/, '');
            const resp = await fetch(base + path, {credentials: 'include'});
            if (!resp.ok) throw new Error(path + ' -> ' + resp.status);
            return await resp.json();
        }""",
        path,
    )
    return data


def _chat_session_key(page: Page, root_id: str, user_id: str) -> str:
    data = _api(
        page,
        f"/api/sessions?root={root_id}&user={user_id}&limit=20",
    )
    items = data.get("items") or []
    assert items, f"会话列表为空：{data!r}"
    for item in items:
        if (item.get("type") or "") in ("", "chat"):
            return item["key"]
    raise AssertionError(f"列表里没有 chat 会话：{[i.get('type') for i in items]}")


def _hygiene(page: Page, session_key: str, root_id: str, user_id: str) -> dict:
    data = _api(
        page,
        f"/api/sessions/{session_key}/context-saving?root={root_id}&user={user_id}",
    )
    return data.get("hygiene") or {}


def _wait_hygiene(page: Page, session_key: str, root_id: str, user_id: str, predicate, timeout_ms: int = 60_000) -> dict:
    deadline = time.time() + timeout_ms / 1000
    last: dict = {}
    while time.time() < deadline:
        last = _hygiene(page, session_key, root_id, user_id)
        if predicate(last):
            return last
        page.wait_for_timeout(1000)
    raise AssertionError(f"hygiene 状态没有到达预期：{json.dumps(last, ensure_ascii=False)}")


# ── 主测试 ─────────────────────────────────────────────────────────────────


def test_hint_then_real_harness_compaction(page: Page) -> None:
    mark = _log_mark()
    _show_chat(page)
    _select_ok_model(page)
    base = _footer_count(page)

    # ⓪ 「久」从第一次观测开始累计：第一轮只有起点、没有可算的间隔，第二轮结束才跨过
    #    阈值（阈值被压到 active_hours=0.0005 ≈ 1.8s，任何一次真实回合都够）。
    _send(page, "只回复 OK 两个字，不要调用任何工具。")
    _wait_for_new_footer(page, base)

    root_id = _current_root_id(page)
    user_id = _current_user_id(page)
    session_key = _chat_session_key(page, root_id, user_id)
    first = _hygiene(page, session_key, root_id, user_id)
    assert first["state"] in ("normal", "notice"), f"第一轮就进了提示档：{first}"
    assert first["active_seconds"] == 0, first

    # ① 第二轮：跨过「久」的阈值，胶囊进入 hint 档（还剩 1 轮自动压缩）。
    before = _footer_count(page)
    _send(page, "再回复一次 OK，同样不要调用工具。")
    _wait_for_new_footer(page, before)

    # 胶囊是提示的唯一载体：档位从 data-hygiene 读（文案随语言变，别拿中文找元素）。
    chip = page.locator('[data-testid="context-saving-chip"]')
    chip.wait_for(timeout=60_000)
    assert chip.get_attribute("data-hygiene") == "hint", "胶囊没有进入 hint 档"
    assert "自动压缩" in (chip.get_attribute("aria-label") or ""), "hint 档的胶囊没说还有几轮自动压缩"

    hinted = _wait_hygiene(
        page, session_key, root_id, user_id,
        lambda h: h.get("state") == "hint" and h.get("turns_until_auto_compact") == 1,
    )
    assert hinted["enabled"] is True
    assert hinted["context_tokens"] > 0, hinted
    assert hinted["active_seconds"] > 0, hinted
    assert hinted["hint_turns"] == 1, hinted
    assert hinted["auto_failed"] is False, hinted

    # ② 第三轮：回合结束后服务端自己补 /compact，harness 真的要压。
    before = _footer_count(page)
    _send(page, "第三次回复 OK，不要调用工具。")
    _wait_for_new_footer(page, before)

    deadline = time.time() + AUTO_COMPACT_TIMEOUT_MS / 1000
    seen = ""
    while time.time() < deadline:
        seen = _log_since(mark)
        if "auto_compact.done" in seen:
            break
        for bad in ("auto_compact.no_notice", "auto_compact.error", "auto_compact.give_up"):
            assert bad not in seen, f"自动压缩没成功（{bad}）：\n{seen[-4000:]}"
        page.wait_for_timeout(1000)
    assert "auto_compact.due" in seen, f"到期没有触发：\n{seen[-4000:]}"
    assert "auto_compact.start" in seen, f"没有开始自动压缩：\n{seen[-4000:]}"
    assert "auto_compact.done" in seen, f"{AUTO_COMPACT_TIMEOUT_MS}ms 内没等到真压缩：\n{seen[-4000:]}"

    # ③ 压缩完成后状态被清：从下一轮重新计数，且没有记失败。
    after = _wait_hygiene(
        page, session_key, root_id, user_id,
        lambda h: h.get("turns_since_hint") == 0 and h.get("auto_failed") is False,
    )
    assert after["turns_until_auto_compact"] == after["hint_turns"], after

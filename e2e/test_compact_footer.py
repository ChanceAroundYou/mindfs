"""真压缩 + 会话尾部双区（轮次控件行 · 常驻状态条）端到端测试。

跑法（刻意不进常规门禁，会真调模型、真烧 token）：

    bash scripts/mindfs-iso.sh start
    .venv/bin/python -m pytest e2e/test_compact_footer.py -v -s -p no:cacheprovider

覆盖范围：

- 真打满 200k 窗口并触发 **真实自动压缩**（不是 mock）：选 Claude Code + `ok`
  模型（菜单里的 id 是 `haiku`，发送时映射成 `ok`），1M 开关关闭。
- 轮次控件行（复制/fork/完成时间）只在**已完整结束**的轮次后出现：正常文本收尾、
  工具收尾、**打断**、压缩之后各一次，且运行中不出现。
- 状态条常驻：模型名、上下文窗口（200k → 压缩后下降）在运行中与结束后都在。
- 压缩链路真的经过 compact-strip 代理：`[proxy] session bound` / `first request`
  / `post-compact signal` / `compact-strip before→after` 四类日志。

隔离：只连 `scripts/mindfs-iso.sh` 起的 7431 实例（独立 XDG 配置目录、独立账户、
隔离项目目录）。不碰 7331 线上服务，也不碰任何真实会话转录 —— 会话只建在
`/tmp/mindfs-iso/proj` 下。

为什么这些断言长这样（踩过的坑）：

- 旧页脚把模型/用量/上下文和复制按钮混在每轮末尾：统计随轮次缺失，控件又依赖
  客户端流式状态而闪烁。2026-11-28 拆成 `turn-actions`（每轮一次，完成才出现）
  与 `session-status-bar`（会话级常驻）。因此这里分别计数与断言，不再找一个
  `turn-meta` 元素包办两件事。
- compact-strip 代理曾经因为解析不到上游 base URL 而**整条链路静默不生效**
  （CLI 直接连 llmux，日志一行 `[proxy]` 都没有）。所以 `session bound` 与
  `first request` 是硬断言，不是参考信息。
"""

from __future__ import annotations

import os
import re
import time
import urllib.request
from pathlib import Path

import pytest
from playwright.sync_api import Page, TimeoutError as PWTimeout, sync_playwright

REPO = Path(__file__).resolve().parent.parent
ISO_ROOT = Path(os.environ.get("MINDFS_ISO_ROOT", "/tmp/mindfs-iso"))
ISO_ACCOUNT = ISO_ROOT / "account"
ISO_LOG = ISO_ROOT / "server.log"
ISO_URL = os.environ.get("MINDFS_ISO_URL", "http://127.0.0.1:7431/mindfs")

CHROME_CANDIDATES = [
    Path.home() / ".cache/ms-playwright/chromium-1223/chrome-linux/chrome",
    Path.home() / ".cache/ms-playwright/chromium-1228/chrome-linux/chrome",
]

# 每轮灌进去的字符数。默认值按「中文≈1 token/字」估，4~5 轮就能把 200k 窗口顶到
# 自动压缩阈值（约 167k）。MINDFS_E2E_FILL_CHARS 可调，小值用于冒烟。
FILL_CHARS = int(os.environ.get("MINDFS_E2E_FILL_CHARS", "36000"))
MAX_FILL_TURNS = int(os.environ.get("MINDFS_E2E_TURNS", "9"))
TURN_TIMEOUT_MS = int(os.environ.get("MINDFS_E2E_TURN_TIMEOUT_MS", "420000"))


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
    return ""  # 交给 Playwright 默认解析


def _log_text() -> str:
    if not ISO_LOG.exists():
        return ""
    return ISO_LOG.read_text(encoding="utf-8", errors="replace")


def _log_mark() -> int:
    return len(_log_text())


def _log_since(mark: int) -> str:
    return _log_text()[mark:]


def _proxy_lines(text: str) -> list[str]:
    return [line for line in text.splitlines() if "[proxy]" in line]


@pytest.fixture(scope="module")
def page() -> Page:
    if not _iso_health():
        pytest.skip("隔离实例没在跑：先执行 bash scripts/mindfs-iso.sh start")
    if not ISO_ACCOUNT.exists():
        pytest.skip("缺少隔离账户：先执行 bash scripts/mindfs-iso.sh start")
    user, password = _read_account()
    with sync_playwright() as p:
        launch_kwargs = {}
        chrome = _chrome_path()
        if chrome:
            launch_kwargs["executable_path"] = chrome
        browser = p.chromium.launch(**launch_kwargs)
        context = browser.new_context(viewport={"width": 1440, "height": 1000})
        page = context.new_page()
        _login(page, user, password)
        try:
            yield page
        finally:
            browser.close()


# ── 浏览器操作 ──────────────────────────────────────────────────────────────


def _login(page: Page, user: str, password: str) -> None:
    page.goto(ISO_URL, wait_until="domcontentloaded")
    page.wait_for_selector('input[autocomplete="username"]', timeout=60_000)
    page.fill('input[autocomplete="username"]', user)
    page.fill('input[autocomplete="current-password"]', password)
    page.press('input[autocomplete="current-password"]', "Enter")
    page.wait_for_selector('[data-onboarding="workspace"]', timeout=60_000)
    try:
        page.click('button:has-text("Skip tour")', timeout=5_000)
        page.wait_for_selector('button:has-text("Skip tour")', state="detached", timeout=5_000)
    except PWTimeout:
        pass


def _show_chat(page: Page) -> None:
    tab = (
        page.locator('[data-onboarding="main-view-switcher"]')
        .first.get_by_role("tab", name="Chat", exact=True)
    )
    tab.click()
    page.wait_for_selector('[data-onboarding="message-input"]', timeout=30_000)


def _select_ok_model(page: Page) -> None:
    """选 Claude Code + `ok`。

    菜单里的模型 id 是 `haiku`、显示名是 `ok`；发送时前端把它映射成 `ok`
    （modelUtils.resolveClaudeBaseAlias）。断言标题停在 `claude · haiku` 是
    「选中的确实是 ok 那一档」的证据。
    """
    selector = page.locator('[data-onboarding="agent-selector"]')
    selector.click()
    page.wait_for_timeout(400)
    # 模型行的 id 是 haiku、可见名是 ok。用可见文本精确匹配（has_text 的正则
    # 在这组按钮上不生效，实测 count 恒为 0）。
    model = page.get_by_text("ok", exact=True)
    if model.count() == 0:
        # 菜单打开时不一定是 claude 的子菜单；点一次 agent 行把它切过来。
        # 注意：已经展开时再点一次会**收起**，所以只在看不到 ok 时才点。
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


def _actions_count(page: Page) -> int:
    """已完整结束轮次的控件行数（运行中的轮次还没有 seq，不计数）。"""
    return page.locator('[data-testid="turn-actions"]').count()


def _wait_for_new_actions(page: Page, previous: int, timeout_ms: int = TURN_TIMEOUT_MS) -> None:
    deadline = time.time() + timeout_ms / 1000
    while time.time() < deadline:
        if _actions_count(page) > previous:
            return
        page.wait_for_timeout(500)
    raise AssertionError(f"{timeout_ms}ms 内没有出现新的轮次控件行（上一轮计数 {previous}）")


def _status_text(page: Page) -> str:
    return page.locator('[data-testid="session-status-bar"]').inner_text().strip()


def _context_badge(page: Page) -> str:
    return (
        page.locator('[data-testid="session-status-bar"]')
        .locator('[data-testid="status-context"]')
        .inner_text()
        .strip()
    )


def _context_total(text: str) -> int | None:
    """`(18K/200K)` → 18000；解析不出来返回 None。"""
    match = re.search(r"\((\d+(?:\.\d+)?)([KM]?)\s*/\s*(\d+(?:\.\d+)?)([KM]?)", text)
    if not match:
        return None
    value, unit = float(match.group(1)), match.group(2)
    return int(value * {"": 1, "K": 1000, "M": 1_000_000}[unit])


def _fill_text(rounds: int) -> str:
    """确定性的可压缩长文本：每行都不同，方便模型不至于整段复制。"""
    lines = []
    for i in range(rounds):
        lines.append(
            f"第{i}条记录：这是一段用于把上下文窗口打满的合成文本，"
            f"包含编号、坐标和校验值 —— id={i:06d} x={i % 97} y={i % 89} "
            f"checksum={(i * 2654435761) % 1000003:06d}。"
        )
    return "\n".join(lines)


def _fill_char_count() -> int:
    return len(_fill_text(1))


# ── 主测试 ─────────────────────────────────────────────────────────────────


def test_real_compaction_and_turn_actions_states(page: Page) -> None:
    mark = _log_mark()
    _show_chat(page)
    _select_ok_model(page)
    # 控件行按「已完成轮次新增」计数：页面可能恢复了上一次会话，已有控件不能算进来。
    base = _actions_count(page)

    # ① 正常文本收尾的一轮：控件行 +1；状态条在会话底部常驻，显示模型与 200k 上下文。
    _send(page, "只回复 OK 两个字，不要调用任何工具。")
    _wait_for_new_actions(page, base, timeout_ms=180_000)
    assert _actions_count(page) == base + 1, "一轮只应有一个控件行"
    status = _status_text(page)
    assert "ok" in status, f"状态条没有模型名：{status!r}"
    badge = _context_badge(page)
    assert re.search(r"/\s*200K", badge), f"上下文窗口不是 200k：{badge!r}"

    # ② 工具收尾的一轮：让模型最后一步是调用工具。控件行仍应落在该轮最后一行之后。
    before = _actions_count(page)
    _send(page, "用 Bash 工具执行：echo footcheck，然后不要再输出任何文字。")
    _wait_for_new_actions(page, before, timeout_ms=TURN_TIMEOUT_MS)
    assert _actions_count(page) == before + 1, "一轮只应有一个控件行"

    # ③ 打断：要求一长串输出，等开始流式后点停止。
    before = _actions_count(page)
    _send(page, "从 1 开始逐行输出数字，一直数到 50000，每行一个，不要省略。")
    cancel = _send_action(page)
    deadline = time.time() + 120
    while time.time() < deadline:
        if cancel.get_attribute("data-state") == "cancel":
            break
        page.wait_for_timeout(300)
    else:
        raise AssertionError("回合没有进入运行态：data-state 一直是 send")
    # 运行中：状态条必须仍然在；完整轮次的控件行不得增加。
    assert page.locator('[data-testid="session-status-bar"]').is_visible(), "运行中状态条消失了"
    assert _actions_count(page) == before, "运行中不该出现新轮次的控件行"
    page.wait_for_timeout(4_000)  # 让它真的吐出一些内容
    cancel.click()
    deadline = time.time() + 120
    while time.time() < deadline:
        if cancel.get_attribute("data-state") == "send":
            break
        page.wait_for_timeout(300)
    else:
        raise AssertionError("打断后发送键没有回到 send 状态")
    _wait_for_new_actions(page, before, timeout_ms=120_000)
    assert "ok" in _status_text(page), "打断后状态条丢了模型"

    # ④ 压缩前置：邀请几个技能。CLI 在压缩后会把「本会话之前调用过的 skill」
    #    整段重新注入；代理现在把整块删除（plan 只留路径行），所以只要压缩前
    #    加载过技能，压缩后的请求就应该看到 compact-strip。只让模型加载、不执行。
    before = _actions_count(page)
    _send(
        page,
        "请依次调用 Skill 工具加载这三个技能：humanizer-zh、ffmpeg-skill、quarkclouddrive。"
        "只加载，立刻只回复 PREFILL-OK，不要执行技能里的任何步骤，也不要改任何文件。",
    )
    _wait_for_new_actions(page, before, timeout_ms=TURN_TIMEOUT_MS)

    # ⑤ 灌满上下文直到真实自动压缩。
    rounds = max(1, FILL_CHARS // _fill_char_count())
    payload = None
    compacted_at = None
    max_total = 0
    for turn in range(MAX_FILL_TURNS):
        before = _actions_count(page)
        payload = payload or _fill_text(rounds)
        _send(page, f"这是第 {turn + 1} 批资料，只回复收到，不要总结：\n{payload}")
        _wait_for_new_actions(page, before, timeout_ms=TURN_TIMEOUT_MS)
        badge = _context_badge(page)
        total = _context_total(badge)
        max_total = max(max_total, total or 0)
        if page.get_by_text("Context compacted", exact=False).count() > 0:
            compacted_at = turn
            break

    assert compacted_at is not None, (
        "没有触发真实自动压缩：上下文没顶到 200k 窗口的阈值。"
        f"峰值上下文={max_total}，当前状态条={_context_badge(page)!r}"
    )
    # 「真实打满」的证据：压缩前确实到过阈值附近，而不是一点上下文就压了。
    assert max_total > 120_000, f"压缩前上下文只有 {max_total}，没有真的打满"

    # ⑥ 压缩后的状态条与代理证据。
    page.get_by_text("Context compacted", exact=False).first.wait_for(state="visible", timeout=60_000)
    before = _actions_count(page)
    _send(page, "压缩之后只回复 POST-COMPACT-OK，不要调用任何工具。")
    _wait_for_new_actions(page, before, timeout_ms=TURN_TIMEOUT_MS)
    post_badge = _context_badge(page)
    post_total = _context_total(post_badge)
    assert post_total is not None and post_total < 120_000, f"压缩后上下文没有下降：{post_badge!r}"

    log = _log_since(mark)
    lines = _proxy_lines(log)
    assert any("session bound" in line for line in lines), f"代理没有绑定会话：{lines}"
    assert any("first request" in line for line in lines), f"CLI 没有真的走代理：{lines}"
    assert any("post-compact signal" in line for line in lines), f"PostCompact hook 没触发：{lines}"
    strips = [line for line in lines if "compact-strip" in line]
    assert strips, (
        "压缩后没有发生剥离。技能没有被 CLI 恢复，或标记没对上；"
        f"代理日志：{lines}"
    )
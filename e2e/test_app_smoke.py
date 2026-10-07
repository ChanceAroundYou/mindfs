"""App.tsx 运行时冒烟测试（Playwright + pytest）。

跑法（**刻意不进常规门禁**）：
    ~/.hermes/hermes-agent/.venv/bin/python -m pytest e2e/ -v

定位：给 App.tsx 这类巨文件的拆分当安全网 —— 只断言「用户真的能看见/点得到」，
不断言实现细节，所以拆分挪代码不会误报，真拆坏了才会红。

选择器口径：优先用 `data-onboarding`（引导浮层用的锚点，属于对外可观测契约）
和 aria-label（无障碍名），避开 class 名与内部 DOM 结构。

**刻意不做「每个用例重载页面」**：一是慢，二是会撞上一个上游既存的后端 bug
（视图交互 + 页面重载累积 8~9 次后，后端整体失去响应；task-35 未改后端一行，
详见 e2e/README.md「已知上游问题」）。用例之间改用应用内导航复位。
"""

from __future__ import annotations

from playwright.sync_api import Page

WORKSPACE = '[data-onboarding="workspace"]'
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
PROJECT = "mindfs-e2e-project0"


def _tab(page: Page, name: str):
    """主视图切换器里的一个 tab（Workspace / Board / Files / Chat）。"""
    return page.locator(VIEW_SWITCHER).first.get_by_role("tab", name=name, exact=True)


def _show(page: Page, name: str) -> None:
    """切到主视图的某个 tab，并确认界面没崩。"""
    _tab(page, name).click()
    page.wait_for_selector(WORKSPACE, timeout=20_000)


def test_app_boots_to_workspace(authed_page: Page) -> None:
    """登录后主工作区渲染出来（不是白屏）。"""
    assert authed_page.locator(WORKSPACE).first.is_visible()


def test_main_view_switcher_present(authed_page: Page) -> None:
    """主视图切换器存在（工作台/看板/文件/会话入口）。"""
    assert authed_page.locator(VIEW_SWITCHER).first.is_visible()


def test_project_tree_lists_seeded_files(authed_page: Page) -> None:
    """项目列表加载出来了，且临时项目的目录/文件都在树里。"""
    page = authed_page
    _show(page, "Workspace")
    for name in (PROJECT, "src", "docs", "README.md"):
        assert page.get_by_text(name, exact=True).first.is_visible(), f"文件树缺少 {name}"


def test_project_tabs_present(authed_page: Page) -> None:
    """项目面板的页签（Files / git / Worktrees / Related files）在。"""
    page = authed_page
    _show(page, "Workspace")
    tabs = page.locator('[data-onboarding="project-tabs"]').first
    for name in ("Files", "git", "Worktrees", "Related files"):
        assert tabs.get_by_role("tab", name=name, exact=True).first.is_visible(), name


def test_open_file_renders_content(authed_page: Page) -> None:
    """点文件树里的文件 → 文件查看器真的把内容取回来渲染了。"""
    page = authed_page
    _show(page, "Workspace")
    page.get_by_text("README.md", exact=True).first.click()
    page.wait_for_selector("text=hello from e2e", timeout=20_000)


def test_view_switcher_switches_to_chat(authed_page: Page) -> None:
    """切到 Chat 视图 → 会话输入框出现（会话视图挂载成功）。"""
    page = authed_page
    _show(page, "Chat")
    page.wait_for_selector('[data-onboarding="message-input"]', timeout=20_000)


def test_view_switcher_switches_to_board(authed_page: Page) -> None:
    """切到 Board 视图 → 看板挂载且没把界面搞崩。"""
    page = authed_page
    _show(page, "Board")


def test_view_switcher_round_trips(authed_page: Page) -> None:
    """Workspace → Files → Workspace 能来回切（视图状态没被拆坏）。"""
    page = authed_page
    _show(page, "Files")
    _show(page, "Workspace")


def test_session_sidebar_present(authed_page: Page) -> None:
    """会话侧栏渲染出来了（会话操作区 + 搜索按钮）。"""
    page = authed_page
    _show(page, "Workspace")
    assert page.locator('[data-onboarding="session-actions"]').first.is_visible()
    assert page.get_by_role("button", name="Search current project sessions").first.is_visible()


def test_sidebar_collapse_round_trip(authed_page: Page) -> None:
    """左侧栏折叠/展开能来回切（顶栏与侧栏状态机没被拆坏）。"""
    page = authed_page
    _show(page, "Workspace")
    page.get_by_role("button", name="Collapse File sidebar").first.click()
    page.wait_for_selector('button[aria-label="Expand File sidebar"]', timeout=15_000)
    page.get_by_role("button", name="Expand File sidebar").first.click()
    page.wait_for_selector('button[aria-label="Collapse File sidebar"]', timeout=15_000)

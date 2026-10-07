"""MindFS 前端运行时冒烟测试夹具（Playwright + pytest）。

定位：给 App.tsx 这类巨文件的拆分提供「跑起来没坏」的安全网。
**刻意不进常规门禁**（`npm test` / `check-upstream`），单独命令跑：

    ~/.hermes/hermes-agent/.venv/bin/python -m pytest e2e/ -v

为什么用「后端直接服务 dist」而不是 vite dev server：
- 本机 7331 被真实 mindfs 服务占着，而 vite.config.ts 的代理目标硬编码 7331；
- 后端支持 `MINDFS_STATIC_DIR` 指定静态目录，于是单进程即可，
  既不用改主配置、也不碰真实服务，而且跑的就是生产产物。

隔离设计（不碰真实机器状态）：
- `XDG_CONFIG_HOME` 指向 pytest 临时目录 → 后端用一份全新账户表/项目表/会话库，
  不读也不写真实的 `~/.config/mindfs`。
- 测试账号 `e2e_test` 在隔离配置目录里由 `POST /api/users` 创建，
  密码是 test 专属临时值（见 `TEST_USER`），与任何真实账户无关。
- 后端监听一个随机空闲端口。
"""

from __future__ import annotations

import json
import os
import socket
import subprocess
import time
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Iterator

import pytest
from playwright.sync_api import Page, TimeoutError as PWTimeout, sync_playwright

REPO = Path(__file__).resolve().parent.parent
WEB = REPO / "web"
DIST = WEB / "dist"

# dist 按 /mindfs 前缀构建（与 make build-web 的 MIND_FS_BASE 默认值一致）
DEPLOY_PREFIX = "/mindfs"

# test 专属临时账号：只在隔离配置目录里存在，随临时目录一起销毁
TEST_USER = {
    "username": "e2e_test",
    "password": "e2e-temp-7Kx9Qm2!",
    "role": "user",
}

READY_TIMEOUT = 180.0


def _wait_http(url: str, timeout: float = READY_TIMEOUT) -> None:
    """轮询直到 URL 返回非 5xx（后端起来需要时间）。"""
    deadline = time.time() + timeout
    last: Exception | None = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as resp:
                if resp.status < 500:
                    return
        except Exception as exc:  # noqa: BLE001 - 就绪前任何异常都只是「还没起来」
            last = exc
        time.sleep(0.3)
    raise RuntimeError(f"等待 {url} 就绪超时（{timeout}s）：{last}")


def _post_json(url: str, payload: dict) -> dict:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read().decode() or "{}")


def _free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


@pytest.fixture(scope="session")
def config_dir(tmp_path_factory: pytest.TempPathFactory) -> Path:
    """隔离的配置目录（后端把它当作 XDG_CONFIG_HOME）。"""
    return tmp_path_factory.mktemp("mindfs-e2e-config")


@pytest.fixture(scope="session")
def web_dist() -> Path:
    """构建前端产物。设 E2E_SKIP_BUILD=1 可跳过（复用已有 dist，迭代更快）。"""
    if os.environ.get("E2E_SKIP_BUILD") != "1":
        subprocess.run(
            ["npm", "run", "build"],
            cwd=WEB,
            env={**os.environ, "VITE_MIND_FS_BASE": DEPLOY_PREFIX},
            check=True,
        )
    index = DIST / "index.html"
    assert index.is_file(), f"缺少前端产物：{index}（去掉 E2E_SKIP_BUILD 先构建）"
    return DIST


@pytest.fixture(scope="session")
def backend_port() -> int:
    return _free_port()


@pytest.fixture(scope="session")
def backend_url(backend_port: int) -> str:
    return f"http://127.0.0.1:{backend_port}"


@pytest.fixture(scope="session")
def api_url(backend_url: str) -> str:
    """带部署前缀的 API 根（后端会剥掉前缀再路由）。"""
    return f"{backend_url}{DEPLOY_PREFIX}"


@pytest.fixture(scope="session")
def app_url(api_url: str) -> str:
    return f"{api_url}/"


@pytest.fixture(scope="session")
def backend(
    config_dir: Path, web_dist: Path, backend_port: int
) -> Iterator[subprocess.Popen]:
    """编译并启动后端（隔离配置目录 + 直接服务 dist）。"""
    env = {
        **os.environ,
        "XDG_CONFIG_HOME": str(config_dir),
        "MINDFS_STATIC_DIR": str(web_dist),
    }
    binary = config_dir / "mindfs-server"
    # 部署前缀是编译期注入的（见 Makefile 第 62 行），必须与 dist 的 VITE_MIND_FS_BASE 一致，
    # 否则 index.html 引用的 /mindfs/assets/... 在根部署后端上全部 404。
    subprocess.run(
        [
            "go",
            "build",
            "-ldflags",
            f"-X mindfs/internal/deploy.Prefix={DEPLOY_PREFIX}",
            "-o",
            str(binary),
            "./server/cmd/mindfs-server",
        ],
        cwd=REPO,
        env=env,
        check=True,
    )
    proc = subprocess.Popen(
        [str(binary), "-addr", f"127.0.0.1:{backend_port}"],
        cwd=REPO,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    try:
        _wait_http(f"http://127.0.0.1:{backend_port}{DEPLOY_PREFIX}/api/auth/status")
        yield proc
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5)


@pytest.fixture(scope="session")
def test_user(backend: subprocess.Popen, api_url: str) -> dict[str, str]:
    """在隔离配置目录里创建 test 专属临时账号。

    返回体里带上服务端分配的 `id`：前端所有数据面请求都会附
    `user=<当前账户 id>`（web/src/services/net/base.ts 的 withAccountUser），
    所以后续用 API 造数据时必须打同一个分区，否则界面上什么都看不到。
    """
    created = _post_json(f"{api_url}/api/users", TEST_USER)
    user = created.get("user") or {}
    assert user.get("username") == TEST_USER["username"], created
    return {**TEST_USER, **user}


@pytest.fixture(scope="session")
def user_query(test_user: dict[str, str]) -> str:
    """`?user=<id>` —— 打测试账户分区用。"""
    return f"user={urllib.parse.quote(str(test_user.get('id') or ''))}"


@pytest.fixture(scope="session")
def project_dir(tmp_path_factory: pytest.TempPathFactory) -> Path:
    """一个内容可预期的临时项目（文件树/文件查看测试用）。"""
    root = tmp_path_factory.mktemp("mindfs-e2e-project")
    (root / "README.md").write_text("# e2e project\n\nhello from e2e\n", encoding="utf-8")
    src = root / "src"
    src.mkdir()
    (src / "main.ts").write_text("export const answer = 42;\n", encoding="utf-8")
    (src / "util.ts").write_text("export function twice(n: number) {\n  return n * 2;\n}\n", encoding="utf-8")
    docs = root / "docs"
    docs.mkdir()
    (docs / "guide.md").write_text("# guide\n\nsome docs\n", encoding="utf-8")
    return root


@pytest.fixture(scope="session")
def project(
    backend: subprocess.Popen, api_url: str, user_query: str, project_dir: Path
) -> dict:
    """把临时项目注册进**测试账户**的分区（POST /api/dirs）。"""
    added = _post_json(f"{api_url}/api/dirs?{user_query}", {"path": str(project_dir)})
    assert added.get("id") or added.get("dir"), added
    return added


def login(page: Page, app_url: str, user: dict[str, str] = TEST_USER) -> None:
    """走真实登录界面登录（AuthGate 的 autoComplete 属性是稳定选择器）。"""
    page.goto(app_url, wait_until="domcontentloaded")
    page.wait_for_selector('input[autocomplete="username"]', timeout=60_000)
    page.fill('input[autocomplete="username"]', user["username"])
    page.fill('input[autocomplete="current-password"]', user["password"])
    # 密码框的 Enter 直接触发 submit()，比找按钮稳
    page.press('input[autocomplete="current-password"]', "Enter")
    page.wait_for_selector('[data-onboarding="workspace"]', timeout=60_000)
    skip_tour(page)


def skip_tour(page: Page) -> None:
    """首次进入会弹 13 步引导浮层，它盖在界面上会吃掉点击；有就跳过。"""
    try:
        page.click('button:has-text("Skip tour")', timeout=4_000)
        page.wait_for_selector('button:has-text("Skip tour")', state="detached", timeout=5_000)
    except PWTimeout:
        pass


@pytest.fixture(scope="session")
def authed_page(
    backend: subprocess.Popen,
    test_user: dict[str, str],
    project: dict,
    app_url: str,
) -> Iterator[Page]:
    """返回一个已登录的主页面；测试内自行导航（session 级，跑得快）。

    依赖 project：项目先注册好，登录后文件树/会话列表才有内容。
    """
    with sync_playwright() as p:
        browser = p.chromium.launch()
        context = browser.new_context(viewport={"width": 1440, "height": 900})
        page = context.new_page()
        login(page, app_url)
        try:
            yield page
        finally:
            browser.close()

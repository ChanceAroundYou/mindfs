"""幽灵任务卡（服务端已删、界面还画着）的浏览器端到端验收（Playwright + pytest）。

跑法（与其它 e2e 用例一致，刻意不进常规门禁）：

    E2E_SKIP_BUILD=1 .venv/bin/python -m pytest e2e/test_task_ghost_card.py -v -p no:cacheprovider

（`E2E_SKIP_BUILD=1` 复用已有 `web/dist`；不带就要先 `npm run build`，本机
内存紧张时构建 + Chromium 并发容易被 OOM 杀掉。）

现场（2026-11-20）：llmux 项目 #16（``task_6c2b5e2430af72eb``）10:24:36 删除成功
（HTTP 200），12:29 浏览器里还画着这张卡。点「完成」回
``400 {"error":"sql: no rows in result set"}``，点「删除」回 ``404`` 同一句话 ——
用户看到的就是「这张卡既不能完成也不能删除」。

成因两条，本用例各验一条：

1. **首次加载走的是增量同步**（``after`` 水位）。增量响应在结构上就看不见
   「少了谁」，于是缓存里的幽灵卡每次打开页面都活着。修法：本页第一次拉某个
   root 走全量对账，不在权威集合里的就地淘汰（内存 + IndexedDB）。
   对应用例 ``test_cached_ghost_is_pruned_on_first_load`` —— 直接往 IndexedDB
   里塞一条服务端不存在的记录，刷新后它必须消失，且缓存里的那条也要被删掉。
   **刻意不点刷新按钮**：那是 ``force`` 全量，本来就会淘汰，会掩盖「首次加载」
   这条真路径。
2. **任务级动作拿到 no rows 只弹一条红字**，卡片原地不动，点一次失败一次。
   修法：认出来就把那张卡摘掉，并说明「已在服务器上删除」。
   对应用例 ``test_live_ghost_delete_removes_it_and_explains`` —— 造一张真幽灵：
   掐断页面的 WebSocket 再断网，从测试进程里删任务，页面全程收不到
   ``task.deleted``，卡片于是留在板上（与真机现场同一条路径：删它的是另一台
   设备 / 当时断线）。

数据准备全部走隔离后端（conftest 的临时 ``XDG_CONFIG_HOME`` + 随机端口 +
``e2e_test`` 账户），不写库、不跑真实 Agent、不碰本机 7331。
"""

from __future__ import annotations

import json
import time
import urllib.parse
import urllib.request

import pytest
from playwright.sync_api import Page, TimeoutError as PWTimeout

BOARD_TAB = "Board"
VIEW_SWITCHER = '[data-onboarding="main-view-switcher"]'
BOARD_REFRESH = '[data-onboarding="task-refresh"]'
WORKSPACE = '[data-onboarding="workspace"]'
DELETE_BUTTON = 'button[aria-label^="Delete task"]'
GONE_TOAST = "This task no longer exists on the server; removed from the board"

CACHE_DB = "mindfs-task-cache"


# ── HTTP 夹具辅助 ──────────────────────────────────────────────────────────


def _post_json(url: str, payload: dict) -> dict:
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read().decode() or "{}")


def _delete_task(api_url: str, user_query: str, root_id: str, task_id: str) -> None:
    """从测试进程里删任务 —— 浏览器那边看不到任何广播。"""
    query = urllib.parse.urlencode({"root_id": root_id}) + f"&{user_query}"
    req = urllib.request.Request(f"{api_url}/api/tasks/{task_id}?{query}", method="DELETE")
    with urllib.request.urlopen(req, timeout=20) as resp:
        assert resp.status == 200, f"删除失败：{resp.status}"


def _create_finished_task(api_url: str, user_query: str, root_id: str, name: str) -> dict:
    detail = _post_json(
        f"{api_url}/api/tasks?{user_query}",
        {
            "root_id": root_id,
            "input": "幽灵卡验收夹具，不需要真的执行。",
            "name": name,
            "create_worktree": False,
            "start_immediately": False,
        },
    )
    task = detail.get("task") or {}
    assert task.get("id"), f"创建任务失败：{detail}"
    _post_json(f"{api_url}/api/tasks/{task['id']}/complete?{user_query}", {"root_id": root_id})
    return task


# ── 浏览器导航辅助 ─────────────────────────────────────────────────────────


def _reload(page: Page) -> None:
    """首屏竞态兜底：reload 一次，让看板/工作台的加载真的发出去（见 kanban_archive 模块注释）。"""
    page.reload(wait_until="domcontentloaded")
    page.wait_for_selector(WORKSPACE, timeout=60_000)
    try:
        page.click('button:has-text("Skip tour")', timeout=3_000)
    except PWTimeout:
        pass


def _show_board(page: Page, refresh: bool = True) -> None:
    page.locator(VIEW_SWITCHER).first.get_by_role("tab", name=BOARD_TAB, exact=True).click()
    page.wait_for_selector(WORKSPACE, timeout=20_000)
    if refresh:
        page.click(BOARD_REFRESH)


def _card(page: Page, name: str):
    return page.locator("article", has=page.get_by_text(name, exact=True)).first


# 让页面记录自己开过的 WebSocket，好在需要时把它们掐断。
# 掐断而不是断网：实测 `context.set_offline(True)` 不会拆掉已经建立的
# WebSocket，删除广播照样送达，幽灵态根本造不出来。
WS_TRACKER = """
(() => {
  const Native = window.WebSocket;
  const sockets = [];
  function Tracked(url, protocols) {
    const ws = protocols === undefined ? new Native(url) : new Native(url, protocols);
    sockets.push(ws);
    return ws;
  }
  Tracked.prototype = Native.prototype;
  for (const name of ["CONNECTING", "OPEN", "CLOSING", "CLOSED"]) {
    Object.defineProperty(Tracked, name, { value: Native[name] });
  }
  window.WebSocket = Tracked;
  window.__mindfsE2ESockets = sockets;
})();
"""

# 往任务缓存里塞一条服务端不存在的记录（克隆一条真记录，键形状自动正确）。
INJECT_GHOST = """
async ({ rootId, templateTaskId, ghostId, ghostName }) => {
  const db = await new Promise((resolve, reject) => {
    const req = indexedDB.open("mindfs-task-cache", 1);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  try {
    const tx = db.transaction(["tasks"], "readwrite");
    const store = tx.objectStore("tasks");
    const all = await new Promise((resolve, reject) => {
      const req = store.getAll();
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
    const template = all.find((rec) => String(rec.taskId) === String(templateTaskId));
    if (!template) throw new Error("模板任务不在缓存里，先让看板把它存下来");
    const ghost = JSON.parse(JSON.stringify(template));
    ghost.taskId = ghostId;
    ghost.cacheKey = String(template.cacheKey).split(String(template.taskId)).join(ghostId);
    ghost.detail.task.id = ghostId;
    ghost.detail.task.name = ghostName;
    ghost.detail.task.task_number = (template.detail.task.task_number || 0) + 900;
    await new Promise((resolve, reject) => {
      const req = store.put(ghost);
      req.onsuccess = () => resolve();
      req.onerror = () => reject(req.error);
    });
    await new Promise((resolve, reject) => {
      tx.oncomplete = () => resolve();
      tx.onerror = () => reject(tx.error);
      tx.onabort = () => reject(tx.error);
    });
    return { cacheKey: ghost.cacheKey, total: all.length };
  } finally {
    db.close();
  }
}
"""

CACHE_HAS = """
async ({ rootId, taskId }) => {
  const db = await new Promise((resolve, reject) => {
    const req = indexedDB.open("mindfs-task-cache", 1);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  try {
    const tx = db.transaction(["tasks"], "readonly");
    const store = tx.objectStore("tasks");
    const all = await new Promise((resolve, reject) => {
      const req = store.index("rootId").getAll(rootId);
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
    return all.some((rec) => String(rec.taskId) === String(taskId));
  } finally {
    db.close();
  }
}
"""


def _ghost_card_gone(page: Page, name: str, timeout_ms: int = 20_000) -> None:
    page.wait_for_function(
        """(name) => !Array.from(document.querySelectorAll('article'))
            .some((el) => (el.textContent || '').includes(name))""",
        arg=name,
        timeout=timeout_ms,
    )


# ── 用例 ───────────────────────────────────────────────────────────────────


def test_cached_ghost_is_pruned_on_first_load(
    authed_page: Page, api_url: str, user_query: str, project: dict
) -> None:
    """缓存里躺着一张服务端已删的卡：刷新（首次加载）就该把它淘汰掉。"""
    page = authed_page
    root_id = str(project["id"])
    name = "幽灵卡-刷新自愈 E2E"
    ghost_id = f"task_ghost_{int(time.time() * 1000)}"

    task = _create_finished_task(api_url, user_query, root_id, name)
    task_id = str(task["id"])
    _reload(page)
    _show_board(page)

    card = _card(page, name)
    card.wait_for(state="visible", timeout=20_000)
    time.sleep(1.0)  # 等 IndexedDB 缓存落盘（applyTaskDetails 的 persist 是异步的）

    # 真任务还留在服务端（它是模板），幽灵只活在缓存里 —— 与 llmux #16 同形。
    injected = page.evaluate(
        INJECT_GHOST,
        {"rootId": root_id, "templateTaskId": task_id, "ghostId": ghost_id, "ghostName": "幽灵卡-缓存注入 E2E"},
    )
    assert injected["cacheKey"].endswith(ghost_id), f"幽灵记录键不对：{injected}"
    assert page.evaluate(CACHE_HAS, {"rootId": root_id, "taskId": ghost_id}), "幽灵记录没写进缓存"

    # 刷新。**不点刷新按钮**：那是 force 全量、本来就会淘汰，掩盖首次加载这条路径。
    _reload(page)
    _show_board(page, refresh=False)

    _ghost_card_gone(page, "幽灵卡-缓存注入 E2E")
    assert not page.evaluate(CACHE_HAS, {"rootId": root_id, "taskId": ghost_id}), \
        "幽灵记录还留在 IndexedDB 里 —— 只清了内存，下次打开又会被喂回来"


def test_live_ghost_delete_removes_it_and_explains(
    authed_page: Page, api_url: str, user_query: str, project: dict
) -> None:
    """不刷新也能点掉：动作拿到 no rows 时就地摘卡 + 说明原因。"""
    page = authed_page
    root_id = str(project["id"])
    name = "幽灵卡-点删除 E2E"

    page.add_init_script(WS_TRACKER)  # 下一次 reload 起生效
    _reload(page)
    _show_board(page)

    # 建卡放在页面就绪之后：广播送达，卡片正常出现在板上。
    task = _create_finished_task(api_url, user_query, root_id, name)
    task_id = str(task["id"])
    card = _card(page, name)
    card.wait_for(state="visible", timeout=20_000)

    # 掐断 WS 再断网，然后从测试进程删任务 —— 本页收不到 task.deleted。
    page.evaluate("window.__mindfsE2ESockets.forEach((s) => { try { s.close(); } catch (e) {} })")
    page.context.set_offline(True)
    time.sleep(1.5)  # 让重连尝试彻底失败，删除广播也就无处可送
    _delete_task(api_url, user_query, root_id, task_id)
    page.context.set_offline(False)
    time.sleep(2.0)  # WS 重连；重连不补发错过的 task.deleted

    ghost = _card(page, name)
    assert ghost.count() == 1, "断线删除后卡片应仍留在页面上（幽灵态没造出来）"

    # 点删除 → 确认。服务端回 404 那句 no rows。
    ghost.locator(DELETE_BUTTON).first.click()
    page.get_by_role("dialog").get_by_role("button", name="Delete", exact=True).click()

    # 卡片当场消失，并给出可读的原因（不是把 sql: no rows 甩给用户）。
    _ghost_card_gone(page, name)
    page.get_by_text(GONE_TOAST, exact=True).first.wait_for(state="visible", timeout=10_000)
